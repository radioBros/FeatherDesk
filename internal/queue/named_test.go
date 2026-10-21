// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestAtLeastOnceIdempotentHandlers: a replay after a crash invokes the
// handler again, and an event-ID dedupe keeps the side effect single.
func TestAtLeastOnceIdempotentHandlers(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	var mu sync.Mutex
	effects := map[string]int{}
	invocations := atomic.Int64{}
	handler := func(block bool, release chan struct{}) Handler {
		return func(ctx context.Context, e events.Event) error {
			invocations.Add(1)
			mu.Lock()
			if effects[e.ID] == 0 { // idempotency key = event ID
				effects[e.ID]++
			}
			mu.Unlock()
			if block {
				<-release
			}
			return nil
		}
	}
	release := make(chan struct{})
	defer close(release)
	q1 := New(db, fastOpts())
	q1.Subscribe("notify", SubscribeOptions{Durable: true, Timeout: time.Hour}, nil, handler(true, release))
	_ = q1.Start(context.Background())
	ids := writeEvents(t, db, 1)
	waitFor(t, 3*time.Second, func() bool { return invocations.Load() == 1 })
	q1.crashed.Store(true)
	q1.cancel()
	q2 := New(db, fastOpts())
	q2.Subscribe("notify", SubscribeOptions{Durable: true}, nil, handler(false, nil))
	_ = q2.Start(context.Background())
	defer stop(t, q2)
	waitFor(t, 3*time.Second, func() bool { return invocations.Load() >= 2 })
	mu.Lock()
	defer mu.Unlock()
	if effects[ids[0]] != 1 {
		t.Fatalf("effect applied %d times", effects[ids[0]])
	}
}

func TestScheduleRunsAfterBoot(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	q := New(db, fastOpts())
	var ran atomic.Bool
	q.Every("daily", 24*time.Hour, func(context.Context) error { ran.Store(true); return nil })
	start := time.Now()
	_ = q.Start(context.Background())
	defer stop(t, q)
	// First run is within min(interval, 5s) ± jitter of start, not after 24 h.
	waitFor(t, 30*time.Second, ran.Load)
	if time.Since(start) > 25*time.Second {
		t.Fatal("first run too late")
	}
}

func TestScheduleStopsOnShutdown(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	before := runtime.NumGoroutine()
	q := New(db, fastOpts())
	for i := 0; i < 5; i++ {
		q.Every("s"+string(rune('a'+i)), 20*time.Millisecond, func(context.Context) error { return nil })
	}
	_ = q.Start(context.Background())
	time.Sleep(100 * time.Millisecond)
	stop(t, q)
	// Goroutines exit asynchronously after Stop: wait (bounded) until they are gone.
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutine leak: %d → %d", before, after)
	}
}

// TestShutdownFlushesWriteBehind / TestWriteBehindFlushOnShutdown: Stop writes pending touches.
func TestShutdownFlushesWriteBehind(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	uid := storetest.CreateCustomer(t, db, "")
	q := New(db, fastOpts())
	_ = q.Start(context.Background())
	at := store.Now()
	q.Touch("users", "last_login_at", uid, at)
	stop(t, q)
	var got time.Time
	_ = db.Read().QueryRowContext(context.Background(), `SELECT last_login_at FROM users WHERE id = ?`, uid).Scan(&got)
	if !got.Equal(at) {
		t.Fatalf("not flushed on shutdown: %v", got)
	}
}

func TestWriteBehindFlushOnShutdown(t *testing.T) { TestShutdownFlushesWriteBehind(t) }

// TestShutdownUnfinishedJobsReplayed: a first attempt still running when Stop's
// deadline passes is recorded (spilled) or replayed, never lost.
func TestShutdownUnfinishedJobsReplayed(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	var calls atomic.Int64
	block := make(chan struct{})
	q1 := New(db, fastOpts())
	q1.Subscribe("notify", SubscribeOptions{Durable: true, Timeout: 10 * time.Second}, nil, func(ctx context.Context, e events.Event) error {
		calls.Add(1)
		select {
		case <-block:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	_ = q1.Start(context.Background())
	writeEvents(t, db, 1)
	waitFor(t, 3*time.Second, func() bool { return calls.Load() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_ = q1.Stop(ctx)
	cancel()
	close(block)

	q2 := New(db, fastOpts())
	q2.Subscribe("notify", SubscribeOptions{Durable: true}, nil, func(ctx context.Context, e events.Event) error { calls.Add(1); return nil })
	_ = q2.Start(context.Background())
	defer stop(t, q2)
	waitFor(t, 5*time.Second, func() bool { return calls.Load() >= 2 })
}
