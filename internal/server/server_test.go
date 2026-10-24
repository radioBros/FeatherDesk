// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func testConfig(t *testing.T, env map[string]string) *config.Config {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	env["FD_SECRET_KEY"] = strings.Repeat("x", 32)
	if env["FD_PUBLIC_URL"] == "" {
		env["FD_PUBLIC_URL"] = "https://help.example.com"
	}
	c, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeAuth maps "Authorization: Test <kind>:<role>" or cookie "sess=<kind>:<role>" to a principal.
type fakeAuth struct{}

func (fakeAuth) Authenticate(r *http.Request) (*domain.Principal, error) {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		spec := strings.TrimPrefix(v, "Bearer ")
		if spec == "bad" {
			return nil, problems.Unauthenticated()
		}
		p := principal(spec)
		p.TokenID = "tok"
		return p, nil
	}
	if c, err := r.Cookie("sess"); err == nil {
		p := principal(c.Value)
		p.SessionID = "s1"
		return p, nil
	}
	return nil, nil
}

func principal(spec string) *domain.Principal {
	kind, role, _ := strings.Cut(spec, ":")
	return &domain.Principal{UserID: "u-" + spec, Kind: domain.UserKind(kind), Role: domain.Role(role), Tier: 1}
}

type fakeCSRF struct{}

func (fakeCSRF) ValidCSRF(p *domain.Principal, tok string) bool { return tok == "good" }

func newTestServer(t *testing.T, opt Options) *Server {
	if opt.Config == nil {
		opt.Config = testConfig(t, nil)
	}
	s := New(opt)
	s.SetAuthenticator(fakeAuth{})
	s.SetCSRFValidator(fakeCSRF{})
	ok := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	g := s.Groups()
	g.Public.Get("/ping", ok)
	g.Public.Post("/auth/login", ok)
	g.Authed.Get("/me", ok)
	g.Authed.Post("/me/thing", ok)
	g.Portal.Get("/tickets", ok)
	g.Agent.Get("/tickets", ok)
	g.Admin.Get("/settings", ok)
	g.Support.Get("/teams", ok)
	return s
}

func do(s *Server, method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func as(spec string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+spec) }
}

func TestGroupGuards(t *testing.T) {
	s := newTestServer(t, Options{})
	cases := []struct {
		path, who string
		want      int
	}{
		{"/api/v1/ping", "", 200},
		{"/api/v1/me", "", 401},
		{"/api/v1/me", "customer:requester", 200},
		{"/api/v1/portal/tickets", "", 401},
		{"/api/v1/portal/tickets", "customer:requester", 200},
		{"/api/v1/portal/tickets", "staff:admin", 404}, // I-1: hide the area
		{"/api/v1/agent/tickets", "customer:requester", 404},
		{"/api/v1/agent/tickets", "staff:agent", 200},
		{"/api/v1/admin/settings", "customer:org_manager", 404},
		{"/api/v1/admin/settings", "staff:agent", 403},
		{"/api/v1/admin/settings", "staff:supervisor", 403},
		{"/api/v1/admin/settings", "staff:admin", 200},
		{"/api/v1/admin/teams", "staff:supervisor", 200},
		{"/api/v1/admin/teams", "staff:agent", 403},
		{"/api/v1/agent/nope", "staff:agent", 404},
		{"/api/v1/me", "bad", 401},
	}
	for _, c := range cases {
		var mod func(*http.Request)
		if c.who != "" {
			mod = as(c.who)
		}
		rec := do(s, "GET", c.path, mod)
		if rec.Code != c.want {
			t.Errorf("%s as %q: got %d want %d (%s)", c.path, c.who, rec.Code, c.want, rec.Body.String())
		}
		if rec.Code >= 400 && rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: error is not problem+json", c.path)
		}
	}
}

func cookie(spec string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "sess", Value: spec}) }
}

func TestCSRF(t *testing.T) {
	s := newTestServer(t, Options{})
	sameOrigin := func(r *http.Request) { r.Header.Set("Origin", "https://help.example.com") }
	chain := func(fs ...func(*http.Request)) func(*http.Request) {
		return func(r *http.Request) {
			for _, f := range fs {
				f(r)
			}
		}
	}
	// TestCSRFMissingTokenRejected
	if c := do(s, "POST", "/api/v1/me/thing", chain(cookie("customer:requester"), sameOrigin)).Code; c != 403 {
		t.Errorf("missing token: %d", c)
	}
	// TestCSRFWrongTokenRejected
	if c := do(s, "POST", "/api/v1/me/thing", chain(cookie("customer:requester"), sameOrigin, func(r *http.Request) { r.Header.Set("X-CSRF-Token", "bad") })).Code; c != 403 {
		t.Errorf("wrong token: %d", c)
	}
	good := func(r *http.Request) { r.Header.Set("X-CSRF-Token", "good") }
	if c := do(s, "POST", "/api/v1/me/thing", chain(cookie("customer:requester"), sameOrigin, good)).Code; c != 200 {
		t.Errorf("valid token: %d", c)
	}
	// TestCSRFForeignOriginRejected
	if c := do(s, "POST", "/api/v1/me/thing", chain(cookie("customer:requester"), good, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })).Code; c != 403 {
		t.Errorf("foreign origin: %d", c)
	}
	// Cookie auth without Origin/Referer is refused.
	if c := do(s, "POST", "/api/v1/me/thing", chain(cookie("customer:requester"), good)).Code; c != 403 {
		t.Errorf("no origin: %d", c)
	}
	// TestCSRFBearerExempt
	if c := do(s, "POST", "/api/v1/me/thing", as("customer:requester")).Code; c != 200 {
		t.Errorf("bearer: %d", c)
	}
	// TestLoginRejectsCrossSiteOrigin
	if c := do(s, "POST", "/api/v1/auth/login", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }).Code; c != 403 {
		t.Errorf("cross-site login: %d", c)
	}
	if c := do(s, "POST", "/api/v1/auth/login", sameOrigin).Code; c != 200 {
		t.Errorf("same-origin login: %d", c)
	}
	// Server-to-server (no Origin) anonymous calls are fine.
	if c := do(s, "POST", "/api/v1/auth/login", nil).Code; c != 200 {
		t.Errorf("no-origin anonymous: %d", c)
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t, Options{})
	rec := do(s, "GET", "/api/v1/ping", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "img-src 'self' data: blob:", "connect-src 'self' wss://help.example.com", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("missing hardening headers")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("missing request id")
	}
	// HSTS only over (trusted) HTTPS: X-Forwarded-Proto from an untrusted peer is ignored.
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be set from an untrusted proxy header")
	}
}

func TestRealIPTrustedProxies(t *testing.T) {
	cfg := testConfig(t, map[string]string{"FD_TRUSTED_PROXIES": "10.0.0.0/8"})
	s := New(Options{Config: cfg})
	var got string
	var https bool
	s.Groups().Public.Get("/ip", func(w http.ResponseWriter, r *http.Request) { got, https = ClientIP(r), IsHTTPS(r) })
	req := httptest.NewRequest("GET", "/api/v1/ip", nil)
	req.RemoteAddr = "10.1.2.3:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.9.9.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if got != "203.0.113.9" || !https {
		t.Fatalf("trusted: ip %q https %v", got, https)
	}
	req = httptest.NewRequest("GET", "/api/v1/ip", nil)
	req.RemoteAddr = "198.51.100.7:1"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if got != "198.51.100.7" {
		t.Fatalf("untrusted peer must not set the IP: %q", got)
	}
}

func TestRateLimit(t *testing.T) {
	s := newTestServer(t, Options{})
	s.Groups().Public.With(s.Limit("login", 1, 3)).Get("/limited", func(w http.ResponseWriter, r *http.Request) {})
	var codes []int
	for i := 0; i < 5; i++ {
		codes = append(codes, do(s, "GET", "/api/v1/limited", nil).Code)
	}
	if codes[2] != 200 || codes[3] != 429 {
		t.Fatalf("codes %v", codes)
	}
	rec := do(s, "GET", "/api/v1/limited", nil)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	// A different principal has its own bucket.
	if c := do(s, "GET", "/api/v1/limited", as("staff:agent")).Code; c != 200 {
		t.Fatalf("other key limited: %d", c)
	}
}

func TestLimiterBounded(t *testing.T) {
	l := NewLimiter(100)
	for i := 0; i < 1000; i++ {
		l.Allow(strings.Repeat("k", i%7)+string(rune(i)), 1, 1)
	}
	if l.Len() != 100 {
		t.Fatalf("limiter grew to %d keys", l.Len())
	}
}

func TestBodyLimit(t *testing.T) {
	s := newTestServer(t, Options{Config: testConfig(t, map[string]string{"FD_MAX_BODY_KB": "1"})})
	read := func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			problems.Write(w, r, err)
		}
	}
	s.Groups().Public.Post("/small", read)
	s.Groups().Public.With(MaxBody(10<<10)).Post("/upload", read)
	big := strings.Repeat("a", 4<<10)
	req := func(path string, chunked bool) int {
		r := httptest.NewRequest("POST", path, strings.NewReader(big))
		if chunked {
			r.ContentLength = -1
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, r)
		return rec.Code
	}
	if c := req("/api/v1/small", false); c != 413 {
		t.Errorf("declared length over limit: %d", c)
	}
	if c := req("/api/v1/small", true); c != 413 {
		t.Errorf("streamed body over limit: %d", c)
	}
	if c := req("/api/v1/upload", true); c != 200 {
		t.Errorf("raised limit: %d", c)
	}
}

func TestGzipJSON(t *testing.T) {
	s := newTestServer(t, Options{})
	s.Groups().Public.Get("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"x":"` + strings.Repeat("y", 5000) + `"}`))
	})
	rec := do(s, "GET", "/api/v1/big", func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") })
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("not gzipped")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if !json.Valid(b) {
		t.Fatal("gzip body invalid")
	}
}

func TestSPA(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app-abc.js":    {Data: []byte("console.log(1)")},
		"assets/app-abc.js.gz": {Data: gz([]byte("console.log(1)"))},
	}
	s := newTestServer(t, Options{SPA: fsys})
	rec := do(s, "GET", "/agent/tickets/42", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("history fallback: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
	}
	rec = do(s, "GET", "/assets/app-abc.js", nil)
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("asset headers %v", rec.Header())
	}
	rec = do(s, "GET", "/assets/app-abc.js", func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") })
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("precompressed asset not served")
	}
	if c := do(s, "GET", "/assets/missing-123.js", nil).Code; c != 404 {
		t.Fatalf("missing asset: %d", c)
	}
	rec = do(s, "GET", "/api/v1/nope", nil)
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("API 404 must be a problem, not the SPA: %d", rec.Code)
	}
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestHealthReadyMetrics(t *testing.T) {
	var fail bool
	cfg := testConfig(t, map[string]string{"FD_METRICS_TOKEN": "mtok"})
	s := newTestServer(t, Options{Config: cfg, Ready: func(ctx context.Context) error {
		if fail {
			return errors.New("db down")
		}
		return nil
	}})
	if c := do(s, "GET", "/healthz", nil).Code; c != 200 {
		t.Fatal("healthz")
	}
	if c := do(s, "GET", "/readyz", nil).Code; c != 200 {
		t.Fatal("readyz")
	}
	fail = true
	if c := do(s, "GET", "/readyz", nil).Code; c != 503 {
		t.Fatal("readyz must fail")
	}
	s.Metrics().Gauge("fd_queue_depth", "queue depth", func() float64 { return 7 })
	if c := do(s, "GET", "/metrics", nil).Code; c != 401 {
		t.Fatalf("metrics without token: %d", c)
	}
	rec := do(s, "GET", "/metrics", func(r *http.Request) { r.Header.Set("Authorization", "Bearer mtok") })
	body := rec.Body.String()
	// Every series and its HELP/TYPE lines use the fd_ prefix.
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		name := strings.TrimPrefix(strings.TrimPrefix(line, "# HELP "), "# TYPE ")
		if !strings.HasPrefix(name, "fd_") {
			t.Errorf("metric line without the fd_ prefix: %q", line)
		}
	}
	for _, want := range []string{"fd_http_requests_total", "fd_queue_depth 7", "fd_go_gc_cpu_seconds_total"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	noTok := newTestServer(t, Options{})
	if c := do(noTok, "GET", "/metrics", nil).Code; c != 404 {
		t.Fatalf("metrics disabled must 404: %d", c)
	}
}

func TestOpenAPIServed(t *testing.T) {
	s := newTestServer(t, Options{Spec: []byte("openapi: 3.1.0\n")})
	rec := do(s, "GET", "/api/v1/openapi.yaml", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "3.1.0") {
		t.Fatalf("spec: %d", rec.Code)
	}
}

func TestPanicRecovered(t *testing.T) {
	s := newTestServer(t, Options{})
	s.Groups().Public.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	rec := do(s, "GET", "/api/v1/boom", nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatalf("panic: %d %s", rec.Code, rec.Body.String())
	}
}

// ---- idempotency (PLT-020) ----

func TestIdempotency(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		s := newTestServer(t, Options{DB: db})
		var mu sync.Mutex
		created := 0
		gate := make(chan struct{})
		s.Groups().Authed.With(s.Idempotent()).Post("/tickets", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Slow") != "" {
				<-gate
			}
			mu.Lock()
			created++
			n := created
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]int{"n": n})
		})
		post := func(key, body, who string, extra func(*http.Request)) *httptest.ResponseRecorder {
			req := httptest.NewRequest("POST", "/api/v1/tickets", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+who)
			req.Header.Set("Idempotency-Key", key)
			if extra != nil {
				extra(req)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			return rec
		}
		a := post("k1", `{"s":1}`, "customer:requester", nil)
		b := post("k1", `{"s":1}`, "customer:requester", nil)
		if a.Code != 201 || b.Code != 201 || a.Body.String() != b.Body.String() || b.Header().Get("Idempotency-Replayed") != "true" || created != 1 {
			t.Fatalf("TestIdempotentReplay: %d %d created=%d", a.Code, b.Code, created)
		}
		if c := post("k1", `{"s":2}`, "customer:requester", nil).Code; c != 422 {
			t.Fatalf("TestIdempotencyMismatch: %d", c)
		}
		// TestIdempotency_KeysPerPrincipal
		if c := post("k1", `{"s":1}`, "staff:agent", nil); c.Code != 201 || created != 2 {
			t.Fatalf("keys must be per principal: %d", c.Code)
		}
		// TestIdempotencyConcurrent: two parallel requests → one creation.
		// Deterministic: release the gate only once the duplicate has already
		// been answered while the first request is still blocked inside it.
		var wg sync.WaitGroup
		res := make([]int, 2)
		answered := make(chan struct{}, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res[i] = post("k2", `{}`, "customer:requester", func(r *http.Request) { r.Header.Set("X-Slow", "1") }).Code
				answered <- struct{}{}
			}(i)
		}
		select {
		case <-answered:
		case <-time.After(30 * time.Second):
			t.Fatal("no request was answered while the other one was in flight")
		}
		close(gate)
		wg.Wait()
		if (res[0] != 201 || res[1] != 409) && (res[0] != 409 || res[1] != 201) {
			t.Fatalf("concurrent: %v", res)
		}
	})
}

// ---- branding assets (PLT-024) ----

type memBlob struct{ m map[string][]byte }

func (b *memBlob) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) (string, error) {
	d, _ := io.ReadAll(r)
	b.m[key] = d
	return "", nil
}

type rsc struct{ *bytes.Reader }

func (rsc) Close() error { return nil }

func (b *memBlob) Open(ctx context.Context, key string) (io.ReadSeekCloser, blob.Info, error) {
	d, ok := b.m[key]
	if !ok {
		return nil, blob.Info{}, blob.ErrNotFound
	}
	return rsc{bytes.NewReader(d)}, blob.Info{Key: key, Size: int64(len(d))}, nil
}
func (b *memBlob) Stat(ctx context.Context, key string) (blob.Info, error) { return blob.Info{}, nil }
func (b *memBlob) Delete(ctx context.Context, key string) error            { return nil }
func (b *memBlob) Backend() string                                         { return "mem" }

func TestBrandingAssets(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	sha := strings.Repeat("a", 64)
	bl := &memBlob{m: map[string][]byte{"branding/logo.svg": []byte("<svg/>")}}
	_, err := db.W.Exec(`INSERT INTO branding_assets (id, slot, blob_key, content_type, sha256, created_at) VALUES ('b1','logo_light','branding/logo.svg','image/svg+xml',?,?)`, sha, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, Options{DB: db, Blob: bl})
	rec := do(s, "GET", "/branding/"+sha+".svg", nil)
	if rec.Code != 200 || rec.Body.String() != "<svg/>" {
		t.Fatalf("branding: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers %v", rec.Header())
	}
	if c := do(s, "GET", "/branding/"+strings.Repeat("b", 64)+".png", nil).Code; c != 404 {
		t.Fatalf("unknown asset: %d", c)
	}
	if c := do(s, "GET", "/branding/../../etc/passwd", nil).Code; c != 404 {
		t.Fatalf("traversal: %d", c)
	}
}

// TestPortalGate: with the portal switched off, customers get 404
// `portal-disabled` on /portal/* while staff keep their own groups.
func TestPortalGate(t *testing.T) {
	s := newTestServer(t, Options{})
	on := true
	s.SetPortalGate(func() bool { return on })
	if rec := do(s, "GET", "/api/v1/portal/tickets", as("customer:requester")); rec.Code != 200 {
		t.Fatalf("portal on: %d", rec.Code)
	}
	on = false
	rec := do(s, "GET", "/api/v1/portal/tickets", as("customer:requester"))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "/problems/portal-disabled") {
		t.Fatalf("portal off: %d %s", rec.Code, rec.Body)
	}
	if rec := do(s, "GET", "/api/v1/agent/tickets", as("staff:agent")); rec.Code != 200 {
		t.Fatalf("staff unaffected: %d", rec.Code)
	}
	if rec := do(s, "GET", "/api/v1/portal/tickets", as("staff:admin")); rec.Code != 404 || strings.Contains(rec.Body.String(), "portal-disabled") {
		t.Fatalf("staff on /portal stays a plain 404: %d %s", rec.Code, rec.Body)
	}
}

func TestSPA_PageMeta(t *testing.T) {
	page := `<html><head><link rel="icon" id="fd-favicon" href="/featherdesk-logo.svg" type="image/svg+xml" /><link rel="manifest" id="fd-manifest" href="/manifest.webmanifest?area=portal" /><title>FeatherDesk</title></head><body></body></html>`
	fsys := fstest.MapFS{"index.html": {Data: []byte(page)}, "index.html.gz": {Data: gz([]byte(page))}}
	s := newTestServer(t, Options{SPA: fsys})
	if body := do(s, "GET", "/", nil).Body.String(); body != page {
		t.Fatalf("without SetPageMeta index.html is served as built: %q", body)
	}
	s.SetPageMeta(func(r *http.Request) PageMeta {
		area := "portal"
		if strings.HasPrefix(r.URL.Path, "/agent") {
			area = "agent"
		}
		return PageMeta{Title: `Acme <Help> & "Co"`, SiteName: "Acme", Description: "We help", ImageURL: "https://help.example.com/branding/a.png", IconURL: "/branding/f.ico",
			ManifestArea: area, ThemeColor: "#2E7D32", AppleTouchIcon: "/branding/b.png", ShortName: "Acme"}
	})
	for _, path := range []string{"/", "/p/ACME", "/index.html", "/agent/tickets"} {
		rec := do(s, "GET", path, func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") })
		body := rec.Body.String()
		if rec.Header().Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(zr)
			body = string(b)
		}
		for _, want := range []string{
			`<title>Acme &lt;Help&gt; &amp; &#34;Co&#34;</title>`,
			`<meta property="og:title" content="Acme &lt;Help&gt; &amp; &#34;Co&#34;" />`,
			`<meta property="og:site_name" content="Acme" />`,
			`<meta name="description" content="We help" />`,
			`<meta property="og:image" content="https://help.example.com/branding/a.png" />`,
			`<link rel="icon" id="fd-favicon" href="/branding/f.ico" />`,
			`<meta name="theme-color" content="#2E7D32" />`,
			`<link rel="apple-touch-icon" href="/branding/b.png" />`,
			`<meta name="apple-mobile-web-app-title" content="Acme" />`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s in %s", path, want, body)
			}
		}
		// Exactly one manifest link, for the area of the path (the static one is replaced, not duplicated).
		area := "portal"
		if strings.HasPrefix(path, "/agent") {
			area = "agent"
		}
		if strings.Count(body, `rel="manifest"`) != 1 || !strings.Contains(body, `href="/manifest.webmanifest?area=`+area+`"`) {
			t.Errorf("%s: want one %s manifest link in %s", path, area, body)
		}
		if strings.Contains(body, "FeatherDesk") || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: static title left or cacheable: %s", path, body)
		}
	}
}
