// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/integrations/oauthflow"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// OAuth popup install (D-21 §2, INT-006). The provider-independent half —
// single-use state, completion redirect to /agent/oauth/complete — is
// internal/integrations/oauthflow; this file is Slack's consent URL, code
// exchange and token storage.

// Provider is the OAuth provider id the SPA knows (oauth/providers.ts).
const Provider = "slack"

func (m *Module) oauthStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := m.load(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if d.ClientID == "" || d.ClientSecretEnc == "" {
		problems.Write(w, r, problems.Conflict("save the Slack app's client ID and secret first"))
		return
	}
	state, exp := m.states.Put(server.PrincipalFrom(ctx).UserID)
	writeJSON(w, http.StatusOK, map[string]any{"authorize_url": m.api.AuthorizeURL(d.ClientID, m.redirectURL(), state, Scopes), "state": state,
		"expires_at": exp.UTC().Truncate(time.Second)})
}

// finish ends the popup on the shared completion page.
func (m *Module) finish(w http.ResponseWriter, r *http.Request, state, reason string) {
	oauthflow.Finish(w, r, m.agent, Provider, state, reason)
}

func (m *Module) oauthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	userID, ok := m.states.Take(state)
	if !ok {
		m.finish(w, r, state, "invalid_state")
		return
	}
	if e := q.Get("error"); e != "" {
		m.finish(w, r, state, e)
		return
	}
	code := q.Get("code")
	if code == "" {
		m.finish(w, r, state, "missing_code")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if reason := m.connect(ctx, r, userID, code); reason != "" {
		m.finish(w, r, state, reason)
		return
	}
	m.finish(w, r, state, "")
}

// connect exchanges code and stores the bot token; it returns a reason code
// on failure.
func (m *Module) connect(ctx context.Context, r *http.Request, userID, code string) string {
	d, err := m.load(ctx)
	if err != nil || d.ClientID == "" || d.ClientSecretEnc == "" {
		return "not_configured"
	}
	secret, err := m.box.OpenString(settings.PurposeSlackSecret, d.ClientSecretEnc)
	if err != nil {
		m.log.Error("slack: open client secret", "err", err)
		return "server_error"
	}
	res, err := m.api.Exchange(ctx, d.ClientID, secret, code, m.redirectURL())
	if err != nil {
		m.log.Warn("slack: oauth exchange failed", "err", err)
		if e, ok := AsAPIError(err); ok && oauthflow.Code(e.Code) == e.Code {
			return e.Code
		}
		return "exchange_failed"
	}
	if res.AccessToken == "" || res.TokenType != "bot" {
		return "no_bot_token"
	}
	if res.IsEnterpriseInstall || res.Team.ID == "" {
		return "enterprise_install_unsupported"
	}
	enc, err := m.box.SealString(settings.PurposeSlackToken, res.AccessToken)
	if err != nil {
		return "server_error"
	}
	if d.TeamID != "" && d.TeamID != res.Team.ID {
		// Another workspace: the thread roots of the old one are meaningless.
		if err := m.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM slack_threads`)
			return err
		}); err != nil {
			return "server_error"
		}
	}
	if d.BotTokenEnc != "" {
		if old, err := m.token(d); err == nil && old != res.AccessToken {
			_ = m.api.Revoke(ctx, old) // best effort: the previous install's token
		}
	}
	now := m.Now().UTC().Truncate(time.Second)
	d.BotTokenEnc, d.TeamID, d.TeamName, d.BotUserID, d.AppID = enc, res.Team.ID, res.Team.Name, res.BotUserID, res.AppID
	d.Scopes = splitScopes(res.Scope)
	d.ConnectedAt, d.ConnectedBy, d.Error, d.ErrorAt = &now, userID, "", nil
	d.TeamURL, d.BotName = "", ""
	if at, err := m.api.AuthTest(ctx, res.AccessToken); err == nil {
		d.TeamURL, d.BotName = at.URL, at.User
	}
	if err := m.save(ctx, d, userID); err != nil {
		return "server_error"
	}
	m.mu.Lock()
	m.channels = nil
	m.mu.Unlock()
	if err := m.audit(r, userID, "slack.connected", res.Team.ID, map[string]any{"team": res.Team.Name, "scopes": d.Scopes}); err != nil {
		m.log.Error("slack: audit connect", "err", err)
	}
	return ""
}

func splitScopes(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (m *Module) disconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := m.load(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if d.BotTokenEnc == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if tok, err := m.token(d); err == nil {
		if err := m.api.Revoke(ctx, tok); err != nil {
			m.log.Warn("slack: revoke on disconnect", "err", err) // the token is dropped anyway
		}
	}
	team := d.TeamID
	d.BotTokenEnc, d.TeamName, d.TeamURL, d.BotUserID, d.BotName, d.AppID = "", "", "", "", "", ""
	d.Scopes, d.ConnectedAt, d.ConnectedBy, d.Error, d.ErrorAt = nil, nil, "", "", nil
	// TeamID stays: reconnecting the same workspace keeps the ticket threads.
	if err := m.save(ctx, d, server.PrincipalFrom(ctx).UserID); err != nil {
		problems.Write(w, r, err)
		return
	}
	m.mu.Lock()
	m.channels = nil
	m.mu.Unlock()
	if err := m.audit(r, "", "slack.disconnected", team, nil); err != nil {
		problems.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
