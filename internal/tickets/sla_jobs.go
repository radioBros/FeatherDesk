// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Scheduled work on the in-process scheduler (PLT-014): auto-close (TKT-020),
// the SLA breach check (TKT-022) and due-date recompute after calendar edits
// (TKT-035). Every job is idempotent, batched (≤ 500 tickets per write
// transaction) and locks its batch up front before writing events.

const jobBatch = 500

const slaRecomputeJob = "tickets.sla_recompute"

func (m *Module) registerJobs(q *queue.Queue) {
	q.Every("tickets.auto_close", time.Hour, func(ctx context.Context) error {
		_, err := m.svc.AutoClose(ctx, time.Now())
		return err
	})
	q.Every("tickets.sla_breach", time.Minute, func(ctx context.Context) error {
		_, err := m.svc.CheckSLA(ctx, time.Now())
		return err
	})
	q.Handle(slaRecomputeJob, func(ctx context.Context, payload json.RawMessage) error {
		var p struct {
			CalendarID string `json:"calendar_id"`
		}
		_ = json.Unmarshal(payload, &p)
		_, err := m.svc.RecomputeDue(ctx, p.CalendarID)
		return err
	})
	m.dir.OnCalendarChanged(func(ctx context.Context, calendarID string) {
		if err := q.Enqueue(context.WithoutCancel(ctx), slaRecomputeJob, map[string]string{"calendar_id": calendarID}); err != nil {
			m.svc.log.Warn("tickets: enqueue SLA recompute", "err", err)
		}
	})
}

// AutoClose closes tickets resolved more than auto_close_days ago, as the
// system actor (TKT-020). Re-running it is harmless.
func (s *Service) AutoClose(ctx context.Context, now time.Time) (int, error) {
	if err := s.warm(ctx); err != nil {
		return 0, err
	}
	days := s.dir.Support(ctx).AutoCloseDays
	if days <= 0 {
		return 0, nil
	}
	cutoff := now.UTC().Add(-time.Duration(days) * 24 * time.Hour)
	total := 0
	for {
		n := 0
		err := s.db.Tx(ctx, func(tx *store.Tx) error {
			ts, err := lockTickets(ctx, tx, `SELECT `+ticketCols+` FROM tickets t WHERE t.status = 'resolved' AND t.resolved_at < ? ORDER BY t.id LIMIT ?`, cutoff, jobBatch)
			if err != nil {
				return err
			}
			n = len(ts)
			for _, t := range ts {
				m := &mutation{t: t}
				if _, err := s.applyStatus(ctx, tx, t, actorSystem, domain.StatusClosed, statusOpts{}); err != nil {
					return err
				}
				m.add(events.TicketStatusChanged, statusEvent(domain.StatusResolved, domain.StatusClosed))
				if err := s.flush(ctx, tx, m, ""); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < jobBatch || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}

// slaMark is one threshold crossing.
type slaMark struct {
	which string // first_response | resolution
	level string // warning | breached
	due   time.Time
}

// slaCrossings lists the thresholds t has crossed at now and not marked yet:
// 80 % of the window → warning, 100 % → breached (lifecycle §4).
func slaCrossings(t *ticketRow, now time.Time) []slaMark {
	if !t.openish() || t.SLAPausedAt.Valid {
		return nil
	}
	var out []slaMark
	check := func(which string, due time.Time) {
		warnAt := t.CreatedAt.Add(time.Duration(float64(due.Sub(t.CreatedAt)) * 0.8))
		if !now.Before(warnAt) && t.SLAState[which+".warning"] == "" {
			out = append(out, slaMark{which, "warning", due})
		}
		if !now.Before(due) && t.SLAState[which+".breached"] == "" {
			out = append(out, slaMark{which, "breached", due})
		}
	}
	if !t.FirstRespondedAt.Valid && t.FirstResponseDueAt.Valid {
		check("first_response", t.FirstResponseDueAt.Time)
	}
	if t.ResolutionDueAt.Valid {
		check("resolution", t.ResolutionDueAt.Time)
	}
	return out
}

// CheckSLA emits sla.warning / sla.breached once per (ticket, which, level);
// the markers live in tickets.sla_state. Paused tickets are skipped. It
// writes only when a threshold was crossed (TKT-022).
func (s *Service) CheckSLA(ctx context.Context, now time.Time) (int, error) {
	now = now.UTC()
	after, emitted := "", 0
	for {
		rows, err := s.db.Read().QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id > ? AND `+openishSQL+`
			AND t.sla_paused_at IS NULL AND (t.resolution_due_at IS NOT NULL OR t.first_response_due_at IS NOT NULL)
			ORDER BY t.id LIMIT 1000`, after)
		if err != nil {
			return emitted, err
		}
		var due []string
		n := 0
		for rows.Next() {
			t, err := scanTicket(rows)
			if err != nil {
				rows.Close()
				return emitted, err
			}
			n++
			after = t.ID
			if len(slaCrossings(t, now)) > 0 {
				due = append(due, t.ID)
			}
		}
		rows.Close()
		for _, id := range due {
			k, err := s.markSLA(ctx, id, now)
			if err != nil {
				return emitted, err
			}
			emitted += k
		}
		if n < 1000 {
			return emitted, nil
		}
	}
}

func (s *Service) markSLA(ctx context.Context, id string, now time.Time) (int, error) {
	k := 0
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		marks := slaCrossings(t, now)
		if len(marks) == 0 {
			return nil
		}
		m := &mutation{t: t}
		for _, mk := range marks {
			t.SLAState[mk.which+"."+mk.level] = now.Format(time.RFC3339)
			typ := events.SLAWarning
			if mk.level == "breached" {
				typ = events.SLABreached
			}
			m.add(typ, events.SLA{Which: mk.which, DueAt: mk.due})
		}
		k = len(marks)
		return s.flush(ctx, tx, m, "")
	})
	return k, err
}

// RecomputeDue recomputes the due dates of open tickets after a calendar
// change (calendarID "" = every open ticket that has a team), ≤ 500 per tx.
func (s *Service) RecomputeDue(ctx context.Context, calendarID string) (int, error) {
	s.dir.Invalidate()
	if err := s.warm(ctx); err != nil {
		return 0, err
	}
	where, args := openishSQL+` AND t.team_id IS NOT NULL`, []any{}
	if calendarID != "" {
		where += ` AND t.team_id IN (SELECT id FROM teams WHERE calendar_id = ?)`
		args = append(args, calendarID)
	}
	after, total := "", 0
	for {
		n := 0
		err := s.db.Tx(ctx, func(tx *store.Tx) error {
			ts, err := lockTickets(ctx, tx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id > ? AND `+where+` ORDER BY t.id LIMIT ?`,
				append(append([]any{after}, args...), jobBatch)...)
			if err != nil {
				return err
			}
			n = len(ts)
			for _, t := range ts {
				after = t.ID
				if err := s.computeDue(ctx, t); err != nil {
					return err
				}
				// No event type exists for a due-date recompute; the row
				// changes only in its SLA columns (not customer-visible).
				if _, err := tx.ExecContext(ctx, `UPDATE tickets SET first_response_due_at = ?, resolution_due_at = ? WHERE id = ?`,
					nullTime(t.FirstResponseDueAt), nullTime(t.ResolutionDueAt), t.ID); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < jobBatch {
			return total, nil
		}
	}
}

// ---- SLA policies API (TKT-021) ----

func (m *Module) mountSLA(g server.Groups) {
	g.Support.Method(http.MethodGet, "/sla-policies", handler(m.listSLA))
	g.Support.Method(http.MethodPut, "/sla-policies", handler(m.putSLA))
	g.ProjectAdmin.Method(http.MethodGet, "/projects/{id}/sla-policies", handler(m.getProjectSLA))
	g.ProjectAdmin.Method(http.MethodPut, "/projects/{id}/sla-policies", handler(m.putProjectSLA))
}

func slaList(byPr map[domain.Priority]SLAPolicy) []SLAPolicy {
	out := []SLAPolicy{}
	for _, pr := range domain.Priorities {
		if p, ok := byPr[pr]; ok {
			out = append(out, p)
		}
	}
	return out
}

func (m *Module) listSLA(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResSLAPolicies, authz.OpRead) != nil {
		return problems.Forbidden("")
	}
	pols, err := m.svc.slaPolicies(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, slaList(pols))
	return nil
}

func (m *Module) putSLA(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResSLAPolicies, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	var in struct {
		Policies []SLAPolicy `json:"policies"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	var errs []problems.FieldError
	if len(in.Policies) == 0 || len(in.Policies) > 4 {
		errs = append(errs, problems.Field("policies", "length", "1..4 policies"))
	}
	errs = append(errs, validateSLAPolicies(in.Policies)...)
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	return m.putSLAApply(w, r, in.Policies)
}

// validateSLAPolicies checks a policies list (global or project override).
func validateSLAPolicies(policies []SLAPolicy) []problems.FieldError {
	var errs []problems.FieldError
	seen := map[string]bool{}
	for i, p := range policies {
		f := func(k string) string { return "policies[" + itoa64(int64(i)) + "]." + k }
		if !domain.Priority(p.Priority).Valid() || seen[p.Priority] {
			errs = append(errs, problems.Field(f("priority"), "enum", "a priority, once"))
		}
		seen[p.Priority] = true
		if p.FirstResponseMinutes < 1 || p.FirstResponseMinutes > 525600 {
			errs = append(errs, problems.Field(f("first_response_minutes"), "range", "1..525600"))
		}
		if p.ResolutionMinutes < 1 || p.ResolutionMinutes > 525600 {
			errs = append(errs, problems.Field(f("resolution_minutes"), "range", "1..525600"))
		}
		if p.FirstResponseMinutes > p.ResolutionMinutes {
			errs = append(errs, problems.Field(f("first_response_minutes"), "order", "must not exceed resolution_minutes"))
		}
		if p.CalendarMode == "" {
			policies[i].CalendarMode = "24x7"
		} else if p.CalendarMode != "24x7" && p.CalendarMode != "team" {
			errs = append(errs, problems.Field(f("calendar_mode"), "enum", "24x7 or team"))
		}
	}
	return errs
}

func (m *Module) putSLAApply(w http.ResponseWriter, r *http.Request, policies []SLAPolicy) error {
	in := struct{ Policies []SLAPolicy }{policies}
	ctx := r.Context()
	err := m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		for _, p := range in.Policies {
			if err := upsertSLA(ctx, tx, "", p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.svc.invalidateSLA()
	pols, err := m.svc.slaPolicies(ctx)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, slaList(pols))
	return nil
}

// upsertSLA writes one policy row: the global default (projectID "") or a
// project override (D-20).
func upsertSLA(ctx context.Context, tx *store.Tx, projectID string, p SLAPolicy) error {
	now := store.Now()
	var res sql.Result
	var err error
	if projectID == "" {
		res, err = tx.ExecContext(ctx, `UPDATE sla_policies SET first_response_minutes = ?, resolution_minutes = ?, calendar_mode = ?, updated_at = ?
			WHERE project_id IS NULL AND priority = ?`, p.FirstResponseMinutes, p.ResolutionMinutes, p.CalendarMode, now, p.Priority)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE sla_policies SET first_response_minutes = ?, resolution_minutes = ?, calendar_mode = ?, updated_at = ?
			WHERE project_id = ? AND priority = ?`, p.FirstResponseMinutes, p.ResolutionMinutes, p.CalendarMode, now, projectID, p.Priority)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	id := "sla-global-" + p.Priority
	if projectID != "" {
		id = store.NewID()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sla_policies (id, project_id, priority, first_response_minutes, resolution_minutes, calendar_mode, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, store.Nullable(projectID), p.Priority, p.FirstResponseMinutes, p.ResolutionMinutes, p.CalendarMode, now)
	return err
}

// ProjectSLAPolicy is one effective policy of a project (ProjectSlaPolicy):
// the override, or the inherited global default.
type ProjectSLAPolicy struct {
	SLAPolicy
	Inherited bool `json:"inherited"`
}

func (m *Module) projectSLAOut(ctx context.Context, projectID string) ([]ProjectSLAPolicy, error) {
	global, err := m.svc.slaPolicies(ctx)
	if err != nil {
		return nil, err
	}
	over, err := m.svc.projectSLAPolicies(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := []ProjectSLAPolicy{}
	for _, pr := range []domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityHigh, domain.PriorityUrgent} {
		if p, ok := over[pr]; ok {
			out = append(out, ProjectSLAPolicy{SLAPolicy: p})
		} else if p, ok := global[pr]; ok {
			out = append(out, ProjectSLAPolicy{SLAPolicy: p, Inherited: true})
		}
	}
	return out, nil
}

// adminProjectFor loads {id} for a caller who administers the project
// (D-20): unknown or foreign projects are 404.
func (m *Module) adminProjectFor(r *http.Request) (string, error) {
	p := principal(r)
	pr, err := m.svc.dir.Project(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !p.AdminsProject(pr.ID) {
		return "", problems.NotFound()
	}
	return pr.ID, nil
}

// projectConfigScope: PATs need admin to write project configuration, and
// admin or directory:read to read it (like the project routes of directory).
func projectConfigScope(p *domain.Principal, write bool) error {
	if write && !authz.HasAnyScope(p, domain.ScopeAdmin) {
		return problems.Forbidden("token lacks admin")
	}
	if !write && !authz.HasAnyScope(p, domain.ScopeAdmin, domain.ScopeDirectoryRead) {
		return problems.Forbidden("token lacks directory:read")
	}
	return nil
}

func (m *Module) getProjectSLA(w http.ResponseWriter, r *http.Request) error {
	if err := projectConfigScope(principal(r), false); err != nil {
		return err
	}
	id, err := m.adminProjectFor(r)
	if err != nil {
		return err
	}
	out, err := m.projectSLAOut(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// putProjectSLA replaces a project's overrides: listed priorities override
// the global defaults, the others inherit them ([] = inherit everything).
func (m *Module) putProjectSLA(w http.ResponseWriter, r *http.Request) error {
	if err := projectConfigScope(principal(r), true); err != nil {
		return err
	}
	id, err := m.adminProjectFor(r)
	if err != nil {
		return err
	}
	var in struct {
		Policies []SLAPolicy `json:"policies"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	var errs []problems.FieldError
	if in.Policies == nil {
		errs = append(errs, problems.Field("policies", "required", "the full override list is required ([] inherits every default)"))
	}
	if len(in.Policies) > 4 {
		errs = append(errs, problems.Field("policies", "length", "0..4 policies"))
	}
	errs = append(errs, validateSLAPolicies(in.Policies)...)
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	ctx := r.Context()
	err = m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sla_policies WHERE project_id = ?`, id); err != nil {
			return err
		}
		for _, p := range in.Policies {
			if err := upsertSLA(ctx, tx, id, p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.svc.invalidateSLA()
	out, err := m.projectSLAOut(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
