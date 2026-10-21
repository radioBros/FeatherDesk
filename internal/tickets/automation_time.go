// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"time"

	"github.com/radioBros/SimpleTickets/internal/automation"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Time-based rules (D-19 §A, TKT-046) run on the hourly scheduler with the
// conditions hours_since_update / hours_since_status (for example: pending
// for 72 h → reminder note, or close). A rule fires once per ticket until
// the ticket changes again: automation_time_runs keeps the ticket's
// updated_at right after the rule applied, and the rule is only evaluated
// again for a ticket updated later than that. Closed tickets are never
// considered.

const timeRulePage = 500

// RunTimeRules evaluates every enabled time rule against the non-closed
// tickets at now and returns the number of tickets changed.
func (s *Service) RunTimeRules(ctx context.Context, now time.Time) (int, error) {
	now = now.UTC().Truncate(time.Microsecond)
	rules, err := s.enabledRules(ctx, automation.EventTime)
	if err != nil || len(rules) == 0 {
		return 0, err
	}
	if err := s.warm(ctx); err != nil {
		return 0, err
	}
	changed, after := 0, ""
	for {
		ts, err := s.timeCandidates(ctx, rules, after, now)
		if err != nil {
			return changed, err
		}
		for _, c := range ts.due {
			ok, err := s.applyTimeRules(ctx, c, rules, now)
			if err != nil {
				return changed, err
			}
			if ok {
				changed++
			}
		}
		if ts.n < timeRulePage || ctx.Err() != nil {
			return changed, ctx.Err()
		}
		after = ts.last
	}
}

type timePage struct {
	n    int
	last string
	due  []string // tickets with at least one matching, not yet fired rule
}

// timeCandidates reads one page of non-closed tickets (reader pool) and
// keeps those where some rule matches and has not fired since the ticket's
// last update.
func (s *Service) timeCandidates(ctx context.Context, rules []*automation.Rule, after string, now time.Time) (timePage, error) {
	var pg timePage
	q := s.db.Read()
	rows, err := q.QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id > ? AND t.status <> 'closed' ORDER BY t.id LIMIT ?`, after, timeRulePage)
	if err != nil {
		return pg, err
	}
	var ts []*ticketRow
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return pg, err
		}
		ts = append(ts, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return pg, err
	}
	pg.n = len(ts)
	if pg.n == 0 {
		return pg, nil
	}
	pg.last = ts[len(ts)-1].ID
	fired, err := timeRunsFor(ctx, q, ts)
	if err != nil {
		return pg, err
	}
	for _, t := range ts {
		pending := pendingTimeRules(rules, fired[t.ID], t)
		if len(pending) == 0 {
			continue
		}
		body, err := firstPublicBody(ctx, q, t.ID)
		if err != nil {
			return pg, err
		}
		f, err := s.loadFacts(ctx, q, t, body, now, true)
		if err != nil {
			return pg, err
		}
		for _, r := range pending {
			if ok, _ := r.Evaluate(f); ok {
				pg.due = append(pg.due, t.ID)
				break
			}
		}
	}
	return pg, nil
}

// timeRunsFor loads the fired markers of a page: ticket → rule → fired_at.
func timeRunsFor(ctx context.Context, q store.Querier, ts []*ticketRow) (map[string]map[string]time.Time, error) {
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	out := map[string]map[string]time.Time{}
	rows, err := q.QueryContext(ctx, `SELECT ticket_id, rule_id, fired_at FROM automation_time_runs WHERE ticket_id IN (`+placeholders(len(ids))+`)`, anyStrings(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, rid string
		var at time.Time
		if err := rows.Scan(&tid, &rid, &at); err != nil {
			return nil, err
		}
		if out[tid] == nil {
			out[tid] = map[string]time.Time{}
		}
		out[tid][rid] = at.UTC()
	}
	return out, rows.Err()
}

// pendingTimeRules keeps the rules that have not fired since t last changed.
func pendingTimeRules(rules []*automation.Rule, fired map[string]time.Time, t *ticketRow) []*automation.Rule {
	var out []*automation.Rule
	for _, r := range rules {
		if at, ok := fired[r.ID]; ok && !t.UpdatedAt.After(at) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// applyTimeRules re-evaluates the pending rules on the locked ticket and
// applies them in one transaction, recording a marker per matching rule.
func (s *Service) applyTimeRules(ctx context.Context, ticketID string, rules []*automation.Rule, now time.Time) (bool, error) {
	changed := false
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, ticketID)
		if errors.Is(err, store.ErrNotFound) {
			return errRollback
		}
		if err != nil {
			return err
		}
		if t.Status == "closed" {
			return errRollback
		}
		fired, err := timeRunsFor(ctx, tx, []*ticketRow{t})
		if err != nil {
			return err
		}
		pending := pendingTimeRules(rules, fired[t.ID], t)
		if len(pending) == 0 {
			return errRollback
		}
		body, err := firstPublicBody(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		run := &ruleRun{s: s, tx: tx, m: &mutation{t: t}, now: now, trigger: automation.EventTime, body: body, timed: true}
		if err := run.exec(ctx, pending); err != nil {
			return err
		}
		if run.changed {
			before := t.UpdatedAt
			if err := s.flush(ctx, tx, run.m, events.ActorAutomation); err != nil {
				return err
			}
			changed = true
			// A change made by a time rule does not re-arm the rules that had
			// already fired for the ticket's previous state.
			if _, err := tx.ExecContext(ctx, `UPDATE automation_time_runs SET fired_at = ? WHERE ticket_id = ? AND fired_at >= ?`,
				t.UpdatedAt, t.ID, before); err != nil {
				return err
			}
		}
		// flush bumped updated_at: the marker is the ticket's state right
		// after this run, so only a later change re-arms the rule.
		for _, res := range run.results {
			if !res.matched {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO automation_time_runs (rule_id, ticket_id, fired_at) VALUES (?, ?, ?)
				ON CONFLICT (rule_id, ticket_id) DO UPDATE SET fired_at = excluded.fired_at`, res.rule.ID, t.ID, t.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errRollback) {
		return false, nil
	}
	return changed, err
}
