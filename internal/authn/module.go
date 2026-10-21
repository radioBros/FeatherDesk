// SPDX-License-Identifier: AGPL-3.0-only

// Package authn authenticates callers (auth_plan AUTH-001…AUTH-034):
// server-side sessions, local login, staff and customer OIDC with role/tier
// and team sync, personal access tokens, one-time WebSocket tickets, email
// flows (verify, reset, magic link, invites), the first-run setup wizard and
// the break-glass admin CLI.
//
// It plugs into the platform as server.Authenticator + server.CSRFValidator +
// realtime.Rechecker. It never imports internal/app (app imports it); the app
// registry adapts app.Deps into authn.Deps.
package authn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Deps mirrors the subset of app.Deps that authn needs.
type Deps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Hub      *realtime.Hub
	Settings *settings.Service
	Secrets  *crypto.Box
	Server   *server.Server
	Getenv   func(string) string
	Version  string
}

// Options are the module's own environment variables (read with config.Env).
type Options struct {
	StaffLocal       bool  // ST_AUTH_STAFF_LOCAL (default false; break-glass admins always may)
	SelfRegistration *bool // ST_SELF_REGISTRATION: when set, overrides the support setting
	InsecureCookies  bool  // ST_INSECURE_COOKIES (refused with an https ST_PUBLIC_URL)
	LinkByEmail      bool  // ST_OIDC_LINK_BY_EMAIL

	IdleStaff, AbsoluteStaff       time.Duration
	IdleCustomer, AbsoluteCustomer time.Duration

	BootstrapEmail, BootstrapPassword string

	Staff, Customer *oidcAudience
}

// Mail job kind handled by the notifications module.
const MailJobKind = "mail.auth"

// Module is the authn feature module.
type Module struct {
	d    Deps
	log  *slog.Logger
	opt  Options
	now  func() time.Time
	mail func(ctx context.Context, p MailPayload) error

	touches  *sessionTouches
	thr      *throttle
	wst      *wsTickets
	scopes   *scopeIndex
	jti      *expiringSet
	setup    *setupState
	reauth   *reauthSet // MFA re-authentication windows (D-19 §B)
	hasAdmin atomic.Bool
	portalOn atomic.Bool // support.portal_enabled (customer portal switch)
	cfgCache atomic.Pointer[authConfigCache]
	setupMu  sync.Mutex
}

// New builds the module: parses its environment, runs the env bootstrap of
// the break-glass admin and arms the first-run wizard when needed.
func New(d Deps) (*Module, error) {
	if d.DB == nil || d.Cfg == nil || d.Secrets == nil {
		return nil, errors.New("authn: DB, Cfg and Secrets are required")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	opt, err := parseOptions(d.Getenv, d.Cfg)
	if err != nil {
		return nil, err
	}
	m := &Module{
		d: d, log: d.Log.With("module", "authn"), opt: opt, now: time.Now,
		touches: newSessionTouches(),
		thr:     newThrottle(),
		wst:     newWSTickets(10_000),
		jti:     newExpiringSet(10_000),
		reauth:  newReauthSet(),
	}
	m.portalOn.Store(true)
	m.mail = m.enqueueMail
	m.scopes, err = loadScopeIndex()
	if err != nil {
		return nil, fmt.Errorf("authn: %w", err)
	}
	registerHub(d.Hub)
	ctx := context.Background()
	if err := m.validateTeamMap(ctx); err != nil {
		return nil, err
	}
	if err := m.bootstrapAdmin(ctx); err != nil {
		return nil, err
	}
	if err := m.armSetup(ctx); err != nil {
		return nil, err
	}
	if d.Settings != nil {
		m.syncPortal(ctx) // also revokes customer sessions left over from a disabled run
		d.Settings.OnChange(func(key string) {
			m.cfgCache.Store(nil)
			if key == settings.KeySupport {
				m.syncPortal(context.Background())
			}
		})
	}
	if d.Server != nil {
		d.Server.SetPortalGate(m.portalOn.Load)
	}
	return m, nil
}

func parseOptions(getenv func(string) string, cfg *config.Config) (Options, error) {
	e := config.NewEnv(getenv)
	o := Options{
		StaffLocal:        e.Bool("ST_AUTH_STAFF_LOCAL", false),
		InsecureCookies:   e.Bool("ST_INSECURE_COOKIES", false),
		LinkByEmail:       e.Bool("ST_OIDC_LINK_BY_EMAIL", false),
		IdleStaff:         e.Duration("ST_SESSION_IDLE_STAFF", 12*time.Hour),
		AbsoluteStaff:     e.Duration("ST_SESSION_ABSOLUTE_STAFF", 7*24*time.Hour),
		IdleCustomer:      e.Duration("ST_SESSION_IDLE_CUSTOMER", 30*24*time.Hour),
		AbsoluteCustomer:  e.Duration("ST_SESSION_ABSOLUTE_CUSTOMER", 90*24*time.Hour),
		BootstrapEmail:    normEmail(e.String("ST_BOOTSTRAP_ADMIN_EMAIL", "")),
		BootstrapPassword: e.String("ST_BOOTSTRAP_ADMIN_PASSWORD", ""),
	}
	if _, ok := e.Lookup("ST_SELF_REGISTRATION"); ok {
		v := e.Bool("ST_SELF_REGISTRATION", true)
		o.SelfRegistration = &v
	}
	if o.InsecureCookies && strings.HasPrefix(cfg.PublicURL, "https://") {
		e.Fail("ST_INSECURE_COOKIES: refused because ST_PUBLIC_URL is https")
	}
	for _, d := range []time.Duration{o.IdleStaff, o.AbsoluteStaff, o.IdleCustomer, o.AbsoluteCustomer} {
		if d <= 0 {
			e.Fail("ST_SESSION_*: durations must be > 0")
			break
		}
	}
	if (o.BootstrapEmail == "") != (o.BootstrapPassword == "") {
		e.Fail("ST_BOOTSTRAP_ADMIN_EMAIL / ST_BOOTSTRAP_ADMIN_PASSWORD: set both or neither")
	} else if o.BootstrapPassword != "" {
		if c := PasswordProblem(o.BootstrapPassword); c != "" {
			e.Fail("ST_BOOTSTRAP_ADMIN_PASSWORD: %s", c)
		}
	}
	o.Staff = parseOIDCAudience(e, domain.AudienceStaff)
	o.Customer = parseOIDCAudience(e, domain.AudienceCustomer)
	if err := e.Err(); err != nil {
		return Options{}, err
	}
	return o, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "authn" }

// Authenticator implements app.AuthProvider.
func (m *Module) Authenticator() server.Authenticator { return m }

// CSRFValidator implements app.AuthProvider.
func (m *Module) CSRFValidator() server.CSRFValidator { return m }

// RegisterQueue flushes coalesced session activity every 30 s (the platform
// write-behind keys rows by `id`, but sessions are keyed by `id_hash`).
func (m *Module) RegisterQueue(q *queue.Queue) {
	q.Every("authn.session_touch", 30*time.Second, m.FlushSessionTouches)
	m.registerMFAQueue(q)
}

// Start flushes pending session activity on shutdown.
func (m *Module) Start(ctx context.Context) error {
	<-ctx.Done()
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.FlushSessionTouches(fctx)
}

// ReportSystem adds the OIDC provider status to GET /admin/system.
func (m *Module) ReportSystem(ctx context.Context, into map[string]any) {
	out := map[string]any{}
	for _, a := range []*oidcAudience{m.opt.Staff, m.opt.Customer} {
		if a == nil {
			continue
		}
		out[string(a.audience)] = a.status()
	}
	into["oidc"] = out
}

// Mount registers every authn route on the platform groups.
func (m *Module) Mount(g server.Groups) {
	g.Public.Get("/auth/config", m.handleAuthConfig)
	g.Public.Post("/auth/login", m.handleLogin)
	g.Public.Get("/auth/oidc/{audience}/start", m.handleOIDCStart)
	g.Public.Get("/auth/oidc/{audience}/callback", m.handleOIDCCallback)
	g.Public.Post("/auth/oidc/{audience}/backchannel-logout", m.handleBackchannelLogout)
	g.Public.Post("/auth/register", m.handleRegister)
	g.Public.Post("/auth/verify-email", m.handleVerifyEmail)
	g.Public.Post("/auth/password/forgot", m.handleForgot)
	g.Public.Post("/auth/password/reset", m.handleReset)
	g.Public.Post("/auth/magic-link", m.handleMagicLink)
	g.Public.Post("/auth/invite/accept", m.handleInviteAccept)
	g.Public.Get("/setup/status", m.handleSetupStatus)
	g.Public.Post("/setup/complete", m.handleSetupComplete)

	g.Authed.Post("/auth/logout", m.handleLogout)
	g.Authed.Get("/me", m.handleGetMe)
	g.Authed.Patch("/me", m.handlePatchMe)
	g.Authed.Get("/me/tokens", m.handleListMyTokens)
	g.Authed.Post("/me/tokens", m.handleCreateMyToken)
	g.Authed.Delete("/me/tokens/{id}", m.handleRevokeMyToken)
	g.Authed.Get("/me/sessions", m.handleListSessions)
	g.Authed.Delete("/me/sessions/{id}", m.handleRevokeSession)
	g.Authed.Post("/ws/ticket", m.handleWSTicket)

	g.Portal.Get("/organization/members", m.handleListOrgMembers)
	g.Portal.Post("/organization/members", m.handleInviteOrgMember)
	g.Portal.Patch("/organization/members/{id}", m.handlePatchOrgMember)

	g.Admin.With(RequireScope(domain.ScopeAdmin)).Get("/tokens", m.handleAdminListTokens)
	g.Admin.With(RequireScope(domain.ScopeAdmin)).Delete("/tokens/{id}", m.handleAdminRevokeToken)

	m.mountMFA(g)
}

func audienceParam(r *http.Request) (domain.Audience, bool) {
	switch a := domain.Audience(chi.URLParam(r, "audience")); a {
	case domain.AudienceStaff, domain.AudienceCustomer:
		return a, true
	}
	return "", false
}

// ---- small helpers ----

func normEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func emailDomain(email string) string {
	_, d, ok := strings.Cut(email, "@")
	if !ok {
		return ""
	}
	return strings.ToLower(d)
}

// portalURL / agentURL build absolute links for emails and redirects.
func (m *Module) portalURL(path string, q url.Values) string {
	u := strings.TrimRight(m.d.Cfg.PortalURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (m *Module) agentURL(path string, q url.Values) string {
	u := strings.TrimRight(m.d.Cfg.AgentURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (m *Module) areaURL(kind domain.UserKind, path string, q url.Values) string {
	if kind == domain.KindStaff {
		return m.agentURL(path, q)
	}
	return m.portalURL(path, q)
}

// ---- test hooks (used by internal/authn/authntest) ----

// SetClock replaces the module clock (sessions, PATs, ws-tickets, OIDC).
func (m *Module) SetClock(now func() time.Time) { m.now = now }

// SetMailer replaces the "mail.auth" enqueuer.
// sendMail hands p to the mailer. The callers are anti-enumeration endpoints
// that answer 202 whatever happens, so a delivery failure is logged, not returned.
func (m *Module) sendMail(ctx context.Context, p MailPayload) {
	if err := m.mail(ctx, p); err != nil {
		m.log.Error("send mail", "template", p.Template, "err", err)
	}
}

func (m *Module) SetMailer(fn func(ctx context.Context, p MailPayload) error) { m.mail = fn }

// SetThrottle turns the login/registration throttle on or off.
func (m *Module) SetThrottle(on bool) { m.thr.disabled = !on }

// SessionTouchFlushes counts session last_seen_at flush transactions.
func (m *Module) SessionTouchFlushes() int {
	m.touches.mu.Lock()
	defer m.touches.mu.Unlock()
	return m.touches.flushes
}

// PendingWSTickets is the number of unconsumed ws-tickets in memory.
func (m *Module) PendingWSTickets() int { return m.wst.len() }

// SetArgonParams replaces the argon2id parameters and returns a restore func.
func SetArgonParams(p ArgonParams) (restore func()) {
	old := argon
	argon = p
	return func() { argon = old }
}
