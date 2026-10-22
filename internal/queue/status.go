// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"time"
)

// ConsumerStatus is one channel's view for /admin/queue (PLT-026).
type ConsumerStatus struct {
	Name     string `json:"name"`
	Durable  bool   `json:"durable"`
	Depth    int    `json:"depth"`
	Capacity int    `json:"capacity"`
	Workers  int    `json:"workers"`
	Handled  int64  `json:"handled"`
	Failed   int64  `json:"failed"`
	Spilled  int64  `json:"spilled"`
	Overflow int64  `json:"overflow"`
}

// Status is the queue snapshot served by GET /api/v1/admin/queue.
type Status struct {
	Consumers      []ConsumerStatus `json:"consumers"`
	JobsDepth      int              `json:"jobs_depth"`
	JobsCapacity   int              `json:"jobs_capacity"`
	Cursor         string           `json:"cursor"`
	ReadCursor     string           `json:"read_cursor"`
	Lag            int64            `json:"lag"`
	LastRound      *time.Time       `json:"last_round,omitempty"`
	Deferred       map[string]int64 `json:"deferred"`
	OldestRetrying *time.Time       `json:"oldest_retrying_next_run_at,omitempty"`
	Schedules      []ScheduleStatus `json:"schedules"`
}

type ScheduleStatus struct {
	Name     string `json:"name"`
	Interval string `json:"interval"`
	Runs     int64  `json:"runs"`
}

// Status reports depths, cursor lag and deferred-job counts.
func (q *Queue) Status(ctx context.Context) (Status, error) {
	s := Status{JobsDepth: len(q.jobs), JobsCapacity: cap(q.jobs), Deferred: map[string]int64{"retrying": 0, "dead": 0}}
	for _, c := range q.consumers {
		s.Consumers = append(s.Consumers, ConsumerStatus{
			Name: c.name, Durable: c.opt.Durable, Depth: len(c.ch), Capacity: cap(c.ch), Workers: c.opt.Workers,
			Handled: c.handled.Load(), Failed: c.failed.Load(), Spilled: c.spilled.Load(), Overflow: c.over.Load(),
		})
	}
	q.mu.Lock()
	s.Cursor, s.ReadCursor = q.persisted, q.readCursor
	if !q.lastRound.IsZero() {
		lr := q.lastRound.UTC()
		s.LastRound = &lr
	}
	q.mu.Unlock()
	q.schedMu.Lock()
	for _, sc := range q.schedules {
		s.Schedules = append(s.Schedules, ScheduleStatus{Name: sc.name, Interval: sc.interval.String(), Runs: sc.runs.Load()})
	}
	q.schedMu.Unlock()
	rd := q.db.Read()
	if err := rd.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_events WHERE id > ?`, s.Cursor).Scan(&s.Lag); err != nil {
		return s, err
	}
	rows, err := rd.QueryContext(ctx, `SELECT status, COUNT(*) FROM deferred_jobs GROUP BY status`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var st string
		var n int64
		if rows.Scan(&st, &n) == nil {
			s.Deferred[st] = n
		}
	}
	rows.Close()
	var raw any
	if err := rd.QueryRowContext(ctx, `SELECT MIN(next_run_at) FROM deferred_jobs WHERE status = 'retrying'`).Scan(&raw); err == nil {
		if o, ok := dbTime(raw); ok {
			s.OldestRetrying = &o
		}
	}
	return s, nil
}

// Stalled reports a dispatcher that has events waiting but has not completed a
// read round for longer than threshold (readiness probe, PLT-013).
func (q *Queue) Stalled(ctx context.Context, threshold time.Duration) bool {
	if !q.started.Load() {
		return false
	}
	q.mu.Lock()
	last, cur := q.lastRound, q.readCursor
	q.mu.Unlock()
	if !last.IsZero() && time.Since(last) < threshold {
		return false
	}
	var pending int
	if err := q.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_events WHERE id > ?`, cur).Scan(&pending); err != nil {
		return false
	}
	return pending > 0
}
