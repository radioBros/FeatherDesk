// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

const testOrigin = "http://localhost:8080"

// env is the test harness: the real router and services over a storetest DB,
// with a fake authenticator mapping "Bearer <key>" to registered principals.
type env struct {
	t    *testing.T
	db   *store.DB
	cfg  *config.Config
	srv  *server.Server
	hub  *realtime.Hub
	q    *queue.Queue
	set  *settings.Service
	blob blob.Store
	mod  *Module
	svc  *Service

	mu    sync.Mutex
	users map[string]*domain.Principal
}

type fakeAuth struct{ e *env }

func (a fakeAuth) Authenticate(r *http.Request) (*domain.Principal, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, nil
	}
	a.e.mu.Lock()
	defer a.e.mu.Unlock()
	p, ok := a.e.users[strings.TrimPrefix(h, "Bearer ")]
	if !ok {
		return nil, problems.Unauthenticated()
	}
	cp := *p
	return &cp, nil
}

func (a fakeAuth) ValidCSRF(p *domain.Principal, token string) bool { return token == "csrf-ok" }

func testConfig(t *testing.T) *config.Config {
	env := map[string]string{"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": t.TempDir(), "ST_PUBLIC_URL": testOrigin}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newEnv(t *testing.T, db *store.DB) *env {
	t.Helper()
	return newEnvMax(t, db, 1)
}

// newEnvMax builds the harness with ST_MAX_UPLOAD_MB = maxMB.
func newEnvMax(t *testing.T, db *store.DB, maxMB int) *env {
	t.Helper()
	return newEnvHub(t, db, maxMB, realtime.Options{})
}

// newEnvHub builds the harness with custom hub options (recheck interval,
// buffers). PublicOrigin is always the test origin.
func newEnvHub(t *testing.T, db *store.DB, maxMB int, hubOpt realtime.Options) *env {
	t.Helper()
	cfg := testConfig(t)
	bl, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox(cfg.SecretKey, cfg.SecretKeyPrev)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, cfg: cfg, blob: bl, users: map[string]*domain.Principal{}}
	e.set = settings.New(db, box, func(string) string { return "" })
	e.q = queue.New(db, queue.Options{})
	hubOpt.PublicOrigin = testOrigin
	e.hub = realtime.NewHub(hubOpt)
	e.srv = server.New(server.Options{Config: cfg, DB: db, Blob: bl, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.srv.SetAuthenticator(fakeAuth{e})
	e.srv.SetCSRFValidator(fakeAuth{e})
	e.hub.SetAuthenticator(fakeAuth{e})
	e.mod, err = New(Deps{DB: db, Queue: e.q, Hub: e.hub, Blob: bl, Settings: e.set, Secrets: box, Server: e.srv, MaxUploadMB: maxMB, PublicURL: testOrigin})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = e.mod.svc
	e.mod.Mount(e.srv.Groups())
	e.mod.RegisterRealtime(e.hub)
	return e
}

// register makes p usable as "Bearer <p.UserID>". Session principals (no
// TokenID) get CSRF headers automatically in do().
func (e *env) register(p *domain.Principal) *domain.Principal {
	e.mu.Lock()
	e.users[p.UserID] = p
	e.mu.Unlock()
	return p
}

// staff creates a staff user and returns its session principal.
func (e *env) staff(role domain.Role, tier int, teams ...string) *domain.Principal {
	id := storetest.CreateStaff(e.t, e.db, role, tier, teams...)
	if tier == 0 {
		tier = 1
	}
	// storetest.CreateStaff makes the user a member of the Default project (D-20).
	return e.register(&domain.Principal{UserID: id, Kind: domain.KindStaff, Role: role, Tier: tier, Teams: teams,
		Projects: []string{domain.DefaultProjectID}, SessionID: "s-" + id})
}

// customer creates a customer and returns its session principal.
func (e *env) customer(org string, role domain.Role) *domain.Principal {
	id := storetest.CreateCustomerRole(e.t, e.db, org, role)
	return e.register(&domain.Principal{UserID: id, Kind: domain.KindCustomer, Role: role, OrganizationID: org, SessionID: "s-" + id})
}

// pat returns a token principal for the same user with the given scopes.
func (e *env) pat(p *domain.Principal, scopes ...string) *domain.Principal {
	cp := *p
	cp.UserID = p.UserID
	cp.SessionID, cp.TokenID, cp.Scopes = "", "tok-"+p.UserID, scopes
	if cp.Scopes == nil {
		cp.Scopes = []string{}
	}
	e.mu.Lock()
	e.users["pat-"+p.UserID] = &cp
	e.mu.Unlock()
	return &cp
}

type resp struct {
	*http.Response
	body []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (r resp) m(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	r.json(t, &m)
	return m
}

func (r resp) problem(t *testing.T) map[string]any { return r.m(t) }

// do runs a request through the real router and validates request and
// response against api/openapi.yaml.
func (e *env) do(p *domain.Principal, method, path string, body any, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	var raw []byte
	if body != nil {
		switch b := body.(type) {
		case []byte:
			raw = b
		case string:
			raw = []byte(b)
		default:
			raw, _ = json.Marshal(body)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if p != nil {
		key := p.UserID
		if p.TokenID != "" {
			key = "pat-" + p.UserID
		}
		req.Header.Set("Authorization", "Bearer "+key)
		if p.TokenID == "" {
			req.Header.Set("Origin", testOrigin)
			req.Header.Set("X-CSRF-Token", "csrf-ok")
		}
	}
	vreq := req.Clone(context.Background())
	if raw != nil {
		vreq.Body = io.NopCloser(bytes.NewReader(raw))
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	res := rec.Result()
	out, _ := io.ReadAll(res.Body)
	res.Body = io.NopCloser(bytes.NewReader(out))
	if strings.HasPrefix(path, "/api/v1/") {
		errs, err := apispec.ResponseErrors(vreq, res)
		if err != nil {
			e.t.Fatalf("apispec: %v", err)
		}
		for _, msg := range errs {
			e.t.Errorf("response %d for %s %s: %s", res.StatusCode, method, path, msg)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(out))
	return resp{res, out}
}

// expect asserts a status code.
func (r resp) expect(t *testing.T, code int) resp {
	t.Helper()
	if r.StatusCode != code {
		t.Fatalf("status %d, want %d: %s", r.StatusCode, code, r.body)
	}
	return r
}

// eventsOf returns the stored event types of a ticket, oldest first.
func (e *env) eventsOf(ticketID string) []string {
	e.t.Helper()
	rows, err := e.db.Read().QueryContext(context.Background(), `SELECT type FROM ticket_events WHERE ticket_id = ? ORDER BY id`, ticketID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// count runs a COUNT(*) query.
func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	err := e.db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// createTicket creates a ticket through the service as p.
func (e *env) createTicket(p *domain.Principal, in CreateInput) string {
	e.t.Helper()
	if in.Subject == "" {
		in.Subject = "Printer offline"
	}
	if in.BodyMD == "" {
		in.BodyMD = "It does not print."
	}
	id, err := e.svc.Create(context.Background(), p, in)
	if err != nil {
		e.t.Fatalf("create: %v", err)
	}
	return id
}

func sp(s string) *string { return &s }
