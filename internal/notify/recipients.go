// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Recipient is one mail target: a user or a team/triage mailbox address.
type Recipient struct {
	UserID   string `json:"user_id,omitempty"`
	Addr     string `json:"addr"`
	Name     string `json:"name,omitempty"`
	Locale   string `json:"locale,omitempty"`
	Audience string `json:"audience"` // customer | staff
	Mailbox  bool   `json:"mailbox,omitempty"`
}

// Target is a (template, recipient) pair produced by the resolver.
type Target struct {
	Key string
	R   Recipient
}

type userRow struct {
	ID, Kind, Email, Name, Role, Status, Locale, Signature string
	Tier                                                   int
	OptOut                                                 []string // email opt-outs (NOT-015)
	InAppOff                                               []string // in-app types turned off (NOT-037)
	BrowserOn                                              []string // browser channel turned on (NOT-037)
}

func (u *userRow) effectiveTier() int {
	if u.Role == "admin" || u.Role == "supervisor" {
		return 3
	}
	return u.Tier
}

func (u *userRow) recipient() Recipient {
	aud := "staff"
	if u.Kind == "customer" {
		aud = "customer"
	}
	return Recipient{UserID: u.ID, Addr: u.Email, Name: u.Name, Locale: u.Locale, Audience: aud}
}

// Visibility is the staff visibility predicate (I-4, I-11) evaluated against the
// event snapshot plus current team membership. The default is a local
// implementation (authz does not exist yet); swap with SetVisibility.
type Visibility interface {
	StaffCanSee(ctx context.Context, userID, role string, tier int, snap events.Snapshot) bool
}

type localVisibility struct{ m *Module }

// StaffCanSee: admins and supervisors see everything; agents nothing outside
// their projects (I-11); the assignee sees the ticket; triage tickets are visible to agents when the support setting
// allows it; otherwise the user must be in the ticket's team with an
// effective tier ≥ the ticket tier (D-05).
func (v localVisibility) StaffCanSee(ctx context.Context, userID, role string, tier int, snap events.Snapshot) bool {
	if role == "admin" || role == "supervisor" {
		return true
	}
	// I-11 (D-20): agents only hear about tickets of their projects.
	var member int
	if err := v.m.db.Read().QueryRowContext(ctx, `SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?`, snap.Project(), userID).Scan(&member); err != nil {
		return false
	}
	if snap.AssigneeID != "" && snap.AssigneeID == userID {
		return true
	}
	if snap.TeamID == "" {
		sup, err := v.m.support(ctx)
		return err == nil && sup.TriageVisibleToAgents
	}
	if tier < snap.Tier {
		return false
	}
	var one int
	err := v.m.db.Read().QueryRowContext(ctx, `SELECT 1 FROM team_members WHERE team_id = ? AND user_id = ?`, snap.TeamID, userID).Scan(&one)
	return err == nil
}

func (m *Module) support(ctx context.Context) (settings.Support, error) {
	if m.set == nil {
		return settings.DefaultSupport(), nil
	}
	return m.set.Support(ctx)
}

const userCols = `id, kind, email, name, role, tier, status, locale, signature_md, notify_prefs`

func scanUser(row interface{ Scan(...any) error }) (*userRow, error) {
	var u userRow
	var tier sql.NullInt64
	var prefs sql.NullString
	if err := row.Scan(&u.ID, &u.Kind, &u.Email, &u.Name, &u.Role, &tier, &u.Status, &u.Locale, &u.Signature, &prefs); err != nil {
		return nil, err
	}
	u.Tier = int(tier.Int64)
	if prefs.Valid && prefs.String != "" {
		var p storedPrefs
		if json.Unmarshal([]byte(prefs.String), &p) == nil {
			u.OptOut, u.InAppOff, u.BrowserOn = p.OptOut, p.InAppOff, p.BrowserOn
		}
	}
	return &u, nil
}

func (m *Module) user(ctx context.Context, id string) (*userRow, error) {
	if id == "" {
		return nil, store.ErrNotFound
	}
	u, err := scanUser(m.db.Read().QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return u, err
}

func (m *Module) users(ctx context.Context, q string, args ...any) ([]*userRow, error) {
	rows, err := m.db.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*userRow
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type teamRow struct {
	ID, Name, Mailbox, Mode string
}

func (m *Module) team(ctx context.Context, id string) (*teamRow, error) {
	if id == "" {
		return nil, store.ErrNotFound
	}
	var t teamRow
	var mb sql.NullString
	err := m.db.Read().QueryRowContext(ctx, `SELECT id, name, notification_email, notify_mode FROM teams WHERE id = ?`, id).Scan(&t.ID, &t.Name, &mb, &t.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	t.Mailbox = strings.TrimSpace(mb.String)
	return &t, err
}

// teamAgents: active staff members of team with effective tier ≥ minTier.
func (m *Module) teamAgents(ctx context.Context, teamID string, minTier int) ([]*userRow, error) {
	us, err := m.users(ctx, `SELECT u.id, u.kind, u.email, u.name, u.role, u.tier, u.status, u.locale, u.signature_md, u.notify_prefs
		FROM team_members tm JOIN users u ON u.id = tm.user_id WHERE tm.team_id = ? AND u.kind = 'staff' AND u.status = 'active' ORDER BY u.email`, teamID)
	if err != nil {
		return nil, err
	}
	out := us[:0]
	for _, u := range us {
		if u.effectiveTier() >= minTier {
			out = append(out, u)
		}
	}
	return out, nil
}

func (m *Module) staffByRole(ctx context.Context, roles ...string) ([]*userRow, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE kind = 'staff' AND status = 'active' AND role IN (` + strings.TrimSuffix(strings.Repeat("?,", len(roles)), ",") + `) ORDER BY email`
	args := make([]any, len(roles))
	for i, r := range roles {
		args[i] = r
	}
	return m.users(ctx, q, args...)
}

// resolver collects targets for one event, applying the global rules: the
// actor is never notified, inactive users never get mail, staff must be
// able to see the ticket, and each (template, address) appears once.
type resolver struct {
	m    *Module
	ctx  context.Context
	e    events.Event
	seen map[string]bool
	out  []Target
	err  error
	// users holds the row of every user target, so per-channel preferences
	// (email opt-out, in-app, browser) are applied after one resolution.
	users map[string]*userRow
}

func (r *resolver) addUser(key string, u *userRow) {
	if u == nil || u.Status != "active" {
		return
	}
	if r.e.ActorID != "" && u.ID == r.e.ActorID {
		return
	}
	isCustomerKey := strings.HasPrefix(key, "customer.")
	if isCustomerKey != (u.Kind == "customer") {
		return // a customer template never goes to staff and vice versa
	}
	if u.Kind == "staff" && !r.m.vis.StaffCanSee(r.ctx, u.ID, u.Role, u.effectiveTier(), r.e.Snapshot) {
		return
	}
	r.users[u.ID] = u
	r.add(key, u.recipient())
}

func (r *resolver) addMailbox(key, addr, name string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return
	}
	r.add(key, Recipient{Addr: addr, Name: name, Audience: "staff", Mailbox: true})
}

func (r *resolver) add(key string, rc Recipient) {
	k := key + "|" + strings.ToLower(rc.Addr)
	if rc.UserID != "" {
		k = key + "|u:" + rc.UserID // address dedupe happens per channel (emailTargets)
	}
	if r.seen[k] {
		return
	}
	r.seen[k] = true
	r.out = append(r.out, Target{Key: key, R: rc})
}

func (r *resolver) userByID(id string) *userRow {
	u, err := r.m.user(r.ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && r.err == nil {
			r.err = err
		}
		return nil
	}
	return u
}

func (r *resolver) list(us []*userRow, err error) []*userRow {
	if err != nil && r.err == nil {
		r.err = err
	}
	return us
}

// teamTargets adds key for a team per its notify_mode (agents with tier ≥
// minTier and/or the team mailbox). Triage (no team) goes to supervisors and
// admins plus the global triage mailbox.
func (r *resolver) teamTargets(key, teamID string, minTier int, useMailbox bool) {
	if teamID == "" {
		for _, u := range r.list(r.m.staffByRole(r.ctx, "supervisor", "admin")) {
			r.addUser(key, u)
		}
		if useMailbox && r.m.set != nil {
			if em, err := r.m.set.Email(r.ctx); err == nil {
				r.addMailbox(key, em.TriageMailbox, "Triage")
			}
		}
		return
	}
	t, err := r.m.team(r.ctx, teamID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && r.err == nil {
			r.err = err
		}
		return
	}
	mode := t.Mode
	if !useMailbox || t.Mailbox == "" {
		mode = "agents"
	}
	if mode == "agents" || mode == "both" {
		for _, u := range r.list(r.m.teamAgents(r.ctx, teamID, minTier)) {
			r.addUser(key, u)
		}
	}
	if mode == "mailbox" || mode == "both" {
		r.addMailbox(key, t.Mailbox, t.Name)
	}
}

// Recipients implements the events_spec §2 matrix for one event's email: the
// resolved targets minus the users' email opt-outs. It reads only the event
// snapshot plus current users/teams/memberships, so a later reassignment
// never changes the recipients of an earlier event.
func (m *Module) Recipients(ctx context.Context, e events.Event) ([]Target, error) {
	all, users, err := m.resolve(ctx, e)
	return emailTargets(all, users), err
}

// emailTargets drops the targets whose user opted out of that template's
// email (NOT-015). Mailbox targets always stay.
func emailTargets(all []Target, users map[string]*userRow) []Target {
	out := make([]Target, 0, len(all))
	seen := map[string]bool{}
	for _, t := range all {
		if u := users[t.R.UserID]; u != nil && isOptional(t.Key) && slices.Contains(u.OptOut, t.Key) {
			continue
		}
		// each (template, address) is mailed once, e.g. a team mailbox that is also an agent's address
		if k := t.Key + "|" + strings.ToLower(t.R.Addr); !seen[k] {
			seen[k] = true
			out = append(out, t)
		}
	}
	return out
}

// resolve returns every target of the matrix before channel preferences,
// with the row of each user target (one resolution serves email and in-app).
func (m *Module) resolve(ctx context.Context, e events.Event) ([]Target, map[string]*userRow, error) {
	r := &resolver{m: m, ctx: ctx, e: e, seen: map[string]bool{}, users: map[string]*userRow{}}
	s := e.Snapshot
	switch e.Type {
	case events.TicketCreated:
		r.addUser(TplCustomerTicketReceived, r.userByID(s.RequesterID))
		r.teamTargets(TplStaffTicketNew, s.TeamID, s.Tier, true)
	case events.MessageCreated:
		var ref events.MessageRef
		_ = e.Decode(&ref)
		if ref.Kind != "" && ref.Kind != "public" {
			break
		}
		actor := r.userByID(e.ActorID)
		// A public reply sent by an automation rule (D-19 §A, actor id
		// "automation", no users row) is a reply to the customer.
		if (actor != nil && actor.Kind == "staff") || e.ActorID == events.ActorAutomation {
			r.addUser(TplCustomerReply, r.userByID(s.RequesterID))
			for _, w := range s.Watchers {
				if u := r.userByID(w); u != nil && u.Kind == "customer" {
					r.addUser(TplCustomerReply, u)
				}
			}
		} else {
			if s.AssigneeID != "" {
				r.addUser(TplStaffCustomerReplied, r.userByID(s.AssigneeID))
			} else {
				r.teamTargets(TplStaffCustomerReplied, s.TeamID, s.Tier, false)
			}
		}
	case events.TicketAssigned:
		var a events.Assignment
		_ = e.Decode(&a)
		to := a.To
		if to == "" {
			to = s.AssigneeID
		}
		r.addUser(TplStaffAssigned, r.userByID(to))
	case events.TicketEscalated:
		var tc events.TierChange
		_ = e.Decode(&tc)
		team, tier := tc.ToTeam, tc.ToTier
		if team == "" {
			team = s.TeamID
		}
		if tier == 0 {
			tier = s.Tier
		}
		r.teamTargets(TplStaffEscalated, team, tier, true)
	case events.TicketStatusChanged:
		var sc events.StatusChange
		_ = e.Decode(&sc)
		if sc.To == "resolved" {
			r.addUser(TplCustomerResolved, r.userByID(s.RequesterID))
		}
	case events.NoteCreated:
		var d struct {
			Mentions  []string `json:"mentions"`
			Mentioned []string `json:"mentioned"` // key written by internal/tickets
		}
		_ = e.Decode(&d)
		for _, id := range append(d.Mentions, d.Mentioned...) {
			if u := r.userByID(id); u != nil && u.Kind == "staff" {
				r.addUser(TplStaffMentioned, u)
			}
		}
	case events.SLAWarning, events.SLABreached:
		if s.AssigneeID != "" {
			r.addUser(TplStaffSLA, r.userByID(s.AssigneeID))
		} else {
			r.teamTargets(TplStaffSLA, s.TeamID, s.Tier, false)
		}
		for _, u := range r.list(r.m.staffByRole(ctx, "supervisor")) {
			r.addUser(TplStaffSLA, u)
		}
	case events.TicketChildrenResolved:
		var d struct {
			ParentID string `json:"parent_id"`
		}
		_ = e.Decode(&d)
		assignee := s.AssigneeID
		if d.ParentID != "" && d.ParentID != e.TicketID {
			var a sql.NullString
			if err := m.db.Read().QueryRowContext(ctx, `SELECT assignee_id FROM tickets WHERE id = ?`, d.ParentID).Scan(&a); err == nil {
				assignee = a.String
			} else {
				assignee = ""
			}
		}
		r.addUser(TplStaffChildrenResolved, r.userByID(assignee))
	}
	return r.out, r.users, r.err
}

// isOptional: per-user opt-out is allowed except for auth.* and customer.reply.
func isOptional(key string) bool {
	return !Transactional(key) && key != TplCustomerReply
}

var notifyTypes = map[string]bool{
	events.TicketCreated: true, events.MessageCreated: true, events.TicketAssigned: true, events.TicketEscalated: true,
	events.TicketStatusChanged: true, events.NoteCreated: true, events.SLAWarning: true, events.SLABreached: true,
	events.TicketChildrenResolved: true,
}

func notifyFilter(e events.Event) bool { return notifyTypes[e.Type] }
