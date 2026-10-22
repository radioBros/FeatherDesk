// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// testModule plays the phase-2 modules: auth (bearer "<kind>:<role>"),
// realtime (tickets topic) and a queue consumer.
type testModule struct {
	got chan events.Event
}

func (m *testModule) Name() string                             { return "test" }
func (m *testModule) Mount(g server.Groups)                    {}
func (m *testModule) Authenticator() server.Authenticator      { return m }
func (m *testModule) CSRFValidator() server.CSRFValidator      { return m }
func (m *testModule) ValidCSRF(*domain.Principal, string) bool { return false }
func (m *testModule) Authenticate(r *http.Request) (*domain.Principal, error) {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if v == "" {
		return nil, nil
	}
	kind, role, _ := strings.Cut(v, ":")
	return &domain.Principal{UserID: "u-" + v, Kind: domain.UserKind(kind), Role: domain.Role(role), Tier: 1, TokenID: "t"}, nil
}
func (m *testModule) RegisterRealtime(h *realtime.Hub) {
	h.Authorize("tickets", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool { return p.IsStaff() }))
	h.OnEvent(func(ctx context.Context, e events.Event) {
		h.Publish("tickets", realtime.EventFrame("tickets", e, map[string]any{"number": e.Snapshot.Number}), nil)
	})
}
func (m *testModule) RegisterQueue(q *queue.Queue) {
	q.Subscribe("notify", queue.SubscribeOptions{Durable: true}, nil, func(ctx context.Context, e events.Event) error {
		m.got <- e
		return nil
	})
}

func testConfig(t *testing.T, dbURL string) *config.Config {
	dir := t.TempDir()
	env := map[string]string{
		"FD_SECRET_KEY": strings.Repeat("k", 32), "FD_DATA_DIR": dir, "FD_PUBLIC_URL": "http://localhost:8080",
		"FD_DATABASE_URL": dbURL,
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func build(t *testing.T, dbURL string) (*App, *testModule) {
	t.Helper()
	mod := &testModule{got: make(chan events.Event, 10)}
	a, err := Build(context.Background(), testConfig(t, dbURL), BuildOptions{
		SPA:          fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
		Modules:      []Factory{func(d *Deps) (Module, error) { return mod, nil }},
		QueueOptions: &queue.Options{Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond},
		Version:      "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, mod
}

// start serves the app on a random port and returns its base URL and a stop func.
func start(t *testing.T, a *App) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()
	base := "http://" + ln.Addr().String()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return base, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	}
}

func get(t *testing.T, url, auth string) (*http.Request, *http.Response, []byte) {
	req, _ := http.NewRequest("GET", url, nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return req, resp, b
}

// TestEndToEnd: the built app serves readiness, admin endpoints (validated
// against api/openapi.yaml), the SPA, and routes a committed event through the
// queue to both a durable consumer and a WebSocket client.
func TestEndToEnd(t *testing.T) {
	engines := map[string]string{"sqlite": ""}
	if os.Getenv("FD_TEST_POSTGRES_URL") != "" {
		engines["postgres"] = "pg"
	}
	for name, kind := range engines {
		t.Run(name, func(t *testing.T) {
			dbURL := ""
			if kind == "pg" {
				dbURL = storetest.SchemaURL(t)
			}
			a, mod := build(t, dbURL)
			base, stop := start(t, a)
			defer stop()

			if _, resp, _ := get(t, base+"/healthz", ""); resp.StatusCode != 200 {
				t.Fatal("healthz")
			}
			if _, resp, b := get(t, base+"/agent/tickets", ""); resp.StatusCode != 200 || !strings.Contains(string(b), "spa") {
				t.Fatal("spa fallback")
			}
			if _, resp, _ := get(t, base+"/api/v1/admin/queue", "staff:agent"); resp.StatusCode != 403 {
				t.Fatalf("agent on admin: %d", resp.StatusCode)
			}
			for _, path := range []string{"/api/v1/admin/queue", "/api/v1/admin/system", "/api/v1/admin/jobs?status=dead"} {
				req, resp, b := get(t, base+path, "staff:admin")
				if resp.StatusCode != 200 {
					t.Fatalf("%s: %d %s", path, resp.StatusCode, b)
				}
				resp.Body = io.NopCloser(strings.NewReader(string(b)))
				apispec.ValidateResponse(t, req, resp)
			}
			_, _, b := get(t, base+"/api/v1/admin/system", "staff:admin")
			var sys map[string]any
			_ = json.Unmarshal(b, &sys)
			if sys["version"] != "1.2.3" || sys["db"].(map[string]any)["engine"] != name {
				t.Fatalf("system %v", sys)
			}
			// DB-017: the boot-time check and backup status are reported.
			dbDoc := sys["db"].(map[string]any)
			integ, _ := dbDoc["integrity"].(map[string]any)
			wantCheck := map[string]string{"sqlite": "quick_check", "postgres": "select_1"}[name]
			if integ["ok"] != true || integ["check"] != wantCheck || integ["checked_at"] == nil {
				t.Fatalf("db.integrity = %v", dbDoc["integrity"])
			}
			if v, ok := dbDoc["last_backup_at"]; !ok || v != nil {
				t.Fatalf("db.last_backup_at = %v (present %t), want null before any backup", v, ok)
			}
			if _, ok := dbDoc["page_count"]; ok != (name == "sqlite") {
				t.Fatalf("db.page_count presence %t on %s", ok, name)
			}

			// WebSocket: bearer staff, subscribe "tickets".
			ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(base, "http")+"/api/v1/ws",
				&websocket.DialOptions{Subprotocols: []string{realtime.Subprotocol}, HTTPHeader: http.Header{"Authorization": {"Bearer staff:agent"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = ws.CloseNow() // safety net: the server usually closed it already
			}()
			_ = ws.Write(context.Background(), websocket.MessageText, []byte(`{"op":"subscribe","id":"1","topics":["tickets"]}`))
			readFrame(t, ws) // ack

			db := a.Deps.DB
			tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, "")})
			err = db.Tx(context.Background(), func(tx *store.Tx) error {
				return events.Write(context.Background(), tx, &events.Event{TicketID: tk, Type: events.TicketCreated, Snapshot: events.Snapshot{Number: 1042}}, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			f := readFrame(t, ws)
			if f["event"] != events.TicketCreated || f["data"].(map[string]any)["number"] != float64(1042) {
				t.Fatalf("ws frame %v", f)
			}
			select {
			case e := <-mod.got:
				if e.TicketID != tk {
					t.Fatal("wrong event")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("durable consumer not called")
			}
		})
	}
}

func readFrame(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// TestRoutesDocumented: every route the platform registers exists in the spec.
func TestRoutesDocumented(t *testing.T) {
	a, _ := build(t, "")
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	apispec.AssertRoutes(t, a.Server.Router())
}

func TestReadinessFailsWhenSchemaBehind(t *testing.T) {
	a, _ := build(t, "")
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	if _, err := a.Deps.DB.W.Exec(`DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readyz with stale schema: %d", rec.Code)
	}
}

// TestReadinessFailsWhenLowerMigrationPending: MAX(version) is at head but a
// lower-numbered migration (D-18 ranges) is missing: not ready.
func TestReadinessFailsWhenLowerMigrationPending(t *testing.T) {
	a, _ := build(t, "")
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	if _, err := a.Deps.DB.W.Exec(`DELETE FROM schema_migrations WHERE version = (SELECT MIN(version) FROM schema_migrations)`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readyz with a pending lower migration: %d", rec.Code)
	}
}

func TestApplyMemoryLimit(t *testing.T) {
	old := cgroupRoot
	defer func() { cgroupRoot = old }()
	cgroupRoot = t.TempDir()
	if got := ApplyMemoryLimit(func(string) string { return "" }); got != 0 {
		t.Fatalf("no cgroup file: %d", got)
	}
	_ = os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("268435456\n"), 0o644)
	if got := ApplyMemoryLimit(func(string) string { return "" }); got != 201326592 {
		t.Fatalf("TestMemLimitFromCgroup: got %d want 201326592 (75%% of 256 MiB)", got)
	}
	if got := ApplyMemoryLimit(func(k string) string { return map[string]string{"GOMEMLIMIT": "100MiB"}[k] }); got != 0 {
		t.Fatal("explicit GOMEMLIMIT must win")
	}
	_ = os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("max\n"), 0o644)
	if got := ApplyMemoryLimit(func(string) string { return "" }); got != 0 {
		t.Fatal("unlimited cgroup must not set a limit")
	}
}
