// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Macros (D-19 §A, TKT-048): one-click bundles of a reply or note, status,
// tags, assignee and priority. Personal macros belong to one agent; shared
// macros (owner NULL) are managed by supervisors and admins through
// /admin/macros and offered to every agent. A macro may be limited to one
// project (D-20); shared project macros are managed by its project admins. Applying one runs as the agent,
// in one transaction, with every part authorised like the single action.

type macroDTO struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Shared     bool      `json:"shared"`
	OwnerID    *string   `json:"owner_id"`
	ReplyKind  string    `json:"reply_kind"`
	ReplyMD    string    `json:"reply_md"`
	Status     *string   `json:"status"`
	Priority   *string   `json:"priority"`
	AddTags    []string  `json:"add_tags"`
	RemoveTags []string  `json:"remove_tags"`
	Assignee   *string   `json:"assignee"`
	ProjectID  *string   `json:"project_id"` // D-20: nil = every project
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const macroCols = `id, name, owner_id, reply_kind, reply_md, status, priority, add_tags, remove_tags, assignee, created_at, updated_at, project_id`

func scanMacro(r scanner) (*macroDTO, error) {
	var x macroDTO
	var owner, status, prio, asg, project sql.NullString
	var add, rem store.JSON[[]string]
	if err := r.Scan(&x.ID, &x.Name, &owner, &x.ReplyKind, &x.ReplyMD, &status, &prio, &add, &rem, &asg, &x.CreatedAt, &x.UpdatedAt, &project); err != nil {
		return nil, store.NotFound(err)
	}
	x.OwnerID, x.Status, x.Priority, x.Assignee = strPtrOf(owner), strPtrOf(status), strPtrOf(prio), strPtrOf(asg)
	x.ProjectID = strPtrOf(project)
	x.Shared = !owner.Valid
	x.AddTags, x.RemoveTags = add.V, rem.V
	if x.AddTags == nil {
		x.AddTags = []string{}
	}
	if x.RemoveTags == nil {
		x.RemoveTags = []string{}
	}
	x.CreatedAt, x.UpdatedAt = x.CreatedAt.UTC(), x.UpdatedAt.UTC()
	return &x, nil
}

func strPtrOf(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func listMacros(ctx context.Context, q store.Querier, where string, args ...any) ([]macroDTO, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+macroCols+` FROM macros WHERE `+where+` ORDER BY lower(name), id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []macroDTO{}
	for rows.Next() {
		m, err := scanMacro(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func loadMacro(ctx context.Context, q store.Querier, id string) (*macroDTO, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	return scanMacro(q.QueryRowContext(ctx, `SELECT `+macroCols+` FROM macros WHERE id = ?`, id))
}

// macroIn is MacroCreate.
type macroIn struct {
	Name       string   `json:"name"`
	ReplyKind  string   `json:"reply_kind"`
	ReplyMD    string   `json:"reply_md"`
	Status     *string  `json:"status"`
	Priority   *string  `json:"priority"`
	AddTags    []string `json:"add_tags"`
	RemoveTags []string `json:"remove_tags"`
	Assignee   *string  `json:"assignee"`
	ProjectID  *string  `json:"project_id"` // D-20: id or key; nil = every project
}

func normTags(field string, in []string) ([]string, []problems.FieldError) {
	out := []string{}
	for _, n := range in {
		n = strings.ToLower(strings.TrimSpace(n))
		if !tagRe.MatchString(n) {
			return nil, []problems.FieldError{problems.Field(field, "pattern", "1..40 characters of a-z, 0-9 and -")}
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if len(out) > MaxTags {
		return nil, []problems.FieldError{problems.Field(field, "max_items", "at most 20 tags")}
	}
	return out, nil
}

func (s *Service) validateMacro(ctx context.Context, in *macroIn) error {
	var errs []problems.FieldError
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		errs = append(errs, problems.Field("name", "length", "1..120 characters"))
	}
	if in.ReplyKind == "" {
		in.ReplyKind = string(domain.MessagePublic)
	}
	if in.ReplyKind != string(domain.MessagePublic) && in.ReplyKind != string(domain.MessageInternal) {
		errs = append(errs, problems.Field("reply_kind", "enum", "public or internal"))
	}
	if len(in.ReplyMD) > MaxBodyBytes || !utf8.ValidString(in.ReplyMD) {
		errs = append(errs, problems.Field("reply_md", "max_length", "at most 64 KiB of UTF-8 text"))
	}
	if strings.TrimSpace(in.ReplyMD) == "" {
		in.ReplyMD = ""
	}
	if in.Status != nil && *in.Status == "" {
		in.Status = nil
	}
	if in.Status != nil && !domain.TicketStatus(*in.Status).Valid() {
		errs = append(errs, problems.Field("status", "enum", "a ticket status"))
	}
	if in.Priority != nil && *in.Priority == "" {
		in.Priority = nil
	}
	if in.Priority != nil && !domain.Priority(*in.Priority).Valid() {
		errs = append(errs, problems.Field("priority", "enum", "low, normal, high or urgent"))
	}
	var fe []problems.FieldError
	if in.AddTags, fe = normTags("add_tags", in.AddTags); fe != nil {
		errs = append(errs, fe...)
	}
	if in.RemoveTags, fe = normTags("remove_tags", in.RemoveTags); fe != nil {
		errs = append(errs, fe...)
	}
	if in.Assignee != nil && *in.Assignee == "" {
		in.Assignee = nil
	}
	if a := in.Assignee; a != nil && *a != "me" && *a != "none" {
		st, err := loadStaff(ctx, s.db.Read(), *a)
		switch {
		case !validID(*a) || errors.Is(err, store.ErrNotFound):
			errs = append(errs, problems.Field("assignee", "staff", "me, none or a staff id"))
		case err != nil:
			return err
		case st.Status != domain.UserActive:
			errs = append(errs, problems.Field("assignee", "inactive", "the user is not active"))
		}
	}
	if in.ProjectID != nil && *in.ProjectID == "" {
		in.ProjectID = nil
	}
	if in.ProjectID != nil {
		if pr, err := s.dir.ResolveProject(ctx, *in.ProjectID); err != nil {
			errs = append(errs, problems.Field("project_id", "not_found", "unknown project"))
		} else {
			id := pr.ID
			in.ProjectID = &id
		}
	}
	if len(errs) == 0 && in.ReplyMD == "" && in.Status == nil && in.Priority == nil && len(in.AddTags) == 0 && len(in.RemoveTags) == 0 && in.Assignee == nil {
		errs = append(errs, problems.Field("body", "min_properties", "a macro needs a reply, a status, a priority, tags or an assignee"))
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	return nil
}

// saveMacro inserts (id "") or updates a macro owned by owner ("" = shared).
// p decides the project scope (D-20): a personal macro may name a project
// the owner works in, a shared one a project the caller administers.
func (s *Service) saveMacro(ctx context.Context, p *domain.Principal, id, owner, createdBy string, in macroIn) (*macroDTO, error) {
	if err := s.validateMacro(ctx, &in); err != nil {
		return nil, err
	}
	pid := ""
	if in.ProjectID != nil {
		pid = *in.ProjectID
	}
	switch {
	case owner != "" && pid != "" && !p.InProject(pid):
		return nil, problems.Validation(problems.Field("project_id", "not_allowed", "you are not a member of this project"))
	case owner == "" && authz.CanProjectConfig(p, authz.PCMacros, pid) != nil:
		if pid == "" {
			return nil, problems.Forbidden("shared macros for every project need a supervisor or admin")
		}
		return nil, problems.Forbidden("not a project admin of the macro's project")
	}
	now := s.clock()
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		if id == "" {
			id = store.NewID()
			_, err := tx.ExecContext(ctx, `INSERT INTO macros (`+macroCols+`, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, in.Name, store.Nullable(owner), in.ReplyKind, in.ReplyMD, in.Status, in.Priority, store.MustJSON(in.AddTags),
				store.MustJSON(in.RemoveTags), in.Assignee, now, now, in.ProjectID, store.Nullable(createdBy))
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE macros SET name = ?, reply_kind = ?, reply_md = ?, status = ?, priority = ?, add_tags = ?,
			remove_tags = ?, assignee = ?, project_id = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.ReplyKind, in.ReplyMD, in.Status, in.Priority, store.MustJSON(in.AddTags), store.MustJSON(in.RemoveTags), in.Assignee,
			in.ProjectID, now, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return loadMacro(ctx, s.db.Read(), id)
}

// ApplyMacro applies a macro the caller may use to a ticket, as the caller,
// in one transaction: the reply or note, then priority, tags, assignee and
// status. Each part needs the same permission as the single action; one
// forbidden part rejects everything (403) and nothing changes.
func (s *Service) ApplyMacro(ctx context.Context, p *domain.Principal, ticketID, macroID, ifMatch string) (Outcome, error) {
	mac, err := loadMacro(ctx, s.db.Read(), macroID)
	if err != nil {
		return Outcome{}, err
	}
	if !mac.Shared && *mac.OwnerID != p.UserID {
		return Outcome{}, problems.NotFound()
	}
	pol := s.Policy(ctx)
	if err := s.warm(ctx); err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		now := s.clock()
		if !authz.CanSeeTicket(p, t.view(pol, now)) {
			return problems.NotFound()
		}
		if ifMatch != "" && strings.TrimSpace(ifMatch) != t.etag() && strings.TrimSpace(ifMatch) != "*" {
			return problems.PreconditionFailed()
		}
		if mac.ProjectID != nil && *mac.ProjectID != t.ProjectID {
			return problems.Validation(problems.Field("macro_id", "wrong_project", "the macro belongs to another project"))
		}
		m := &mutation{t: t}
		need := func(a authz.Action) error { return authz.Can(p, a, t.view(pol, now)) }
		origTeam, origAssignee := t.TeamID, t.AssigneeID

		if mac.ReplyMD != "" {
			if err := s.macroReply(ctx, tx, p, pol, m, mac, now); err != nil {
				return err
			}
		}
		if mac.Priority != nil {
			if err := need(authz.ActSetPriority); err != nil {
				return err
			}
			pr := domain.Priority(*mac.Priority)
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
		if len(mac.AddTags) > 0 || len(mac.RemoveTags) > 0 {
			if err := need(authz.ActSetCategory); err != nil { // tags share the category row of §3
				return err
			}
			cur, err := ticketTags(ctx, tx, t.ID)
			if err != nil {
				return err
			}
			next := slices.Clone(cur)
			for _, n := range mac.AddTags {
				if !slices.Contains(next, n) {
					next = append(next, n)
				}
			}
			next = slices.DeleteFunc(next, func(x string) bool { return slices.Contains(mac.RemoveTags, x) })
			from, to, err := s.setTags(ctx, tx, t, next)
			if err != nil {
				return err
			}
			if !slices.Equal(from, to) {
				m.add(events.TicketTagsChanged, events.Change{From: from, To: to})
			}
		}
		if mac.Assignee != nil {
			target := *mac.Assignee
			switch target {
			case "me":
				target = p.UserID
			case "none":
				target = ""
			}
			if err := s.assign(ctx, tx, p, pol, m, target); err != nil {
				return err
			}
		}
		if mac.Status != nil {
			if err := s.staffSetStatus(ctx, tx, p, pol, m, domain.TicketStatus(*mac.Status)); err != nil {
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

// macroReply adds the macro's reply (public) or note (internal) like
// AddStaffMessage, with the canned-response placeholders rendered.
func (s *Service) macroReply(ctx context.Context, tx *store.Tx, p *domain.Principal, pol authz.TicketPolicy, m *mutation, mac *macroDTO, now time.Time) error {
	t := m.t
	kind := domain.MessageKind(mac.ReplyKind)
	act := authz.ActReply
	if kind == domain.MessageInternal {
		act = authz.ActNote
	}
	if err := authz.Can(p, act, t.view(pol, now)); err != nil {
		return err
	}
	refs, err := loadUserRefs(ctx, tx, []string{t.RequesterID, p.UserID})
	if err != nil {
		return err
	}
	name := refs[t.RequesterID].Name
	s.fillKey(ctx, t)
	body, err := validBody("reply_md", RenderCanned(mac.ReplyMD, map[string]string{"customer.name": name, "requester.name": name,
		"ticket.number": strconv.FormatInt(t.Number, 10), "ticket.ref": t.Ref(), "agent.name": refs[p.UserID].Name}))
	if err != nil {
		return err
	}
	ch := channelFor(p)
	if ch == domain.ChannelPortal {
		ch = domain.ChannelAgent
	}
	msg := &msgRow{TicketID: t.ID, AuthorID: p.UserID, Kind: kind, Channel: ch, Body: body, CreatedAt: now}
	if err := insertMessage(ctx, tx, msg); err != nil {
		return err
	}
	if kind == domain.MessageInternal {
		m.add(events.NoteCreated, map[string]any{"message_id": msg.ID, "kind": string(kind)})
		return nil
	}
	t.LastStaffActivityAt = sql.NullTime{Time: now, Valid: true}
	if !t.FirstRespondedAt.Valid {
		t.FirstRespondedAt = sql.NullTime{Time: now, Valid: true}
	}
	m.add(events.MessageCreated, events.MessageRef{MessageID: msg.ID, Kind: string(kind), Channel: string(ch)})
	if t.Status == domain.StatusNew && (mac.Status == nil || *mac.Status == string(domain.StatusOpen)) {
		if _, err := s.applyStatus(ctx, tx, t, actorStaff, domain.StatusOpen, statusOpts{}); err != nil {
			return err
		}
		m.add(events.TicketStatusChanged, statusEvent(domain.StatusNew, domain.StatusOpen))
	}
	return nil
}

// ---- HTTP ----

// agentListMacros: shared and own macros of every project the caller works
// in plus the global ones; `project` narrows to one project's (and global).
func (m *Module) agentListMacros(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	p := principal(r)
	filter := ""
	if v := r.URL.Query().Get("project"); v != "" {
		pid, err := m.svc.projectParam(r.Context(), v)
		if err != nil {
			return err
		}
		filter = pid
	}
	all, err := listMacros(r.Context(), m.svc.db.Read(), `owner_id IS NULL OR owner_id = ?`, p.UserID)
	if err != nil {
		return err
	}
	out := make([]macroDTO, 0, len(all))
	for _, mac := range all {
		if mac.ProjectID != nil && (!p.InProject(*mac.ProjectID) || (filter != "" && *mac.ProjectID != filter)) {
			continue
		}
		out = append(out, mac)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (m *Module) agentCreateMacro(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in macroIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	mac, err := m.svc.saveMacro(r.Context(), p, "", p.UserID, p.UserID, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, mac)
	return nil
}

// ownMacro loads a personal macro of the caller: another agent's is a 404,
// a shared one a 403 (managed through /admin/macros).
func (m *Module) ownMacro(r *http.Request) (*macroDTO, error) {
	mac, err := loadMacro(r.Context(), m.svc.db.Read(), urlParam(r, "id"))
	if err != nil {
		return nil, err
	}
	if mac.Shared {
		return nil, problems.Forbidden("shared macros are managed by supervisors")
	}
	if *mac.OwnerID != principal(r).UserID {
		return nil, problems.NotFound()
	}
	return mac, nil
}

func (m *Module) agentUpdateMacro(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	cur, err := m.ownMacro(r)
	if err != nil {
		return err
	}
	var in macroIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	mac, err := m.svc.saveMacro(r.Context(), principal(r), cur.ID, *cur.OwnerID, "", in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, mac)
	return nil
}

func (m *Module) agentDeleteMacro(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	cur, err := m.ownMacro(r)
	if err != nil {
		return err
	}
	return m.deleteMacro(w, r, cur.ID)
}

func (m *Module) deleteMacro(w http.ResponseWriter, r *http.Request, id string) error {
	err := m.svc.db.Tx(r.Context(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(r.Context(), `DELETE FROM macros WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// adminListMacros: shared macros; project admins see those of their
// projects (D-20), `project` narrows to one project.
func (m *Module) adminListMacros(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	p := principal(r)
	filter := ""
	if v := r.URL.Query().Get("project"); v != "" {
		pid, err := m.svc.projectParam(r.Context(), v)
		if err != nil {
			return err
		}
		filter = pid
	}
	all, err := listMacros(r.Context(), m.svc.db.Read(), `owner_id IS NULL`)
	if err != nil {
		return err
	}
	out := make([]macroDTO, 0, len(all))
	for _, mac := range all {
		pid := ""
		if mac.ProjectID != nil {
			pid = *mac.ProjectID
		}
		if (filter != "" && pid != filter) || (!p.IsSupervisorOrAdmin() && (pid == "" || !p.AdminsProject(pid))) {
			continue
		}
		out = append(out, mac)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (m *Module) adminCreateMacro(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	var in macroIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	mac, err := m.svc.saveMacro(r.Context(), principal(r), "", "", principal(r).UserID, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, mac)
	return nil
}

// sharedMacro loads a shared macro; personal ones are not reachable (404).
func (m *Module) sharedMacro(r *http.Request) (*macroDTO, error) {
	mac, err := loadMacro(r.Context(), m.svc.db.Read(), urlParam(r, "id"))
	if err != nil {
		return nil, err
	}
	if !mac.Shared {
		return nil, problems.NotFound()
	}
	p := principal(r)
	if !p.IsSupervisorOrAdmin() && (mac.ProjectID == nil || !p.AdminsProject(*mac.ProjectID)) {
		return nil, problems.NotFound()
	}
	return mac, nil
}

func (m *Module) adminUpdateMacro(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	cur, err := m.sharedMacro(r)
	if err != nil {
		return err
	}
	var in macroIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	mac, err := m.svc.saveMacro(r.Context(), principal(r), cur.ID, "", "", in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, mac)
	return nil
}

func (m *Module) adminDeleteMacro(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	cur, err := m.sharedMacro(r)
	if err != nil {
		return err
	}
	return m.deleteMacro(w, r, cur.ID)
}

func (m *Module) agentApplyMacro(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	out, err := m.svc.ApplyMacro(r.Context(), principal(r), urlParam(r, "id"), urlParam(r, "macro_id"), r.Header.Get("If-Match"))
	if err != nil {
		return err
	}
	return m.writeUpdate(w, r, out)
}
