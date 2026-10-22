// SPDX-License-Identifier: AGPL-3.0-only

// Package kb is the knowledge base (KB-001…KB-015): a category tree, articles
// with a draft → published → archived lifecycle and per-locale translations,
// slug history, Markdown rendering, images in blob storage, search on both
// engines with I-10 visibility in SQL, suggestions for the new-ticket form,
// anonymous helpful votes and view counts (write-behind), cacheable public
// pages, the sitemap and the staff-only `kb` realtime topic.
//
// The package never imports internal/app (app imports kb through its module
// registry); app adapts its Deps into kb.Deps.
package kb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Deps are the platform services the KB needs.
type Deps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Blob     blob.Store
	Settings *settings.Service
	Server   *server.Server // rate limits; nil disables them (tests of the service layer)
}

// Tunables (tests lower or raise them).
const (
	renderCacheBytes  = 4 << 20 // KB-006
	suggestCacheBytes = 2 << 20 // KB-010
	voteDedupeBytes   = 1 << 20 // KB-011
	suggestTTL        = 30 * time.Second
	voteWindow        = 24 * time.Hour
	flushEvery        = 30 * time.Second // PLT-023 window
	maxBodyMD         = 256 << 10
	maxTitle          = 200
	maxCategoryDepth  = 3
	slugReuseAfter    = 90 * 24 * time.Hour
	suggestLimit      = 5
	excerptLen        = 160
)

// Module is the KB feature module.
type Module struct {
	d        Deps
	log      *slog.Logger
	render   *renderer
	suggest  *lru[[]summaryDTO]
	votes    *lru[struct{}]
	counters *coalescer
	hub      atomic.Pointer[realtime.Hub]
	// gen is bumped after every committed KB write; caches key on it.
	gen atomic.Uint64
	// assetVis caches the computed visibility rank of an image per generation.
	assetVis *lru[int]
	now      func() time.Time
}

// New builds the module.
func New(d Deps) (*Module, error) {
	if d.DB == nil {
		return nil, errors.New("kb: DB is required")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	m := &Module{
		d:        d,
		log:      d.Log,
		render:   newRenderer(renderCacheBytes),
		suggest:  newLRU[[]summaryDTO](suggestCacheBytes),
		votes:    newLRU[struct{}](voteDedupeBytes),
		assetVis: newLRU[int](256 << 10),
		now:      time.Now,
	}
	m.counters = newCoalescer(d.DB, d.Log)
	return m, nil
}

func (m *Module) Name() string { return "kb" }

// Mount registers every KB route on the platform groups.
func (m *Module) Mount(g server.Groups) {
	limit := func(name string, perSec float64, burst int) func(http.Handler) http.Handler {
		if m.d.Server == nil {
			return func(h http.Handler) http.Handler { return h }
		}
		return m.d.Server.Limit(name, perSec, burst)
	}
	maxUpload := int64(25) << 20
	if m.d.Cfg != nil && m.d.Cfg.MaxUploadMB > 0 {
		maxUpload = int64(m.d.Cfg.MaxUploadMB) << 20
	}

	// Public (anonymous allowed; principal-aware). kb_enabled=false → 404.
	pub := g.Public.With(m.requireEnabled)
	pub.Get("/kb/categories", m.handlePublicCategories)
	pub.Get("/kb/articles", m.handlePublicList)
	pub.Get("/kb/articles/{slug}", m.handlePublicArticle)
	pub.With(limit("kb.feedback", 1, 10)).Post("/kb/articles/{id}/feedback", m.handleFeedback)
	g.Public.Get("/kb/assets/{id}", m.handleAsset) // staff still see images while the KB is off

	// Portal: suggestions for the new-ticket form (KB-010).
	g.Portal.With(m.requireEnabled, limit("kb.suggest", 10, 20)).Get("/kb/suggest", m.handleSuggest)

	// Editor (any staff; actions checked in authz.go).
	g.Agent.Get("/kb/articles", m.handleEditorList)
	g.Agent.Post("/kb/articles", m.handleCreateArticle)
	g.Agent.Get("/kb/articles/{id}", m.handleEditorGet)
	g.Agent.Patch("/kb/articles/{id}", m.handlePatchArticle)
	g.Agent.Delete("/kb/articles/{id}", m.handleDeleteArticle)
	g.Agent.Get("/kb/articles/{id}/translations/{locale}", m.handleGetTranslation)
	g.Agent.With(server.MaxBody(2<<20)).Put("/kb/articles/{id}/translations/{locale}", m.handlePutTranslation)
	g.Agent.Delete("/kb/articles/{id}/translations/{locale}", m.handleDeleteTranslation)
	g.Agent.With(server.MaxBody(maxUpload+64<<10)).Post("/kb/assets", m.handleUploadAsset)

	// Category admin (supervisor or admin).
	// D-20: project admins manage the KB categories of their projects.
	g.ProjectAdmin.Get("/kb/categories", m.handleAdminCategories)
	g.ProjectAdmin.Post("/kb/categories", m.handleCreateCategory)
	g.ProjectAdmin.Patch("/kb/categories/{id}", m.handleUpdateCategory)
	g.ProjectAdmin.Delete("/kb/categories/{id}", m.handleDeleteCategory)

	// Root: sitemap (KB-009).
	g.Root.Get("/kb/sitemap.xml", m.handleSitemap)
}

// RegisterQueue schedules the counter flush (views + helpful votes, PLT-023).
func (m *Module) RegisterQueue(q *queue.Queue) {
	q.Every("kb.counters", flushEvery, m.counters.Flush)
}

// Start flushes pending counters on shutdown.
func (m *Module) Start(ctx context.Context) error {
	<-ctx.Done()
	fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.counters.Flush(fctx)
}

// FlushCounters writes pending view and vote increments now (tests, shutdown).
func (m *Module) FlushCounters(ctx context.Context) error { return m.counters.Flush(ctx) }

// ---- settings ----

func (m *Module) support(ctx context.Context) settings.Support {
	if m.d.Settings == nil {
		return settings.DefaultSupport()
	}
	s, err := m.d.Settings.Support(ctx)
	if err != nil {
		m.log.Warn("kb: support settings", "err", err)
		return settings.DefaultSupport()
	}
	return s
}

func (m *Module) defaultLocale(ctx context.Context) string {
	l := m.support(ctx).DefaultLocale
	if !isLocale(l) {
		return "en"
	}
	return l
}

// requireEnabled hides the KB (404) when settings.support.kb_enabled is false.
func (m *Module) requireEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.support(r.Context()).KBEnabled {
			problems.Write(w, r, problems.NotFound())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// committed runs after every KB write transaction: caches are invalidated and
// editors are told over the `kb` topic.
func (m *Module) committed(ev updateEvent) {
	m.gen.Add(1)
	m.publish(ev)
}

// ---- HTTP helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON object into dst; malformed input is a 400.
func decodeJSON(r *http.Request, dst any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return problems.From(err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return problems.BadRequest("empty body")
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return problems.BadRequest("malformed JSON")
	}
	return nil
}

func param(r *http.Request, name string) string { return chi.URLParam(r, name) }
