// SPDX-License-Identifier: AGPL-3.0-only

// Package webhooks delivers signed outbound webhooks (NOT-010..014,
// events_spec §3): admin CRUD with sealed secrets, an SSRF guard checked at
// create time and at dial time, a durable queue consumer whose first attempt
// runs in memory, retries through deferred_jobs, auto-disable on 410 or after
// 20 consecutive failures, and a sampled delivery log.
package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Env variables read by this module.
const (
	EnvAllowPrivate  = "ST_WEBHOOK_ALLOW_PRIVATE"  // true: private/loopback targets allowed
	EnvAllowInsecure = "ST_WEBHOOK_ALLOW_INSECURE" // true: http:// targets allowed
)

// Queue names.
const (
	ConsumerName = "webhook"         // durable event consumer
	JobDeliver   = "webhook.deliver" // retry job (deferred_jobs.kind)
)

// MaxAttempts is the total number of delivery attempts (first + retries).
const MaxAttempts = 8

// DisableAfter consecutive failures auto-disables a webhook.
const DisableAfter = 20

// successRing is the number of success rows kept per webhook.
const successRing = 50

// Deps are the module dependencies (wired by internal/app/modules.go).
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

// Module is the webhooks feature module.
type Module struct {
	db       *store.DB
	log      *slog.Logger
	q        *queue.Queue
	box      *crypto.Box
	guard    *Guard
	client   *http.Client
	baseURL  string
	webhookE map[string]bool

	// Now and FirstRetry are injectable for tests (fake clock).
	Now        func() time.Time
	FirstRetry func() time.Duration

	mu    sync.Mutex
	cache []*hook // active webhooks, nil = not loaded
	state map[string]*hookState
}

type hookState struct {
	failing   bool  // last known attempt failed (consecutive_failures > 0)
	successes int64 // successes since start (sampling)
}

// hook is an active webhook with its plaintext secret (memory only).
type hook struct {
	ID              string
	Name            string
	URL             string
	Secret          string
	Events          []string
	IncludeInternal bool
	Failures        int
}

// WebhookEvents are the event types a webhook may subscribe to (OpenAPI EventType).
var WebhookEvents = []string{
	events.TicketCreated, events.TicketStatusChanged, events.TicketPriorityChanged, events.TicketAssigned,
	events.TicketTeamChanged, events.TicketEscalated, events.TicketDeescalated, events.TicketCategoryChanged,
	events.TicketTagsChanged, events.TicketFieldsChanged, events.MessageCreated, events.NoteCreated,
	events.SLAWarning, events.SLABreached, events.TicketCSAT, events.TicketFollowupCreated, events.TicketMerged,
	events.TicketSplit, events.TicketLinked, events.TicketUnlinked, events.TicketChildrenResolved,
}

// New builds the module. It does no network I/O.
func New(d Deps) (*Module, error) {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	env := config.NewEnv(getenv)
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	box := d.Secrets
	if box == nil && d.Settings != nil {
		box = d.Settings.Box()
	}
	g := NewGuard(env.Bool(EnvAllowPrivate, false), env.Bool(EnvAllowInsecure, false), nil)
	m := &Module{
		db:       d.DB,
		log:      log.With("module", "webhooks"),
		q:        d.Queue,
		box:      box,
		guard:    g,
		client:   g.Client(10 * time.Second),
		webhookE: map[string]bool{},
		Now:      time.Now,
		state:    map[string]*hookState{},
	}
	if d.Cfg != nil {
		m.baseURL = d.Cfg.PublicURL
	}
	m.FirstRetry = func() time.Duration {
		return time.Duration(float64(30*time.Second) * (0.8 + 0.4*rand.Float64()))
	}
	for _, t := range WebhookEvents {
		m.webhookE[t] = true
	}
	return m, nil
}

// Guard returns the SSRF guard (tests swap the resolver).
func (m *Module) Guard() *Guard { return m.guard }

// Name implements app.Module.
func (m *Module) Name() string { return "webhooks" }

// Mount registers the admin routes.
func (m *Module) Mount(g server.Groups) {
	g.Admin.Get("/webhooks", m.list)
	g.Admin.Post("/webhooks", m.create)
	g.Admin.Get("/webhooks/{id}", m.get)
	g.Admin.Patch("/webhooks/{id}", m.patch)
	g.Admin.Delete("/webhooks/{id}", m.delete)
	g.Admin.Post("/webhooks/{id}/test", m.test)
	g.Admin.Get("/webhooks/{id}/deliveries", m.deliveries)
}

// RegisterQueue subscribes the durable consumer and the retry job.
func (m *Module) RegisterQueue(q *queue.Queue) {
	m.q = q
	q.Subscribe(ConsumerName, queue.SubscribeOptions{Durable: true, Workers: 2, Timeout: 60 * time.Second},
		func(e events.Event) bool { return m.webhookE[e.Type] }, m.handleEvent)
	q.Handle(JobDeliver, m.handleRetry, queue.JobOptions{Timeout: 20 * time.Second, MaxAttempts: MaxAttempts})
}

// invalidate drops the active-webhook cache (after CRUD or auto-disable).
func (m *Module) invalidate() {
	m.mu.Lock()
	m.cache = nil
	m.mu.Unlock()
}

func (m *Module) st(id string) *hookState {
	s := m.state[id]
	if s == nil {
		s = &hookState{}
		m.state[id] = s
	}
	return s
}

// activeHooks returns the cached active webhooks, loading them once.
func (m *Module) activeHooks(ctx context.Context) ([]*hook, error) {
	m.mu.Lock()
	if m.cache != nil {
		c := m.cache
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()
	rows, err := m.db.Read().QueryContext(ctx, `SELECT id, name, url, secret_enc, events, include_internal, consecutive_failures FROM webhooks WHERE active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*hook{}
	for rows.Next() {
		h, err := m.scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache = out
	for _, h := range out {
		if h.Failures > 0 {
			m.st(h.ID).failing = true
		}
	}
	m.mu.Unlock()
	return out, nil
}

func (m *Module) scanHook(row interface{ Scan(...any) error }) (*hook, error) {
	var h hook
	var enc string
	var evs store.JSON[[]string]
	if err := row.Scan(&h.ID, &h.Name, &h.URL, &enc, &evs, &h.IncludeInternal, &h.Failures); err != nil {
		return nil, err
	}
	h.Events = evs.V
	if enc != "" {
		s, err := m.box.OpenString(settings.PurposeWebhook, enc)
		if err != nil {
			return nil, err
		}
		h.Secret = s
	}
	return &h, nil
}

// loadActive loads one webhook if it is active (retry path).
func (m *Module) loadActive(ctx context.Context, id string) (*hook, error) {
	row := m.db.Read().QueryRowContext(ctx, `SELECT id, name, url, secret_enc, events, include_internal, consecutive_failures FROM webhooks WHERE id = ? AND active`, id)
	h, err := m.scanHook(row)
	if err != nil {
		return nil, store.NotFound(err)
	}
	return h, nil
}

func (h *hook) wants(typ string) bool { return slices.Contains(h.Events, typ) }

// retryPayload is the deferred_jobs payload of JobDeliver.
type retryPayload struct {
	WebhookID string       `json:"webhook_id"`
	Event     events.Event `json:"event"`
}

// deferJob writes the one deferred_jobs row for a failed first attempt
// (attempts = already made). The queue's retry loop takes it from there.
func (m *Module) deferJob(ctx context.Context, webhookID string, e events.Event, attempts int, lastErr string, delay time.Duration) error {
	b, err := json.Marshal(retryPayload{WebhookID: webhookID, Event: e})
	if err != nil {
		return err
	}
	now := m.Now().UTC().Truncate(time.Microsecond)
	if len(lastErr) > 2000 {
		lastErr = lastErr[:2000]
	}
	return m.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO deferred_jobs (id, kind, payload, attempts, max_attempts, next_run_at, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'retrying', ?, ?, ?)`,
			store.NewID(), JobDeliver, string(b), attempts, MaxAttempts, now.Add(delay), lastErr, now, now)
		return err
	})
}
