// SPDX-License-Identifier: AGPL-3.0-only

// Package mailin turns inbound email into tickets and replies (D-16):
// IMAP IDLE sources and a raw-MIME webhook feed one pipeline that records
// every message once (inbound_messages + raw bytes in the BlobStore), applies
// loop/noise protection, blocklist, per-sender rate limit, sender rules and
// threading, and hands the result to the ticket service through TicketSink.
package mailin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/notify/thread"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// JobProcess is the queue job kind that processes one recorded message.
const JobProcess = "mailin.process"

// Deps are the platform services mailin needs (wired in internal/app/modules.go).
type Deps struct {
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Blob     blob.Store
	Settings *settings.Service
	Secrets  *crypto.Box
	Server   *server.Server // may be nil (tests)
	Cfg      *config.Config
	Getenv   func(string) string
}

// Module is the mailin feature module.
type Module struct {
	d   Deps
	log *slog.Logger
	q   *queue.Queue

	host            string // Message-ID domain (ST_PUBLIC_URL host)
	maxBytes        int64  // raw MIME cap (ST_MAX_ATTACHMENT_MB × 10 MiB)
	ratePerHour     int
	trustedAuthserv []string

	mu        sync.RWMutex
	sink      TicketSink
	resolver  SenderResolver
	staffAuth StaffAuthCheck

	limiter *senderLimiter
	locks   keyedMutex
	sources *supervisor

	// test hooks
	now func() time.Time
}

// New builds the module. It never touches the network.
func New(d Deps) (*Module, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	env := config.NewEnv(d.Getenv)
	maxMB := 25
	publicURL := "http://localhost"
	if d.Cfg != nil {
		if d.Cfg.MaxUploadMB > 0 {
			maxMB = d.Cfg.MaxUploadMB
		}
		publicURL = d.Cfg.PublicURL
	}
	maxMB = env.Int("ST_MAX_ATTACHMENT_MB", maxMB)
	m := &Module{
		d:           d,
		log:         d.Log.With("module", "mailin"),
		q:           d.Queue,
		host:        thread.HostFromURL(publicURL),
		maxBytes:    int64(maxMB) * 10 << 20,
		ratePerHour: env.Int("ST_INBOUND_RATE_PER_HOUR", 20),
		now:         time.Now,
	}
	for _, s := range env.List("ST_INBOUND_TRUSTED_AUTHSERV_IDS") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			m.trustedAuthserv = append(m.trustedAuthserv, s)
		}
	}
	m.resolver = &dbResolver{db: d.DB, set: d.Settings}
	m.limiter = newSenderLimiter(10_000)
	m.sources = newSupervisor(m)
	return m, nil
}

func (m *Module) Name() string { return "mailin" }

// SetTicketSink wires the ticket service (tickets module). Until it is set,
// accepted messages are recorded as failed/no_sink and can be retried.
func (m *Module) SetTicketSink(s TicketSink) {
	m.mu.Lock()
	m.sink = s
	m.mu.Unlock()
}

// SetSenderResolver replaces the built-in DB resolver (e.g. with authn's).
func (m *Module) SetSenderResolver(r SenderResolver) {
	m.mu.Lock()
	m.resolver = r
	m.mu.Unlock()
}

// StaffAuthCheck decides whether a staff From address is authenticated by
// the Authentication-Results headers (one value per line) added by the
// trusted authserv-id. modules.go plugs in authn.ResolveEmailSender (AUTH-034);
// without it mailin uses its built-in check (dkim=pass or aligned spf=pass).
type StaffAuthCheck func(ctx context.Context, from, authResults, trustedAuthServID string) (bool, error)

// SetStaffAuthCheck replaces the built-in staff authentication check.
func (m *Module) SetStaffAuthCheck(fn StaffAuthCheck) {
	m.mu.Lock()
	m.staffAuth = fn
	m.mu.Unlock()
}

func (m *Module) staffAuthCheck() StaffAuthCheck {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.staffAuth
}

// staffAuthenticated applies the plugged-in check (any trusted id passing
// wins), else the built-in authPass.
func (m *Module) staffAuthenticated(ctx context.Context, h mail.Header, trusted []string, from, fromDomain string) (bool, error) {
	fn := m.staffAuthCheck()
	if fn == nil {
		return authPass(h, trusted, fromDomain), nil
	}
	vals := make([]string, 0, len(h["Authentication-Results"]))
	for _, v := range h["Authentication-Results"] {
		vals = append(vals, unfold(v))
	}
	joined := strings.Join(vals, "\n")
	for _, id := range trusted {
		ok, err := fn(ctx, from, joined, id)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) ticketSink() TicketSink {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sink
}

func (m *Module) senderResolver() SenderResolver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resolver
}

type processJob struct {
	ID    string `json:"id"`
	Retry bool   `json:"retry,omitempty"`
}

// RegisterQueue registers the processing job (first attempt in memory,
// failures persisted once to deferred_jobs by the queue).
func (m *Module) RegisterQueue(q *queue.Queue) {
	m.q = q
	q.Handle(JobProcess, func(ctx context.Context, payload json.RawMessage) error {
		var j processJob
		if err := json.Unmarshal(payload, &j); err != nil {
			return nil // malformed: nothing to retry
		}
		return m.Process(ctx, j.ID, j.Retry)
	}, queue.JobOptions{Timeout: 2 * time.Minute, MaxAttempts: 8})
}

// enqueue schedules processing; without a queue it processes synchronously.
func (m *Module) enqueue(ctx context.Context, id string) {
	if m.q != nil {
		if err := m.q.Enqueue(ctx, JobProcess, processJob{ID: id}); err == nil {
			return
		}
	}
	if err := m.Process(context.WithoutCancel(ctx), id, false); err != nil {
		m.log.Warn("inbound: process failed", "id", id, "err", err)
	}
}

// Start re-enqueues messages recorded but never processed (crash recovery),
// creates the env-defined mailbox if ST_IMAP_HOST is set, and runs the IMAP
// sources until ctx ends.
func (m *Module) Start(ctx context.Context) error {
	if err := m.ensureEnvMailbox(ctx); err != nil {
		m.log.Warn("inbound: env mailbox", "err", err)
	}
	if err := m.Recover(ctx); err != nil {
		m.log.Warn("inbound: recover pending", "err", err)
	}
	return m.sources.run(ctx)
}

// Recover re-enqueues rows left in the pending state.
func (m *Module) Recover(ctx context.Context) error {
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT id FROM inbound_messages WHERE status = 'failed' AND reason = ? ORDER BY id`, reasonPending)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		m.enqueue(ctx, id)
	}
	return nil
}

// keyedMutex serialises work per key (cross-mailbox Message-ID dedupe).
type keyedMutex struct {
	mu sync.Mutex
	m  map[string]*keyedEntry
}

type keyedEntry struct {
	mu  sync.Mutex
	ref int
}

func (k *keyedMutex) Lock(key string) func() {
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*keyedEntry{}
	}
	e := k.m[key]
	if e == nil {
		e = &keyedEntry{}
		k.m[key] = e
	}
	e.ref++
	k.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		k.mu.Lock()
		e.ref--
		if e.ref == 0 {
			delete(k.m, key)
		}
		k.mu.Unlock()
	}
}
