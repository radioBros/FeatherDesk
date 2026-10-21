// SPDX-License-Identifier: AGPL-3.0-only

// Package privacy is the GDPR tooling of D-19 §C (PRV-001…): data-subject
// exports (an async job building a ZIP in blob storage under privacy/),
// erasure (customer requests with admin approval or auto-approval, and direct
// admin erasure in two modes), retention policies run by a daily job with a
// dry-run preview, and consent records captured at registration.
//
// The package never imports internal/app; app adapts its Deps into
// privacy.Deps (internal/app/modules.go). authn never imports privacy: the
// registration consent hook is plugged in from modules.go.
//
// Audit entries written here never carry PII: ids, modes and counts only.
package privacy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Deps are the platform services the privacy module needs.
type Deps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Blob     blob.Store
	Settings *settings.Service
	Server   *server.Server // rate limits; nil disables them
	// Reindex rebuilds the search index entries of the given tickets
	// synchronously (the tickets service's IndexOne). nil skips it (the
	// `simpletickets reindex` command then repairs the index).
	Reindex func(ctx context.Context, ticketIDs []string) error
}

// Job kinds and schedules.
const (
	JobExport         = "privacy.export"
	scheduleRetention = "privacy.retention"
	exportPrefix      = "privacy"
	redacted          = "[redacted]"
)

// Request types and statuses (privacy_requests).
const (
	TypeExport  = "export"
	TypeErasure = "erasure"

	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
	StatusExpired    = "expired"
	StatusCompleted  = "completed"
	StatusRejected   = "rejected"
)

// Erasure modes.
const (
	ModeAnonymize = "anonymize"
	ModeRedact    = "anonymize_and_redact"
)

// Module is the privacy feature module.
type Module struct {
	d   Deps
	log *slog.Logger
	// exportTimeout bounds one export job (tests lower it).
	exportTimeout time.Duration
	// queueExport and mail are swapped by tests (run exports inline,
	// capture mails); the defaults go through the queue.
	queueExport func(ctx context.Context, id string)
	mail        func(ctx context.Context, a notify.AuthMail)
}

// New builds the module.
func New(d Deps) (*Module, error) {
	if d.DB == nil || d.Blob == nil {
		return nil, errors.New("privacy: DB and Blob are required")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	m := &Module{d: d, log: d.Log, exportTimeout: 10 * time.Minute}
	m.queueExport = func(ctx context.Context, id string) {
		if d.Queue == nil {
			return
		}
		if err := d.Queue.Enqueue(context.WithoutCancel(ctx), JobExport, exportJob{RequestID: id}); err != nil {
			m.log.Error("privacy: export not queued", "request", id, "err", err)
		}
	}
	m.mail = func(ctx context.Context, a notify.AuthMail) {
		if d.Queue == nil {
			return
		}
		if err := d.Queue.Enqueue(context.WithoutCancel(ctx), notify.JobAuthMail, a); err != nil {
			m.log.Error("privacy: mail not queued", "template", a.Template, "err", err)
		}
	}
	return m, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "privacy" }

// Mount registers the self-service and admin routes.
func (m *Module) Mount(g server.Groups) {
	limit := func(name string, perSec float64, burst int) func(http.Handler) http.Handler {
		if m.d.Server == nil {
			return func(h http.Handler) http.Handler { return h }
		}
		return m.d.Server.Limit(name, perSec, burst)
	}
	// Self-service: the browser session (PATs need the admin scope).
	me := g.Authed.With(authn.RequireScope(domain.ScopeAdmin))
	me.Get("/me/privacy", m.handleMyPrivacy)
	me.With(limit("privacy.export", 1.0/60, 3)).Post("/me/privacy/export", m.handleMyExport)
	me.Post("/me/privacy/erasure", m.handleMyErasure)
	me.Get("/me/privacy/exports/{id}", m.handleMyDownload)

	ad := g.Admin.With(authn.RequireScope(domain.ScopeAdmin))
	ad.Get("/privacy/requests", m.handleListRequests)
	ad.Post("/privacy/requests/{id}/approve", m.handleApprove)
	ad.Post("/privacy/requests/{id}/reject", m.handleReject)
	ad.Get("/privacy/exports/{id}", m.handleAdminDownload)
	ad.Post("/privacy/users/{id}/export", m.handleAdminExport)
	ad.Post("/privacy/users/{id}/erase", m.handleErase)
	ad.Get("/privacy/settings", m.handleGetSettings)
	ad.Put("/privacy/settings", m.handlePutSettings)
	ad.Get("/privacy/retention/preview", m.handlePreview)
}

// RegisterQueue registers the export job and the daily retention run.
func (m *Module) RegisterQueue(q *queue.Queue) {
	q.Handle(JobExport, m.handleExportJob, queue.JobOptions{Timeout: m.exportTimeout, MaxAttempts: 1})
	q.Every(scheduleRetention, 24*time.Hour, func(ctx context.Context) error {
		_, err := m.RunRetention(ctx)
		return err
	})
}

// Start re-enqueues exports a crash or restart left behind (Enqueue is
// in-memory first, so a queued job can be lost; the row is the source of truth).
func (m *Module) Start(ctx context.Context) error {
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT id FROM privacy_requests WHERE type = 'export' AND status IN ('pending', 'processing') ORDER BY created_at`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		m.enqueueExport(ctx, id)
	}
	return nil
}

type exportJob struct {
	RequestID string `json:"request_id"`
}

func (m *Module) enqueueExport(ctx context.Context, id string) { m.queueExport(ctx, id) }

func (m *Module) handleExportJob(ctx context.Context, payload json.RawMessage) error {
	var j exportJob
	if err := json.Unmarshal(payload, &j); err != nil || j.RequestID == "" {
		m.log.Error("privacy: bad export payload")
		return nil // not retryable
	}
	return m.RunExport(ctx, j.RequestID)
}

// sendReadyMail tells the requester the export can be downloaded. The mail
// goes through notify's transactional job (privacy.* behaves like auth.*:
// never disabled, never opted out of).
func (m *Module) sendReadyMail(ctx context.Context, r reqRow, days int) {
	if !r.RequestedBy.Valid {
		return
	}
	portal, agent := "", "/agent"
	if m.d.Cfg != nil {
		portal, agent = m.d.Cfg.PortalURL, m.d.Cfg.AgentURL
	}
	url := portal + "/profile/data"
	switch {
	case r.RequestedBy.String != r.UserID:
		url = agent + "/admin/privacy"
	case r.subjectKind == string(domain.KindStaff):
		url = agent + "/profile/data"
	}
	m.mail(ctx, notify.AuthMail{Template: notify.TplPrivacyExportReady, UserID: r.RequestedBy.String,
		Vars: map[string]string{"url": url, "expires_days": itoa(days)}})
}
