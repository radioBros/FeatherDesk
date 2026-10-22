// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// backchannelSkew bounds `iat` of a logout token (AUTH-030).
const backchannelSkew = 5 * time.Minute

// POST /auth/oidc/{audience}/backchannel-logout: OpenID Back-Channel Logout.
// No cookie, CSRF-exempt (server-to-server, no Origin header). go-oidc checks
// signature, iss, aud, exp, the logout event, no nonce, and sub/sid presence.
func (m *Module) handleBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	aud, ok := audienceParam(r)
	a := m.oidcFor(aud)
	if !ok || a == nil {
		problems.Write(w, r, problems.BadRequest("audience has no OIDC client"))
		return
	}
	if err := r.ParseForm(); err != nil {
		problems.Write(w, r, problems.BadRequest("malformed form"))
		return
	}
	raw := r.PostForm.Get("logout_token")
	if raw == "" {
		problems.Write(w, r, problems.BadRequest("logout_token required"))
		return
	}
	p, err := a.getProvider(ctx)
	if err != nil {
		problems.Write(w, r, problems.BadRequest("identity provider unavailable"))
		return
	}
	lt, err := p.Verifier(&oidc.Config{ClientID: a.clientID, Now: m.now}).VerifyLogout(ctx, raw)
	if err != nil {
		m.log.Warn("backchannel logout token rejected", "audience", aud, "err", err)
		problems.Write(w, r, problems.BadRequest("invalid logout token"))
		return
	}
	now := m.now()
	if lt.IssuedAt.IsZero() || now.Sub(lt.IssuedAt) > backchannelSkew || lt.IssuedAt.Sub(now) > backchannelSkew {
		problems.Write(w, r, problems.BadRequest("logout token too old"))
		return
	}
	if !m.jti.add(lt.Issuer+"|"+lt.TokenID, 10*time.Minute, now) {
		w.WriteHeader(http.StatusOK) // replay: already handled
		return
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		q := `SELECT s.id_hash, s.user_id FROM sessions s JOIN user_identities i ON i.user_id = s.user_id WHERE i.issuer = ? AND i.audience = ?`
		args := []any{lt.Issuer, string(aud)}
		if lt.Subject != "" {
			q += ` AND i.subject = ?`
			args = append(args, lt.Subject)
		}
		if lt.SessionID != "" {
			q += ` AND s.oidc_sid = ?`
			args = append(args, lt.SessionID)
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		var ids, users []string
		for rows.Next() {
			var h, u string
			if err := rows.Scan(&h, &u); err != nil {
				rows.Close()
				return err
			}
			ids, users = append(ids, h), append(users, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h); err != nil {
				return err
			}
		}
		tx.AfterCommit(func() {
			for _, h := range ids {
				closeSessionSockets(h, "logged out by identity provider")
			}
		})
		target := ""
		if len(users) > 0 {
			target = users[0]
		}
		return Audit(ctx, tx, AuditEntry{Action: "auth.backchannel_logout", TargetType: "user", TargetID: target, IP: server.ClientIP(r),
			Data: map[string]any{"audience": aud, "sessions": len(ids), "by_sid": lt.SessionID != ""}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
