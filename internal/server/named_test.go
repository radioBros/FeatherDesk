// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// Tests named in platform_plan.md PLT-003/004/006/007/009/024.

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"favicon.ico":          {Data: []byte("ico")},
		"assets/app-abc.js":    {Data: []byte("console.log(1)")},
		"assets/app-abc.js.gz": {Data: gz([]byte("console.log(1)"))},
		"assets/app-abc.js.br": {Data: []byte("brotli-bytes")},
	}
}

func TestSPAFallback(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	rec := do(s, "GET", "/agent/tickets/42", nil)
	if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestAPINotFoundIsProblemJSON(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	rec := do(s, "GET", "/api/v1/does-not-exist", nil)
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestAssetCacheHeaders(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	if cc := do(s, "GET", "/assets/app-abc.js", nil).Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatal(cc)
	}
	if cc := do(s, "GET", "/", nil).Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatal(cc)
	}
}

func TestServesBrotliWhenAccepted(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	rec := do(s, "GET", "/assets/app-abc.js", func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip, br") })
	if rec.Header().Get("Content-Encoding") != "br" || rec.Body.String() != "brotli-bytes" {
		t.Fatalf("%q", rec.Header().Get("Content-Encoding"))
	}
}

func TestRealIPIgnoresUntrustedXFF(t *testing.T) {
	s := New(Options{Config: testConfig(t, nil)})
	var got string
	s.Groups().Public.Get("/ip", func(w http.ResponseWriter, r *http.Request) { got = ClientIP(r) })
	req := httptest.NewRequest("GET", "/api/v1/ip", nil)
	req.RemoteAddr = "198.51.100.7:1"
	req.Header.Set("X-Forwarded-For", "6.6.6.6")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if got != "198.51.100.7" {
		t.Fatal(got)
	}
}

func TestRecoverReturnsProblem(t *testing.T) { TestPanicRecovered(t) }

func TestBodyLimit413(t *testing.T) { TestBodyLimit(t) }

// TestMiddlewareOrder: request ID and security headers wrap recover (a panic
// response still carries both); authentication runs before the API guards.
func TestMiddlewareOrder(t *testing.T) {
	s := newTestServer(t, Options{})
	s.Groups().Public.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("x") })
	rec := do(s, "GET", "/api/v1/boom", nil)
	if rec.Code != 500 || rec.Header().Get("X-Request-ID") == "" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("panic response lacks outer middleware headers: %v", rec.Header())
	}
	if c := do(s, "GET", "/api/v1/agent/tickets", as("staff:agent")).Code; c != 200 {
		t.Fatalf("auth must run before guards: %d", c)
	}
}

func TestWSRouteSkipsGzip(t *testing.T) {
	s := newTestServer(t, Options{})
	s.Groups().Public.Get("/upgrade", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat("{}", 2000)))
	})
	rec := do(s, "GET", "/api/v1/upgrade", func(r *http.Request) {
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Accept-Encoding", "gzip")
	})
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatal("upgrade requests must not be gzipped")
	}
}

// TestLogRedaction: the access log never records credentials or bodies.
func TestLogRedaction(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{Config: testConfig(t, nil), Logger: slog.New(slog.NewJSONHandler(&buf, nil))})
	s.SetAuthenticator(fakeAuth{})
	s.Groups().Public.Post("/auth/login", func(w http.ResponseWriter, r *http.Request) {})
	req := httptest.NewRequest("POST", "/api/v1/auth/login?token=qs-secret", strings.NewReader(`{"password":"body-secret"}`))
	req.Header.Set("Authorization", "Bearer customer:requester")
	req.Header.Set("Cookie", "fd_session=cookie-secret")
	req.Header.Set("X-CSRF-Token", "csrf-secret")
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	out := buf.String()
	if !strings.Contains(out, `"route":"/api/v1/auth/login"`) {
		t.Fatalf("access log missing: %s", out)
	}
	for _, secret := range []string{"Bearer", "cookie-secret", "csrf-secret", "body-secret", "qs-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	for _, path := range []string{"/", "/api/v1/ping", "/api/v1/nope"} {
		h := do(s, "GET", path, nil).Header()
		if h.Get("Content-Security-Policy") == "" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") == "" {
			t.Errorf("%s missing headers", path)
		}
	}
}

func TestCSPAllowsSameOriginWSS(t *testing.T) {
	s := newTestServer(t, Options{})
	if csp := do(s, "GET", "/api/v1/ping", nil).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self' wss://help.example.com") {
		t.Fatal(csp)
	}
}

func TestLimiterMemoryBounded(t *testing.T) {
	l := NewLimiter(50_000)
	for i := 0; i < 1_000_000; i++ {
		l.Allow("ip:"+strconv.Itoa(i), 1, 1)
	}
	if l.Len() > 50_000 {
		t.Fatalf("limiter holds %d keys", l.Len())
	}
}

func brandingServer(t *testing.T) (*Server, string) {
	db := storetest.Open(t, store.SQLite)
	sha := strings.Repeat("c", 64)
	bl := &memBlob{m: map[string][]byte{"branding/fav.svg": []byte("<svg/>")}}
	if _, err := db.W.Exec(`INSERT INTO branding_assets (id, slot, blob_key, content_type, sha256, created_at) VALUES ('f1','favicon','branding/fav.svg','image/svg+xml',?,?)`, sha, store.Now()); err != nil {
		t.Fatal(err)
	}
	return newTestServer(t, Options{DB: db, Blob: bl, SPA: spaFS()}), sha
}

func TestBrandingAssetImmutableHeaders(t *testing.T) {
	s, sha := brandingServer(t)
	if cc := do(s, "GET", "/branding/"+sha+".svg", nil).Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatal(cc)
	}
}

func TestBrandingSVGSandboxed(t *testing.T) {
	s, sha := brandingServer(t)
	if csp := do(s, "GET", "/branding/"+sha+".svg", nil).Header().Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox") {
		t.Fatal(csp)
	}
}

func TestBrandingUnknownHash404NotSPA(t *testing.T) {
	s, _ := brandingServer(t)
	rec := do(s, "GET", "/branding/"+strings.Repeat("d", 64)+".png", nil)
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestFaviconRedirectsToCurrentAsset(t *testing.T) {
	s, sha := brandingServer(t)
	rec := do(s, "GET", "/favicon.ico", nil)
	if rec.Code != 302 || rec.Header().Get("Location") != "/branding/"+sha+".svg" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
	}
}
