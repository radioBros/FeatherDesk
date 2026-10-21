// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// MaxActiveTokens per user (AUTH-010).
const MaxActiveTokens = 20

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newPAT returns "st_pat_" + base32(32 random bytes) and its 8-char prefix.
func newPAT() (tok, prefix string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	body := strings.ToLower(b32.EncodeToString(b))
	return PATPrefix + body, body[:8]
}

type tokenRow struct {
	ID, Name, Prefix          string
	Scopes                    store.JSON[[]string]
	LastUsed, Expires, Revoke store.NullTime
	Created                   time.Time
	UserID, UserName          string
	UserKind                  domain.UserKind
}

const tokenCols = `t.id, t.name, t.prefix, t.scopes, t.last_used_at, t.expires_at, t.revoked_at, t.created_at, u.id, u.name, u.kind`

func (t *tokenRow) dest() []any {
	return []any{&t.ID, &t.Name, &t.Prefix, &t.Scopes, &t.LastUsed, &t.Expires, &t.Revoke, &t.Created, &t.UserID, &t.UserName, &t.UserKind}
}

func (t *tokenRow) json() map[string]any {
	scopes := t.Scopes.V
	if scopes == nil {
		scopes = []string{}
	}
	nt := func(v store.NullTime) any {
		if v.Valid {
			return v.Time.UTC()
		}
		return nil
	}
	return map[string]any{
		"id": t.ID, "name": t.Name, "prefix": t.Prefix, "scopes": scopes, "created_at": t.Created.UTC(),
		"last_used_at": nt(t.LastUsed), "expires_at": nt(t.Expires), "revoked_at": nt(t.Revoke),
		"user": map[string]any{"id": t.UserID, "name": t.UserName, "kind": t.UserKind},
	}
}

func queryTokens(ctx context.Context, q store.Querier, where string, args ...any) ([]map[string]any, []string, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE `+where, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	var ids []string
	for rows.Next() {
		var t tokenRow
		if err := rows.Scan(t.dest()...); err != nil {
			return nil, nil, err
		}
		out = append(out, t.json())
		ids = append(ids, t.ID)
	}
	return out, ids, rows.Err()
}

func (m *Module) handleListMyTokens(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	items, _, err := queryTokens(r.Context(), m.d.DB.Read(), `t.user_id = ? AND t.revoked_at IS NULL ORDER BY t.created_at DESC, t.id DESC`, p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type tokenCreate struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (m *Module) handleCreateMyToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	var in tokenCreate
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	var fields []problems.FieldError
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 80 {
		fields = append(fields, problems.Field("name", "length", "1 to 80 characters"))
	}
	if len(in.Scopes) == 0 {
		fields = append(fields, problems.Field("scopes", "required", "at least one scope"))
	}
	for _, s := range in.Scopes {
		if !slices.Contains(domain.Scopes, s) {
			fields = append(fields, problems.Field("scopes", "invalid", "unknown scope "+s))
		}
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(m.now()) {
		fields = append(fields, problems.Field("expires_at", "past", "must be in the future"))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	// I-6 at creation: never more than the owner's role may hold. (A PAT
	// caller never reaches this handler: token management needs a session.)
	if bad := authz.InvalidScopes(p.Kind, p.Role, in.Scopes); len(bad) > 0 {
		problems.Write(w, r, problems.Forbidden("scope not allowed for your role: "+strings.Join(bad, ", ")))
		return
	}
	scopes := slices.Compact(slices.Sorted(slices.Values(in.Scopes)))
	tok, prefix := newPAT()
	id := store.NewID()
	now := m.nowDB()
	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC().Truncate(time.Microsecond)
	}
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, p.UserID, now).Scan(&n); err != nil {
			return err
		}
		if n >= MaxActiveTokens {
			return problems.Validation(problems.Field("name", "too_many_tokens", "at most "+strconv.Itoa(MaxActiveTokens)+" active tokens"))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, p.UserID, in.Name, prefix, hashToken(tok), store.MustJSON(scopes), expires, now); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.token_created", TargetType: "api_token", TargetID: id, IP: server.ClientIP(r),
			Data: map[string]any{"token_name": in.Name, "scopes": scopes}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	items, _, err := queryTokens(ctx, m.d.DB.Read(), `t.id = ?`, id)
	if err != nil || len(items) != 1 {
		problems.Write(w, r, problems.Internal(err))
		return
	}
	out := items[0]
	out["token"] = tok
	writeJSON(w, http.StatusCreated, out)
}

func (m *Module) handleRevokeMyToken(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	m.revokeToken(w, r, chi.URLParam(r, "id"), p.UserID)
}

// revokeToken sets revoked_at; ownerID "" = any owner (admin).
func (m *Module) revokeToken(w http.ResponseWriter, r *http.Request, id, ownerID string) {
	ctx := r.Context()
	p := principal(r)
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		q, args := `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, []any{m.nowDB(), id}
		if ownerID != "" {
			q += ` AND user_id = ?`
			args = append(args, ownerID)
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.token_revoked", TargetType: "api_token", TargetID: id, IP: server.ClientIP(r)})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	noContent(w)
}

// ---- /admin/tokens (V1) ----

func (m *Module) handleAdminListTokens(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = min(v, 200)
	}
	where, args := `1 = 1`, []any{}
	if uid := q.Get("user_id"); uid != "" {
		where += ` AND t.user_id = ?`
		args = append(args, uid)
	}
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problems.Write(w, r, problems.Validation(problems.Field("cursor", "invalid", "")))
			return
		}
		where += ` AND t.id < ?`
		args = append(args, string(b))
	}
	where += ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, limit+1)
	items, ids, err := queryTokens(r.Context(), m.d.DB.Read(), where, args...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var next any
	if len(items) > limit {
		items = items[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(ids[limit-1]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (m *Module) handleAdminRevokeToken(w http.ResponseWriter, r *http.Request) {
	m.revokeToken(w, r, chi.URLParam(r, "id"), "")
}
