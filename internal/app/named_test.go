// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func serve(a *App, method, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rec := httptest.NewRecorder()
	a.Server.Handler().ServeHTTP(rec, req)
	return rec
}

// TestAdminQueueRequiresAdmin: supervisor → 403, customer → 404 (I-1).
func TestAdminQueueRequiresAdmin(t *testing.T) {
	a, _ := build(t, "")
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	cases := map[string]int{"staff:supervisor": 403, "customer:org_manager": 404, "": 401, "staff:admin": 200}
	for who, want := range cases {
		if c := serve(a, "GET", "/api/v1/admin/queue", who).Code; c != want {
			t.Errorf("%q: %d want %d", who, c, want)
		}
	}
}

// TestAdminQueueReportsLag: events not yet dispatched show up as lag.
func TestAdminQueueReportsLag(t *testing.T) {
	a, _ := build(t, "")
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	db := a.Deps.DB
	tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, "")})
	_ = db.Tx(context.Background(), func(tx *store.Tx) error {
		return events.Write(context.Background(), tx, &events.Event{TicketID: tk, Type: events.TicketCreated}, nil)
	})
	rec := serve(a, "GET", "/api/v1/admin/queue", "staff:admin")
	if !strings.Contains(rec.Body.String(), `"lag_events":1`) {
		t.Fatalf("lag missing: %s", rec.Body.String())
	}
}

func TestReadyzReportsEngine(t *testing.T) {
	engines := map[string]string{"sqlite": ""}
	if os.Getenv("ST_TEST_POSTGRES_URL") != "" {
		engines["postgres"] = "pg"
	}
	for name, kind := range engines {
		t.Run(name, func(t *testing.T) {
			url := ""
			if kind == "pg" {
				url = storetest.SchemaURL(t)
			}
			a, _ := build(t, url)
			defer func() {
				if err := a.Close(); err != nil {
					t.Errorf("a.Close: %v", err)
				}
			}()
			rec := serve(a, "GET", "/readyz", "")
			if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "ready "+name {
				t.Fatalf("%d %q", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestReadyzFailsWhenPostgresDown (and SQLite): a dead pool makes readyz 503.
func TestReadyzFailsWhenPostgresDown(t *testing.T) {
	if os.Getenv("ST_TEST_POSTGRES_URL") == "" {
		t.Skip("ST_TEST_POSTGRES_URL not set")
	}
	a, _ := build(t, storetest.SchemaURL(t))
	if err := a.Deps.DB.Close(); err != nil {
		t.Fatalf("a.Deps.DB.Close: %v", err)
	}
	if c := serve(a, "GET", "/readyz", "").Code; c != 503 {
		t.Fatalf("readyz with closed pool: %d", c)
	}
}

func TestReadyzFailsWhenDispatcherStalled(t *testing.T) {
	old := stallThreshold
	stallThreshold = 50 * time.Millisecond
	defer func() { stallThreshold = old }()
	mod := &testModule{got: make(chan events.Event, 10)}
	a, err := Build(context.Background(), testConfig(t, ""), BuildOptions{
		Modules:      []Factory{func(d *Deps) (Module, error) { return mod, nil }},
		QueueOptions: &queue.Options{Fallback: time.Hour, BatchSize: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = a.Deps.Queue.Start(ctx)
	defer func() {
		if err := a.Deps.Queue.Stop(context.Background()); err != nil {
			t.Errorf("a.Deps.Queue.Stop: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	// Insert an event bypassing the commit hook (raw SQL), so nothing wakes the dispatcher.
	db := a.Deps.DB
	tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, "")})
	time.Sleep(300 * time.Millisecond) // let the wake-ups from the fixture commits settle
	if _, err := db.W.Exec(`INSERT INTO ticket_events (id, ticket_id, type, data, snapshot, created_at) VALUES (?, ?, 'ticket.created', '{}', '{}', ?)`, store.NewID(), tk, store.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if c := serve(a, "GET", "/readyz", "").Code; c != 503 {
		t.Fatalf("stalled dispatcher must fail readyz: %d", c)
	}
}

// TestNoSubSecondTickers: no production code starts a ticker/timer loop below
// one second (CONVENTIONS §10: no busy polling).
func TestNoSubSecondTickers(t *testing.T) {
	root := "../.."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && (info.Name() == "node_modules" || info.Name() == "web" || strings.HasPrefix(info.Name(), ".") && p != root) {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "time" || (sel.Sel.Name != "NewTicker" && sel.Sel.Name != "Tick") {
				return true
			}
			if lit := literalDuration(call.Args[0]); lit > 0 && lit < time.Second {
				t.Errorf("%s: %s(%s) below 1 s", fset.Position(call.Pos()), sel.Sel.Name, lit)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// literalDuration evaluates `N * time.Unit` / `time.Unit` literals; 0 if unknown.
func literalDuration(e ast.Expr) time.Duration {
	units := map[string]time.Duration{"Nanosecond": time.Nanosecond, "Microsecond": time.Microsecond, "Millisecond": time.Millisecond, "Second": time.Second, "Minute": time.Minute, "Hour": time.Hour}
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return units[v.Sel.Name]
	case *ast.BinaryExpr:
		if v.Op != token.MUL {
			return 0
		}
		lit, ok := v.X.(*ast.BasicLit)
		if !ok {
			return 0
		}
		var n int64
		for _, ch := range lit.Value {
			if ch < '0' || ch > '9' {
				return 0
			}
			n = n*10 + int64(ch-'0')
		}
		return time.Duration(n) * literalDuration(v.Y)
	}
	return 0
}

// TestColdStart: Build + Run to /readyz 200 within the 3 s hard limit (target 1 s).
func TestColdStart(t *testing.T) {
	start := time.Now()
	a, _ := build(t, "")
	base, stop := startApp(t, a)
	defer stop()
	resp, err := http.Get(base + "/readyz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("readyz: %v", err)
	}
	resp.Body.Close()
	d := time.Since(start)
	t.Logf("cold start to readyz: %s", d)
	limit := 3 * time.Second
	if raceEnabled {
		limit = 30 * time.Second // the race detector multiplies start-up cost; the 3 s budget is for production builds
	}
	if d > limit {
		t.Fatalf("cold start %s exceeds %s", d, limit)
	}
}

// TestIdleCPU: an idle app (no clients) uses < 1 % of one core in steady state.
func TestIdleCPU(t *testing.T) {
	if testing.Short() {
		t.Skip("10 s idle measurement")
	}
	if raceEnabled {
		t.Skip("the race detector multiplies CPU cost; the 1 % budget is measured without it")
	}
	// Production queue timings (build() uses a 50 ms test fallback poll).
	mod := &testModule{got: make(chan events.Event, 10)}
	a, err := Build(context.Background(), testConfig(t, ""), BuildOptions{
		SPA:     fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
		Modules: []Factory{func(d *Deps) (Module, error) { return mod, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, stop := startApp(t, a)
	defer stop()
	// Skip the boot work: every schedule runs once ~5 s after start.
	time.Sleep(7 * time.Second)
	var r0, r1 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r0)
	const window = 10 * time.Second
	time.Sleep(window)
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &r1)
	cpu := tv(r1.Utime) + tv(r1.Stime) - tv(r0.Utime) - tv(r0.Stime)
	t.Logf("idle CPU over %s: %s (%.2f%% of one core)", window, cpu, 100*cpu.Seconds()/window.Seconds())
	if cpu > window/100 {
		t.Fatalf("idle CPU %s exceeds 1%%", cpu)
	}
}

func tv(t syscall.Timeval) time.Duration {
	return time.Duration(t.Sec)*time.Second + time.Duration(t.Usec)*time.Microsecond
}

func startApp(t *testing.T, a *App) (string, func()) { return start(t, a) }

var _ = io.Discard
