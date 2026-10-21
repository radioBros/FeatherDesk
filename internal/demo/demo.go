// SPDX-License-Identifier: AGPL-3.0-only

// Package demo fills a fresh installation with a complete, believable demo
// data set (`simpletickets seed --demo`): three projects (D-20: "Web App" WEB
// and "Mobile" MOB in the portal, "Internal IT" IT staff-only, with team
// mapping, an organization limited to one project, a Mobile SLA override,
// project-scoped categories, custom field, canned response, macro, rule and
// KB category, a Mobile project admin and an agent who works in Mobile
// only), organizations, staff of every role and tier, customers, teams with
// calendars and assignment strategies,
// categories, custom fields, SLA policies, about sixty tickets spread over two
// months, canned responses, saved views, automation rules and macros, a
// multilingual knowledge base, branding, email, webhook, inbound-mailbox and
// privacy settings, privacy requests, the in-app notification centre, an API
// token and one user enrolled in two-step verification (TOTP).
//
// Everything is written through the real application: the seeder builds the
// full app exactly like `serve` does (app.Build) and drives its HTTP handler
// in-process, signed in as the demo users. Events, timelines, search indexes,
// SLA due dates, snapshots and the audit log are therefore the production
// ones. Timestamps are backdated with store.SetClock, so every row (events
// included) carries the simulated time. Only data with no service API is
// written through store helpers: webhook delivery log rows, inbound log rows,
// KB counters, customer passwords, the IdP-managed flag, the
// pending-verification state, and consent records (only portal
// self-registration writes those, and it needs working email; they go through
// privacy.RecordConsent, the registration hook's own function).
//
// The seeder never starts the queue, never opens a socket and never sends
// mail: with --smtp-mailpit only the email settings point at localhost:1025.
// Work that the queue would do is run synchronously through the same entry
// points the queue calls, never by writing rows by hand:
//   - SLA warnings and breaches: tickets.Service.CheckSLA;
//   - the hourly automation time rules: tickets.Service.RunTimeRules, once
//     (the enabled time rule only adds a note and a tag); trigger rules are
//     not replayed over the scripted history;
//   - privacy exports: privacy.Module.RunExport, so they end up ready;
//   - in-app notifications: every seeded ticket event, in time order and at
//     its own simulated time, goes through notify.Module.DeliverInApp (the
//     notify consumer's recipient resolution and in-app write, without the
//     email half). Some users then read them through /me/notifications.
//
// Finally the dispatcher cursor is moved past every seeded event, so a server
// started on the data replays nothing (no mail, no webhooks, no rules).
//
// Two-step verification is enrolled last, through /me/mfa on the wall clock
// (authn's clock), for a staff member the seeder and the screenshot script
// never sign in as; the policy stays optional. The secret and the recovery
// codes are shown once in the summary, like the API token.
package demo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"testing/fstest"
	"time"

	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/privacy"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

// DefaultPassword is the demo password of every account unless overridden.
const DefaultPassword = "demo-password-123"

// DefaultTickets is the default number of demo tickets.
const DefaultTickets = 60

// settingsKey is the settings row that marks a database as demo-seeded.
const settingsKey = "demo"

var (
	// ErrNotDemo: the database holds data that was not created by the demo seeder.
	ErrNotDemo = errors.New("demo: the database is not empty and was not seeded by the demo; refusing to touch it")
	// ErrAlreadySeeded: the database was seeded by the demo already.
	ErrAlreadySeeded = errors.New("demo: the database already holds demo data; pass --force to wipe it and seed again")
)

// Options configure Run.
type Options struct {
	Tickets     int       // number of tickets (default 60; the scripted scenarios are always created)
	SMTPMailpit bool      // point the email settings at Mailpit (localhost:1025, no auth)
	Force       bool      // wipe and re-seed a database that was itself seeded by the demo
	Password    string    // demo password of every account (default DefaultPassword)
	Now         time.Time // "now" of the simulated history (default time.Now)
	Progress    io.Writer // optional progress lines
}

// Account is one demo login.
type Account struct {
	Role     string // admin | supervisor | agent | idp agent | org_manager | requester | ...
	Email    string
	Password string
	Note     string
	Area     string // agent | portal
}

// Count is one line of the summary table.
type Count struct {
	Name string
	N    int
}

// Summary is what Run reports.
type Summary struct {
	Counts              []Count
	Accounts            []Account
	PortalURL, AgentURL string
	APIToken            string       // shown once
	MFA                 []MFAAccount // two-step verification secrets and recovery codes, shown once
	MailpitURL          string
	Projects            []ProjectInfo // D-20: projects with their ticket counts and sample refs
	Notes               []string
}

// Count returns the number recorded under name (0 when absent).
func (s *Summary) Count(name string) int {
	for _, c := range s.Counts {
		if c.Name == name {
			return c.N
		}
	}
	return 0
}

// seedEnv derives the environment of the seeding app: no outbound mail or
// IMAP, no OIDC discovery, no bootstrap admin; local staff login on (the
// seeder signs in as every demo user) and private/insecure webhook URLs
// allowed so the dead-end webhook can be stored.
func seedEnv(getenv func(string) string) func(string) string {
	return func(k string) string {
		switch {
		case strings.HasPrefix(k, "ST_SMTP_"), strings.HasPrefix(k, "ST_IMAP_"), strings.HasPrefix(k, "ST_OIDC_"),
			strings.HasPrefix(k, "ST_BOOTSTRAP_ADMIN_"), k == "ST_TRUSTED_PROXIES":
			return ""
		case k == "ST_AUTH_STAFF_LOCAL", k == "ST_WEBHOOK_ALLOW_PRIVATE", k == "ST_WEBHOOK_ALLOW_INSECURE", k == "ST_INSECURE_COOKIES":
			return "true"
		case k == "ST_NOTIFY_COALESCE_SECONDS":
			return "0"
		}
		return getenv(k)
	}
}

// seeder carries the state of one Run.
type seeder struct {
	ctx  context.Context
	opt  Options
	a    *app.App
	db   *store.DB
	rng  *rand.Rand
	now  time.Time // wall "now" of this run
	simN atomic.Int64
	out  io.Writer

	svc        *tickets.Service
	dir        *directory.Directory
	authnMod   *authn.Module
	notifyMod  *notify.Module
	privacyMod *privacy.Module
	origin     string
	host       string
	ipSeq      int

	admin                *client
	pat                  *client // bearer client of the admin PAT (channel "api")
	patToken             string
	webhookID, mailboxID string

	orgs  map[string]*orgRef
	staff map[string]*person
	custs map[string]*person
	teams map[string]*teamRef
	cals  map[string]string
	cats  map[string]*catRef
	// projects by key (web, mob, it), D-20
	projects map[string]*projRef
	fields   map[string]*fieldRef

	canned     map[string]string // canned response title → id
	kbArticles map[string]string // slug → id
	tickets    []*tk
	mfa        []MFAAccount

	warnings []string
}

func (s *seeder) logf(format string, args ...any) {
	if s.out != nil {
		fmt.Fprintf(s.out, format+"\n", args...)
	}
}

// sim moves the simulated clock to t (every persisted timestamp from now on);
// wall switches back to the real clock.
func (s *seeder) sim(t time.Time) { s.simN.Store(t.UTC().UnixMicro()) }
func (s *seeder) wall()           { s.simN.Store(0) }

// cur is the current simulated time (the wall clock while not simulating).
func (s *seeder) cur() time.Time {
	if n := s.simN.Load(); n != 0 {
		return time.UnixMicro(n).UTC()
	}
	return time.Now().UTC()
}

// advance moves the simulated clock forward, never past "now" minus a minute.
func (s *seeder) advance(d time.Duration) time.Time {
	t := s.cur().Add(d)
	if limit := s.now.Add(-time.Minute); t.After(limit) {
		t = limit
	}
	s.sim(t)
	return t
}

func (s *seeder) newClient() *client {
	s.ipSeq++
	return newClient(s.a.Server.Handler(), s.origin, s.host, fmt.Sprintf("10.77.%d.%d", s.ipSeq/250, s.ipSeq%250+1))
}

// Run seeds the database named by getenv's configuration (ST_DATABASE_URL or
// the SQLite file in ST_DATA_DIR) and returns the summary.
func Run(ctx context.Context, getenv func(string) string, opt Options) (sum *Summary, err error) {
	if opt.Tickets <= 0 {
		opt.Tickets = DefaultTickets
	}
	if opt.Password == "" {
		opt.Password = DefaultPassword
	}
	if p := authn.PasswordProblem(opt.Password); p != "" {
		return nil, fmt.Errorf("demo: the demo password is rejected (%s): choose a longer or less common one", p)
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	env := seedEnv(getenv)
	cfg, err := config.Load(env)
	if err != nil {
		return nil, err
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.Build(ctx, cfg, app.BuildOptions{Logger: log, Getenv: env, Version: "demo",
		SPA: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>SimpleTickets</title>")}}})
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := a.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	s := &seeder{ctx: ctx, opt: opt, a: a, db: a.Deps.DB, now: opt.Now.UTC().Truncate(time.Microsecond), out: opt.Progress,
		rng: rand.New(rand.NewPCG(20260101, 42)), origin: cfg.PublicOrigin(),
		orgs: map[string]*orgRef{}, staff: map[string]*person{}, custs: map[string]*person{}, teams: map[string]*teamRef{},
		cals: map[string]string{}, cats: map[string]*catRef{}, projects: map[string]*projRef{}, fields: map[string]*fieldRef{}, kbArticles: map[string]string{},
		canned: map[string]string{}}
	s.host = strings.TrimPrefix(strings.TrimPrefix(cfg.PublicOrigin(), "https://"), "http://")
	for _, m := range a.Modules {
		switch x := m.(type) {
		case *authn.Module:
			s.authnMod = x
		case *notify.Module:
			s.notifyMod = x
		case *privacy.Module:
			s.privacyMod = x
		case interface {
			Service() *tickets.Service
			Directory() *directory.Directory
		}:
			s.svc, s.dir = x.Service(), x.Directory()
		}
	}
	if s.authnMod == nil || s.svc == nil || s.dir == nil || s.notifyMod == nil || s.privacyMod == nil {
		return nil, errors.New("demo: authn, tickets, notify or privacy module missing from the app")
	}
	s.authnMod.SetThrottle(false)
	restore := store.SetClock(func() time.Time {
		if n := s.simN.Load(); n != 0 {
			return time.UnixMicro(n)
		}
		return time.Now()
	})
	defer restore()
	defer s.wall()
	return s.run(cfg)
}

func (s *seeder) run(cfg *config.Config) (*Summary, error) {
	if err := s.prepare(); err != nil {
		return nil, err
	}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"administrator", s.setupAdmin},
		{"organizations, calendars and teams", s.setupStructure},
		{"projects", s.setupProjects},
		{"staff", s.setupStaff},
		{"customers", s.setupCustomers},
		{"categories, custom fields and SLA", s.setupSupport},
		{"sign-in and signatures", s.signIn},
		{"canned responses", s.setupCanned},
		{"branding", s.setupBranding},
		{"webhook and inbound mailbox", s.setupIntegrations},
		{"knowledge base", s.setupKB},
		{"tickets", s.seedTickets},
		{"project assignments", s.projectExtras},
		{"saved views", s.setupViews},
		{"automation rules, macros and assignment", s.setupAutomation},
		{"privacy requests, consents and retention", s.setupPrivacy},
		{"finishing touches", s.finish},
	}
	for _, st := range steps {
		s.logf("demo: %s ...", st.name)
		if err := st.fn(); err != nil {
			return nil, fmt.Errorf("demo: %s: %w", st.name, err)
		}
	}
	return s.summary(cfg)
}

// prepare refuses foreign data, wipes an earlier demo on --force and writes
// the marker (before any data, so an interrupted run can be repeated with
// --force).
func (s *seeder) prepare() error {
	var users, tickets int
	if err := s.db.Read().QueryRowContext(s.ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		return err
	}
	if err := s.db.Read().QueryRowContext(s.ctx, `SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil {
		return err
	}
	if users > 0 || tickets > 0 {
		var raw string
		err := s.db.Read().QueryRowContext(s.ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
		switch {
		case err != nil && !errors.Is(store.NotFound(err), store.ErrNotFound):
			return err
		case err != nil:
			return ErrNotDemo
		case !s.opt.Force:
			return ErrAlreadySeeded
		}
		s.logf("demo: wiping the earlier demo data (--force)")
		keys, err := store.WipeData(s.ctx, s.db)
		if err != nil {
			return fmt.Errorf("wipe: %w", err)
		}
		for _, k := range keys {
			if derr := s.a.Deps.Blob.Delete(s.ctx, k); derr != nil && !errors.Is(derr, blob.ErrNotFound) {
				s.warnings = append(s.warnings, "blob "+k+": "+derr.Error())
			}
		}
		s.a.Deps.Settings.Invalidate()
		// The wipe re-created the Default project: drop the directory snapshot
		// (projects, teams, categories) of the app built before it.
		s.dir.Invalidate()
		s.a.Deps.Queue.Wake()
	}
	return s.a.Deps.Settings.Put(s.ctx, settingsKey, map[string]any{"seeded_at": s.now.Format(time.RFC3339), "complete": false}, "")
}
