// SPDX-License-Identifier: AGPL-3.0-only

// Package slack is the Slack integration (D-21): the admin creates a Slack app
// from a pre-filled manifest, pastes its credentials and installs it through
// an OAuth popup; routes then post ticket events to channels, one thread per
// ticket, through a durable consumer on the in-process queue.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/integrations/oauthflow"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/webhooks"
)

// EnvBaseURL overrides Slack's origin (tests and the e2e fake Slack only).
const EnvBaseURL = "FD_SLACK_BASE_URL"

// Scopes are the bot scopes the manifest requests (D-21 §4).
var Scopes = []string{"chat:write", "chat:write.public", "channels:read", "groups:read"}

// CallbackPath is the OAuth redirect path under the public URL.
const CallbackPath = "/api/v1/integrations/slack/oauth/callback"

// Deps are the module dependencies (wired by internal/app/modules.go).
type Deps struct {
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Settings *settings.Service
	Secrets  *crypto.Box
	Server   *server.Server // may be nil
	Cfg      *config.Config
	Getenv   func(string) string
}

// Module is the Slack feature module.
type Module struct {
	db       *store.DB
	log      *slog.Logger
	q        *queue.Queue
	settings *settings.Service
	box      *crypto.Box
	srv      *server.Server
	render   *webhooks.Renderer
	api      *Client
	public   string // FD_PUBLIC_URL
	agent    string // FD_AGENT_URL

	// Now and FirstRetry are injectable for tests.
	Now        func() time.Time
	FirstRetry func() time.Duration

	states *oauthflow.States

	mu       sync.Mutex
	routes   []*route // active routes, nil = not loaded
	channels *channelCache
}

type channelCache struct {
	at   time.Time
	list []Channel
}

// New builds the module. It does no network I/O.
func New(d Deps) (*Module, error) {
	getenv := d.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	box := d.Secrets
	if box == nil && d.Settings != nil {
		box = d.Settings.Box()
	}
	m := &Module{db: d.DB, log: log.With("module", "slack"), q: d.Queue, settings: d.Settings, box: box, srv: d.Server,
		api: NewClient(getenv(EnvBaseURL), nil), Now: time.Now, states: oauthflow.NewStates()}
	if d.Cfg != nil {
		m.public = strings.TrimRight(d.Cfg.PublicURL, "/")
		m.agent = strings.TrimRight(d.Cfg.AgentURL, "/")
	}
	if m.agent == "" {
		m.agent = m.public + "/agent"
	}
	m.render = webhooks.NewRenderer(d.DB, m.public)
	m.FirstRetry = func() time.Duration { return 30 * time.Second }
	return m, nil
}

// Name implements app.Module.
func (m *Module) Name() string { return "slack" }

// Client exposes the Slack API client (tests swap it for a fake server).
func (m *Module) SetClient(c *Client) { m.api = c }

// Mount registers the admin routes and the public OAuth callback.
func (m *Module) Mount(g server.Groups) {
	g.Admin.Get("/integrations/slack", m.getStatus)
	g.Admin.Patch("/integrations/slack", m.patchApp)
	g.Admin.Post("/integrations/slack/oauth/start", m.oauthStart)
	g.Admin.Delete("/integrations/slack/connection", m.disconnect)
	g.Admin.Get("/integrations/slack/channels", m.listChannels)
	g.Admin.Get("/integrations/slack/routes", m.listRoutes)
	g.Admin.Post("/integrations/slack/routes", m.createRoute)
	g.Admin.Patch("/integrations/slack/routes/{id}", m.patchRoute)
	g.Admin.Delete("/integrations/slack/routes/{id}", m.deleteRoute)
	g.Admin.Post("/integrations/slack/routes/{id}/test", m.testRoute)
	cb := g.Public
	if m.srv != nil {
		cb = g.Public.With(m.srv.Limit("slack.oauth", 1, 10))
	}
	cb.Get("/integrations/slack/oauth/callback", m.oauthCallback)
}

// ---- settings document ----

// doc is the settings document `slack`. Secrets are sealed (rotate-secrets
// covers client_secret_enc and bot_token_enc).
type doc struct {
	ClientID        string     `json:"client_id,omitempty"`
	ClientSecretEnc string     `json:"client_secret_enc,omitempty"`
	BotTokenEnc     string     `json:"bot_token_enc,omitempty"`
	TeamID          string     `json:"team_id,omitempty"`
	TeamName        string     `json:"team_name,omitempty"`
	TeamURL         string     `json:"team_url,omitempty"`
	BotUserID       string     `json:"bot_user_id,omitempty"`
	BotName         string     `json:"bot_name,omitempty"`
	AppID           string     `json:"app_id,omitempty"`
	Scopes          []string   `json:"scopes,omitempty"`
	ConnectedAt     *time.Time `json:"connected_at,omitempty"`
	ConnectedBy     string     `json:"connected_by,omitempty"`
	Error           string     `json:"error,omitempty"` // Slack auth error code; delivery stops
	ErrorAt         *time.Time `json:"error_at,omitempty"`
}

func (m *Module) load(ctx context.Context) (doc, error) {
	var d doc
	err := m.settings.Get(ctx, settings.KeySlack, &d)
	return d, err
}

func (m *Module) save(ctx context.Context, d doc, actor string) error {
	return m.settings.Put(ctx, settings.KeySlack, d, actor)
}

// token returns the plaintext bot token ("" = not connected).
func (m *Module) token(d doc) (string, error) {
	if d.BotTokenEnc == "" {
		return "", nil
	}
	return m.box.OpenString(settings.PurposeSlackToken, d.BotTokenEnc)
}

// markBroken records a Slack auth error: delivery stops until reconnect.
func (m *Module) markBroken(ctx context.Context, code string) {
	d, err := m.load(ctx)
	if err != nil || d.BotTokenEnc == "" || d.Error == code {
		return
	}
	now := m.Now().UTC()
	d.Error, d.ErrorAt = code, &now
	if err := m.save(ctx, d, ""); err != nil {
		m.log.Error("slack: record connection error", "err", err)
		return
	}
	m.log.Warn("slack: connection broken; delivery stopped until an admin reconnects", "error", code)
}

func (m *Module) redirectURL() string { return m.public + CallbackPath }

// Manifest is the app manifest the "Create Slack app" button pre-fills.
func (m *Module) manifest(ctx context.Context) map[string]any {
	name := "FeatherDesk"
	if b, err := m.settings.Branding(ctx); err == nil && strings.TrimSpace(b.ProductName) != "" {
		name = strings.TrimSpace(b.ProductName)
	}
	if r := []rune(name); len(r) > 35 { // Slack's app name limit
		name = string(r[:35])
	}
	return map[string]any{
		"display_information": map[string]any{"name": name, "description": "Ticket notifications from " + name},
		"features":            map[string]any{"bot_user": map[string]any{"display_name": name, "always_online": true}},
		"oauth_config":        map[string]any{"redirect_urls": []string{m.redirectURL()}, "scopes": map[string]any{"bot": Scopes}},
		"settings":            map[string]any{"org_deploy_enabled": false, "socket_mode_enabled": false, "token_rotation_enabled": false},
	}
}

// ---- status ----

type userRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// statusDTO is the SlackStatus schema.
type statusDTO struct {
	State           string         `json:"state"` // not_configured | configured | connected | error
	ClientID        string         `json:"client_id"`
	ClientSecretSet bool           `json:"client_secret_set"`
	TeamID          *string        `json:"team_id"`
	TeamName        *string        `json:"team_name"`
	TeamURL         *string        `json:"team_url"`
	BotUserID       *string        `json:"bot_user_id"`
	BotName         *string        `json:"bot_name"`
	Scopes          []string       `json:"scopes"`
	ConnectedAt     *time.Time     `json:"connected_at"`
	ConnectedBy     *userRef       `json:"connected_by"`
	Error           *string        `json:"error"`
	RedirectURL     string         `json:"redirect_url"`
	HTTPS           bool           `json:"https"`
	Manifest        map[string]any `json:"manifest"`
	ManifestURL     string         `json:"manifest_url"`
	Routes          int            `json:"routes"`
	ActiveRoutes    int            `json:"active_routes"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (m *Module) status(ctx context.Context) (statusDTO, error) {
	d, err := m.load(ctx)
	if err != nil {
		return statusDTO{}, err
	}
	man := m.manifest(ctx)
	mj, _ := json.Marshal(man)
	s := statusDTO{ClientID: d.ClientID, ClientSecretSet: d.ClientSecretEnc != "", Scopes: d.Scopes,
		ConnectedAt: d.ConnectedAt, Error: strPtr(d.Error), RedirectURL: m.redirectURL(), HTTPS: strings.HasPrefix(m.public, "https://"),
		Manifest: man, ManifestURL: "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(string(mj))}
	if d.BotTokenEnc != "" { // after a disconnect only team_id stays (thread roots)
		s.TeamID, s.TeamName, s.TeamURL, s.BotUserID, s.BotName = strPtr(d.TeamID), strPtr(d.TeamName), strPtr(d.TeamURL), strPtr(d.BotUserID), strPtr(d.BotName)
	}
	if s.Scopes == nil {
		s.Scopes = []string{}
	}
	switch {
	case d.BotTokenEnc != "" && d.Error != "":
		s.State = "error"
	case d.BotTokenEnc != "":
		s.State = "connected"
	case d.ClientID != "" && d.ClientSecretEnc != "":
		s.State = "configured"
	default:
		s.State = "not_configured"
	}
	if d.ConnectedBy != "" {
		u := userRef{ID: d.ConnectedBy}
		_ = m.db.Read().QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, d.ConnectedBy).Scan(&u.Name)
		s.ConnectedBy = &u
	}
	if err := m.db.Read().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN active THEN 1 ELSE 0 END), 0) FROM slack_routes`).Scan(&s.Routes, &s.ActiveRoutes); err != nil {
		return s, err
	}
	return s, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return problems.PayloadTooLarge()
		}
		return problems.BadRequest("invalid JSON body")
	}
	return nil
}

func (m *Module) audit(r *http.Request, actor, action, target string, data map[string]any) error {
	ctx := r.Context()
	if actor == "" {
		if p := server.PrincipalFrom(ctx); p != nil {
			actor = p.UserID
		}
	}
	return m.db.Tx(ctx, func(tx *store.Tx) error {
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: actor, Action: action, TargetType: "slack", TargetID: target, IP: server.ClientIP(r), Data: data})
	})
}

func (m *Module) getStatus(w http.ResponseWriter, r *http.Request) {
	s, err := m.status(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// clientIDPattern: Slack client IDs are "<digits>.<digits>".
var clientIDPattern = regexp.MustCompile(`^[0-9]{3,20}\.[0-9]{3,20}$`)

func (m *Module) patchApp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		ClientID     *string `json:"client_id"`
		ClientSecret *string `json:"client_secret"`
	}
	if err := decode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	d, err := m.load(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var fe []problems.FieldError
	if in.ClientID != nil {
		v := strings.TrimSpace(*in.ClientID)
		switch {
		case v == "":
			fe = append(fe, problems.Field("client_id", "required", "client ID is required"))
		case !clientIDPattern.MatchString(v):
			fe = append(fe, problems.Field("client_id", "pattern", "a Slack client ID looks like 1234567890.1234567890"))
		}
		in.ClientID = &v
	} else if d.ClientID == "" {
		fe = append(fe, problems.Field("client_id", "required", "client ID is required"))
	}
	if in.ClientSecret != nil {
		v := strings.TrimSpace(*in.ClientSecret)
		switch {
		case v == "" && d.ClientSecretEnc == "":
			fe = append(fe, problems.Field("client_secret", "required", "client secret is required"))
		case len(v) > 200:
			fe = append(fe, problems.Field("client_secret", "max_length", "at most 200 characters"))
		}
		in.ClientSecret = &v
	} else if d.ClientSecretEnc == "" {
		fe = append(fe, problems.Field("client_secret", "required", "client secret is required"))
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	changed := []string{}
	if in.ClientID != nil && *in.ClientID != d.ClientID {
		d.ClientID = *in.ClientID
		changed = append(changed, "client_id")
	}
	if in.ClientSecret != nil && *in.ClientSecret != "" {
		enc, err := m.box.SealString(settings.PurposeSlackSecret, *in.ClientSecret)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		d.ClientSecretEnc = enc
		changed = append(changed, "client_secret")
	}
	p := server.PrincipalFrom(ctx)
	if len(changed) > 0 {
		if err := m.save(ctx, d, p.UserID); err != nil {
			problems.Write(w, r, err)
			return
		}
		if err := m.audit(r, "", "slack.app_updated", d.ClientID, map[string]any{"changed": changed}); err != nil {
			problems.Write(w, r, err)
			return
		}
	}
	m.getStatus(w, r)
}
