// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// errInvalidCred marks credentials that do not resolve to an active principal.
var errInvalidCred = errors.New("authn: invalid credentials")

// PATPrefix starts every personal access token.
const PATPrefix = "st_pat_"

// userRow is a users row as authn reads it.
type userRow struct {
	ID          string
	Kind        domain.UserKind
	Email       string
	Name        string
	Role        domain.Role
	Tier        sql.NullInt64
	OrgID       sql.NullString
	Status      domain.UserStatus
	Password    sql.NullString
	BreakGlass  bool
	IDPManaged  bool
	Locale      string
	NotifyPrefs store.RawJSON
	LastLogin   store.NullTime
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const userCols = `u.id, u.kind, u.email, u.name, u.role, u.tier, u.organization_id, u.status, u.password_hash, u.break_glass, u.idp_managed, u.locale, u.notify_prefs, u.last_login_at, u.created_at, u.updated_at`

func (u *userRow) dest() []any {
	return []any{&u.ID, &u.Kind, &u.Email, &u.Name, &u.Role, &u.Tier, &u.OrgID, &u.Status, &u.Password, &u.BreakGlass,
		&u.IDPManaged, &u.Locale, &u.NotifyPrefs, &u.LastLogin, &u.CreatedAt, &u.UpdatedAt}
}

func (u *userRow) hasPassword() bool { return u.Password.Valid && u.Password.String != "" }

func loadUser(ctx context.Context, q store.Querier, where string, arg any) (userRow, error) {
	var u userRow
	err := q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users u WHERE `+where, arg).Scan(u.dest()...)
	return u, store.NotFound(err)
}

// principalOf turns a user row into a Principal (teams from the ≤ 30 s cache).
func (m *Module) principalOf(ctx context.Context, u userRow) (*domain.Principal, error) {
	p := &domain.Principal{UserID: u.ID, Kind: u.Kind, Role: u.Role, Locale: u.Locale, IDPManaged: u.IDPManaged}
	if u.Kind == domain.KindStaff {
		p.Tier = int(u.Tier.Int64)
		teams, err := userTeams(ctx, m.d.DB, u.ID)
		if err != nil {
			return nil, err
		}
		p.Teams = teams
	} else {
		p.OrganizationID = u.OrgID.String
	}
	return p, nil
}

// Authenticate implements server.Authenticator: a bearer PAT, a one-time
// ws-ticket on /api/v1/ws, or the st_session cookie. A stale or unknown
// cookie is treated as anonymous (so /auth/config and /auth/login keep
// working); a presented but invalid bearer token is a 401.
func (m *Module) Authenticate(r *http.Request) (*domain.Principal, error) {
	if p := server.PrincipalFrom(r.Context()); p != nil {
		return p, nil // already resolved (the hub re-authenticates /ws)
	}
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/api/v1/inbound/") {
		return nil, nil
	}
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, _ := strings.Cut(h, " ")
		if strings.EqualFold(scheme, "Bearer") {
			tok = strings.TrimSpace(tok)
			if !strings.HasPrefix(tok, PATPrefix) || len(tok) > 128 {
				return nil, problems.Unauthenticated()
			}
			p, err := m.patPrincipal(ctx, "t.token_hash = ?", hashToken(tok), true)
			if err != nil {
				if errors.Is(err, errInvalidCred) {
					return nil, problems.Unauthenticated()
				}
				return nil, problems.Internal(err)
			}
			if p.IsCustomer() && !m.portalOn.Load() && path != "/api/v1/ws" { // the socket is refused with 4403 by Admit instead
				if strings.HasPrefix(path, "/api/v1/portal/") {
					return nil, errPortalDisabled()
				}
				return nil, problems.Unauthenticated() // the token no longer works (documented 401)
			}
			if err := m.enforceTokenScope(r, p); err != nil {
				return nil, err
			}
			return p, nil
		}
	}
	if path == "/api/v1/ws" {
		if t := r.URL.Query().Get("ticket"); t != "" {
			p, err := m.consumeWSTicket(ctx, t)
			if err != nil {
				if errors.Is(err, errInvalidCred) {
					return nil, problems.Unauthenticated()
				}
				return nil, problems.Internal(err)
			}
			return p, nil
		}
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return nil, nil
	}
	p, err := m.sessionPrincipal(ctx, hashToken(c.Value), true)
	if err != nil {
		if errors.Is(err, errInvalidCred) {
			return nil, nil
		}
		return nil, problems.Internal(err)
	}
	if p.IsCustomer() && !m.portalOn.Load() && path != "/api/v1/ws" {
		return nil, nil // sessions are deleted on the switch; a stale cookie is anonymous
	}
	return p, nil
}

// sessionPrincipal resolves a session hash: absolute expiry, idle expiry
// against max(db last_seen_at, buffered touch), active user (I-5).
func (m *Module) sessionPrincipal(ctx context.Context, idHash string, touch bool) (*domain.Principal, error) {
	var (
		aud           domain.Audience
		last, expires time.Time
		u             userRow
	)
	dest := append([]any{&aud, &last, &expires}, u.dest()...)
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT s.audience, s.last_seen_at, s.expires_at, `+userCols+`
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id_hash = ?`, idHash).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvalidCred
	}
	if err != nil {
		return nil, err
	}
	now := m.now()
	if t, ok := m.touches.get(idHash); ok && t.After(last) {
		last = t
	}
	if !now.Before(expires) || now.Sub(last) >= m.idle(aud) || u.Status != domain.UserActive || string(u.Kind) != string(aud) {
		return nil, errInvalidCred
	}
	p, err := m.principalOf(ctx, u)
	if err != nil {
		return nil, err
	}
	p.SessionID = idHash
	if touch {
		m.touches.touch(idHash, m.nowDB())
	}
	return p, nil
}

// patPrincipal resolves a PAT (by hash or id). Effective scopes are the
// token's scopes ∩ the owner's current role (I-6), evaluated every request.
func (m *Module) patPrincipal(ctx context.Context, where, arg string, touch bool) (*domain.Principal, error) {
	var (
		id      string
		scopes  store.JSON[[]string]
		expires store.NullTime
		revoked store.NullTime
		u       userRow
	)
	dest := append([]any{&id, &scopes, &expires, &revoked}, u.dest()...)
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT t.id, t.scopes, t.expires_at, t.revoked_at, `+userCols+`
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE `+where, arg).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvalidCred
	}
	if err != nil {
		return nil, err
	}
	now := m.now()
	if revoked.Valid || (expires.Valid && !now.Before(expires.Time)) || u.Status != domain.UserActive {
		return nil, errInvalidCred
	}
	p, err := m.principalOf(ctx, u)
	if err != nil {
		return nil, err
	}
	p.TokenID = id
	p.Scopes = authz.EffectiveScopes(scopes.V, u.Kind, u.Role)
	if touch && m.d.Queue != nil {
		m.d.Queue.Touch("api_tokens", "last_used_at", id, m.nowDB())
	}
	return p, nil
}

// Recheck implements realtime.Rechecker: the hub re-validates every socket's
// principal (60 s timer); an error closes it with 4401.
func (m *Module) Recheck(ctx context.Context, p *domain.Principal) (*domain.Principal, error) {
	switch {
	case p == nil:
		return nil, errInvalidCred
	case !m.Admit(p):
		return nil, errInvalidCred
	case p.TokenID != "":
		return m.patPrincipal(ctx, "t.id = ?", p.TokenID, false)
	case p.SessionID != "":
		return m.sessionPrincipal(ctx, p.SessionID, false)
	}
	return nil, errInvalidCred
}

// enforceTokenScope applies the operation's `x-scopes` from api/openapi.yaml
// to PAT callers (I-6). Operations without x-scopes: reads pass, account
// management needs a session, other writes need tickets:write. Wrong-area
// requests are left to the route guard (404, I-1).
func (m *Module) enforceTokenScope(r *http.Request, p *domain.Principal) error {
	rel, ok := strings.CutPrefix(r.URL.Path, "/api/v1")
	if !ok {
		return nil
	}
	switch {
	case strings.HasPrefix(rel, "/portal/") && !p.IsCustomer():
		return nil
	case (strings.HasPrefix(rel, "/agent/") || strings.HasPrefix(rel, "/admin/")) && !p.IsStaff():
		return nil
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	op := m.scopes.match(method, rel)
	if op == nil {
		return nil
	}
	deny := problems.Forbidden("token scope does not allow this operation")
	if len(op.scopes) > 0 {
		if !authz.HasAnyScope(p, op.scopes...) {
			return deny
		}
		return nil
	}
	if method == http.MethodGet {
		return nil
	}
	if sessionOnly(method, op.path) {
		return problems.Forbidden("not available with an API token")
	}
	if !authz.HasAnyScope(p, domain.ScopeTicketsWrite) {
		return deny
	}
	return nil
}

func sessionOnly(method, path string) bool {
	switch {
	case strings.HasPrefix(path, "/me/tokens"), strings.HasPrefix(path, "/me/sessions"), strings.HasPrefix(path, "/me/mfa"):
		return true
	case path == "/me" && method == http.MethodPatch, path == "/auth/logout":
		return true
	}
	return false
}

// ---- team cache (≤ 30 s, explicit invalidation) ----

type teamEntry struct {
	teams []string
	exp   time.Time
}

var teamCache = struct {
	mu sync.Mutex
	m  map[string]teamEntry
}{m: map[string]teamEntry{}}

const (
	teamCacheTTL = 30 * time.Second
	teamCacheMax = 10_000
)

// InvalidateTeams drops the cached team list of userID (call after changing
// team membership so the next request sees it immediately).
func InvalidateTeams(userID string) {
	teamCache.mu.Lock()
	delete(teamCache.m, userID)
	teamCache.mu.Unlock()
}

func userTeams(ctx context.Context, db *store.DB, userID string) ([]string, error) {
	now := time.Now()
	teamCache.mu.Lock()
	if e, ok := teamCache.m[userID]; ok && now.Before(e.exp) {
		teamCache.mu.Unlock()
		return e.teams, nil
	}
	teamCache.mu.Unlock()
	rows, err := db.Read().QueryContext(ctx, `SELECT team_id FROM team_members WHERE user_id = ? ORDER BY team_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	teams := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	teamCache.mu.Lock()
	if len(teamCache.m) >= teamCacheMax {
		teamCache.m = map[string]teamEntry{}
	}
	teamCache.m[userID] = teamEntry{teams: teams, exp: now.Add(teamCacheTTL)}
	teamCache.mu.Unlock()
	return teams, nil
}
