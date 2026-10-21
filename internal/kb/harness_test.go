// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"bytes"
	"time"

	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// env is one KB module mounted on the real router with a fake authenticator
// ("Authorization: Bearer <who>"; authn is built in parallel).
type env struct {
	t    *testing.T
	db   *store.DB
	srv  *server.Server
	m    *Module
	hub  *realtime.Hub
	set  *settings.Service
	blob blob.Store
	cfg  *config.Config
	who  map[string]*domain.Principal
}

func (e *env) Authenticate(r *http.Request) (*domain.Principal, error) {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if v == "" {
		return nil, nil
	}
	if p, ok := e.who[v]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}

func newEnv(t *testing.T, db *store.DB) *env {
	t.Helper()
	vars := map[string]string{
		"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": t.TempDir(), "ST_PUBLIC_URL": "https://help.example.com",
		"ST_MAX_UPLOAD_MB": "1",
	}
	cfg, err := config.Load(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	bl, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, cfg: cfg, blob: bl, who: map[string]*domain.Principal{}}
	e.srv = server.New(server.Options{Config: cfg, DB: db, Blob: bl, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.srv.SetAuthenticator(e)
	e.set = settings.New(db, nil, func(string) string { return "" })
	e.hub = realtime.NewHub(realtime.Options{PublicOrigin: cfg.PublicOrigin()})
	e.hub.SetAuthenticator(e)
	m, err := New(Deps{Cfg: cfg, DB: db, Blob: bl, Settings: e.set, Server: e.srv})
	if err != nil {
		t.Fatal(err)
	}
	e.m = m
	m.Mount(e.srv.Groups())
	m.RegisterRealtime(e.hub)
	m.RegisterQueue(queue.New(db, queue.Options{}))

	org := storetest.CreateOrg(t, db, "")
	staff := func(name string, role domain.Role, tier int) {
		id := storetest.CreateStaff(t, db, role, tier)
		e.who[name] = &domain.Principal{UserID: id, Kind: domain.KindStaff, Role: role, Tier: tier, TokenID: "tok-" + name}
	}
	staff("agent", domain.RoleAgent, 1)
	staff("supervisor", domain.RoleSupervisor, 3)
	staff("admin", domain.RoleAdmin, 3)
	cust := storetest.CreateCustomer(t, db, org)
	e.who["customer"] = &domain.Principal{UserID: cust, Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: org, TokenID: "tok-c"}
	// PAT variants (I-6): scopes narrow the owner's permissions.
	agentRO := *e.who["agent"]
	agentRO.Scopes = []string{domain.ScopeTicketsRead}
	e.who["agent-pat-read"] = &agentRO
	supRO := *e.who["supervisor"]
	supRO.Scopes = []string{domain.ScopeKBRead}
	e.who["supervisor-pat-read"] = &supRO
	agentW := *e.who["agent"]
	agentW.Scopes = []string{domain.ScopeKBWrite}
	e.who["agent-pat-write"] = &agentW
	custNoKB := *e.who["customer"]
	custNoKB.Scopes = []string{domain.ScopeTicketsWrite}
	e.who["customer-pat-nokb"] = &custNoKB
	return e
}

func (e *env) p(who string) *domain.Principal { return e.who[who] }

func (e *env) setSupport(mut func(*settings.Support)) {
	e.t.Helper()
	s, err := e.set.Support(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	mut(&s)
	if err := e.set.Put(context.Background(), settings.KeySupport, s, ""); err != nil {
		e.t.Fatal(err)
	}
}

type resp struct {
	*httptest.ResponseRecorder
	req *http.Request
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v\n%s", r.req.URL.Path, err, r.Body.String())
	}
}

// do sends a request through the real router; who "" is anonymous.
// Requests and documented responses are validated against api/openapi.yaml.
func (e *env) do(method, path, who string, body any, hdr ...string) resp {
	e.t.Helper()
	var rd io.Reader
	var raw []byte
	if body != nil {
		switch b := body.(type) {
		case []byte:
			raw = b
		default:
			raw, _ = json.Marshal(b)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+who)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "X-Test-IP" { // client address, not a header
			req.RemoteAddr = hdr[i+1] + ":40000"
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	vreq := httptest.NewRequest(method, path, bytes.NewReader(raw))
	vreq.Header = req.Header.Clone()
	apispec.ValidateResponse(e.t, vreq, rec.Result())
	return resp{rec, req}
}

// guard sends a request expected to be stopped by a platform route guard
// (requireKind/requireRole) or the KB kill switch; the answer is validated
// like any other.
func (e *env) guard(method, path, who string, body any, code int) {
	e.t.Helper()
	e.want(e.do(method, path, who, body), code)
}

func (e *env) want(r resp, code int) {
	e.t.Helper()
	if r.Code != code {
		e.t.Fatalf("%s %s: status %d, want %d\n%s", r.req.Method, r.req.URL.String(), r.Code, code, r.Body.String())
	}
}

// ---- fixtures through the service ----

func (e *env) category(slug, vis, parent string) string {
	e.t.Helper()
	in := categoryInput{Slug: &slug, Visibility: &vis, Title: map[string]string{"en": strings.ToUpper(slug[:1]) + slug[1:]}}
	if parent != "" {
		in.ParentID = json.RawMessage(`"` + parent + `"`)
	}
	c, err := e.m.CreateCategory(context.Background(), e.p("admin"), in)
	if err != nil {
		e.t.Fatalf("category %s: %v", slug, err)
	}
	return c.ID
}

// article creates an article (en) and moves it to status.
func (e *env) article(cat, slug, vis, status, title, body string) string {
	e.t.Helper()
	ctx := context.Background()
	a, err := e.m.CreateArticle(ctx, e.p("admin"), ArticleInput{CategoryID: cat, Slug: slug, Visibility: vis, Locale: "en", Title: title, BodyMD: body})
	if err != nil {
		e.t.Fatalf("article %s: %v", slug, err)
	}
	if status != "draft" {
		if _, err := e.m.PatchArticle(ctx, e.p("admin"), a.ID, ArticlePatch{Status: &status}); err != nil {
			e.t.Fatalf("article %s → %s: %v", slug, status, err)
		}
	}
	return a.ID
}

func (e *env) translate(id, locale, title, body string) {
	e.t.Helper()
	if _, err := e.m.PutTranslation(context.Background(), e.p("admin"), id, locale, TranslationInput{Title: &title, BodyMD: &body}); err != nil {
		e.t.Fatalf("translate %s/%s: %v", id, locale, err)
	}
}

type settingsSupport = settings.Support

// testPrincipals are the matrix principals without a database (nil = anonymous).
func testPrincipals() map[string]*domain.Principal {
	staff := func(r domain.Role, scopes ...string) *domain.Principal {
		return &domain.Principal{UserID: "u-" + string(r), Kind: domain.KindStaff, Role: r, Tier: 1, Scopes: scopes}
	}
	cust := func(scopes ...string) *domain.Principal {
		return &domain.Principal{UserID: "u-c", Kind: domain.KindCustomer, Role: domain.RoleRequester, Scopes: scopes}
	}
	return map[string]*domain.Principal{
		"anonymous":           nil,
		"customer":            cust(),
		"agent":               staff(domain.RoleAgent),
		"supervisor":          staff(domain.RoleSupervisor),
		"admin":               staff(domain.RoleAdmin),
		"agent-pat-read":      staff(domain.RoleAgent, domain.ScopeTicketsRead),
		"supervisor-pat-read": staff(domain.RoleSupervisor, domain.ScopeKBRead),
		"agent-pat-write":     staff(domain.RoleAgent, domain.ScopeKBWrite),
		"customer-pat-nokb":   cust(domain.ScopeTicketsWrite),
	}
}

// ---- WebSocket helpers ----

func wsDial(t *testing.T, srv *httptest.Server, who string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + who}}, Subprotocols: []string{realtime.Subprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	})
	return c
}

func wsRecv(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func wsSubscribe(t *testing.T, c *websocket.Conn, topics ...string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"op": "subscribe", "id": "s1", "topics": topics})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return wsRecv(t, c)
}

// engines runs fn on SQLite and Postgres with a fresh env each.
func engines(t *testing.T, fn func(t *testing.T, e *env)) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) { fn(t, newEnv(t, db)) })
}
