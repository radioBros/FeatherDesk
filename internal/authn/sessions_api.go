// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// GET /me/sessions (AUTH-028).
func (m *Module) handleListSessions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := m.d.DB.Read().QueryContext(r.Context(), `SELECT id_hash, audience, created_at, last_seen_at, expires_at, ip, user_agent
		FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY created_at DESC`, p.UserID, m.nowDB())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			h, aud, ip, ua         string
			created, seen, expires time.Time
		)
		if err := rows.Scan(&h, &aud, &created, &seen, &expires, &ip, &ua); err != nil {
			problems.Write(w, r, err)
			return
		}
		if t, ok := m.touches.get(h); ok && t.After(seen) {
			seen = t
		}
		out = append(out, map[string]any{
			"id": publicSessionID(h), "audience": aud, "created_at": created.UTC(), "last_seen_at": seen.UTC(),
			"expires_at": expires.UTC(), "ip": ip, "user_agent": ua, "current": h == p.SessionID,
		})
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /me/sessions/{id}: only the caller's own sessions (else 404).
func (m *Module) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	id := chi.URLParam(r, "id")
	if len(id) != 32 {
		problems.Write(w, r, problems.NotFound())
		return
	}
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var h string
		err := tx.QueryRowContext(ctx, `SELECT id_hash FROM sessions WHERE user_id = ? AND substr(id_hash, 1, 32) = ?`, p.UserID, id).Scan(&h)
		if err != nil {
			return store.NotFound(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h); err != nil {
			return err
		}
		tx.AfterCommit(func() { closeSessionSockets(h, "session revoked") })
		return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.session_revoked", TargetType: "user", TargetID: p.UserID, IP: server.ClientIP(r)})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if len(p.SessionID) == 64 && publicSessionID(p.SessionID) == id {
		m.clearSessionCookie(w, r)
	}
	noContent(w)
}
