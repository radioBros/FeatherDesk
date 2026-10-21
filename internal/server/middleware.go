// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
)

// ---- request ID ----

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		l := s.log.With("request_id", id)
		ctx := context.WithValue(r.Context(), problems.LoggerKey, l)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---- real IP (trusted proxies only) ----

func (s *Server) realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip, proto := host, ""
		if s.trusted(host) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				// Walk from the right; the first untrusted hop is the client.
				for i := len(parts) - 1; i >= 0; i-- {
					cand := strings.TrimSpace(parts[i])
					if cand == "" {
						continue
					}
					ip = cand
					if !s.trusted(cand) {
						break
					}
				}
			}
			proto = strings.ToLower(r.Header.Get("X-Forwarded-Proto"))
		}
		ctx := context.WithValue(r.Context(), ipKey{}, ip)
		if proto != "" {
			ctx = context.WithValue(ctx, protoKey{}, proto)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) trusted(host string) bool {
	if s.cfg == nil || len(s.cfg.TrustedNets) == 0 {
		return false
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, p := range s.cfg.TrustedNets {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// ---- recover ----

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				Logger(r.Context()).Error("panic", "panic", rec, "stack", string(debug.Stack()))
				problems.Write(w, r, problems.Internal(fmt.Errorf("panic: %v", rec)))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---- access log + metrics ----

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		d := time.Since(start)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		s.metrics.observe(r.Method, route, sw.status, d)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		attrs := []any{"method", r.Method, "route", route, "status", sw.status, "bytes", sw.bytes, "duration_ms", d.Milliseconds(), "ip", ClientIP(r)}
		if p := PrincipalFrom(r.Context()); p != nil {
			attrs = append(attrs, "principal_id", p.UserID)
		}
		lvl := slog.LevelInfo
		if sw.status >= 500 {
			lvl = slog.LevelError
		}
		Logger(r.Context()).Log(r.Context(), lvl, "http", attrs...)
	})
}

// ---- security headers + CSP (PLT-007) ----

func (s *Server) csp() string {
	connect := "'self'"
	if s.cfg != nil {
		for _, o := range s.cfg.Origins() {
			ws := strings.Replace(strings.Replace(o, "https://", "wss://", 1), "http://", "ws://", 1)
			connect += " " + ws
		}
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; " +
		"connect-src " + connect + "; frame-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"
}

// previewPolicy is the CSP of the admin email-template preview: rendered
// email HTML, framed by the admin email and branding pages (same origin
// only), never running script (PLT-007; found by web/e2e/csp.spec.ts).
const previewPolicy = "sandbox; default-src 'none'; img-src 'self' data: https:; style-src 'unsafe-inline'; font-src 'self' data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

// sameOriginFramable reports the only responses the SPA itself frames.
func sameOriginFramable(r *http.Request) bool {
	p := r.URL.Path
	return r.Method == http.MethodGet && strings.HasPrefix(p, "/api/v1/admin/email/templates/") && strings.HasSuffix(p, "/preview")
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	policy := s.csp()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if sameOriginFramable(r) {
			h.Set("Content-Security-Policy", previewPolicy)
			h.Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			h.Set("Content-Security-Policy", policy)
			h.Set("X-Frame-Options", "DENY")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if IsHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- gzip ----

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed); return w }}

type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
	enabled bool
}

func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "yaml") || strings.Contains(ct, "svg")
}

func (w *gzipWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	if h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) && !strings.HasPrefix(h.Get("Content-Type"), "text/event-stream") {
		w.enabled = true
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		h.Add("Vary", "Accept-Encoding")
		w.gz = gzPool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
}

func (w *gzipWriter) WriteHeader(code int) {
	if code != http.StatusNoContent && code != http.StatusNotModified && code >= 200 {
		w.decide()
	} else {
		w.decided = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.decided {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.decide()
	}
	if w.enabled {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipWriter) Flush() {
	if w.enabled {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *gzipWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipWriter) close() {
	if w.enabled {
		_ = w.gz.Close()
		gzPool.Put(w.gz)
	}
}

func (s *Server) gzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// ---- authentication ----

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.auth.Load().a.Authenticate(r)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		if p != nil {
			ctx := WithPrincipal(r.Context(), p)
			ctx = context.WithValue(ctx, problems.LoggerKey, Logger(ctx).With("principal_id", p.UserID))
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()) == nil {
			problems.Write(w, r, problems.Unauthenticated())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireKind: anonymous → 401; wrong kind → 404 so the area's existence is
// not disclosed (I-1).
func requireKind(k domain.UserKind) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r.Context())
			switch {
			case p == nil:
				problems.Write(w, r, problems.Unauthenticated())
			case p.Kind != k:
				problems.Write(w, r, problems.NotFound())
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// requireRole runs after requireKind(staff): a staff member without the role gets 403.
func requireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r.Context())
			if p == nil || !slices.Contains(roles, p.Role) {
				problems.Write(w, r, problems.Forbidden(""))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireProjectAdmin admits supervisors, admins and project admins (D-20).
func requireProjectAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := PrincipalFrom(r.Context()); p == nil || !p.IsProjectAdmin() {
			problems.Write(w, r, problems.Forbidden(""))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- body limit ----

type limitKey struct{}

// limitedBody enforces a limit that route middleware can raise (MaxBody) after
// the API-wide default was installed.
type limitedBody struct {
	rc    io.ReadCloser
	limit *int64
	read  int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.read += int64(n)
	if b.read > *b.limit {
		return n, &http.MaxBytesError{Limit: *b.limit}
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

func (s *Server) bodyLimit(next http.Handler) http.Handler {
	def := int64(1 << 20)
	if s.cfg != nil && s.cfg.MaxBodyKB > 0 {
		def = int64(s.cfg.MaxBodyKB) << 10
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > def && r.ContentLength > 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") && !strings.HasPrefix(r.URL.Path, "/api/v1/inbound/") {
			problems.Write(w, r, problems.PayloadTooLarge())
			return
		}
		limit := def
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = &limitedBody{rc: r.Body, limit: &limit}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), limitKey{}, &limit)))
	})
}

// MaxBody raises (or lowers) the body limit of a route, e.g. uploads:
// g.Agent.With(server.MaxBody(25<<20)).Post("/attachments", h).
func MaxBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := r.Context().Value(limitKey{}).(*int64); ok {
				*p = n
			}
			if r.ContentLength > n {
				problems.Write(w, r, problems.PayloadTooLarge())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- CSRF (PLT-008) ----

func isMutation(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

func (s *Server) csrfCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutation(r.Method) || strings.HasPrefix(r.URL.Path, "/api/v1/inbound/") {
			next.ServeHTTP(w, r)
			return
		}
		p := PrincipalFrom(r.Context())
		cookieAuth := p != nil && p.SessionID != "" && p.TokenID == ""
		if cookieAuth {
			if !s.sameOrigin(r, true) {
				problems.Write(w, r, problems.CSRF())
				return
			}
			if !s.csrf.Load().v.ValidCSRF(p, r.Header.Get("X-CSRF-Token")) {
				problems.Write(w, r, problems.CSRF())
				return
			}
		} else if p == nil {
			// Anonymous mutations (login, register): refuse obvious cross-site posts.
			if !s.sameOrigin(r, false) {
				problems.Write(w, r, problems.CSRF())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin checks Origin (or Referer) against ST_PUBLIC_URL and
// ST_ALLOWED_ORIGINS. With required, a request carrying neither header is refused.
func (s *Server) sameOrigin(r *http.Request, required bool) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return false
	}
	var want []string
	if s.cfg != nil {
		want = s.cfg.Origins()
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if i := strings.Index(ref[min(len(ref), 8):], "/"); i >= 0 {
				origin = ref[:min(len(ref), 8)+i]
			} else {
				origin = ref
			}
		}
	}
	if origin == "" {
		return !required
	}
	if len(want) == 0 {
		return true
	}
	for _, w := range want {
		if strings.EqualFold(origin, w) {
			return true
		}
	}
	return false
}

// ---- 404 / SPA fallback ----

func (s *Server) notFound(spa http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if spa != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasPrefix(r.URL.Path, "/api/") {
			spa.ServeHTTP(w, r)
			return
		}
		problems.Write(w, r, problems.NotFound())
	}
}
