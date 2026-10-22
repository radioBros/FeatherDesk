// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// SLA (TKT-021, lifecycle §4): due dates from per-priority policies, 24×7 or
// the team's business calendar (TKT-035); pending/on_hold pause the clock and
// the paused business time extends the resolution due date.

// SLAPolicy is one row of sla_policies.
type SLAPolicy struct {
	Priority             string `json:"priority"`
	FirstResponseMinutes int    `json:"first_response_minutes"`
	ResolutionMinutes    int    `json:"resolution_minutes"`
	CalendarMode         string `json:"calendar_mode"`
}

type slaCache struct {
	mu   sync.RWMutex
	byPr map[domain.Priority]SLAPolicy // global defaults (project_id NULL)
	// byProject holds the per-project overrides (D-20); a priority missing
	// here falls back to byPr.
	byProject map[string]map[domain.Priority]SLAPolicy
}

// slaPolicies returns the global SLA policies (project_id NULL).
func (s *Service) slaPolicies(ctx context.Context) (map[domain.Priority]SLAPolicy, error) {
	if err := s.loadSLA(ctx); err != nil {
		return nil, err
	}
	s.sla.mu.RLock()
	defer s.sla.mu.RUnlock()
	return s.sla.byPr, nil
}

// projectSLAPolicies returns the overrides of one project (may be empty).
func (s *Service) projectSLAPolicies(ctx context.Context, projectID string) (map[domain.Priority]SLAPolicy, error) {
	if err := s.loadSLA(ctx); err != nil {
		return nil, err
	}
	s.sla.mu.RLock()
	defer s.sla.mu.RUnlock()
	out := map[domain.Priority]SLAPolicy{}
	for k, v := range s.sla.byProject[projectID] {
		out[k] = v
	}
	return out, nil
}

// slaPolicyFor is the policy of a ticket: the project's override for the
// priority, else the global default (D-20).
func (s *Service) slaPolicyFor(ctx context.Context, projectID string, pr domain.Priority) (SLAPolicy, bool, error) {
	if err := s.loadSLA(ctx); err != nil {
		return SLAPolicy{}, false, err
	}
	s.sla.mu.RLock()
	defer s.sla.mu.RUnlock()
	if p, ok := s.sla.byProject[projectID][pr]; ok {
		return p, true, nil
	}
	p, ok := s.sla.byPr[pr]
	return p, ok, nil
}

func (s *Service) loadSLA(ctx context.Context) error {
	s.sla.mu.RLock()
	loaded := s.sla.byPr != nil
	s.sla.mu.RUnlock()
	if loaded {
		return nil
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT priority, first_response_minutes, resolution_minutes, calendar_mode, project_id FROM sla_policies`)
	if err != nil {
		return err
	}
	defer rows.Close()
	global := map[domain.Priority]SLAPolicy{}
	byProject := map[string]map[domain.Priority]SLAPolicy{}
	for rows.Next() {
		var p SLAPolicy
		var project store.NullString
		if err := rows.Scan(&p.Priority, &p.FirstResponseMinutes, &p.ResolutionMinutes, &p.CalendarMode, &project); err != nil {
			return err
		}
		if !project.Valid {
			global[domain.Priority(p.Priority)] = p
			continue
		}
		if byProject[project.String] == nil {
			byProject[project.String] = map[domain.Priority]SLAPolicy{}
		}
		byProject[project.String][domain.Priority(p.Priority)] = p
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.sla.mu.Lock()
	s.sla.byPr, s.sla.byProject = global, byProject
	s.sla.mu.Unlock()
	return nil
}

func (s *Service) invalidateSLA() {
	s.sla.mu.Lock()
	s.sla.byPr, s.sla.byProject = nil, nil
	s.sla.mu.Unlock()
}

// slaCalendar returns the calendar the ticket's SLA runs on (nil = 24×7).
func (s *Service) slaCalendar(ctx context.Context, t *ticketRow, pol SLAPolicy) *directory.Calendar {
	if pol.CalendarMode != "team" || t.TeamID == "" {
		return nil
	}
	cal, err := s.dir.TeamCalendar(ctx, t.TeamID)
	if err != nil {
		return nil
	}
	return cal
}

// computeDue sets both due dates from created_at, the priority's policy and
// the paused business minutes (lifecycle §4: recomputed on create, priority
// change and every resume; escalation never resets them).
func (s *Service) computeDue(ctx context.Context, t *ticketRow) error {
	pol, ok, err := s.slaPolicyFor(ctx, t.ProjectID, t.Priority)
	if err != nil {
		return err
	}
	if !ok {
		t.FirstResponseDueAt.Valid, t.ResolutionDueAt.Valid = false, false
		return nil
	}
	cal := s.slaCalendar(ctx, t, pol)
	t.FirstResponseDueAt.Time = directory.AddBusinessMinutes(cal, t.CreatedAt, pol.FirstResponseMinutes).UTC().Truncate(time.Microsecond)
	t.FirstResponseDueAt.Valid = true
	t.ResolutionDueAt.Time = directory.AddBusinessMinutes(cal, t.CreatedAt, pol.ResolutionMinutes+t.SLAPausedMinutes).UTC().Truncate(time.Microsecond)
	t.ResolutionDueAt.Valid = true
	return nil
}

func paused(st domain.TicketStatus) bool {
	return st == domain.StatusPending || st == domain.StatusOnHold
}

// slaOnStatus keeps the pause bookkeeping in step with a status change.
func (s *Service) slaOnStatus(ctx context.Context, t *ticketRow, from, to domain.TicketStatus, now time.Time) error {
	switch {
	case !paused(from) && paused(to):
		t.SLAPausedAt = store.NullTime{Time: now, Valid: true}
	case paused(from) && !paused(to) && t.SLAPausedAt.Valid:
		pol, _, err := s.slaPolicyFor(ctx, t.ProjectID, t.Priority)
		if err != nil {
			return err
		}
		cal := s.slaCalendar(ctx, t, pol)
		d := directory.BusinessDuration(cal, t.SLAPausedAt.Time, now)
		t.SLAPausedMinutes += int((d + 30*time.Second) / time.Minute)
		t.SLAPausedAt = store.NullTime{}
		if t.ResolutionDueAt.Valid {
			return s.computeDue(ctx, t)
		}
	}
	return nil
}
