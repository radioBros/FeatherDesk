// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// coalescer buffers view and helpful-vote increments in memory and writes
// them in one transaction per flush window (KB-011, KB-012, PLT-023). A popular
// article costs zero writes per read; a crash loses at most one window,
// which is acceptable for advisory counters. updated_at is not touched, so
// public ETags do not churn.
type coalescer struct {
	db  *store.DB
	log *slog.Logger

	mu sync.Mutex
	m  map[string]*delta

	flushMu sync.Mutex
	// Flushes counts write transactions (tests assert coalescing).
	Flushes atomic.Int64
}

type delta struct{ views, yes, no int64 }

// maxPending bounds the buffer; reaching it triggers an early flush.
const maxPending = 50_000

func newCoalescer(db *store.DB, log *slog.Logger) *coalescer {
	return &coalescer{db: db, log: log, m: map[string]*delta{}}
}

func (c *coalescer) add(articleID string, views, yes, no int64) {
	c.mu.Lock()
	d, ok := c.m[articleID]
	if !ok {
		d = &delta{}
		c.m[articleID] = d
	}
	d.views += views
	d.yes += yes
	d.no += no
	full := len(c.m) >= maxPending
	c.mu.Unlock()
	if full {
		go func() { _ = c.Flush(context.Background()) }()
	}
}

func (c *coalescer) View(articleID string) { c.add(articleID, 1, 0, 0) }

func (c *coalescer) Vote(articleID string, helpful bool) {
	if helpful {
		c.add(articleID, 0, 1, 0)
	} else {
		c.add(articleID, 0, 0, 1)
	}
}

// Pending returns the buffered deltas of one article (tests, editor display).
func (c *coalescer) Pending(articleID string) (views, yes, no int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.m[articleID]; ok {
		return d.views, d.yes, d.no
	}
	return 0, 0, 0
}

// Flush writes every pending delta in a single transaction. On failure the
// deltas are merged back so the next window retries them.
func (c *coalescer) Flush(ctx context.Context) error {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	c.mu.Lock()
	batch := c.m
	c.m = map[string]*delta{}
	c.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := c.db.Tx(ctx, func(tx *store.Tx) error {
		for id, d := range batch {
			if _, err := tx.ExecContext(ctx, `UPDATE kb_articles SET views = views + ?, helpful_yes = helpful_yes + ?, helpful_no = helpful_no + ? WHERE id = ?`,
				d.views, d.yes, d.no, id); err != nil {
				return err
			}
		}
		return nil
	})
	c.Flushes.Add(1)
	if err != nil {
		c.mu.Lock()
		for id, d := range batch {
			cur, ok := c.m[id]
			if !ok {
				c.m[id] = d
				continue
			}
			cur.views += d.views
			cur.yes += d.yes
			cur.no += d.no
		}
		c.mu.Unlock()
		c.log.Warn("kb: counter flush failed; retrying next window", "err", err, "articles", len(batch))
	}
	return err
}
