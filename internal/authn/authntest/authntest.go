// SPDX-License-Identifier: AGPL-3.0-only

// Package authntest builds a real app router (app.Build with the authn and
// staff-admin modules) on a storetest database, plus a small HTTP client that
// carries the session cookie and CSRF token and validates every response
// against api/openapi.yaml. Test-only: never import from runtime code.
package authntest

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
	"testing/fstest"
	"time"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// PublicURL is the default ST_PUBLIC_URL of test instances.
const PublicURL = "http://localhost:8080"

// TestPassword passes the password rules.
const TestPassword = "correct-horse-battery-9"

// Env is one running instance (router not listening; requests go through
// ServeHTTP unless Serve is called).
type Env struct {
	T      testing.TB
	App    *app.App
	DB     *store.DB
	Auth   *authn.Module
	Staff  *directory.StaffAdmin
	Logs   *SyncBuffer
	Vars   map[string]string
	Clock  *Clock
	mu     sync.Mutex
	mails  []authn.MailPayload
	Extra  []app.Factory
	Origin string
}

// Options tune New.
type Options struct {
	Env      map[string]string // extra ST_* variables
	Throttle bool              // keep the login throttle on
	Extra    []app.Factory     // additional modules (mounted after authn)
	KeepMail bool              // do not replace the mailer (use the real queue)
}

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// SyncBuffer is a goroutine-safe log sink.
type SyncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *SyncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *SyncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var fastArgonOnce sync.Once

// FastArgon lowers argon2id cost for the whole test binary.
func FastArgon() {
	fastArgonOnce.Do(func() {
		authn.SetArgonParams(authn.ArgonParams{MemoryKiB: 512, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	})
}

// New builds an instance on db (from storetest.Engines).
func New(t testing.TB, db *store.DB, o Options) *Env {
	t.Helper()
	FastArgon()
	vars := map[string]string{
		"ST_SECRET_KEY": strings.Repeat("s", 32), "ST_DATA_DIR": t.TempDir(), "ST_PUBLIC_URL": PublicURL,
		"ST_DATABASE_URL": db.URL, "ST_LOG_FORMAT": "text",
	}
	for k, v := range o.Env {
		vars[k] = v
	}
	getenv := func(k string) string { return vars[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{T: t, Logs: &SyncBuffer{}, Vars: vars, Clock: &Clock{t: time.Now().UTC().Truncate(time.Microsecond)}}
	e.Origin = cfg.PublicOrigin()
	factories := []app.Factory{
		func(d *app.Deps) (app.Module, error) {
			m, err := authn.New(authn.Deps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Queue: d.Queue, Hub: d.Hub, Settings: d.Settings,
				Secrets: d.Secrets, Server: d.Server, Getenv: d.Getenv, Version: "test"})
			if err != nil {
				return nil, err
			}
			e.Auth = m
			return m, nil
		},
		func(d *app.Deps) (app.Module, error) {
			s, err := directory.NewStaffAdmin(directory.StaffAdminDeps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Queue: d.Queue, Settings: d.Settings})
			e.Staff = s
			return s, err
		},
	}
	factories = append(factories, o.Extra...)
	a, err := app.Build(context.Background(), cfg, app.BuildOptions{
		Logger: slog.New(slog.NewTextHandler(e.Logs, &slog.HandlerOptions{Level: slog.LevelDebug})), Getenv: getenv,
		SPA: fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}}, Modules: factories, Version: "test",
		QueueOptions: &queue.Options{Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("app.Build: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	})
	e.App, e.DB = a, a.Deps.DB
	e.Auth.SetClock(e.Clock.Now)
	e.Auth.SetThrottle(o.Throttle)
	if !o.KeepMail {
		e.Auth.SetMailer(func(ctx context.Context, p authn.MailPayload) error {
			e.mu.Lock()
			e.mails = append(e.mails, p)
			e.mu.Unlock()
			return nil
		})
	}
	return e
}

// Mails returns the captured auth mails.
func (e *Env) Mails() []authn.MailPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]authn.MailPayload(nil), e.mails...)
}

// LastMail returns the last captured mail to `to` (fails if none).
func (e *Env) LastMail(to string) authn.MailPayload {
	e.T.Helper()
	ms := e.Mails()
	for i := len(ms) - 1; i >= 0; i-- {
		if ms[i].To == to {
			return ms[i]
		}
	}
	e.T.Fatalf("no mail to %s (have %d)", to, len(ms))
	return authn.MailPayload{}
}

// TokenFromURL extracts ?token= from a mail link.
func TokenFromURL(u string) string {
	_, q, _ := strings.Cut(u, "token=")
	q, _, _ = strings.Cut(q, "&")
	return strings.ReplaceAll(q, "%3D", "=")
}

// Handler is the root handler.
func (e *Env) Handler() http.Handler { return e.App.Server.Handler() }

// Exec runs one statement on the instance DB.
func (e *Env) Exec(q string, args ...any) {
	e.T.Helper()
	err := e.DB.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	})
	if err != nil {
		e.T.Fatalf("exec %q: %v", q, err)
	}
}

// Int runs a scalar COUNT-like query.
func (e *Env) Int(q string, args ...any) int {
	e.T.Helper()
	var n int
	if err := e.DB.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.T.Fatalf("query %q: %v", q, err)
	}
	return n
}

// Str runs a scalar text query.
func (e *Env) Str(q string, args ...any) string {
	e.T.Helper()
	var s store.NullString
	if err := e.DB.Read().QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		e.T.Fatalf("query %q: %v", q, err)
	}
	return s.String
}

// ---- users ----

// User is a fixture user with a known password.
type User struct {
	ID, Email string
	Kind      domain.UserKind
}

// SetPassword gives a fixture user the TestPassword.
func (e *Env) SetPassword(id string) {
	e.T.Helper()
	h, err := authn.HashPassword(context.Background(), TestPassword)
	if err != nil {
		e.T.Fatal(err)
	}
	e.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, h, id)
}

// Staff creates a staff user with a local password (break_glass for admins
// when bg is true).
func (e *Env) StaffUser(role domain.Role, tier int, bg bool, teams ...string) User {
	e.T.Helper()
	id := storetest.CreateStaff(e.T, e.DB, role, tier, teams...)
	e.SetPassword(id)
	if bg {
		e.Exec(`UPDATE users SET break_glass = ? WHERE id = ?`, true, id)
	}
	return User{ID: id, Email: e.Str(`SELECT email FROM users WHERE id = ?`, id), Kind: domain.KindStaff}
}

// Customer creates an active customer with a local password.
func (e *Env) Customer(org string, role domain.Role) User {
	e.T.Helper()
	id := storetest.CreateCustomerRole(e.T, e.DB, org, role)
	e.SetPassword(id)
	return User{ID: id, Email: e.Str(`SELECT email FROM users WHERE id = ?`, id), Kind: domain.KindCustomer}
}

// ---- HTTP client ----

// Client carries a session cookie, CSRF token, bearer token and source IP.
type Client struct {
	E       *Env
	Cookie  string
	CSRF    string
	Bearer  string
	IP      string
	Origin  string // "" = instance origin; "-" = none
	NoCheck bool   // skip OpenAPI response validation
	Header  http.Header
}

// Client returns an anonymous client.
func (e *Env) Client() *Client { return &Client{E: e, IP: "192.0.2.1"} }

// Login signs u in through POST /auth/login and returns the client.
func (e *Env) Login(u User) *Client {
	e.T.Helper()
	c := e.Client()
	resp, body := c.Do("POST", "/api/v1/auth/login", map[string]any{"audience": string(u.Kind), "email": u.Email, "password": TestPassword})
	if resp.StatusCode != 200 {
		e.T.Fatalf("login %s: %d %s", u.Email, resp.StatusCode, body)
	}
	return c
}

// PAT mints a token for a logged-in client and returns a bearer client.
func (c *Client) PAT(scopes ...string) *Client {
	c.E.T.Helper()
	resp, body := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "test", "scopes": scopes})
	if resp.StatusCode != 201 {
		c.E.T.Fatalf("create PAT: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &out)
	return &Client{E: c.E, Bearer: out.Token, IP: c.IP}
}

// Do sends a request (body is JSON-encoded unless it is a string or nil).
func (c *Client) Do(method, path string, body any) (*http.Response, []byte) {
	c.E.T.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case string:
		rd, ct = strings.NewReader(b), "application/x-www-form-urlencoded"
	case []byte:
		rd, ct = bytes.NewReader(b), "application/json"
	default:
		j, _ := json.Marshal(b)
		rd, ct = bytes.NewReader(j), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = c.IP + ":40000"
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range c.Header {
		req.Header[k] = v
	}
	if method != "GET" && method != "HEAD" {
		switch c.Origin {
		case "":
			req.Header.Set("Origin", c.E.Origin)
		case "-":
		default:
			req.Header.Set("Origin", c.Origin)
		}
		if c.CSRF != "" && c.Bearer == "" {
			req.Header.Set("X-CSRF-Token", c.CSRF)
		}
	}
	if c.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: authn.CookieName, Value: c.Cookie})
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	rec := httptest.NewRecorder()
	c.E.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	data, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if !c.NoCheck && strings.HasPrefix(path, "/api/v1") {
		vreq := httptest.NewRequest(method, path, nil)
		vreq.Header = req.Header.Clone()
		apispec.ValidateResponse(c.E.T, vreq, resp)
		resp.Body = io.NopCloser(bytes.NewReader(data))
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == authn.CookieName {
			if ck.MaxAge < 0 || ck.Value == "" {
				c.Cookie = ""
			} else {
				c.Cookie = ck.Value
			}
		}
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") && resp.StatusCode < 300 {
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			if v, ok := m["csrf_token"].(string); ok && v != "" {
				c.CSRF = v
			}
		}
	}
	return resp, data
}

// JSON decodes body into a map.
func JSON(t testing.TB, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("json: %v: %s", err, body)
	}
	return m
}

// Principal is a convenience to build a request context (unit tests).
func Principal(ctx context.Context, p *domain.Principal) context.Context {
	return server.WithPrincipal(ctx, p)
}

// CaptureStaffMail redirects the staff admin's invite mails to fn.
func CaptureStaffMail(e *Env, fn func(authn.MailPayload)) {
	e.Staff.SetMailer(func(_ context.Context, p authn.MailPayload) { fn(p) })
}
