// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TKT-032 instrumented fan-out: every statement either pool of the DB runs
// is counted at the database/sql driver level, so a per-subscriber query
// anywhere on the fan-out path (authorisation, payload build, publish) would
// show up as a count that grows with the number of subscribers.

// countingDriver wraps a driver and counts every statement sent to the
// database (queries, execs and prepares).
type countingDriver struct {
	inner driver.Driver
	n     *atomic.Int64
}

func (d countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, n: d.n}, nil
}

type dsnConnector struct {
	dsn string
	d   countingDriver
}

func (c dsnConnector) Connect(context.Context) (driver.Conn, error) { return c.d.Open(c.dsn) }
func (c dsnConnector) Driver() driver.Driver                        { return c.d }

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c *countingConn) Prepare(q string) (driver.Stmt, error) {
	c.n.Add(1)
	return c.Conn.Prepare(q)
}

func (c *countingConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	c.n.Add(1)
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, q)
	}
	return c.Conn.Prepare(q)
}

func (c *countingConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := qc.QueryContext(ctx, q, args)
	if !errors.Is(err, driver.ErrSkip) { // a skip falls back to Prepare, counted there
		c.n.Add(1)
	}
	return rows, err
}

func (c *countingConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := ec.ExecContext(ctx, q, args)
	if !errors.Is(err, driver.ErrSkip) {
		c.n.Add(1)
	}
	return res, err
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin() //nolint:staticcheck // fallback for drivers without BeginTx
}

func (c *countingConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ch, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ch.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *countingConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *countingConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (c *countingConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

// sqliteTestDSN mirrors store.sqliteDSN (unexported) for a file database.
func sqliteTestDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "cache_size(-2000)")
	q.Add("_time_format", "sqlite")
	return "file:" + path + "?" + q.Encode()
}

// instrumentDB swaps db's writer and reader pools for counting ones on the
// same database and returns the statement counter. Call it before building
// anything on db.
func instrumentDB(t *testing.T, db *store.DB) *atomic.Int64 {
	t.Helper()
	n := &atomic.Int64{}
	oldW, oldR := db.W, db.R
	switch db.Dialect {
	case store.Postgres:
		p := sql.OpenDB(dsnConnector{dsn: db.URL, d: countingDriver{inner: oldW.Driver(), n: n}})
		p.SetMaxOpenConns(5)
		p.SetMaxIdleConns(5)
		db.W, db.R = p, p
	default:
		path := strings.TrimPrefix(db.URL, "sqlite://")
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		dsn := sqliteTestDSN(path)
		w := sql.OpenDB(dsnConnector{dsn: dsn + "&_txlock=immediate", d: countingDriver{inner: oldW.Driver(), n: n}})
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		r := sql.OpenDB(dsnConnector{dsn: dsn + "&mode=ro", d: countingDriver{inner: oldR.Driver(), n: n}})
		r.SetMaxOpenConns(2)
		r.SetMaxIdleConns(2)
		db.W, db.R = w, r
	}
	if err := db.W.PingContext(context.Background()); err != nil {
		t.Fatalf("instrumented pool: %v", err)
	}
	_ = oldW.Close()
	if oldR != oldW {
		_ = oldR.Close()
	}
	return n
}

// fanoutWorld is one ticket with a stream of stored events and a pool of
// principals allowed to subscribe to it on every topic.
type fanoutWorld struct {
	e       *env
	team    string
	id      string
	evs     []events.Event
	staff   []*domain.Principal
	custs   []*domain.Principal
	clients []*wsClient
}

func newFanoutWorld(t *testing.T, db *store.DB, maxConns int) *fanoutWorld {
	e := newEnvHub(t, db, 1, realtime.Options{MaxConns: maxConns + 10, MaxConnsPerUser: maxConns + 10})
	ctx := context.Background()
	w := &fanoutWorld{e: e, team: storetest.CreateTeam(t, db, "")}
	org := storetest.CreateOrg(t, db, "")
	req := e.customer(org, domain.RoleRequester)
	w.custs = []*domain.Principal{req, e.customer(org, domain.RoleOrgManager)}
	w.staff = []*domain.Principal{e.staff(domain.RoleAgent, 1, w.team), e.staff(domain.RoleAgent, 3, w.team), e.staff(domain.RoleSupervisor, 1)}
	w.id = e.createTicket(req, CreateInput{Subject: "Fan-out", BodyMD: "body"})
	sup := w.staff[2]
	if _, err := e.svc.Patch(ctx, sup, w.id, PatchInput{TeamID: optStr{true, sp(w.team)}}, ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := e.svc.AddStaffMessage(ctx, w.staff[i%2], w.id, MessageInput{Kind: []string{"public", "internal"}[i%2], BodyMD: "update"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Patch(ctx, sup, w.id, PatchInput{Status: optStr{true, sp("pending")}}, ""); err != nil {
		t.Fatal(err)
	}
	evs, err := events.ReadAfter(ctx, db.Read(), "", 1000)
	if err != nil || len(evs) < 6 {
		t.Fatalf("events: %v %d", err, len(evs))
	}
	w.evs = evs
	return w
}

// subscribe opens n more sockets, cycling through staff and customers. With
// all, staff hold tickets, ticket:<id> and queue:<team> and customers tickets
// and ticket:<id>; otherwise every socket holds the tickets topic only.
func (w *fanoutWorld) subscribe(t *testing.T, n int, all bool) {
	ps := append(append([]*domain.Principal{}, w.staff...), w.custs...)
	for i := 0; i < n; i++ {
		p := ps[len(w.clients)%len(ps)]
		c := w.e.wsDial(p)
		topics := []string{"tickets"}
		if all {
			topics = append(topics, "ticket:"+w.id)
			if p.IsStaff() {
				topics = append(topics, "queue:"+w.team)
			}
		}
		if d := c.subscribe("s", topics...); len(d) != 0 {
			t.Fatalf("denied %v", d)
		}
		w.clients = append(w.clients, c)
	}
}

// dispatch hands every stored event to the hub (the realtime consumer's
// job) and returns the statements run meanwhile and the time per event.
func (w *fanoutWorld) dispatch(n *atomic.Int64, pause time.Duration) (int64, []time.Duration) {
	ctx := context.Background()
	before := n.Load()
	var took []time.Duration
	for _, ev := range w.evs {
		s := time.Now()
		_ = w.e.hub.HandleEvent(ctx, ev)
		took = append(took, time.Since(s))
		time.Sleep(pause) // let 1 000 writers drain: no slow-consumer closes
	}
	return n.Load() - before, took
}

// TestWS_FanoutZeroQueriesPerSubscriber (TKT-032, realtime_spec §4): the
// statements run to fan the same events out are identical with 0, 3 and 60
// subscribers: authorisation and payload filtering per subscriber are in
// memory, and the payload is loaded once per event, never per subscriber.
func TestWS_FanoutZeroQueriesPerSubscriber(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		n := instrumentDB(t, db)
		w := newFanoutWorld(t, db, 100)
		q0, _ := w.dispatch(n, 0)
		w.subscribe(t, 3, true)
		q3, _ := w.dispatch(n, 0)
		w.subscribe(t, 57, true)
		q60, _ := w.dispatch(n, 5*time.Millisecond)
		t.Logf("%d events: %d statements with 0 subscribers, %d with 3, %d with 60", len(w.evs), q0, q3, q60)
		if q0 == 0 {
			t.Fatal("instrumentation counted nothing: the payload load must show up")
		}
		if q3 != q0 || q60 != q0 {
			t.Fatalf("statements grow with subscribers: 0→%d, 3→%d, 60→%d", q0, q3, q60)
		}
		last := w.clients[len(w.clients)-1]
		if last.waitFor(5*time.Second, func(f map[string]any) bool { return f["event"] == events.TicketStatusChanged }) == nil {
			t.Fatal("the last subscriber received nothing: fan-out did not happen")
		}
	})
}

// TestWS_FanoutBenchmark_1000Subscribers is the instrumented benchmark of
// TKT-032: one event fanned out to 1 000 subscribers (250 under -race, which
// caps goroutines) runs 0 statements per subscriber, and the fan-out cost
// (HandleEvent time with the subscribers minus the same events with none,
// i.e. without the once-per-event payload load) stays under 5 ms per event
// without the race detector. Every subscriber holds the tickets topic, so one
// event is decided and encoded 1 000 times (TestWS_FanoutZeroQueriesPerSubscriber
// covers subscribers holding every topic of the ticket). It runs on
// SQLite (the fan-out is engine-independent once the payload is loaded) and
// is skipped with -short. The pure in-memory part is
// BenchmarkFanout_1000Subscribers.
func TestWS_FanoutBenchmark_1000Subscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	subs := 1000
	if raceEnabled {
		subs = 250
	}
	db := storetest.Open(t, store.SQLite)
	n := instrumentDB(t, db)
	w := newFanoutWorld(t, db, subs)
	median := func(d []time.Duration) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2]
	}
	_, _ = w.dispatch(n, 0) // warm caches (settings, field definitions)
	q0, base := w.dispatch(n, 0)
	w.subscribe(t, subs, false)
	qN, took := w.dispatch(n, 50*time.Millisecond)
	fanout := median(took) - median(base)
	t.Logf("%d subscribers, %d events: %d statements (0 subscribers: %d); per event: %v with subscribers, %v without, fan-out %v",
		subs, len(w.evs), qN, q0, median(took), median(base), fanout)
	if perSub := float64(qN-q0) / float64(subs*len(w.evs)); qN != q0 {
		t.Fatalf("%d extra statements with %d subscribers (%.4f per subscriber per event), want 0", qN-q0, subs, perSub)
	}
	if !raceEnabled && fanout > 5*time.Millisecond {
		t.Errorf("fan-out %v per event to %d subscribers, budget 5 ms", fanout, subs)
	}
	for _, c := range []*wsClient{w.clients[0], w.clients[subs-1]} {
		if c.waitFor(5*time.Second, func(f map[string]any) bool { return f["event"] == events.TicketStatusChanged }) == nil {
			t.Fatal("a subscriber received nothing")
		}
	}
}
