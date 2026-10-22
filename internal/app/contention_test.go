// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/radioBros/FeatherDesk/internal/app"
	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/notify/smtptest"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestWriterContention (DB-026): the in-process queue removes per-job writes.
// The full app (default Registry, production queue options: batch 200, cursor
// write at most once per second) handles N ticket replies, alternating agent
// and customer replies so every pair produces a customer notification
// (customer.reply) and a staff notification (staff.customer_replied), a
// WebSocket fan-out to a subscribed agent and an FTS index update.
//
// Writer transactions are counted with store.DB.WriteStats and attributed:
//
//	replies (1 each) + index worker (1 per indexed event) + cursor advances
//	+ deferred_jobs writes + session/write-behind flushes + other
//
// "other" must be 0: notifications and WebSocket events never write. A second
// pass fails 5 % of SMTP deliveries (451): the extra writes are exactly one
// deferred_jobs insert per failed job. Agent reply latency p95 is measured
// while the queue drains (budget 20 ms, asserted only with FD_PERF=1 and
// without -race). N = 1 000 with FD_PERF=1, else 100 (CI).
func TestWriterContention(t *testing.T) {
	n := 100
	perf := os.Getenv("FD_PERF") == "1"
	if perf {
		n = 1000
	}
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		happy := runContention(t, db, n, 0)
		failing := runContention(t, storetest.Open(t, db.Dialect), n, 20)
		t.Logf("SUMMARY engine=%s N=%d happy: %s", db.Dialect, n, happy)
		t.Logf("SUMMARY engine=%s N=%d 5%%-smtp-fail: %s", db.Dialect, n, failing)
		// D-07 (superseded: every notification, webhook and index update a
		// persisted job, 3 writes per job: insert, claim, complete).
		jobs07 := happy.jobs + int(happy.index)
		d07 := n + 3*jobs07
		t.Logf("SUMMARY engine=%s D-07 baseline: %d replies + 3 x (%d notification + %d index jobs) = %d writer txs; D-11 measured %d (%.1fx fewer)",
			db.Dialect, n, happy.jobs, happy.index, d07, happy.total, float64(d07)/float64(happy.total))
		extra := failing.total - failing.cursor - failing.flushes - (happy.total - happy.cursor - happy.flushes)
		t.Logf("SUMMARY engine=%s extra writer txs with 5%% SMTP failures: %d for %d failed jobs (deferred_jobs writes %d)",
			db.Dialect, extra, failing.failedMails, failing.deferred)
		if failing.failedMails == 0 {
			t.Fatal("no SMTP failure was injected")
		}
		if failing.deferred != int64(failing.failedMails) {
			t.Errorf("deferred_jobs writes %d != failed jobs %d", failing.deferred, failing.failedMails)
		}
		if extra != int64(failing.failedMails) {
			t.Errorf("extra writes %d != failed jobs %d", extra, failing.failedMails)
		}
		if perf && !app.RaceEnabled && happy.p95 > 20*time.Millisecond {
			t.Errorf("agent reply p95 %v > 20ms while the queue drains", happy.p95)
		}
	})
}

type contentionResult struct {
	total, replies, index, cursor, deferred, flushes, other int64
	inApp                                                   int64 // events that wrote in-app notifications (1 tx each)
	stmts, queueDeferredStat                                int64
	jobs, mails, failedMails                                int
	p50, p95                                                time.Duration
	elapsed                                                 time.Duration
	bound                                                   int64
}

func (r contentionResult) String() string {
	return fmt.Sprintf("writer txs=%d (statements %d) = replies %d + index %d + cursor %d + deferred_jobs inserts %d + flushes %d + in-app %d + other %d; mails=%d (failed %d) notify jobs=%d; agent reply p50=%v p95=%v; drained in %v; formula bound N+ceil(N/200)+FTS=%d",
		r.total, r.stmts, r.replies, r.index, r.cursor, r.deferred, r.flushes, r.inApp, r.other, r.mails, r.failedMails, r.jobs,
		r.p50.Round(10*time.Microsecond), r.p95.Round(10*time.Microsecond), r.elapsed.Round(time.Millisecond), r.bound)
}

// failEvery > 0 answers 451 to every failEvery-th DATA (5 % with 20).
func runContention(t *testing.T, db *store.DB, n, failEvery int) contentionResult {
	ctx := context.Background()
	sink, err := smtptest.Start(smtptest.Options{Mode: smtptest.Plain})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sink.Close)
	var datas, failed atomic.Int64
	if failEvery > 0 {
		sink.SetReply(func(stage, arg string) (int, string) {
			if stage != "DATA" {
				return 0, ""
			}
			if datas.Add(1)%int64(failEvery) == 0 {
				failed.Add(1)
				return 451, "4.3.0 try again later"
			}
			return 0, ""
		})
	}
	team := storetest.CreateTeam(t, db, "Support")
	vars := e2eEnv(t, db, sink, t.TempDir())
	// Production queue options (no test shortcuts): batch 200, cursor ≤ 1/s.
	authntest.FastArgon()
	getenv := func(k string) string { return vars[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Build(ctx, cfg, app.BuildOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Getenv: getenv,
		SPA: fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}}, Version: "bench"})
	if err != nil {
		t.Fatal(err)
	}
	s := &stack{t: t, a: a, db: a.Deps.DB, sink: sink, vars: vars, logs: &authntest.SyncBuffer{}}
	stop := s.run()
	defer stop()
	q := a.Deps.Queue

	findModule[interface{ SetThrottle(bool) }](a).SetThrottle(false) // 51 logins from one test IP
	agent, agentID := s.staff(domain.RoleAgent, 1, team)
	const nTickets = 50
	custs := make([]*client, nTickets)
	tickets := make([]string, nTickets)
	for i := range tickets {
		cid := storetest.CreateCustomer(t, s.db, "")
		custs[i] = s.loginFixture(cid, domain.KindCustomer)
		tickets[i] = storetest.CreateTicket(t, s.db, storetest.Ticket{Requester: cid, TeamID: team, AssigneeID: agentID, Status: domain.StatusOpen})
	}
	ws := s.ws(agent)
	ws.subscribe("s1", "tickets", "queue:"+team)

	// Let every schedule take its first (jittered ≤ 5 s) run before the window.
	poll(t, 15*time.Second, "first schedule runs", func() bool {
		st, err := q.Status(ctx)
		if err != nil {
			return false
		}
		for _, sc := range st.Schedules {
			if sc.Runs == 0 {
				return false
			}
		}
		return st.Lag == 0
	})
	time.Sleep(1200 * time.Millisecond) // the cursor write for the setup events
	authnMod := findModule[interface{ SessionTouchFlushes() int }](a)
	ws0, qs0 := s.db.WriteStats(), q.Stats()
	cur0, def0, wb0, sess0 := qs0.CursorWrites.Load(), qs0.DeferredWrites.Load(), q.WriteBehindFlushes(), authnMod.SessionTouchFlushes()
	idx0, notify0 := consumerHandled(t, q, "index"), consumerHandled(t, q, "notify")
	mails0 := len(sink.Messages())
	jobs0 := countRows(t, s.db, "deferred_jobs")
	inApp0 := countInAppEvents(t, s.db)

	start := time.Now()
	var agentLat []time.Duration
	for i := 0; i < n; i++ {
		k := i % nTickets
		body := map[string]any{"kind": "public", "body_md": fmt.Sprintf("Reply %d zebra%d", i, i)}
		if i%2 == 0 {
			d := timedPost(t, s, agent, "/api/v1/agent/tickets/"+tickets[k]+"/messages", body)
			agentLat = append(agentLat, d)
		} else {
			delete(body, "kind")
			timedPost(t, s, custs[k], "/api/v1/portal/tickets/"+tickets[k]+"/messages", body)
		}
	}
	replyDone := time.Since(start)
	poll(t, 120*time.Second, "queue drained", func() bool {
		st, err := q.Status(ctx)
		if err != nil || st.Lag != 0 {
			return false
		}
		for _, c := range st.Consumers {
			if c.Depth != 0 {
				return false
			}
		}
		return consumerHandled(t, q, "index")-idx0 >= int64(n) && len(sink.Messages()) >= mails0+n-int(failed.Load()) && consumerHandled(t, q, "notify")-notify0 >= int64(n)
	})
	elapsed := time.Since(start)
	time.Sleep(1500 * time.Millisecond) // final cursor advance (≤ 1/s)
	if ws.waitFor(time.Second, func(f map[string]any) bool { return f["type"] == "event" }) == nil {
		t.Errorf("the WebSocket subscriber received no event: %s", ws.dump())
	}
	ws1, qs1 := s.db.WriteStats(), q.Stats()
	r := contentionResult{
		total: ws1.Txs - ws0.Txs, stmts: ws1.Statements - ws0.Statements, replies: int64(n),
		index:    consumerHandled(t, q, "index") - idx0,
		cursor:   qs1.CursorWrites.Load() - cur0,
		deferred: countRows(t, s.db, "deferred_jobs") - jobs0, queueDeferredStat: qs1.DeferredWrites.Load() - def0,
		flushes: q.WriteBehindFlushes() - wb0 + int64(authnMod.SessionTouchFlushes()-sess0),
		jobs:    int(consumerHandled(t, q, "notify") - notify0),
		mails:   len(sink.Messages()) - mails0, failedMails: int(failed.Load()), elapsed: elapsed,
		inApp: countInAppEvents(t, s.db) - inApp0,
	}
	// Retries (attempt 2+) run after the 30 s backoff, outside this window.
	// In-app notifications (D-19 §D) cost one transaction per event that has
	// a recipient (NOT-036), never one per recipient.
	r.other = r.total - r.replies - r.index - r.cursor - max(r.deferred, r.queueDeferredStat) - r.flushes - r.inApp
	if r.inApp > int64(n) {
		t.Errorf("in-app writes %d > %d events", r.inApp, n)
	}
	r.bound = int64(n) + int64(math.Ceil(float64(n)/200)) + r.index
	sort.Slice(agentLat, func(i, j int) bool { return agentLat[i] < agentLat[j] })
	r.p50, r.p95 = agentLat[len(agentLat)/2], agentLat[len(agentLat)*95/100]
	t.Logf("engine=%s N=%d failEvery=%d replies took %v: %s", s.db.Dialect, n, failEvery, replyDone.Round(time.Millisecond), r)
	if r.other != 0 {
		t.Errorf("%d writer transactions not explained by replies/index/cursor/deferred/flushes (notifications and WS events must not write)", r.other)
	}
	if maxCursor := int64(elapsed/time.Second) + 2; r.cursor > maxCursor {
		t.Errorf("cursor writes %d > %d (≤ 1 per second)", r.cursor, maxCursor)
	}
	if failEvery == 0 && r.deferred != 0 {
		t.Errorf("happy path wrote deferred_jobs %d times", r.deferred)
	}
	if r.mails+r.failedMails < n {
		t.Errorf("mails %d + failed %d < %d replies", r.mails, r.failedMails, n)
	}
	return r
}

// countInAppEvents counts the events that wrote notification rows.
func countInAppEvents(t *testing.T, db *store.DB) int64 {
	var n int64
	if err := db.Read().QueryRowContext(context.Background(), `SELECT COUNT(DISTINCT event_id) FROM notifications`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countRows(t *testing.T, db *store.DB, table string) int64 {
	var n int64
	if err := db.Read().QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func consumerHandled(t *testing.T, q *queue.Queue, name string) int64 {
	st, err := q.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range st.Consumers {
		if c.Name == name {
			return c.Handled
		}
	}
	t.Fatalf("no consumer %q", name)
	return 0
}

func findModule[T any](a *app.App) T {
	for _, m := range a.Modules {
		if v, ok := m.(T); ok {
			return v
		}
	}
	var zero T
	panic(fmt.Sprintf("no module implements %T", zero))
}

// timedPost runs one cookie-authenticated POST through the full router and
// returns the handler time only (no spec validation inside the timing).
func timedPost(t *testing.T, s *stack, c *client, path string, body any) time.Duration {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", e2ePublicURL)
	req.Header.Set("X-CSRF-Token", c.csrf)
	req.AddCookie(&http.Cookie{Name: authn.CookieName, Value: c.cookie})
	rec := httptest.NewRecorder()
	start := time.Now()
	s.a.Server.Handler().ServeHTTP(rec, req)
	d := time.Since(start)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s: %d %s", path, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return d
}
