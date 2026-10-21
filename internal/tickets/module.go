// SPDX-License-Identifier: AGPL-3.0-only

// Package tickets is the ticket domain: visibility-checked reads, the status
// machine, messages, assignment, escalation, attachments, search indexing,
// links/merge/split, custom field values, SLA and business hours, reports,
// realtime topics and the inbound-email entry points.
//
// It does not import internal/app (app imports it through modules.go); the
// adapter in app/modules.go builds Deps from app.Deps.
package tickets

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Deps are the platform services the module needs (a subset of app.Deps).
type Deps struct {
	DB          *store.DB
	Log         *slog.Logger
	Queue       *queue.Queue
	Hub         *realtime.Hub
	Blob        blob.Store
	Settings    *settings.Service
	Secrets     *crypto.Box
	Server      *server.Server // Idempotent(), Limit(); nil in some tests
	MaxUploadMB int            // ST_MAX_UPLOAD_MB (default 25)
	PublicURL   string         // ST_PUBLIC_URL (links in CSAT tokens)
}

// Module mounts the ticket and directory routes and registers queue
// consumers, schedules and realtime topics.
type Module struct {
	d   Deps
	svc *Service
	dir *directory.Directory
	rt  *realtimeRouter
	q   *queue.Queue // set by RegisterQueue (deferred blob deletes)
}

// New builds the module.
func New(d Deps) (*Module, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.MaxUploadMB <= 0 {
		d.MaxUploadMB = 25
	}
	dir := directory.NewDirectory(directory.DirDeps{DB: d.DB, Log: d.Log, Settings: d.Settings, Hub: d.Hub, Blob: d.Blob})
	svc := &Service{db: d.DB, dir: dir, set: d.Settings, blob: d.Blob, hub: d.Hub, box: d.Secrets, log: d.Log,
		maxUpload: int64(d.MaxUploadMB) << 20}
	m := &Module{d: d, svc: svc, dir: dir}
	// M-4 re-validation when staff lose a team, a tier or their account
	// (TKT-017): registered process-wide so the staff admin API reaches it
	// through directory.MembershipChanged. It is idempotent.
	directory.RegisterMembershipHook(svc.revalidateMembership)
	return m, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "tickets" }

// Service exposes the ticket service (mailin, notify, tests).
func (m *Module) Service() *Service { return m.svc }

// Directory exposes the directory service.
func (m *Module) Directory() *directory.Directory { return m.dir }

func (m *Module) idem() func(http.Handler) http.Handler {
	if m.d.Server == nil {
		return func(h http.Handler) http.Handler { return h }
	}
	return m.d.Server.Idempotent()
}

// Mount implements app.Module.
func (m *Module) Mount(g server.Groups) {
	idem := m.idem()
	upload := server.MaxBody(m.svc.maxUpload + 64<<10) // multipart overhead

	g.Agent.Route("/tickets", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.agentList))
		r.With(idem).Method(http.MethodPost, "/", handler(m.agentCreate))
		r.Method(http.MethodGet, "/counts", handler(m.agentCounts))
		r.Method(http.MethodGet, "/by-number/{n}", handler(m.agentByNumber))
		r.Method(http.MethodGet, "/by-ref/{ref}", handler(m.agentByRef))
		r.Method(http.MethodGet, "/{id}", handler(m.agentGet))
		r.Method(http.MethodPatch, "/{id}", handler(m.agentPatch))
		r.Method(http.MethodDelete, "/{id}", handler(m.agentDelete))
		r.With(idem).Method(http.MethodPost, "/{id}/messages", handler(m.agentMessage))
		r.Method(http.MethodPatch, "/{id}/messages/{mid}", handler(m.agentEditMessage))
		r.Method(http.MethodPost, "/{id}/escalate", m.agentEscalate(+1))
		r.Method(http.MethodPost, "/{id}/deescalate", m.agentEscalate(-1))
		r.Method(http.MethodGet, "/{id}/events", handler(m.agentEvents))
		r.Method(http.MethodPost, "/{id}/watchers", handler(m.agentAddWatcher))
		r.Method(http.MethodDelete, "/{id}/watchers/{user_id}", handler(m.agentRemoveWatcher))
		r.Method(http.MethodGet, "/{id}/links", handler(m.agentListLinks))
		r.Method(http.MethodPost, "/{id}/links", handler(m.agentCreateLink))
		r.Method(http.MethodDelete, "/{id}/links/{link_id}", handler(m.agentDeleteLink))
		r.Method(http.MethodPost, "/{id}/merge", handler(m.agentMerge))
		r.With(idem).Method(http.MethodPost, "/{id}/split", handler(m.agentSplit))
		r.With(idem).Method(http.MethodPost, "/{id}/macros/{macro_id}/apply", handler(m.agentApplyMacro))
	})
	g.Agent.With(upload).Method(http.MethodPost, "/attachments", handler(m.uploadAttachment))
	g.Agent.Method(http.MethodGet, "/attachments/{id}", handler(m.downloadAttachment))
	g.Agent.Method(http.MethodGet, "/assignable", handler(m.agentAssignable))
	g.Agent.Method(http.MethodGet, "/tags", handler(m.agentTags))
	g.Agent.Route("/canned-responses", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.agentCanned))
		r.Method(http.MethodPost, "/", handler(m.agentCreateCanned))
		r.Method(http.MethodPatch, "/{id}", handler(m.agentUpdateCanned))
		r.Method(http.MethodDelete, "/{id}", handler(m.agentDeleteCanned))
		r.Method(http.MethodGet, "/{id}/preview", handler(m.agentPreviewCanned))
	})
	g.Agent.Route("/views", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.listViews))
		r.Method(http.MethodPost, "/", handler(m.createView))
		r.Method(http.MethodGet, "/{id}", handler(m.getView))
		r.Method(http.MethodPatch, "/{id}", handler(m.updateView))
		r.Method(http.MethodDelete, "/{id}", handler(m.deleteView))
	})
	g.Agent.Method(http.MethodGet, "/reports/summary", handler(m.reportSummary))

	g.Portal.Route("/tickets", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.portalList))
		r.With(idem).Method(http.MethodPost, "/", handler(m.portalCreate))
		r.Method(http.MethodGet, "/by-number/{n}", handler(m.portalByNumber))
		r.Method(http.MethodGet, "/by-ref/{ref}", handler(m.portalByRef))
		r.Method(http.MethodGet, "/{id}", handler(m.portalGet))
		r.With(idem).Method(http.MethodPost, "/{id}/messages", handler(m.portalReply))
		r.Method(http.MethodPost, "/{id}/resolve", m.portalStatus(func(s *Service, r *http.Request) error {
			return s.CustomerResolve(r.Context(), principal(r), urlParam(r, "id"))
		}))
		r.Method(http.MethodPost, "/{id}/reopen", m.portalStatus(func(s *Service, r *http.Request) error {
			return s.CustomerReopen(r.Context(), principal(r), urlParam(r, "id"))
		}))
		r.Method(http.MethodPatch, "/{id}/fields", m.portalStatus(func(s *Service, r *http.Request) error {
			var in struct {
				CustomFields map[string]any `json:"custom_fields"`
			}
			if err := decodeJSON(r, &in); err != nil {
				return err
			}
			return s.CustomerUpdateFields(r.Context(), principal(r), urlParam(r, "id"), in.CustomFields)
		}))
	})
	// CSAT accepts a session or a signed csat_token without one, so it lives
	// on the public group (more specific than the /portal mount).
	g.Public.Method(http.MethodPost, "/portal/tickets/{id}/csat", handler(m.portalCSAT))
	g.Portal.With(upload).Method(http.MethodPost, "/attachments", handler(m.uploadAttachment))
	g.Portal.Method(http.MethodGet, "/attachments/{id}", handler(m.downloadAttachment))

	g.ProjectAdmin.Route("/canned-responses", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.adminListCanned))
		r.Method(http.MethodPost, "/", handler(m.adminCreateCanned))
		r.Method(http.MethodPatch, "/{id}", handler(m.adminUpdateCanned))
		r.Method(http.MethodDelete, "/{id}", handler(m.adminDeleteCanned))
	})
	m.mountSLA(g) // sla-policies, calendars
	m.mountDirectory(g)
	m.mountBranding(g)
	m.mountAutomation(g) // D-19 §A
}
