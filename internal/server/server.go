// SPDX-License-Identifier: AGPL-3.0-only

// Package server owns the HTTP surface: the chi router, the middleware stack,
// the route groups modules mount on, the embedded SPA, health, metrics and
// branding assets (PLT-003/004/007/008/009/013/020/021/024).
package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Authenticator resolves the caller from a session cookie, a bearer PAT or a
// ws-ticket. It returns (nil, nil) for anonymous requests and an error (use
// problems.Unauthenticated) for credentials that are present but invalid.
type Authenticator interface {
	Authenticate(r *http.Request) (*domain.Principal, error)
}

// CSRFValidator checks the X-CSRF-Token header of a cookie-authenticated
// mutation against the session's token.
type CSRFValidator interface {
	ValidCSRF(p *domain.Principal, token string) bool
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(r *http.Request) (*domain.Principal, error)

func (f AuthenticatorFunc) Authenticate(r *http.Request) (*domain.Principal, error) { return f(r) }

// Groups are the routers modules mount on. Every group already carries its
// guard middleware (docs/dev/internals.md "Wiring contract").
type Groups struct {
	Public  chi.Router // /api/v1, anonymous allowed (rate-limit sensitive routes yourself)
	Authed  chi.Router // /api/v1, any principal
	Portal  chi.Router // /api/v1/portal, customers only (others → 404, I-1)
	Agent   chi.Router // /api/v1/agent, staff only (others → 404)
	Admin   chi.Router // /api/v1/admin, admin only
	Support chi.Router // /api/v1/admin, supervisor or admin
	Root    chi.Router // /, no API middleware (inbound webhooks, sitemap)
}

// Options configure the server; DB and Blob are optional (health/branding).
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	DB     *store.DB
	Blob   blob.Store
	SPA    fs.FS  // built web app (web.Dist()); nil = no SPA
	Spec   []byte // api/openapi.yaml, served at /api/v1/openapi.yaml
	Ready  func(ctx context.Context) error
}

// Server is the HTTP front of the app.
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	db      *store.DB
	blob    blob.Store
	root    *chi.Mux
	groups  Groups
	auth    atomic.Pointer[authHolder]
	csrf    atomic.Pointer[csrfHolder]
	metrics *Metrics
	limiter *Limiter
	idem    *idempotency
	ready   func(ctx context.Context) error
	brand   *brandCache
	portal  atomic.Pointer[func() bool]
	http    *http.Server
}

type authHolder struct{ a Authenticator }
type csrfHolder struct{ v CSRFValidator }

// New builds the router and all groups. Modules mount routes via Groups().
func New(opt Options) *Server {
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	s := &Server{
		cfg:     opt.Config,
		log:     opt.Logger,
		db:      opt.DB,
		blob:    opt.Blob,
		root:    chi.NewRouter(),
		metrics: NewMetrics(),
		limiter: NewLimiter(50_000),
		ready:   opt.Ready,
		brand:   newBrandCache(32),
	}
	s.auth.Store(&authHolder{a: AuthenticatorFunc(func(*http.Request) (*domain.Principal, error) { return nil, nil })})
	s.csrf.Store(&csrfHolder{v: denyCSRF{}})
	if opt.DB != nil {
		s.idem = newIdempotency(opt.DB)
	}

	r := s.root
	r.Use(s.requestID, s.realIP, s.recoverer, s.accessLog, s.securityHeaders, s.gzip, s.authenticate)
	spa := newSPA(opt.SPA)
	if opt.DB != nil && opt.Blob != nil {
		spa = s.withFavicon(spa) // /favicon.ico → uploaded brand favicon (not an API route)
	}
	r.NotFound(s.notFound(spa))
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		problems.Write(w, r, problems.New(problems.SlugMethodNotAllowed, ""))
	})
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.Get("/readyz", s.readyz)
	r.Get("/metrics", s.serveMetrics)
	if opt.DB != nil && opt.Blob != nil {
		r.Get("/branding/{file}", s.serveBranding)
	}

	api := chi.NewRouter()
	api.Use(s.bodyLimit, s.csrfCheck)
	api.NotFound(func(w http.ResponseWriter, r *http.Request) { problems.Write(w, r, problems.NotFound()) })
	api.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		problems.Write(w, r, problems.New(problems.SlugMethodNotAllowed, ""))
	})
	if len(opt.Spec) > 0 {
		spec := opt.Spec
		api.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(spec)
		})
	}
	portal := chi.NewRouter()
	portal.Use(requireKind(domain.KindCustomer), s.portalOpen)
	agent := chi.NewRouter()
	agent.Use(requireKind(domain.KindStaff))
	admin := chi.NewRouter()
	admin.Use(requireKind(domain.KindStaff))
	for _, sub := range []*chi.Mux{portal, agent, admin} {
		sub.NotFound(func(w http.ResponseWriter, r *http.Request) { problems.Write(w, r, problems.NotFound()) })
	}
	api.Mount("/portal", portal)
	api.Mount("/agent", agent)
	api.Mount("/admin", admin)
	r.Mount("/api/v1", api)

	s.groups = Groups{
		Public:  api,
		Authed:  api.With(requireAuth),
		Portal:  portal,
		Agent:   agent,
		Admin:   admin.With(requireRole(domain.RoleAdmin)),
		Support: admin.With(requireRole(domain.RoleAdmin, domain.RoleSupervisor)),
		Root:    r,
	}
	return s
}

// Groups returns the mount points for modules.
func (s *Server) Groups() Groups { return s.groups }

// Router returns the root router (route-inventory tests walk it).
func (s *Server) Router() *chi.Mux { return s.root }

// Handler is the root http.Handler.
func (s *Server) Handler() http.Handler { return s.root }

// Metrics exposes the metrics registry (modules add gauges).
func (s *Server) Metrics() *Metrics { return s.metrics }

// SetAuthenticator plugs in authn (phase 2). Safe to call at any time.
func (s *Server) SetAuthenticator(a Authenticator) { s.auth.Store(&authHolder{a: a}) }

// SetPortalGate plugs in the "customer portal enabled" check (support.portal_enabled).
// While it reports false, customer principals get 404 `portal-disabled` on
// every /api/v1/portal route. Without a gate the portal is open.
func (s *Server) SetPortalGate(enabled func() bool) { s.portal.Store(&enabled) }

// portalOpen refuses customer principals while the portal is switched off.
func (s *Server) portalOpen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g := s.portal.Load(); g != nil && !(*g)() {
			problems.Write(w, r, problems.New(problems.SlugPortalDisabled, "the customer portal is disabled"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SetCSRFValidator plugs in authn's CSRF token check.
func (s *Server) SetCSRFValidator(v CSRFValidator) { s.csrf.Store(&csrfHolder{v: v}) }

// Limit returns a per-route token-bucket middleware keyed by principal or IP.
func (s *Server) Limit(name string, perSecond float64, burst int) func(http.Handler) http.Handler {
	return s.limiter.Middleware(name, perSecond, burst)
}

// Idempotent returns the Idempotency-Key middleware (opt-in per route, PLT-020).
func (s *Server) Idempotent() func(http.Handler) http.Handler {
	if s.idem == nil {
		return func(h http.Handler) http.Handler { return h }
	}
	return s.idem.middleware
}

// Run serves until ctx is cancelled, then shuts down gracefully within
// ST_SHUTDOWN_TIMEOUT. beforeShutdown runs first (close WebSockets with 1001).
func (s *Server) Run(ctx context.Context, ln net.Listener, beforeShutdown func()) error {
	s.http = &http.Server{
		Handler:           s.root,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	if beforeShutdown != nil {
		beforeShutdown()
	}
	timeout := 20 * time.Second
	if s.cfg != nil && s.cfg.ShutdownTimeout > 0 {
		timeout = s.cfg.ShutdownTimeout
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	err := s.http.Shutdown(sctx)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

type denyCSRF struct{}

func (denyCSRF) ValidCSRF(*domain.Principal, string) bool { return false }

// ---- request context ----

type principalKey struct{}
type ipKey struct{}

// WithPrincipal stores the caller in ctx.
func WithPrincipal(ctx context.Context, p *domain.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the caller or nil (anonymous).
func PrincipalFrom(ctx context.Context) *domain.Principal {
	p, _ := ctx.Value(principalKey{}).(*domain.Principal)
	return p
}

// ClientIP returns the client IP resolved by the real-IP middleware.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ipKey{}).(string); ok {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Logger returns the request-scoped logger (carries request_id, principal_id).
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(problems.LoggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// IsHTTPS reports whether the client connection is TLS (directly or via a trusted proxy).
func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if v, ok := r.Context().Value(protoKey{}).(string); ok {
		return v == "https"
	}
	return false
}

type protoKey struct{}
