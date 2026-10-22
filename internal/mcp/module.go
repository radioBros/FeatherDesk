// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// KeyIntegrations is the settings document {"mcp_enabled": bool}.
const KeyIntegrations = "integrations"

// Integrations is the integrations settings document (Admin → Settings →
// Integrations). The MCP endpoint is off by default.
type Integrations struct {
	MCPEnabled bool `json:"mcp_enabled"`
}

// LoadIntegrations reads the document (defaults: everything off).
func LoadIntegrations(ctx context.Context, s *settings.Service) (Integrations, error) {
	var v Integrations
	err := s.Get(ctx, KeyIntegrations, &v)
	return v, err
}

// Deps are the platform services the module needs (a subset of app.Deps).
type Deps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Settings *settings.Service
	Server   *server.Server
	Version  string
}

// Module mounts /mcp, the integrations settings and the connect-helper info.
type Module struct {
	d Deps
	h *Handler
}

// NewModule builds the MCP module.
func NewModule(d Deps) (*Module, error) {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	m := &Module{d: d}
	opt := Options{
		Enabled: func(ctx context.Context) (bool, error) {
			v, err := LoadIntegrations(ctx, d.Settings)
			return v.MCPEnabled, err
		},
		Log:     d.Log,
		Version: d.Version,
	}
	if d.Cfg != nil {
		opt.Origins = d.Cfg.Origins()
	}
	if d.Server != nil {
		opt.API = d.Server.Handler()
		opt.Limit = d.Server.Limit
	}
	m.h = New(opt)
	return m, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "mcp" }

// Handler exposes the protocol handler (tests).
func (m *Module) Handler() *Handler { return m.h }

// Mount implements app.Module.
func (m *Module) Mount(g server.Groups) {
	pre := func(h http.Handler) http.Handler { return h }
	if m.d.Server != nil {
		// Before authentication the limiter keys by client IP.
		pre = m.d.Server.Limit("mcp.ip", 20, 60)
	}
	g.Root.With(pre).Post("/mcp", m.h.ServePost)
	g.Root.Get("/mcp", m.h.ServeGet)
	g.Root.With(pre).Delete("/mcp", m.h.ServeDelete)

	g.Admin.Get("/integrations", m.getIntegrations)
	g.Admin.Patch("/integrations", m.patchIntegrations)
	g.Authed.Get("/integrations/mcp", m.mcpInfo)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Module) getIntegrations(w http.ResponseWriter, r *http.Request) {
	v, err := LoadIntegrations(r.Context(), m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (m *Module) patchIntegrations(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), server.PrincipalFrom(r.Context())
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		problems.Write(w, r, problems.BadRequest("unreadable body"))
		return
	}
	var in map[string]json.RawMessage
	if len(bytes.TrimSpace(b)) == 0 || json.Unmarshal(b, &in) != nil {
		problems.Write(w, r, problems.Validation(problems.Field("body", "invalid_json", "a JSON object")))
		return
	}
	cur, err := LoadIntegrations(ctx, m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	next := cur
	var fe []problems.FieldError
	for k, raw := range in {
		switch k {
		case "mcp_enabled":
			if json.Unmarshal(raw, &next.MCPEnabled) != nil {
				fe = append(fe, problems.Field(k, "boolean", "true or false"))
			}
		default:
			fe = append(fe, problems.Field(k, "unknown", "unknown setting"))
		}
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	if next != cur {
		if err := m.d.Settings.Put(ctx, KeyIntegrations, next, p.UserID); err != nil {
			problems.Write(w, r, err)
			return
		}
		if m.d.DB != nil {
			err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
				return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: p.UserID, Action: "integrations.updated", TargetType: "settings",
					TargetID: KeyIntegrations, IP: server.ClientIP(r), Data: map[string]any{"mcp_enabled": next.MCPEnabled}})
			})
			if err != nil {
				problems.Write(w, r, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, next)
}

// McpInfo is what the "Connect an AI assistant" helper shows.
type McpInfo struct {
	Enabled           bool     `json:"enabled"`
	Endpoint          string   `json:"endpoint"`
	ProtocolVersions  []string `json:"protocol_versions"`
	RecommendedScopes []string `json:"recommended_scopes"`
	ReadOnlyScopes    []string `json:"read_only_scopes"`
}

// RecommendedScopes for an assistant that reads and works tickets.
func RecommendedScopes(kind domain.UserKind) (full, readOnly []string) {
	if kind == domain.KindStaff {
		return []string{domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeDirectoryRead, domain.ScopeKBRead},
			[]string{domain.ScopeTicketsRead, domain.ScopeDirectoryRead, domain.ScopeKBRead}
	}
	return []string{domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeKBRead},
		[]string{domain.ScopeTicketsRead, domain.ScopeKBRead}
}

func (m *Module) mcpInfo(w http.ResponseWriter, r *http.Request) {
	v, err := LoadIntegrations(r.Context(), m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	p := server.PrincipalFrom(r.Context())
	if p == nil {
		problems.Write(w, r, errors.New("mcp: no principal"))
		return
	}
	full, ro := RecommendedScopes(p.Kind)
	endpoint := "/mcp"
	if m.d.Cfg != nil && m.d.Cfg.PublicURL != "" {
		endpoint = strings.TrimSuffix(m.d.Cfg.PublicURL, "/") + "/mcp"
	}
	writeJSON(w, http.StatusOK, McpInfo{Enabled: v.MCPEnabled, Endpoint: endpoint, ProtocolVersions: SupportedVersions,
		RecommendedScopes: full, ReadOnlyScopes: ro})
}
