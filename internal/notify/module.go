// SPDX-License-Identifier: AGPL-3.0-only

// Package notify sends email (NOT-001…NOT-022, NOT-032, NOT-034, NOT-035):
// the SMTP sender over go-mail, branded templates in five locales, the
// durable "notify" queue consumer that turns ticket events into mails, auth
// and admin mail jobs, and the admin email settings API.
package notify

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Deps are the platform services the module needs (adapted from app.Deps in
// internal/app/modules.go; this package never imports internal/app).
type Deps struct {
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Blob     blob.Store
	Settings *settings.Service
	Secrets  *crypto.Box
	Server   *server.Server // may be nil
	Cfg      *config.Config
	Getenv   func(string) string
}

// Module is the notify feature module.
type Module struct {
	db     *store.DB
	log    *slog.Logger
	set    *settings.Service
	box    *crypto.Box
	getenv func(string) string
	tpl    *Templates
	sender Sender
	vis    Visibility
	hub    *realtime.Hub // notification frames (RegisterRealtime); nil in tests without a hub

	host      string
	publicURL string
	portalURL string
	agentURL  string
	coalesce  time.Duration

	cfg     atomic.Pointer[SMTPConfig]
	errMu   sync.Mutex
	lastErr string
	sent    *idLRU

	coMu    sync.Mutex
	pending map[string]*pendingCoalesce

	now func() time.Time
}

// current is the most recently built module; Enabled reads it. One process
// builds one app, so "last built" is the running instance (tests that build
// several apps should use EnabledWith).
var current atomic.Pointer[Module]

// New builds the module. It never dials SMTP; it fails only when the
// embedded templates are incomplete (missing key or locale names the file).
func New(d Deps) (*Module, error) {
	tpl, err := LoadTemplates(nil)
	if err != nil {
		return nil, err
	}
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	env := config.NewEnv(getenv)
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	m := &Module{getenv: getenv, db: d.DB, log: log.With("module", "notify"), set: d.Settings, box: d.Secrets, tpl: tpl, sender: SMTPSender{},
		coalesce: time.Duration(env.Int("FD_NOTIFY_COALESCE_SECONDS", 120)) * time.Second,
		sent:     newIDLRU(4096), pending: map[string]*pendingCoalesce{}, now: time.Now}
	if m.box == nil && d.Settings != nil {
		m.box = d.Settings.Box()
	}
	m.vis = localVisibility{m: m}
	pub := "http://localhost:8080"
	if d.Cfg != nil {
		pub = d.Cfg.PublicURL
		m.portalURL, m.agentURL = d.Cfg.PortalURL, d.Cfg.AgentURL
	}
	m.publicURL = strings.TrimRight(pub, "/")
	if m.portalURL == "" {
		m.portalURL = m.publicURL
	}
	if m.agentURL == "" {
		m.agentURL = m.publicURL + "/agent"
	}
	m.portalURL, m.agentURL = strings.TrimRight(m.portalURL, "/"), strings.TrimRight(m.agentURL, "/")
	m.host = thread.HostFromURL(m.publicURL)
	if d.Settings != nil {
		d.Settings.OnChange(func(key string) {
			if key == settings.KeySMTP {
				m.cfg.Store(nil) // next send rebuilds the effective config (no restart)
				m.setLastErr("")
			}
		})
	}
	current.Store(m)
	return m, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "notify" }

// SetSender replaces the transport (tests).
func (m *Module) SetSender(s Sender) { m.sender = s }

// SetVisibility replaces the staff visibility predicate (e.g. authz.CanSee once it exists).
func (m *Module) SetVisibility(v Visibility) { m.vis = v }

// Templates exposes the parsed templates.
func (m *Module) Templates() *Templates { return m.tpl }

// RegisterQueue registers the notify consumer and job handlers.
func (m *Module) RegisterQueue(q *queue.Queue) {
	q.Subscribe("notify", queue.SubscribeOptions{Durable: true, Timeout: consumerTimeout, Workers: 1}, notifyFilter, m.handleEvent)
	q.Handle(JobSend, m.handleSendJob, queue.JobOptions{Timeout: 60 * time.Second, MaxAttempts: 8})
	q.Handle(JobCoalesced, m.handleCoalescedJob, queue.JobOptions{Timeout: 60 * time.Second, MaxAttempts: 8})
	q.Handle(JobAuthMail, m.handleAuthJob, queue.JobOptions{Timeout: 60 * time.Second, MaxAttempts: 5})
	q.Handle(JobAdminMail, m.handleAdminJob, queue.JobOptions{Timeout: 120 * time.Second, MaxAttempts: 5})
	// NOT-039: notification centre retention (90 days, 500 per user).
	q.Every("notify.prune_notifications", time.Hour, func(ctx context.Context) error {
		n, err := PruneNotifications(ctx, m.db, store.Now())
		if n > 0 {
			m.log.Info("pruned", "what", "notifications", "rows", n)
		}
		return err
	})
}

// ReportSystem adds "smtp" to GET /admin/system.
func (m *Module) ReportSystem(ctx context.Context, into map[string]any) {
	cfg, _ := m.smtpConfig(ctx)
	last := m.LastError()
	var le any
	if last != "" {
		le = last
	}
	configured := cfg.Configured()
	into["smtp"] = map[string]any{"configured": configured, "ok": configured && last == "", "last_error": le}
}

// Enabled reports whether outbound email is configured on the most recently
// built notify module (false when none was built). Auth uses it to hide
// self-registration and password reset (NOT-022, AUTH-016).
func Enabled(ctx context.Context) bool {
	m := current.Load()
	if m == nil {
		return false
	}
	c, err := m.smtpConfig(ctx)
	return err == nil && c.Configured()
}

// EnabledWith is the stateless variant: the effective SMTP host is set.
func EnabledWith(ctx context.Context, s *settings.Service) bool {
	if s == nil {
		return false
	}
	v, err := s.SMTP(ctx)
	return err == nil && strings.TrimSpace(v.Host) != ""
}

// smtpConfig returns the cached effective config, rebuilding it after a change.
func (m *Module) smtpConfig(ctx context.Context) (*SMTPConfig, error) {
	if c := m.cfg.Load(); c != nil {
		return c, nil
	}
	if m.set == nil {
		return &SMTPConfig{}, nil
	}
	v, err := m.set.SMTP(ctx)
	if err != nil {
		return &SMTPConfig{}, err
	}
	pw := ""
	if v.PasswordSet {
		if pw, err = m.set.SMTPPassword(ctx); err != nil {
			m.log.Warn("smtp password cannot be decrypted (FD_SECRET_KEY changed?)")
			pw = ""
		}
	}
	c := configFrom(v.SMTP, pw)
	m.cfg.Store(c)
	return c, nil
}

// LastError is the last delivery error ("" = ok), without secrets.
func (m *Module) LastError() string {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	return m.lastErr
}

func (m *Module) setLastErr(s string) {
	if len(s) > 300 {
		s = s[:300]
	}
	m.errMu.Lock()
	m.lastErr = s
	m.errMu.Unlock()
}

// idLRU remembers recently sent Message-IDs so a replayed event (crash or a
// spilled retry of the whole event) does not mail the same recipient twice
// within one process lifetime. Bounded.
type idLRU struct {
	mu   sync.Mutex
	set  map[string]struct{}
	ring []string
	next int
}

func newIDLRU(n int) *idLRU { return &idLRU{set: map[string]struct{}{}, ring: make([]string, n)} }

func (l *idLRU) has(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.set[id]
	return ok
}

func (l *idLRU) add(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.set[id]; ok {
		return
	}
	if old := l.ring[l.next]; old != "" {
		delete(l.set, old)
	}
	l.ring[l.next] = id
	l.set[id] = struct{}{}
	l.next = (l.next + 1) % len(l.ring)
}
