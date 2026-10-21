// SPDX-License-Identifier: AGPL-3.0-only

// Package app wires the platform together (docs/dev/internals.md "Wiring
// contract"): it opens the DB and blob store, builds the queue, WebSocket hub,
// settings and HTTP server, mounts every registered module and runs them.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/radioBros/SimpleTickets/api"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/web"
)

// Deps are handed to every module factory.
type Deps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Hub      *realtime.Hub
	Blob     blob.Store
	Settings *settings.Service
	Secrets  *crypto.Box
	Server   *server.Server // Limit(), Idempotent(), Metrics()
	Getenv   func(string) string
	Version  string
	Commit   string
}

// Module is implemented by every feature package; app mounts it.
type Module interface {
	Name() string
	Mount(g server.Groups)
}

// Optional capabilities a module may implement.
type (
	// AuthProvider supplies authentication (authn module).
	AuthProvider interface {
		Authenticator() server.Authenticator
		CSRFValidator() server.CSRFValidator
	}
	// RealtimeProvider registers topic authorizers, event routers, replayer.
	RealtimeProvider interface{ RegisterRealtime(h *realtime.Hub) }
	// QueueProvider registers consumers, job handlers and schedules (before Start).
	QueueProvider interface{ RegisterQueue(q *queue.Queue) }
	// Starter runs background work until ctx ends (e.g. IMAP IDLE).
	Starter interface {
		Start(ctx context.Context) error
	}
	// SystemReporter adds keys (e.g. "smtp", "oidc") to GET /admin/system.
	SystemReporter interface {
		ReportSystem(ctx context.Context, into map[string]any)
	}
)

// Factory builds a module from the shared deps.
type Factory func(d *Deps) (Module, error)

// BuildOptions are knobs for tests and the CLI.
type BuildOptions struct {
	Logger  *slog.Logger
	Getenv  func(string) string
	SPA     fs.FS     // default web.Dist()
	Modules []Factory // default Registry
	Version string
	Commit  string
	// NoMigrate skips migrations even when ST_MIGRATE_ON_BOOT is true.
	NoMigrate bool
	// QueueOptions override queue defaults (tests).
	QueueOptions *queue.Options
	// LockInstance takes the single-instance lock (store.LockInstance) before
	// touching the database and holds it until Run returns or Close is called.
	// `serve` sets it; tests that build several apps on one database do not.
	LockInstance bool
	// BackupDir, when set, receives the automatic pre-migrate SQLite backup
	// (DB-015, newest 3 kept). `serve` sets it to $ST_DATA_DIR/backups.
	BackupDir string
}

// App is a built, not yet running, instance.
type App struct {
	Deps    *Deps
	Server  *server.Server
	Modules []Module
	started time.Time
	release func() // single-instance lock (BuildOptions.LockInstance)
	boot    store.BootReport
	backups string // BuildOptions.BackupDir, for /admin/system last_backup_at
}

// PrincipalFrom returns the caller of a request (nil = anonymous).
func PrincipalFrom(ctx context.Context) *domain.Principal { return server.PrincipalFrom(ctx) }

// WithPrincipal stores p in ctx (tests, background work acting as a user).
func WithPrincipal(ctx context.Context, p *domain.Principal) context.Context {
	return server.WithPrincipal(ctx, p)
}

// NewLogger builds the slog logger from config.
func NewLogger(cfg *config.Config) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel))
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// OpenBlob selects the blob backend: ST_BLOB_URL, else local under ST_DATA_DIR.
func OpenBlob(cfg *config.Config) (blob.Store, error) {
	if cfg.BlobURL == "" {
		return blob.NewLocal(cfg.DataDir)
	}
	return blob.NewFromURL(cfg.BlobURL)
}

// Build opens resources and wires every module. Close with (*App).Close if Run
// is never called.
func Build(ctx context.Context, cfg *config.Config, opt BuildOptions) (*App, error) {
	log := opt.Logger
	if log == nil {
		log = NewLogger(cfg)
	}
	getenv := opt.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	db, err := store.Open(ctx, cfg.EffectiveDatabaseURL(), store.Options{Readers: cfg.DBReaders, PoolSize: cfg.DBPool})
	if err != nil {
		return nil, err
	}
	release := func() {}
	if opt.LockInstance {
		r, err := db.LockInstance(ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("refusing to start: %w", err), db.Close())
		}
		release = r
	}
	fail := func(err error) (*App, error) {
		release()
		return nil, errors.Join(err, db.Close())
	}
	// DB-017: fail fast on a corrupt SQLite file before anything writes to it.
	boot, err := store.BootCheck(ctx, db)
	if err != nil {
		return fail(err)
	}
	log.Info("database check", "engine", db.Dialect.String(), "check", boot.Check, "ok", boot.OK, "duration_ms", boot.Millis)
	if cfg.MigrateOnBoot && !opt.NoMigrate {
		if opt.BackupDir != "" {
			path, err := store.PreMigrateBackup(ctx, db, opt.BackupDir, 3, time.Now())
			if err != nil {
				return fail(err)
			}
			if path != "" {
				log.Info("pre-migrate backup written", "path", path)
			}
		}
		if err := db.Migrate(ctx); err != nil {
			return fail(err)
		}
	}
	bl, err := OpenBlob(cfg)
	if err != nil {
		return fail(fmt.Errorf("blob: %w", err))
	}
	box, err := crypto.NewBox(cfg.SecretKey, cfg.SecretKeyPrev)
	if err != nil {
		return fail(err)
	}
	qopt := queue.Options{Capacity: cfg.QueueCapacity, Logger: log}
	if opt.QueueOptions != nil {
		qopt = *opt.QueueOptions
		qopt.Logger = log
	}
	q := queue.New(db, qopt)
	hub := realtime.NewHub(realtime.Options{PublicOrigin: cfg.PublicOrigin(), AllowedOrigins: cfg.Origins(), MaxConns: cfg.WSMaxConns, MaxConnsPerUser: cfg.WSMaxConnsUser, Logger: log})
	set := settings.New(db, box, getenv)
	spa := opt.SPA
	if spa == nil {
		spa = web.Dist()
	}
	srv := server.New(server.Options{Config: cfg, Logger: log, DB: db, Blob: bl, SPA: spa, Spec: api.Spec, Ready: readiness(db, bl, q)})
	d := &Deps{Cfg: cfg, DB: db, Log: log, Queue: q, Hub: hub, Blob: bl, Settings: set, Secrets: box, Server: srv, Getenv: getenv, Version: opt.Version, Commit: opt.Commit}
	a := &App{Deps: d, Server: srv, release: release, boot: boot, backups: opt.BackupDir}

	g := srv.Groups()
	g.Public.Get("/ws", hub.ServeHTTP)

	factories := opt.Modules
	if factories == nil {
		factories = Registry
	}
	core := newCoreModule(d, a)
	mods := []Module{core}
	for _, f := range factories {
		m, err := f(d)
		if err != nil {
			return fail(err)
		}
		mods = append(mods, m)
	}
	for _, m := range mods {
		m.Mount(g)
		if ap, ok := m.(AuthProvider); ok {
			auth := ap.Authenticator()
			srv.SetAuthenticator(auth)
			hub.SetAuthenticator(auth)
			srv.SetCSRFValidator(ap.CSRFValidator())
			if rc, ok := auth.(realtime.Rechecker); ok {
				hub.SetRechecker(rc)
			}
		}
		if rp, ok := m.(RealtimeProvider); ok {
			rp.RegisterRealtime(hub)
		}
		if qp, ok := m.(QueueProvider); ok {
			qp.RegisterQueue(q)
		}
	}
	a.Modules = mods

	// Realtime fan-out: non-durable, never blocks the dispatcher (D-11).
	q.Subscribe("realtime", queue.SubscribeOptions{Capacity: cfg.QueueCapacity, OnOverflow: hub.Overflow}, nil, hub.HandleEvent)
	registerMaintenance(d)
	set.OnChange(func(key string) {
		if key == settings.KeyBranding {
			hub.Broadcast(realtime.Frame{Type: "event", Event: "settings.branding_changed"})
		}
	})
	m := srv.Metrics()
	m.Gauge("st_ws_connections", "Open WebSocket connections.", func() float64 { return float64(hub.Conns()) })
	m.Counter("st_queue_dispatched_total", "Events dispatched by the queue.", func() float64 { return float64(q.Stats().Dispatched.Load()) })
	m.Counter("st_queue_deferred_writes_total", "deferred_jobs writes (failures and spills only).", func() float64 { return float64(q.Stats().DeferredWrites.Load()) })
	m.Counter("st_queue_dead_total", "Jobs that exhausted their retries.", func() float64 { return float64(q.Stats().Dead.Load()) })
	return a, nil
}

// stallThreshold: the dispatcher reads at least every 5 s (fallback timer).
var stallThreshold = 60 * time.Second

// readiness: DB answers, migrations at head, blob backend reachable.
func readiness(db *store.DB, bl blob.Store, q *queue.Queue) func(ctx context.Context) error {
	var head int
	if ms, err := store.Migrations(db.Dialect); err == nil && len(ms) > 0 {
		head = ms[len(ms)-1].Version
	}
	return func(ctx context.Context) error {
		if err := db.R.PingContext(ctx); err != nil {
			return fmt.Errorf("db: %w", err)
		}
		v, err := db.SchemaVersion(ctx)
		if err != nil {
			return fmt.Errorf("db schema: %w", err)
		}
		if v != head {
			return fmt.Errorf("db schema at %d, binary expects %d", v, head)
		}
		// MAX(version) can be at head while a lower-numbered migration is not
		// applied yet (D-18 ranges): compare the applied set too.
		if pend, err := db.PendingMigrations(ctx); err != nil {
			return fmt.Errorf("db schema: %w", err)
		} else if len(pend) > 0 {
			return fmt.Errorf("db schema: migration %s not applied", pend[0].Name)
		}
		if _, err := bl.Stat(ctx, "readyz-probe"); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return fmt.Errorf("blob: %w", err)
		}
		if q.Stalled(ctx, stallThreshold) {
			return errors.New("queue dispatcher stalled")
		}
		return nil
	}
}

// Run serves until ctx is cancelled, then shuts down in order: close
// WebSockets (1001) → drain HTTP → stop the queue (persist cursor, flush
// write-behind) → close the DB (PLT-015).
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	d := a.Deps
	a.started = time.Now()
	if err := d.Queue.Start(ctx); err != nil {
		// A shutdown that arrives while the queue is still loading its
		// cursor aborts the start; release the listener, lock and DB either way.
		return errors.Join(err, ln.Close(), a.Close())
	}
	bg, cancelBG := context.WithCancel(ctx)
	defer cancelBG()
	// Deliberate CI proof (DEP-012): a 500 ms polling ticker with a real query
	// must make the idle-CPU gate fail. Never merged.
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-bg.Done():
				return
			case <-t.C:
				for i := 0; i < 20; i++ {
					var n int
					_ = d.DB.Read().QueryRowContext(bg, `SELECT COUNT(*) FROM tickets t JOIN ticket_messages m ON m.ticket_id = t.id WHERE t.status <> 'closed' AND m.body_md LIKE '%zz%'`).Scan(&n)
				}
			}
		}
	}()
	for _, m := range a.Modules {
		if s, ok := m.(Starter); ok {
			go func(s Starter, name string) {
				if err := s.Start(bg); err != nil && bg.Err() == nil {
					d.Log.Error("module stopped", "module", name, "err", err)
				}
			}(s, m.Name())
		}
	}
	d.Log.Info("serving", "addr", ln.Addr().String(), "engine", d.DB.Dialect.String(), "blob", d.Blob.Backend(), "version", d.Version)
	err := a.Server.Run(ctx, ln, d.Hub.Shutdown)
	cancelBG()
	timeout := d.Cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	sctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if qerr := d.Queue.Stop(sctx); qerr != nil {
		d.Log.Warn("queue stop", "err", qerr)
	}
	a.unlock()
	if cerr := d.DB.Close(); cerr != nil && err == nil {
		err = cerr
	}
	d.Log.Info("stopped")
	return err
}

// Close releases resources of an App that was built but not run.
func (a *App) Close() error {
	a.unlock()
	return a.Deps.DB.Close()
}

// unlock releases the single-instance lock once (Run and Close may both run).
func (a *App) unlock() {
	if r := a.release; r != nil {
		a.release = nil
		r()
	}
}

// registerMaintenance schedules the store prune jobs (DB-018/019) and the
// SQLite WAL checkpoint. Blob keys returned by prunes are deleted afterwards.
func registerMaintenance(d *Deps) {
	q, db, bl, log := d.Queue, d.DB, d.Blob, d.Log
	simple := func(name string, every time.Duration, fn func(context.Context, *store.DB, time.Time) (int64, error)) {
		q.Every("prune."+name, every, func(ctx context.Context) error {
			n, err := fn(ctx, db, time.Now())
			if n > 0 {
				log.Info("pruned", "what", name, "rows", n)
			}
			return err
		})
	}
	withBlobs := func(name string, every time.Duration, fn func(context.Context, *store.DB, time.Time) (int64, []string, error)) {
		q.Every("prune."+name, every, func(ctx context.Context) error {
			n, keys, err := fn(ctx, db, time.Now())
			for _, k := range keys {
				if derr := bl.Delete(ctx, k); derr != nil && !errors.Is(derr, blob.ErrNotFound) {
					log.Warn("prune: blob delete", "key", k, "err", derr)
				}
			}
			if n > 0 {
				log.Info("pruned", "what", name, "rows", n)
			}
			return err
		})
	}
	day := 24 * time.Hour
	simple("sessions", day, store.PruneSessions)
	simple("auth_tokens", day, store.PruneAuthTokens)
	simple("api_tokens", day, store.PruneAPITokens)
	simple("dead_jobs", day, store.PruneDeadJobs)
	simple("webhook_deliveries", day, store.PruneWebhookDeliveries)
	simple("idempotency_keys", time.Hour, store.PruneIdempotencyKeys)
	withBlobs("orphan_attachments", time.Hour, store.PruneOrphanAttachments)
	withBlobs("inbound_messages", day, store.PruneInboundMessages)
	retention := time.Duration(d.Cfg.AuditRetention) * day
	q.Every("prune.audit_log", day, func(ctx context.Context) error {
		_, err := store.PruneAuditLog(ctx, db, time.Now(), retention)
		return err
	})
	if db.Dialect == store.SQLite {
		q.Every("sqlite.wal_checkpoint", 5*time.Minute, func(ctx context.Context) error {
			_, err := db.W.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`)
			return err
		})
	}
}
