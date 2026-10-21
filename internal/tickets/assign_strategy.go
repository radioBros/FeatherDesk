// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Team assignment strategies (D-19 §A, TKT-043). A team's strategy picks an
// owner for its unassigned tickets on create, team change, an escalation or
// de-escalation that cleared the assignee, and unassign. Only agents allowed
// by M-4 (active, member of the team, effective tier ≥ ticket tier) and not
// marked away are candidates. The change is written as ticket.assigned under
// the automation actor, so trigger rules never re-fire on it (loop guard).

// Strategy names (teams.assignment_strategy).
const (
	StrategyManual     = "manual"
	StrategyRoundRobin = "round_robin"
	StrategyLeastBusy  = "least_busy"
)

// autoState is the in-memory automation state of the service.
type autoState struct {
	mu     sync.Mutex
	rr     map[string]string // team → last user picked by round robin
	rrSeen map[string]bool   // pointer loaded from the store
	dirty  map[string]bool   // pointer not yet written back

	rules rulesCache
}

// rrLast returns the round-robin pointer of a team, loading it lazily.
func (s *Service) rrLast(ctx context.Context, q store.Querier, teamID string) (string, error) {
	a := &s.auto
	a.mu.Lock()
	if a.rrSeen[teamID] {
		v := a.rr[teamID]
		a.mu.Unlock()
		return v, nil
	}
	a.mu.Unlock()
	var last sql.NullString
	err := q.QueryRowContext(ctx, `SELECT rr_last_user_id FROM teams WHERE id = ?`, teamID).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rr == nil {
		a.rr, a.rrSeen, a.dirty = map[string]string{}, map[string]bool{}, map[string]bool{}
	}
	if !a.rrSeen[teamID] {
		a.rr[teamID], a.rrSeen[teamID] = last.String, true
	}
	return a.rr[teamID], nil
}

// rrAdvance moves the pointer once the assignment committed.
func (s *Service) rrAdvance(teamID, userID string) {
	a := &s.auto
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rr == nil {
		a.rr, a.rrSeen, a.dirty = map[string]string{}, map[string]bool{}, map[string]bool{}
	}
	a.rr[teamID], a.rrSeen[teamID], a.dirty[teamID] = userID, true, true
}

// FlushRoundRobin writes the changed round-robin pointers back (coalesced:
// one UPDATE per team per flush, never per assignment). It runs on the
// scheduler every 30 s.
func (s *Service) FlushRoundRobin(ctx context.Context) error {
	a := &s.auto
	a.mu.Lock()
	pending := map[string]string{}
	for team := range a.dirty {
		pending[team] = a.rr[team]
	}
	a.dirty = map[string]bool{}
	a.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	teams := make([]string, 0, len(pending))
	for t := range pending {
		teams = append(teams, t)
	}
	sort.Strings(teams)
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		for _, team := range teams {
			if _, err := tx.ExecContext(ctx, `UPDATE teams SET rr_last_user_id = ? WHERE id = ?`, store.Nullable(pending[team]), team); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil { // retry on the next flush
		a.mu.Lock()
		for _, team := range teams {
			a.dirty[team] = true
		}
		a.mu.Unlock()
	}
	return err
}

// eligibleAgents lists the staff who may own t (M-4) and are not away,
// ordered by id. It is the Assignable query minus away agents.
func eligibleAgents(ctx context.Context, q store.Querier, t *ticketRow) ([]string, error) {
	if t.TeamID == "" {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT u.id FROM users u
		JOIN team_members m ON m.user_id = u.id AND m.team_id = ?
		WHERE u.kind = 'staff' AND u.status = 'active' AND u.away_since IS NULL
		  AND (u.role IN ('admin', 'supervisor') OR u.tier >= ?)
		ORDER BY u.id`, t.TeamID, t.Tier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// pickAgent chooses an owner for t by strategy; "" when nobody is eligible.
// exclude (the assignee just removed) is skipped unless it is the only one.
func (s *Service) pickAgent(ctx context.Context, q store.Querier, t *ticketRow, strategy, exclude string) (string, error) {
	cands, err := eligibleAgents(ctx, q, t)
	if err != nil || len(cands) == 0 {
		return "", err
	}
	if exclude != "" && len(cands) > 1 {
		kept := cands[:0:0]
		for _, c := range cands {
			if c != exclude {
				kept = append(kept, c)
			}
		}
		cands = kept
	}
	switch strategy {
	case StrategyRoundRobin:
		last, err := s.rrLast(ctx, q, t.TeamID)
		if err != nil {
			return "", err
		}
		for _, c := range cands { // ordered by id: the first one after the pointer
			if c > last {
				return c, nil
			}
		}
		return cands[0], nil
	case StrategyLeastBusy:
		rows, err := q.QueryContext(ctx, `SELECT assignee_id, COUNT(*) FROM tickets
			WHERE assignee_id IN (`+placeholders(len(cands))+`) AND status NOT IN ('resolved', 'closed')
			GROUP BY assignee_id`, anyStrings(cands)...)
		if err != nil {
			return "", err
		}
		load := map[string]int{}
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return "", err
			}
			load[id] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		best := cands[0]
		for _, c := range cands[1:] { // ties go to the lowest id
			if load[c] < load[best] {
				best = c
			}
		}
		return best, nil
	}
	return "", nil
}

// teamStrategy returns the team's assignment strategy from the directory
// snapshot (no read), or from q when the snapshot does not know the team yet;
// manual when unknown.
func (s *Service) teamStrategy(ctx context.Context, q store.Querier, teamID string) (string, error) {
	if teamID == "" {
		return StrategyManual, nil
	}
	if team, err := s.dir.Team(ctx, teamID); err == nil && team.AssignmentStrategy != "" {
		return team.AssignmentStrategy, nil
	}
	var st string
	err := q.QueryRowContext(ctx, `SELECT assignment_strategy FROM teams WHERE id = ?`, teamID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return StrategyManual, nil
	}
	return st, err
}

// autoAssign gives an unassigned working ticket an owner by its team's
// strategy. exclude is the assignee that was just removed.
func (s *Service) autoAssign(ctx context.Context, tx *store.Tx, m *mutation, exclude string) error {
	t := m.t
	if t.AssigneeID != "" || t.TeamID == "" || !t.openish() {
		return nil
	}
	strategy, err := s.teamStrategy(ctx, tx, t.TeamID)
	if err != nil || strategy == StrategyManual {
		return err
	}
	_, err = s.assignByStrategy(ctx, tx, m, strategy, exclude)
	return err
}

// assignByStrategy picks and sets an owner; it reports whether one was found.
func (s *Service) assignByStrategy(ctx context.Context, tx *store.Tx, m *mutation, strategy, exclude string) (bool, error) {
	uid, err := s.pickAgent(ctx, tx, m.t, strategy, exclude)
	if err != nil || uid == "" {
		return false, err
	}
	if err := s.setAssigneeAuto(ctx, tx, m, uid); err != nil {
		return false, err
	}
	if strategy == StrategyRoundRobin {
		team := m.t.TeamID
		tx.AfterCommit(func() { s.rrAdvance(team, uid) })
	}
	return true, nil
}

// setAssigneeAuto assigns uid (already M-4 checked) as the automation actor;
// a `new` ticket opens, as with any assignment (lifecycle §2).
func (s *Service) setAssigneeAuto(ctx context.Context, tx *store.Tx, m *mutation, uid string) error {
	t := m.t
	m.addAs(events.ActorAutomation, events.TicketAssigned, events.Assignment{From: t.AssigneeID, To: uid})
	t.AssigneeID = uid
	if t.Status == domain.StatusNew {
		if _, err := s.applyStatus(ctx, tx, t, actorStaff, domain.StatusOpen, statusOpts{}); err != nil {
			return err
		}
		m.addAs(events.ActorAutomation, events.TicketStatusChanged, statusEvent(domain.StatusNew, domain.StatusOpen))
	}
	return nil
}

// maybeAutoAssign runs the team strategy when a change left a working ticket
// unassigned through a team change or an unassign.
func (s *Service) maybeAutoAssign(ctx context.Context, tx *store.Tx, m *mutation, origTeam, origAssignee string) error {
	t := m.t
	if t.AssigneeID != "" || (t.TeamID == origTeam && origAssignee == "") {
		return nil
	}
	return s.autoAssign(ctx, tx, m, origAssignee)
}
