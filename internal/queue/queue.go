// SPDX-License-Identifier: AGPL-3.0-only

// Package queue is the in-process queue of D-11.
//
//   - The ticket_events log is the outbox: a dispatcher reads new events after a
//     cursor (reader pool, woken post-commit, 5 s fallback) and fans them out.
//   - Non-durable consumers (realtime, index) get events immediately and never
//     block the dispatcher: a full channel calls OnOverflow instead.
//   - Durable consumers (notify, webhook) get one in-memory first attempt with a
//     bounded timeout. A failure, or a full channel, is written once to
//     deferred_jobs and retried from there; the happy path writes nothing.
//   - The persisted cursor is a low-water mark: it only moves past an event
//     once every durable job for it finished its first attempt or was spilled.
//     A crash replays from the cursor (at-least-once; handlers are idempotent).
//
// It also hosts non-event jobs (Enqueue/Handle), the periodic scheduler
// (Every) and the write-behind buffer (Touch).
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Handler consumes one event.
type Handler func(ctx context.Context, e events.Event) error

// JobHandler consumes one non-event job payload.
type JobHandler func(ctx context.Context, payload json.RawMessage) error

// SubscribeOptions configures one consumer.
type SubscribeOptions struct {
	Durable     bool
	Workers     int           // default 1
	Capacity    int           // channel bound, default Options.Capacity
	Timeout     time.Duration // per-attempt timeout, default 10s
	MaxAttempts int           // durable only, default 8
	// OnOverflow is called (non-durable only) when the channel is full; the
	// event is not delivered to this consumer. Realtime sends resync, index
	// marks itself dirty.
	OnOverflow func(e events.Event)
}

// JobOptions configures a non-event job kind.
type JobOptions struct {
	Timeout     time.Duration // default 10s
	MaxAttempts int           // default 8
}

// Options tune the queue; zero values take defaults.
type Options struct {
	BatchSize     int           // events per read, default 200
	Fallback      time.Duration // dispatcher fallback timer, default 5s
	Capacity      int           // default channel bound, default 1024
	JobWorkers    int           // non-event job workers, default 2
	FlushEvery    time.Duration // write-behind flush interval, default 30s
	RetryMaxSleep time.Duration // longest retry-loop sleep, default 60s
	BackoffBase   time.Duration // first retry delay, default 30s
	BackoffMax    time.Duration // retry delay cap, default 6h
	CursorEvery   time.Duration // minimum gap between cursor writes, default 1s
	Logger        *slog.Logger
}

// minInterval is the shortest schedule (CONVENTIONS §10: no poll < 1 s).
// Tests lower it.
var minInterval = time.Second

const cursorName = "dispatch_cursor"

type consumer struct {
	name    string
	opt     SubscribeOptions
	filter  func(events.Event) bool
	h       Handler
	ch      chan events.Event
	handled atomic.Int64
	failed  atomic.Int64
	spilled atomic.Int64
	over    atomic.Int64
}

type jobKind struct {
	h   JobHandler
	opt JobOptions
}

type job struct {
	kind    string
	payload json.RawMessage
}

type schedule struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context) error
	runs     atomic.Int64
}

// Queue is safe for concurrent use. Register consumers, handlers and
// schedules before Start (schedules may also be added later).
type Queue struct {
	db  *store.DB
	opt Options
	log *slog.Logger

	consumers []*consumer
	byName    map[string]*consumer
	kinds     map[string]jobKind
	jobs      chan job

	wake      chan struct{}
	retryWake chan struct{}
	cursorCh  chan struct{}

	// low-water tracking
	mu         sync.Mutex
	entries    []entry
	persisted  string // last cursor written to the DB
	lowWater   string
	readCursor string
	lastRound  time.Time

	schedMu   sync.Mutex
	schedules []*schedule

	wb *writeBehind

	started  atomic.Bool
	crashed  atomic.Bool
	runCtx   context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup // workers + loops
	stats    Stats
	stopOnce sync.Once
}

type entry struct {
	id      string
	pending int
}

// Stats are cheap counters for tests and /metrics.
type Stats struct {
	Reads          atomic.Int64 // dispatcher reads of ticket_events
	Dispatched     atomic.Int64 // events read
	CursorWrites   atomic.Int64
	DeferredWrites atomic.Int64 // inserts + updates of deferred_jobs
	Retried        atomic.Int64
	Dead           atomic.Int64
	JobsHandled    atomic.Int64
}

// New creates an idle queue.
func New(db *store.DB, opt Options) *Queue {
	if opt.BatchSize <= 0 {
		opt.BatchSize = 200
	}
	if opt.Fallback <= 0 {
		opt.Fallback = 5 * time.Second
	}
	if opt.Capacity <= 0 {
		opt.Capacity = 1024
	}
	if opt.JobWorkers <= 0 {
		opt.JobWorkers = 2
	}
	if opt.FlushEvery <= 0 {
		opt.FlushEvery = 30 * time.Second
	}
	if opt.RetryMaxSleep <= 0 {
		opt.RetryMaxSleep = 60 * time.Second
	}
	if opt.BackoffBase <= 0 {
		opt.BackoffBase = 30 * time.Second
	}
	if opt.BackoffMax <= 0 {
		opt.BackoffMax = 6 * time.Hour
	}
	if opt.CursorEvery <= 0 {
		opt.CursorEvery = time.Second
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	q := &Queue{
		db:        db,
		opt:       opt,
		log:       opt.Logger.With("component", "queue"),
		byName:    map[string]*consumer{},
		kinds:     map[string]jobKind{},
		jobs:      make(chan job, opt.Capacity),
		wake:      make(chan struct{}, 1),
		retryWake: make(chan struct{}, 1),
		cursorCh:  make(chan struct{}, 1),
	}
	q.wb = newWriteBehind(db, q.log)
	db.OnCommit(q.Wake)
	return q
}

// Stats exposes the counters.
func (q *Queue) Stats() *Stats { return &q.stats }

// Subscribe registers a consumer. filter nil = all events. Must precede Start.
func (q *Queue) Subscribe(name string, opt SubscribeOptions, filter func(events.Event) bool, h Handler) {
	if q.started.Load() {
		panic("queue: Subscribe after Start")
	}
	if _, dup := q.byName[name]; dup {
		panic("queue: duplicate consumer " + name)
	}
	if opt.Workers <= 0 {
		opt.Workers = 1
	}
	if opt.Capacity <= 0 {
		opt.Capacity = q.opt.Capacity
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = 8
	}
	if filter == nil {
		filter = func(events.Event) bool { return true }
	}
	c := &consumer{name: name, opt: opt, filter: filter, h: h, ch: make(chan events.Event, opt.Capacity)}
	q.consumers = append(q.consumers, c)
	q.byName[name] = c
}

// Handle registers a handler for a non-event job kind (e.g. "mail.verify").
func (q *Queue) Handle(kind string, h JobHandler, opts ...JobOptions) {
	var o JobOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 8
	}
	q.mu.Lock()
	q.kinds[kind] = jobKind{h: h, opt: o}
	q.mu.Unlock()
}

// Enqueue submits a non-event job after the caller's transaction committed.
// It is best-effort in memory; a full queue or failed first attempt spills to
// deferred_jobs. A crash before the first attempt loses it (documented: the
// user retries, e.g. "resend verification email").
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	q.mu.Lock()
	_, ok := q.kinds[kind]
	q.mu.Unlock()
	if !ok {
		return fmt.Errorf("queue: no handler for job kind %q", kind)
	}
	select {
	case q.jobs <- job{kind: kind, payload: b}:
		return nil
	default:
		return q.spill(ctx, kind, b, 0, "")
	}
}

// Wake nudges the dispatcher (called by the store's post-commit hook).
func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Start launches the dispatcher, workers, retry loop, write-behind flusher
// and schedules. It loads the persisted cursor first.
func (q *Queue) Start(ctx context.Context) error {
	if !q.started.CompareAndSwap(false, true) {
		return errors.New("queue: already started")
	}
	cur, err := q.loadCursor(ctx)
	if err != nil {
		return fmt.Errorf("queue: load cursor: %w", err)
	}
	q.readCursor, q.lowWater, q.persisted = cur, cur, cur
	q.runCtx, q.cancel = context.WithCancel(context.WithoutCancel(ctx))
	for _, c := range q.consumers {
		for i := 0; i < c.opt.Workers; i++ {
			q.wg.Add(1)
			go q.worker(c)
		}
	}
	for i := 0; i < q.opt.JobWorkers; i++ {
		q.wg.Add(1)
		go q.jobWorker()
	}
	q.wg.Add(4)
	go q.dispatchLoop()
	go q.cursorLoop()
	go q.retryLoop()
	go q.flushLoop()
	q.schedMu.Lock()
	for _, s := range q.schedules {
		q.startSchedule(s)
	}
	q.schedMu.Unlock()
	return nil
}

// Stop stops reading, gives in-flight attempts until ctx's deadline, persists
// the low-water cursor and flushes write-behind. Unfinished events replay on
// the next start.
func (q *Queue) Stop(ctx context.Context) error {
	if !q.started.Load() {
		return nil
	}
	var err error
	q.stopOnce.Do(func() {
		q.cancel()
		done := make(chan struct{})
		go func() { q.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			q.log.Warn("queue: stop deadline reached with work in flight; it will replay")
		}
		fctx := context.WithoutCancel(ctx)
		if e := q.writeCursor(fctx); e != nil {
			err = e
		}
		if e := q.wb.Flush(fctx); e != nil && err == nil {
			err = e
		}
	})
	return err
}

// ---- dispatcher ----

func (q *Queue) dispatchLoop() {
	defer q.wg.Done()
	t := time.NewTimer(q.opt.Fallback)
	defer t.Stop()
	for {
		q.round(q.runCtx)
		select {
		case <-q.runCtx.Done():
			return
		case <-q.wake:
		case <-t.C:
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(q.opt.Fallback)
	}
}

func (q *Queue) round(ctx context.Context) {
	for ctx.Err() == nil {
		q.mu.Lock()
		cur := q.readCursor
		q.mu.Unlock()
		evs, err := events.ReadAfter(ctx, q.db.Read(), cur, q.opt.BatchSize)
		q.stats.Reads.Add(1)
		if err != nil {
			if ctx.Err() == nil {
				q.log.Error("queue: read events", "err", err)
			}
			return
		}
		for _, e := range evs {
			if err := q.dispatch(ctx, e); err != nil {
				q.log.Error("queue: dispatch stalled; will retry", "event", e.ID, "err", err)
				return
			}
			q.mu.Lock()
			q.readCursor = e.ID
			q.mu.Unlock()
			q.stats.Dispatched.Add(1)
		}
		q.mu.Lock()
		q.lastRound = time.Now()
		q.mu.Unlock()
		if len(evs) < q.opt.BatchSize {
			return
		}
	}
}

func (q *Queue) dispatch(ctx context.Context, e events.Event) error {
	var durable []*consumer
	for _, c := range q.consumers {
		if !c.filter(e) {
			continue
		}
		if c.opt.Durable {
			durable = append(durable, c)
			continue
		}
		select {
		case c.ch <- e:
		default:
			c.over.Add(1)
			if c.opt.OnOverflow != nil {
				c.opt.OnOverflow(e)
			}
		}
	}
	q.addEntry(e.ID, len(durable))
	for _, c := range durable {
		select {
		case c.ch <- e:
		default:
			b, _ := json.Marshal(e)
			if err := q.spill(ctx, "event:"+c.name, b, 0, ""); err != nil {
				// Leave pending: the cursor stays before this event so it replays.
				return err
			}
			c.spilled.Add(1)
			q.done(e.ID)
		}
	}
	return nil
}

// ---- low-water mark ----

func (q *Queue) addEntry(id string, pending int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) == 0 && pending == 0 {
		q.lowWater = id
		q.signalCursor()
		return
	}
	if n := len(q.entries); n > 0 && pending == 0 && q.entries[n-1].pending == 0 {
		q.entries[n-1].id = id // merge runs of finished events
		return
	}
	q.entries = append(q.entries, entry{id: id, pending: pending})
}

func (q *Queue) done(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.entries {
		if q.entries[i].id == id {
			q.entries[i].pending--
			break
		}
	}
	advanced := false
	for len(q.entries) > 0 && q.entries[0].pending <= 0 {
		q.lowWater = q.entries[0].id
		q.entries = q.entries[1:]
		advanced = true
	}
	if advanced {
		q.signalCursor()
	}
}

func (q *Queue) signalCursor() {
	select {
	case q.cursorCh <- struct{}{}:
	default:
	}
}

// cursorLoop persists the low-water mark at most once per CursorEvery, so a
// stream of single-event commits costs one cursor write per second, not one
// per event. A crash replays at most that window (handlers are idempotent).
func (q *Queue) cursorLoop() {
	defer q.wg.Done()
	for {
		select {
		case <-q.runCtx.Done():
			return
		case <-q.cursorCh:
			if err := q.writeCursor(q.runCtx); err != nil && q.runCtx.Err() == nil {
				q.log.Error("queue: write cursor", "err", err)
			}
			t := time.NewTimer(q.opt.CursorEvery)
			select {
			case <-q.runCtx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}

func (q *Queue) writeCursor(ctx context.Context) error {
	q.mu.Lock()
	lw := q.lowWater
	same := lw == q.persisted
	q.mu.Unlock()
	if same {
		return nil
	}
	err := q.db.Tx(ctx, func(tx *store.Tx) error { return store.SetCursor(ctx, tx, cursorName, lw) })
	if err == nil {
		q.stats.CursorWrites.Add(1)
		q.mu.Lock()
		q.persisted = lw
		q.mu.Unlock()
	}
	return err
}

func (q *Queue) loadCursor(ctx context.Context) (string, error) {
	v, err := store.Cursor(ctx, q.db.Read(), cursorName)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// ---- workers ----

func (q *Queue) worker(c *consumer) {
	defer q.wg.Done()
	for {
		select {
		case <-q.runCtx.Done():
			return
		case e := <-c.ch:
			err := q.attempt(q.runCtx, c.opt.Timeout, func(ctx context.Context) error { return c.h(ctx, e) })
			if q.crashed.Load() {
				return // simulated crash (tests): no spill, no cursor progress
			}
			if err == nil {
				c.handled.Add(1)
			} else {
				c.failed.Add(1)
				if c.opt.Durable {
					b, _ := json.Marshal(e)
					if perr := q.spill(context.WithoutCancel(q.runCtx), "event:"+c.name, b, 1, err.Error()); perr != nil {
						q.log.Error("queue: persist failed job; will replay", "consumer", c.name, "event", e.ID, "err", perr)
						continue // not done: cursor stays before this event
					}
				} else {
					q.log.Warn("queue: consumer failed", "consumer", c.name, "event", e.ID, "err", err)
				}
			}
			if c.opt.Durable {
				q.done(e.ID)
			}
		}
	}
}

func (q *Queue) jobWorker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.runCtx.Done():
			return
		case j := <-q.jobs:
			q.mu.Lock()
			k, ok := q.kinds[j.kind]
			q.mu.Unlock()
			if !ok {
				continue
			}
			err := q.attempt(q.runCtx, k.opt.Timeout, func(ctx context.Context) error { return k.h(ctx, j.payload) })
			if err != nil {
				if perr := q.spill(context.WithoutCancel(q.runCtx), j.kind, j.payload, 1, err.Error()); perr != nil {
					q.log.Error("queue: job lost", "kind", j.kind, "err", perr)
				}
				continue
			}
			q.stats.JobsHandled.Add(1)
		}
	}
}

// attempt runs fn with a timeout and turns panics into errors. A handler that
// ignores ctx still frees the caller at the deadline (the goroutine finishes
// on its own), so a hung SMTP server cannot pin a worker forever.
func (q *Queue) attempt(parent context.Context, timeout time.Duration, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	res := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				res <- fmt.Errorf("panic: %v", r)
			}
		}()
		res <- fn(ctx)
	}()
	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		return fmt.Errorf("attempt timed out after %s: %w", timeout, ctx.Err())
	}
}

// ---- deferred jobs ----

func (q *Queue) spill(ctx context.Context, kind string, payload []byte, attempts int, lastErr string) error {
	next := store.Now()
	if attempts > 0 {
		next = next.Add(q.backoff(attempts))
	}
	err := q.db.Tx(ctx, func(tx *store.Tx) error {
		now := store.Now()
		_, err := tx.ExecContext(ctx, `INSERT INTO deferred_jobs (id, kind, payload, attempts, next_run_at, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'retrying', ?, ?, ?)`,
			store.NewID(), kind, string(payload), attempts, next, trunc(lastErr), now, now)
		return err
	})
	if err == nil {
		q.stats.DeferredWrites.Add(1)
		select {
		case q.retryWake <- struct{}{}:
		default:
		}
	}
	return err
}

func (q *Queue) backoff(attempts int) time.Duration {
	d := q.opt.BackoffBase
	for i := 1; i < attempts && d < q.opt.BackoffMax; i++ {
		d *= 2
	}
	d = min(d, q.opt.BackoffMax)
	jitter := 0.8 + 0.4*rand.Float64() // ±20 %
	return time.Duration(float64(d) * jitter)
}

func trunc(s string) string {
	if len(s) > 2000 {
		s = s[:2000]
	}
	return s
}

type deferredRow struct {
	id       string
	kind     string
	payload  string
	attempts int
}

func (q *Queue) retryLoop() {
	defer q.wg.Done()
	for {
		sleep := q.retryOnce(q.runCtx)
		t := time.NewTimer(sleep)
		select {
		case <-q.runCtx.Done():
			t.Stop()
			return
		case <-q.retryWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// retryOnce processes due deferred jobs and returns how long to sleep.
func (q *Queue) retryOnce(ctx context.Context) time.Duration {
	for ctx.Err() == nil {
		rows, err := q.db.Read().QueryContext(ctx, `SELECT id, kind, payload, attempts FROM deferred_jobs WHERE status = 'retrying' AND next_run_at <= ? ORDER BY next_run_at LIMIT 50`, store.Now())
		if err != nil {
			if ctx.Err() == nil {
				q.log.Error("queue: read deferred", "err", err)
			}
			return q.opt.RetryMaxSleep
		}
		var due []deferredRow
		for rows.Next() {
			var r deferredRow
			if err := rows.Scan(&r.id, &r.kind, &r.payload, &r.attempts); err == nil {
				due = append(due, r)
			}
		}
		rows.Close()
		if len(due) == 0 {
			break
		}
		for _, r := range due {
			if ctx.Err() != nil {
				return 0
			}
			q.runDeferred(ctx, r)
		}
		if len(due) < 50 {
			break
		}
	}
	// Sleep until the next due job (bounded), so an idle queue costs one
	// query per RetryMaxSleep and nothing more.
	var raw any
	if err := q.db.Read().QueryRowContext(ctx, `SELECT MIN(next_run_at) FROM deferred_jobs WHERE status = 'retrying'`).Scan(&raw); err != nil {
		return q.opt.RetryMaxSleep
	}
	next, ok := dbTime(raw)
	if !ok {
		return q.opt.RetryMaxSleep
	}
	d := time.Until(next)
	return max(min(d, q.opt.RetryMaxSleep), minInterval)
}

func (q *Queue) runDeferred(ctx context.Context, r deferredRow) {
	var run func(ctx context.Context) error
	timeout, maxAttempts := 10*time.Second, 8
	if name, ok := cutPrefix(r.kind, "event:"); ok {
		c := q.byName[name]
		if c != nil {
			var e events.Event
			if err := json.Unmarshal([]byte(r.payload), &e); err == nil {
				run = func(ctx context.Context) error { return c.h(ctx, e) }
				timeout, maxAttempts = c.opt.Timeout, c.opt.MaxAttempts
			}
		}
	} else {
		q.mu.Lock()
		k, ok := q.kinds[r.kind]
		q.mu.Unlock()
		if ok {
			payload := json.RawMessage(r.payload)
			run = func(ctx context.Context) error { return k.h(ctx, payload) }
			timeout, maxAttempts = k.opt.Timeout, k.opt.MaxAttempts
		}
	}
	if run == nil {
		q.updateDeferred(ctx, r.id, r.attempts, "dead", "no handler for kind "+r.kind)
		q.stats.Dead.Add(1)
		return
	}
	q.stats.Retried.Add(1)
	err := q.attempt(ctx, timeout, run)
	if err == nil {
		if e := q.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM deferred_jobs WHERE id = ?`, r.id)
			return err
		}); e == nil {
			q.stats.DeferredWrites.Add(1)
		}
		return
	}
	attempts := r.attempts + 1
	if attempts >= maxAttempts {
		q.updateDeferred(ctx, r.id, attempts, "dead", err.Error())
		q.stats.Dead.Add(1)
		q.log.Warn("queue: job dead", "kind", r.kind, "id", r.id, "err", err)
		return
	}
	q.updateDeferred(ctx, r.id, attempts, "retrying", err.Error())
}

func (q *Queue) updateDeferred(ctx context.Context, id string, attempts int, status, lastErr string) {
	next := store.Now().Add(q.backoff(max(attempts, 1)))
	err := q.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE deferred_jobs SET attempts = ?, status = ?, last_error = ?, next_run_at = ?, updated_at = ? WHERE id = ?`, attempts, status, trunc(lastErr), next, store.Now(), id)
		return err
	})
	if err == nil {
		q.stats.DeferredWrites.Add(1)
	}
}

// Retry moves a dead (or retrying) job back to run now (admin "retry" button).
func (q *Queue) Retry(ctx context.Context, id string) error {
	var n int64
	err := q.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE deferred_jobs SET status = 'retrying', next_run_at = ?, updated_at = ? WHERE id = ?`, store.Now(), store.Now(), id)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	select {
	case q.retryWake <- struct{}{}:
	default:
	}
	return nil
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return s, false
}

// ---- scheduler ----

// Every runs fn every interval (±10 % jitter), once shortly after start, never
// overlapping itself. interval < 1 s panics (CONVENTIONS §10).
func (q *Queue) Every(name string, interval time.Duration, fn func(ctx context.Context) error) {
	if interval < minInterval {
		panic(fmt.Sprintf("queue: schedule %q interval %s below minimum %s", name, interval, minInterval))
	}
	s := &schedule{name: name, interval: interval, fn: fn}
	q.schedMu.Lock()
	q.schedules = append(q.schedules, s)
	q.schedMu.Unlock()
	if q.started.Load() {
		q.startSchedule(s)
	}
}

func (q *Queue) startSchedule(s *schedule) {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		first := min(s.interval, 5*time.Second)
		t := time.NewTimer(jitter(first))
		defer t.Stop()
		for {
			select {
			case <-q.runCtx.Done():
				return
			case <-t.C:
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						q.log.Error("queue: schedule panic", "schedule", s.name, "panic", r)
					}
				}()
				if err := s.fn(q.runCtx); err != nil && q.runCtx.Err() == nil {
					q.log.Warn("queue: schedule failed", "schedule", s.name, "err", err)
				}
			}()
			s.runs.Add(1)
			t.Reset(jitter(s.interval))
		}
	}()
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.9 + 0.2*rand.Float64()))
}

// ---- write-behind ----

func (q *Queue) flushLoop() {
	defer q.wg.Done()
	t := time.NewTicker(q.opt.FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-q.runCtx.Done():
			return
		case <-t.C:
			if err := q.wb.Flush(q.runCtx); err != nil && q.runCtx.Err() == nil {
				q.log.Warn("queue: write-behind flush", "err", err)
			}
		}
	}
}

// Touch records that column of table row id should become at least t. Values
// are coalesced in memory and written in one transaction every FlushEvery and
// on Stop (PLT-023). Losing up to one interval on a crash is acceptable.
func (q *Queue) Touch(table, column, id string, t time.Time) { q.wb.Touch(table, column, id, t) }

// FlushWriteBehind writes pending touches now (tests, shutdown).
func (q *Queue) FlushWriteBehind(ctx context.Context) error { return q.wb.Flush(ctx) }

var ident = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type wbKey struct{ table, column, id string }

type writeBehind struct {
	db  *store.DB
	log *slog.Logger
	mu  sync.Mutex
	m   map[wbKey]time.Time
	// Flushes counts transactions (tests assert coalescing).
	Flushes atomic.Int64
}

const wbMaxEntries = 100_000

func newWriteBehind(db *store.DB, log *slog.Logger) *writeBehind {
	return &writeBehind{db: db, log: log, m: map[wbKey]time.Time{}}
}

func (w *writeBehind) Touch(table, column, id string, t time.Time) {
	if !ident.MatchString(table) || !ident.MatchString(column) || id == "" {
		w.log.Error("queue: write-behind: invalid identifier", "table", table, "column", column)
		return
	}
	k := wbKey{table, column, id}
	w.mu.Lock()
	if old, ok := w.m[k]; !ok || t.After(old) {
		w.m[k] = t.UTC().Truncate(time.Microsecond)
	}
	full := len(w.m) >= wbMaxEntries
	w.mu.Unlock()
	if full {
		go func() { _ = w.Flush(context.Background()) }()
	}
}

func (w *writeBehind) Flush(ctx context.Context) error {
	w.mu.Lock()
	if len(w.m) == 0 {
		w.mu.Unlock()
		return nil
	}
	batch := w.m
	w.m = map[wbKey]time.Time{}
	w.mu.Unlock()
	err := w.db.Tx(ctx, func(tx *store.Tx) error {
		for k, t := range batch {
			q := `UPDATE ` + k.table + ` SET ` + k.column + ` = ? WHERE id = ? AND (` + k.column + ` IS NULL OR ` + k.column + ` < ?)`
			if _, err := tx.ExecContext(ctx, q, t, k.id, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Put the values back so the next flush retries them.
		w.mu.Lock()
		for k, t := range batch {
			if old, ok := w.m[k]; !ok || t.After(old) {
				w.m[k] = t
			}
		}
		w.mu.Unlock()
		return err
	}
	w.Flushes.Add(1)
	return nil
}

// WriteBehindFlushes is the number of flush transactions so far.
func (q *Queue) WriteBehindFlushes() int64 { return q.wb.Flushes.Load() }
