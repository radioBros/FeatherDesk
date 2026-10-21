// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// optStr is a JSON field that distinguishes "absent" from null.
type optStr struct {
	Set bool
	Val *string
}

func (o *optStr) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Val = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Val = &s
	return nil
}

func (o optStr) str() string {
	if o.Val == nil {
		return ""
	}
	return *o.Val
}

// PatchInput is TicketPatch.
type PatchInput struct {
	Status       optStr         `json:"status"`
	Priority     optStr         `json:"priority"`
	CategoryID   optStr         `json:"category_id"`
	TeamID       optStr         `json:"team_id"`
	AssigneeID   optStr         `json:"assignee_id"`
	Subject      optStr         `json:"subject"`
	Tags         *[]string      `json:"tags"`
	CustomFields map[string]any `json:"custom_fields"`
}

func (in PatchInput) empty() bool {
	return !in.Status.Set && !in.Priority.Set && !in.CategoryID.Set && !in.TeamID.Set && !in.AssigneeID.Set &&
		!in.Subject.Set && in.Tags == nil && in.CustomFields == nil
}

// pendingEvent is emitted after the row is saved, so every snapshot carries
// the final state of the change.
type pendingEvent struct {
	typ   string
	data  any
	actor string // overrides flush's actor (automatic assignment: events.ActorAutomation)
}

// mutation collects the effects of one write transaction on one ticket.
type mutation struct {
	t        *ticketRow
	events   []pendingEvent
	warnings []string
}

func (m *mutation) add(typ string, data any) {
	m.events = append(m.events, pendingEvent{typ: typ, data: data})
}

// addAs records an event written under another actor than the mutation's.
func (m *mutation) addAs(actor, typ string, data any) {
	m.events = append(m.events, pendingEvent{typ: typ, data: data, actor: actor})
}

func (s *Service) flush(ctx context.Context, tx *store.Tx, m *mutation, actor string) error {
	if len(m.events) == 0 {
		return nil
	}
	if err := s.saveTicket(ctx, tx, m.t); err != nil {
		return err
	}
	for _, e := range m.events {
		a := actor
		if e.actor != "" {
			a = e.actor
		}
		if err := s.emit(ctx, tx, m.t, a, e.typ, e.data); err != nil {
			return err
		}
	}
	return nil
}

// Outcome is the result of a ticket mutation.
type Outcome struct {
	TicketID string
	Warnings []string
}

// Patch applies TicketPatch: each field is authorised separately and the
// whole change is one transaction, so a forbidden field rejects everything
// (TKT-008). ifMatch, when set, must equal the current ETag (TKT-030).
func (s *Service) Patch(ctx context.Context, p *domain.Principal, id string, in PatchInput, ifMatch string) (Outcome, error) {
	if in.empty() {
		return Outcome{}, problems.Validation(problems.Field("body", "min_properties", "nothing to change"))
	}
	pol := s.Policy(ctx)
	var out Outcome
	if err := s.warm(ctx); err != nil {
		return Outcome{}, err
	}
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock()
		v := t.view(pol, now)
		if !authz.CanSeeTicket(p, v) {
			return problems.NotFound()
		}
		if ifMatch != "" && strings.TrimSpace(ifMatch) != t.etag() && strings.TrimSpace(ifMatch) != "*" {
			return problems.PreconditionFailed()
		}
		m := &mutation{t: t}
		need := func(a authz.Action) error { return authz.Can(p, a, t.view(pol, now)) }
		origTeam, origAssignee := t.TeamID, t.AssigneeID

		if in.Subject.Set {
			if err := need(authz.ActEditSubject); err != nil {
				return err
			}
			subj, err := validSubject(in.Subject.str())
			if err != nil {
				return err
			}
			if subj != t.Subject {
				m.add(events.TicketSubjectChanged, events.Change{From: t.Subject, To: subj})
				t.Subject = subj
			}
		}
		if in.Priority.Set {
			if err := need(authz.ActSetPriority); err != nil {
				return err
			}
			pr := domain.Priority(in.Priority.str())
			if !pr.Valid() {
				return problems.Validation(problems.Field("priority", "enum", "low, normal, high or urgent"))
			}
			if !authz.CanSetPriority(p, pr, pol) {
				return problems.Forbidden("urgent priority needs a supervisor")
			}
			if pr != t.Priority {
				m.add(events.TicketPriorityChanged, events.Change{From: string(t.Priority), To: string(pr)})
				t.Priority = pr
				if err := s.computeDue(ctx, t); err != nil {
					return err
				}
			}
		}
		if in.CategoryID.Set {
			if err := need(authz.ActSetCategory); err != nil {
				return err
			}
			cat := in.CategoryID.str()
			if cat != "" {
				if _, err := s.dir.Category(ctx, cat); err != nil {
					return problems.Validation(problems.Field("category_id", "not_found", "unknown category"))
				}
			}
			if cat != t.CategoryID {
				m.add(events.TicketCategoryChanged, events.Change{From: nilIfEmpty(t.CategoryID), To: nilIfEmpty(cat)})
				t.CategoryID = cat
			}
		}
		if in.TeamID.Set {
			if err := need(authz.ActTransfer); err != nil {
				return err
			}
			if err := s.transfer(ctx, tx, m, in.TeamID.str()); err != nil {
				return err
			}
		}
		if in.AssigneeID.Set {
			if err := s.assign(ctx, tx, p, pol, m, in.AssigneeID.str()); err != nil {
				return err
			}
		}
		if in.Tags != nil {
			if err := need(authz.ActSetCategory); err != nil { // tags share the category row of §3
				return err
			}
			from, to, err := s.setTags(ctx, tx, t, *in.Tags)
			if err != nil {
				return err
			}
			if !slices.Equal(from, to) {
				m.add(events.TicketTagsChanged, events.Change{From: from, To: to})
			}
		}
		if in.CustomFields != nil {
			if err := need(authz.ActEditFields); err != nil {
				return err
			}
			keys, err := s.applyFieldValues(ctx, tx, t, p, in.CustomFields, false)
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				m.add(events.TicketFieldsChanged, events.FieldsChange{Keys: keys})
			}
		}
		if in.Status.Set {
			to := domain.TicketStatus(in.Status.str())
			if err := s.staffSetStatus(ctx, tx, p, pol, m, to); err != nil {
				return err
			}
		}
		if err := s.maybeAutoAssign(ctx, tx, m, origTeam, origAssignee); err != nil {
			return err
		}
		if err := s.flush(ctx, tx, m, p.UserID); err != nil {
			return err
		}
		out = Outcome{TicketID: t.ID, Warnings: m.warnings}
		return nil
	})
	return out, err
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// staffSetStatus authorises and applies an explicit staff status change.
func (s *Service) staffSetStatus(ctx context.Context, tx *store.Tx, p *domain.Principal, pol authz.TicketPolicy, m *mutation, to domain.TicketStatus) error {
	t := m.t
	if !to.Valid() {
		return problems.Validation(problems.Field("status", "enum", "unknown status"))
	}
	if to == t.Status {
		return nil
	}
	act := authz.ActSetStatus
	switch {
	case t.Status == domain.StatusClosed:
		act = authz.ActReopen
	case to == domain.StatusClosed:
		act = authz.ActClose
	}
	if err := authz.Can(p, act, t.view(pol, s.clock())); err != nil {
		// A transition that is not an edge at all is a 409 with the allowed
		// targets, even when the actor also lacks the permission.
		if !CanTransition(t.Status, to) {
			return problems.InvalidTransition(string(t.Status), allowedTargets(actorOf(p), t.Status))
		}
		return err
	}
	from := t.Status
	changed, err := s.applyStatus(ctx, tx, t, actorOf(p), to, statusOpts{})
	if err != nil || !changed {
		return err
	}
	m.add(events.TicketStatusChanged, statusEvent(from, to))
	if to == domain.StatusResolved {
		if err := s.onResolved(ctx, tx, m, p.UserID); err != nil {
			return err
		}
	}
	return nil
}

// transfer moves the ticket to another team (or triage); the assignee is kept
// only while still M-4 valid in the new team (TKT-008).
func (s *Service) transfer(ctx context.Context, tx *store.Tx, m *mutation, teamID string) error {
	t := m.t
	if teamID != "" {
		if _, err := s.dir.Team(ctx, teamID); err != nil {
			return problems.Validation(problems.Field("team_id", "not_found", "unknown team"))
		}
	}
	if teamID == t.TeamID {
		return nil
	}
	m.add(events.TicketTeamChanged, events.Change{From: nilIfEmpty(t.TeamID), To: nilIfEmpty(teamID)})
	t.TeamID = teamID
	return s.revalidateAssignee(ctx, tx, m)
}

// revalidateAssignee clears an assignee that no longer satisfies M-4.
func (s *Service) revalidateAssignee(ctx context.Context, tx *store.Tx, m *mutation) error {
	t := m.t
	if t.AssigneeID == "" {
		return nil
	}
	st, err := loadStaff(ctx, tx, t.AssigneeID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil && satisfiesM4(st, t.TeamID, t.Tier) {
		return nil
	}
	m.add(events.TicketAssigned, events.Assignment{From: t.AssigneeID})
	t.AssigneeID = ""
	return nil
}

// assign handles assignee_id: an id assigns (M-4 + §3 ◐), "" unassigns.
func (s *Service) assign(ctx context.Context, tx *store.Tx, p *domain.Principal, pol authz.TicketPolicy, m *mutation, target string) error {
	t := m.t
	now := s.clock()
	if target == "" {
		if t.AssigneeID == "" {
			return nil
		}
		if err := authz.Can(p, authz.ActUnassign, t.view(pol, now)); err != nil {
			return err
		}
		m.add(events.TicketAssigned, events.Assignment{From: t.AssigneeID})
		t.AssigneeID = ""
		return nil
	}
	if err := authz.Can(p, authz.ActAssign, t.view(pol, now)); err != nil {
		return err
	}
	if target == t.AssigneeID {
		return nil
	}
	if err := s.checkAssign(ctx, tx, p, t, target); err != nil {
		return err
	}
	m.add(events.TicketAssigned, events.Assignment{From: t.AssigneeID, To: target})
	t.AssigneeID = target
	if t.Status == domain.StatusNew { // assignment opens a new ticket (lifecycle §2)
		if _, err := s.applyStatus(ctx, tx, t, actorStaff, domain.StatusOpen, statusOpts{}); err != nil {
			return err
		}
		m.add(events.TicketStatusChanged, statusEvent(domain.StatusNew, domain.StatusOpen))
	}
	return nil
}

// checkAssign enforces M-4 on the target (422 when it fails).
func (s *Service) checkAssign(ctx context.Context, tx *store.Tx, p *domain.Principal, t *ticketRow, target string) error {
	st, err := loadStaff(ctx, tx, target)
	if errors.Is(err, store.ErrNotFound) {
		return problems.Validation(problems.Field("assignee_id", "staff", "the assignee must be a staff member"))
	}
	if err != nil {
		return err
	}
	switch {
	case st.Status != domain.UserActive:
		return problems.Validation(problems.Field("assignee_id", "inactive", "the assignee is not active"))
	case t.TeamID == "" || !st.InTeam(t.TeamID):
		return problems.Validation(problems.Field("assignee_id", "team", "the assignee is not a member of the ticket's team"))
	case st.EffectiveTier() < t.Tier:
		return problems.Validation(problems.Field("assignee_id", "tier", "the assignee's tier is below the ticket's tier"))
	}
	return nil
}

var tagRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// MaxTags per ticket (TKT-024).
const MaxTags = 20

// setTags replaces the tag set (auto-creating tags) and returns old and new sets.
func (s *Service) setTags(ctx context.Context, tx *store.Tx, t *ticketRow, names []string) (from, to []string, err error) {
	set := []string{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if !tagRe.MatchString(n) {
			return nil, nil, problems.Validation(problems.Field("tags", "pattern", "1..40 characters of a-z, 0-9 and -"))
		}
		if !slices.Contains(set, n) {
			set = append(set, n)
		}
	}
	if len(set) > MaxTags {
		return nil, nil, problems.Validation(problems.Field("tags", "max_items", "at most 20 tags"))
	}
	sort.Strings(set)
	from, err = ticketTags(ctx, tx, t.ID)
	if err != nil {
		return nil, nil, err
	}
	if slices.Equal(from, set) {
		return from, set, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_tags WHERE ticket_id = ?`, t.ID); err != nil {
		return nil, nil, err
	}
	for _, n := range set {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE lower(name) = ?`, n).Scan(&id)
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			id = store.NewID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)`, id, n, s.clock()); err != nil {
				return nil, nil, err
			}
		} else if err != nil {
			return nil, nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_tags (ticket_id, tag_id) VALUES (?, ?)`, t.ID, id); err != nil {
			return nil, nil, err
		}
	}
	return from, set, nil
}

func ticketTags(ctx context.Context, q store.Querier, ticketID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT g.name FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id WHERE tt.ticket_id = ? ORDER BY g.name`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
