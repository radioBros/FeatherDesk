// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func init() { minInterval = 10 * time.Millisecond }

func fastOpts() Options {
	return Options{Fallback: 50 * time.Millisecond, BackoffBase: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond, RetryMaxSleep: 50 * time.Millisecond, FlushEvery: time.Hour, CursorEvery: 100 * time.Millisecond}
}

func writeEvents(t *testing.T, db *store.DB, n int) []string {
	t.Helper()
	ctx := context.Background()
	tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, "")})
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		err := db.Tx(ctx, func(tx *store.Tx) error {
			e := &events.Event{TicketID: tk, Type: events.MessageCreated}
			if err := events.Write(ctx, tx, e, nil); err != nil {
				return err
			}
			ids = append(ids, e.ID)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

type recorder struct {
	mu  sync.Mutex
	ids map[string]int
	at  map[string]time.Time
}

func newRecorder() *recorder { return &recorder{ids: map[string]int{}, at: map[string]time.Time{}} }

func (r *recorder) add(id string) {
	r.mu.Lock()
	r.ids[id]++
	if _, ok := r.at[id]; !ok {
		r.at[id] = time.Now()
	}
	r.mu.Unlock()
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func deferredCount(t *testing.T, db *store.DB, status string) int {
	var n int
	if err := db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM deferred_jobs WHERE status = ?`, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func stop(t *testing.T, q *Queue) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = q.Stop(ctx)
}

// TestHappyPathZeroJobWrites: successful durable consumers cause no
// deferred_jobs writes; only coalesced cursor advances hit the writer.
func TestHappyPathZeroJobWrites(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		q := New(db, fastOpts())
		rec := newRecorder()
		q.Subscribe("notify", SubscribeOptions{Durable: true, Workers: 2}, nil, func(ctx context.Context, e events.Event) error { rec.add(e.ID); return nil })
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q)
		ids := writeEvents(t, db, 100)
		waitFor(t, 5*time.Second, func() bool { return rec.count() == 100 })
		waitFor(t, 2*time.Second, func() bool { s, _ := q.Status(context.Background()); return s.Cursor == ids[99] })
		if n := q.Stats().DeferredWrites.Load(); n != 0 {
			t.Fatalf("happy path wrote %d deferred_jobs rows", n)
		}
		if n := deferredCount(t, db, "retrying") + deferredCount(t, db, "dead"); n != 0 {
			t.Fatalf("deferred rows: %d", n)
		}
		if cw := q.Stats().CursorWrites.Load(); cw > 20 {
			t.Fatalf("cursor writes not coalesced: %d", cw)
		}
		t.Logf("100 events → %d cursor writes, 0 job writes", q.Stats().CursorWrites.Load())
	})
}

// TestCrashReplay: events whose durable first attempt never finished are
// re-dispatched after a restart (cursor stayed before them).
func TestCrashReplay(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		release := make(chan struct{})
		defer close(release)
		q1 := New(db, fastOpts())
		started := make(chan struct{}, 10)
		q1.Subscribe("notify", SubscribeOptions{Durable: true, Timeout: time.Hour}, nil, func(ctx context.Context, e events.Event) error {
			started <- struct{}{}
			<-release // hang, ignoring ctx: a "crash" mid-attempt
			return nil
		})
		if err := q1.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		ids := writeEvents(t, db, 3)
		<-started
		// Crash: stop without letting the attempt finish.
		q1.crashed.Store(true)
		q1.cancel()
		time.Sleep(50 * time.Millisecond)

		rec := newRecorder()
		q2 := New(db, fastOpts())
		q2.Subscribe("notify", SubscribeOptions{Durable: true}, nil, func(ctx context.Context, e events.Event) error { rec.add(e.ID); return nil })
		if err := q2.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q2)
		waitFor(t, 5*time.Second, func() bool { return rec.count() == 3 })
		for _, id := range ids {
			if rec.ids[id] < 1 {
				t.Fatalf("event %s not replayed", id)
			}
		}
	})
}

// TestFirstFailurePersistedOnce: one failing first attempt = exactly one
// deferred_jobs insert; the retry then succeeds and deletes it.
func TestFirstFailurePersistedOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		var calls atomic.Int64
		q := New(db, fastOpts())
		q.Subscribe("notify", SubscribeOptions{Durable: true}, nil, func(ctx context.Context, e events.Event) error {
			if calls.Add(1) == 1 {
				return errors.New("smtp 421")
			}
			return nil
		})
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q)
		writeEvents(t, db, 1)
		waitFor(t, 5*time.Second, func() bool { return calls.Load() >= 2 })
		waitFor(t, 2*time.Second, func() bool { return deferredCount(t, db, "retrying") == 0 })
		// 1 insert (first failure) + 1 delete (retry success)
		if n := q.Stats().DeferredWrites.Load(); n != 2 {
			t.Fatalf("deferred writes = %d, want 2", n)
		}
	})
}

func TestRetryBackoffThenDead(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		var calls atomic.Int64
		q := New(db, fastOpts())
		q.Subscribe("webhook", SubscribeOptions{Durable: true, MaxAttempts: 3}, nil, func(ctx context.Context, e events.Event) error {
			calls.Add(1)
			return errors.New("receiver down")
		})
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q)
		writeEvents(t, db, 1)
		waitFor(t, 5*time.Second, func() bool { return deferredCount(t, db, "dead") == 1 })
		if c := calls.Load(); c != 3 {
			t.Fatalf("attempts = %d, want 3", c)
		}
		var lastErr string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT last_error FROM deferred_jobs`).Scan(&lastErr)
		if lastErr != "receiver down" {
			t.Fatalf("last_error %q", lastErr)
		}
		// Admin retry brings it back.
		var id string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT id FROM deferred_jobs`).Scan(&id)
		if err := q.Retry(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 5*time.Second, func() bool { return calls.Load() >= 4 })
	})
}

// TestBackpressureNeverDrops: a blocked durable consumer with a tiny channel
// spills the overflow to deferred_jobs; after unblocking every event arrives.
func TestBackpressureNeverDrops(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		gate := make(chan struct{})
		rec := newRecorder()
		q := New(db, fastOpts())
		q.Subscribe("notify", SubscribeOptions{Durable: true, Capacity: 4, Timeout: 5 * time.Second}, nil, func(ctx context.Context, e events.Event) error {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
			rec.add(e.ID)
			return nil
		})
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q)
		ids := writeEvents(t, db, 200)
		waitFor(t, 5*time.Second, func() bool { s, _ := q.Status(context.Background()); return s.ReadCursor == ids[199] })
		if q.byName["notify"].spilled.Load() == 0 {
			t.Fatal("expected spills while blocked")
		}
		close(gate)
		waitFor(t, 10*time.Second, func() bool { return rec.count() == 200 })
		waitFor(t, 5*time.Second, func() bool { return deferredCount(t, db, "retrying") == 0 })
	})
}

// TestHungSMTPDoesNotDelayWS: a durable consumer whose handler never returns
// must not slow the realtime fan-out (no head-of-line blocking).
func TestHungSMTPDoesNotDelayWS(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		hang := make(chan struct{})
		defer close(hang)
		q := New(db, fastOpts())
		q.Subscribe("notify", SubscribeOptions{Durable: true, Workers: 2, Capacity: 2, Timeout: 300 * time.Millisecond}, nil, func(ctx context.Context, e events.Event) error {
			<-hang
			return nil
		})
		rt := newRecorder()
		q.Subscribe("realtime", SubscribeOptions{}, nil, func(ctx context.Context, e events.Event) error { rt.add(e.ID); return nil })
		if err := q.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer stop(t, q)
		var lat []time.Duration
		for i := 0; i < 20; i++ {
			before := time.Now()
			ids := writeEvents(t, db, 1)
			waitFor(t, 2*time.Second, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); _, ok := rt.at[ids[0]]; return ok })
			rt.mu.Lock()
			lat = append(lat, rt.at[ids[0]].Sub(before))
			rt.mu.Unlock()
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		p95 := lat[len(lat)*95/100-1]
		// Head-of-line blocking would delay every frame by the 300 ms handler
		// timeout, so the median is a load-robust detector (a few slow outliers
		// on a starved runner do not matter).
		if med := lat[len(lat)/2]; med > 250*time.Millisecond {
			t.Fatalf("WS median latency %s with hung SMTP (p95 %s)", med, p95)
		}
		// The cursor still advances because stuck first attempts time out and spill.
		waitFor(t, 5*time.Second, func() bool { return deferredCount(t, db, "retrying") > 0 })
		t.Logf("WS p95 %s while notify hangs", p95)
	})
}

// TestNoPollUnderOneSecond: idle, the dispatcher reads only on the fallback timer.
func TestNoPollUnderOneSecond(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, Options{}) // production defaults: 5 s fallback
	q.Subscribe("realtime", SubscribeOptions{}, nil, func(context.Context, events.Event) error { return nil })
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	// Let start-up reads finish (a starved runner may run them late): wait
	// until the counter has been stable for a while, then measure.
	base := q.Stats().Reads.Load()
	for stable := 0; stable < 6; {
		time.Sleep(50 * time.Millisecond)
		if n := q.Stats().Reads.Load(); n != base {
			base, stable = n, 0
		} else {
			stable++
		}
	}
	time.Sleep(1500 * time.Millisecond)
	if d := q.Stats().Reads.Load() - base; d > 0 {
		t.Fatalf("idle dispatcher read %d times in 1.5 s", d)
	}
}

func TestEnqueueJobsAndSpill(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, fastOpts())
	var got atomic.Int64
	q.Handle("mail.verify", func(ctx context.Context, p json.RawMessage) error {
		var v struct{ To string }
		_ = json.Unmarshal(p, &v)
		if v.To == "a@b.c" {
			got.Add(1)
		}
		return nil
	})
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	if err := q.Enqueue(context.Background(), "mail.verify", map[string]string{"To": "a@b.c"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), "unknown.kind", nil); err == nil {
		t.Fatal("unknown kind must error")
	}
	waitFor(t, 2*time.Second, func() bool { return got.Load() == 1 })
}

func TestScheduleNoOverlap(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, fastOpts())
	var running, maxRunning, runs atomic.Int64
	q.Every("slow", 20*time.Millisecond, func(ctx context.Context) error {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		running.Add(-1)
		runs.Add(1)
		return nil
	})
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	waitFor(t, 3*time.Second, func() bool { return runs.Load() >= 3 })
	if maxRunning.Load() != 1 {
		t.Fatalf("schedule overlapped: %d concurrent", maxRunning.Load())
	}
}

func TestScheduleMinIntervalEnforced(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, fastOpts())
	defer func() {
		if recover() == nil {
			t.Fatal("interval below minimum must panic")
		}
	}()
	q.Every("too-fast", time.Millisecond, func(context.Context) error { return nil })
}

// TestWriteBehindCoalesces: many touches → one transaction, latest value wins.
func TestWriteBehindCoalesces(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		q := New(db, fastOpts())
		base := store.Now()
		for i := 0; i < 500; i++ {
			q.Touch("users", "last_login_at", uid, base.Add(time.Duration(i)*time.Second))
		}
		if err := q.FlushWriteBehind(ctx); err != nil {
			t.Fatal(err)
		}
		if f := q.WriteBehindFlushes(); f != 1 {
			t.Fatalf("flushes = %d, want 1", f)
		}
		var got time.Time
		if err := db.Read().QueryRowContext(ctx, `SELECT last_login_at FROM users WHERE id = ?`, uid).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(base.Add(499 * time.Second)) {
			t.Fatalf("got %s want latest", got)
		}
		// An older touch never moves the value back.
		q.Touch("users", "last_login_at", uid, base)
		_ = q.FlushWriteBehind(ctx)
		_ = db.Read().QueryRowContext(ctx, `SELECT last_login_at FROM users WHERE id = ?`, uid).Scan(&got)
		if !got.Equal(base.Add(499 * time.Second)) {
			t.Fatal("older touch regressed the value")
		}
	})
}

func TestNonDurableOverflowCallsHook(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	gate := make(chan struct{})
	var overflow atomic.Int64
	q := New(db, fastOpts())
	q.Subscribe("realtime", SubscribeOptions{Capacity: 1, OnOverflow: func(events.Event) { overflow.Add(1) }}, nil, func(ctx context.Context, e events.Event) error {
		<-gate
		return nil
	})
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	defer close(gate)
	ids := writeEvents(t, db, 20)
	waitFor(t, 3*time.Second, func() bool { s, _ := q.Status(context.Background()); return s.ReadCursor == ids[19] })
	if overflow.Load() == 0 {
		t.Fatal("overflow hook not called")
	}
	// Non-durable work never holds the cursor back.
	waitFor(t, 3*time.Second, func() bool { s, _ := q.Status(context.Background()); return s.Cursor == ids[19] })
}

func TestStatus(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, fastOpts())
	q.Subscribe("notify", SubscribeOptions{Durable: true}, nil, func(context.Context, events.Event) error { return nil })
	q.Every("prune", time.Second, func(context.Context) error { return nil })
	if err := q.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	s, err := q.Status(context.Background())
	if err != nil || len(s.Consumers) != 1 || s.Consumers[0].Capacity != 1024 || len(s.Schedules) != 1 {
		t.Fatalf("status %+v %v", s, err)
	}
}
