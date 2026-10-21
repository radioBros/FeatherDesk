// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// oidcAudience is one OIDC client (staff or customer, D-04 §2). The kind of
// the user always comes from the audience of the callback path, never from
// the token.
type oidcAudience struct {
	audience     domain.Audience
	issuer       string
	clientID     string
	clientSecret string
	label        string
	scopes       []string

	// staff only
	roleClaim string
	roleMap   map[string]domain.Role
	tierMap   map[string]int
	teamClaim string
	teamMap   map[string]string // group → team id, name or idp_group

	mu         sync.Mutex
	provider   *oidc.Provider
	endSession string
	lastErr    error
	nextTry    time.Time
	backoff    time.Duration
}

func parseOIDCAudience(e *config.Env, aud domain.Audience) *oidcAudience {
	prefix := "ST_OIDC_" + strings.ToUpper(string(aud)) + "_"
	issuer := e.String(prefix+"ISSUER", "")
	if issuer == "" {
		return nil
	}
	a := &oidcAudience{
		audience: aud,
		// Kept exactly as configured: go-oidc compares the discovered issuer
		// byte for byte, and Authentik issuers end in "/".
		issuer:       strings.TrimSpace(issuer),
		clientID:     e.Required(prefix + "CLIENT_ID"),
		clientSecret: e.String(prefix+"CLIENT_SECRET", ""),
		label:        e.String(prefix+"LABEL", "Single sign-on"),
		scopes:       e.List(prefix + "SCOPES"),
	}
	if len(a.scopes) == 0 {
		a.scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	} else if !slices.Contains(a.scopes, oidc.ScopeOpenID) {
		a.scopes = append([]string{oidc.ScopeOpenID}, a.scopes...)
	}
	if aud != domain.AudienceStaff {
		return a // customers: always requesters, no role sync (AUTH-025)
	}
	a.roleClaim = e.String(prefix+"ROLE_CLAIM", "groups")
	a.roleMap = map[string]domain.Role{}
	for g, r := range e.Map(prefix + "ROLE_MAP") {
		role := domain.Role(r)
		if !role.ValidFor(domain.KindStaff) {
			e.Fail("%sROLE_MAP: %q is not a staff role (admin, supervisor, agent)", prefix, r)
			continue
		}
		a.roleMap[g] = role
	}
	if len(a.roleMap) == 0 {
		e.Fail("%sROLE_MAP: required when %sISSUER is set (e.g. st-admins=admin,st-agents=agent)", prefix, prefix)
	}
	a.tierMap = map[string]int{}
	for g, t := range e.Map(prefix + "TIER_MAP") {
		n, err := strconv.Atoi(t)
		if err != nil || n < domain.MinTier || n > domain.MaxTier {
			e.Fail("%sTIER_MAP: %q must be a tier 1..3", prefix, t)
			continue
		}
		a.tierMap[g] = n
	}
	a.teamClaim = e.String(prefix+"TEAM_CLAIM", a.roleClaim)
	a.teamMap = e.Map(prefix + "TEAM_MAP")
	return a
}

func (m *Module) oidcFor(aud domain.Audience) *oidcAudience {
	switch aud {
	case domain.AudienceStaff:
		return m.opt.Staff
	case domain.AudienceCustomer:
		return m.opt.Customer
	}
	return nil
}

// provider discovers lazily with backoff: an IdP outage never blocks boot
// or readiness; /admin/system reports the last error.
func (a *oidcAudience) getProvider(ctx context.Context) (*oidc.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, nil
	}
	now := time.Now()
	if now.Before(a.nextTry) && a.lastErr != nil {
		return nil, a.lastErr
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(cctx, a.issuer)
	if err != nil {
		a.lastErr = err
		if a.backoff == 0 {
			a.backoff = 2 * time.Second
		} else {
			a.backoff = min(2*a.backoff, 5*time.Minute)
		}
		a.nextTry = now.Add(a.backoff)
		return nil, err
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	a.provider, a.endSession, a.lastErr, a.backoff = p, meta.EndSession, nil, 0
	return p, nil
}

func (a *oidcAudience) endSessionURL(ctx context.Context) string {
	if _, err := a.getProvider(ctx); err != nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.endSession
}

func (a *oidcAudience) status() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]any{"issuer": a.issuer, "discovered": a.provider != nil}
	if a.lastErr != nil {
		out["error"] = a.lastErr.Error()
	}
	return out
}

func (m *Module) redirectURL(aud domain.Audience) string {
	return strings.TrimRight(m.d.Cfg.PublicURL, "/") + "/api/v1/auth/oidc/" + string(aud) + "/callback"
}

func (m *Module) oauthConfig(a *oidcAudience, p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{ClientID: a.clientID, ClientSecret: a.clientSecret, Endpoint: p.Endpoint(),
		RedirectURL: m.redirectURL(a.audience), Scopes: a.scopes}
}

// ---- transaction state: sealed cookie, no table (AUTH-005) ----

const (
	oidcCookie     = "st_oidc"
	oidcCookiePath = "/api/v1/auth/oidc/"
	oidcStateTTL   = 10 * time.Minute
	oidcPurpose    = "oidc-state"
)

type oidcState struct {
	Audience string `json:"a"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	ReturnTo string `json:"r"`
	Exp      int64  `json:"e"`
}

func (m *Module) setOIDCCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: value, Path: oidcCookiePath, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: m.secureCookies(r), MaxAge: maxAge})
}

// GET /auth/oidc/{audience}/start
func (m *Module) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	aud, ok := audienceParam(r)
	if ok && aud == domain.AudienceCustomer && m.portalRefused(w, r) {
		return
	}
	a := m.oidcFor(aud)
	if !ok || a == nil {
		problems.Write(w, r, problems.NotFound())
		return
	}
	p, err := a.getProvider(r.Context())
	if err != nil {
		m.log.Warn("oidc discovery failed", "audience", aud, "err", err)
		problems.Write(w, r, problems.New(problems.SlugUnavailable, "identity provider unavailable"))
		return
	}
	kind := domain.UserKind(aud)
	st := oidcState{Audience: string(aud), State: randomToken(24), Nonce: randomToken(24), Verifier: oauth2.GenerateVerifier(),
		ReturnTo: SafeReturnTo(r.URL.Query().Get("redirect"), kind), Exp: m.now().Add(oidcStateTTL).Unix()}
	b, _ := json.Marshal(st)
	sealed, err := m.d.Secrets.SealString(oidcPurpose, string(b))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.setOIDCCookie(w, r, sealed, int(oidcStateTTL/time.Second))
	u := m.oauthConfig(a, p).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusFound)
}

// identityClaims are what the callback extracts from the ID token / userinfo.
type identityClaims struct {
	Issuer, Subject string
	Email           string
	EmailVerified   bool
	Name            string
	SID             string
	Groups          []string
	TeamGroups      []string
}

const (
	refusalNoRole   = "no mapped identity-provider group"
	refusalDisabled = "account disabled"
)

func errOIDC(detail string) error { return problems.Forbidden(detail) }

// oidcRefused answers a refused callback. A browser navigation in the staff
// audience lands on /agent/login?error=<code> (no_role, disabled, oidc_failed,
// idp_unavailable), which explains the refusal in plain words (FE-020,
// AUTH-006); API clients and the customer audience get the problem document.
func (m *Module) oidcRefused(w http.ResponseWriter, r *http.Request, aud domain.Audience, code string, err error) {
	if aud == domain.AudienceStaff && strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, m.agentURL("/login", url.Values{"error": {code}}), http.StatusFound)
		return
	}
	problems.Write(w, r, err)
}

// GET /auth/oidc/{audience}/callback
func (m *Module) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aud, ok := audienceParam(r)
	if ok && aud == domain.AudienceCustomer && m.portalRefused(w, r) {
		return
	}
	a := m.oidcFor(aud)
	if !ok || a == nil {
		problems.Write(w, r, problems.NotFound())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c, err := r.Cookie(oidcCookie)
	m.setOIDCCookie(w, r, "", -1) // one attempt per state
	if err != nil || c.Value == "" {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("login session expired; start again"))
		return
	}
	raw, err := m.d.Secrets.OpenString(oidcPurpose, c.Value)
	var st oidcState
	if err != nil || json.Unmarshal([]byte(raw), &st) != nil {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("login session invalid; start again"))
		return
	}
	q := r.URL.Query()
	if st.Audience != string(aud) || m.now().Unix() > st.Exp ||
		subtle.ConstantTimeCompare([]byte(st.State), []byte(q.Get("state"))) != 1 {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("state mismatch"))
		return
	}
	if e := q.Get("error"); e != "" || q.Get("code") == "" {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("identity provider refused the login"))
		return
	}
	p, err := a.getProvider(ctx)
	if err != nil {
		m.oidcRefused(w, r, aud, "idp_unavailable", problems.New(problems.SlugUnavailable, "identity provider unavailable"))
		return
	}
	tok, err := m.oauthConfig(a, p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		m.log.Warn("oidc code exchange failed", "audience", aud, "err", err)
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("code exchange failed"))
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("no id_token in token response"))
		return
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: a.clientID, Now: m.now}).Verify(ctx, rawID)
	if err != nil {
		m.log.Warn("oidc id_token rejected", "audience", aud, "err", err)
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("id_token rejected"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("nonce mismatch"))
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("unreadable claims"))
		return
	}
	if azp, _ := claims["azp"].(string); azp != "" && azp != a.clientID {
		m.oidcRefused(w, r, aud, "oidc_failed", errOIDC("authorized party mismatch"))
		return
	}
	id := identityClaims{Issuer: idt.Issuer, Subject: idt.Subject, Email: normEmail(claimString(claims, "email")),
		EmailVerified: claimBool(claims, "email_verified"), SID: claimString(claims, "sid")}
	id.Name = firstNonEmpty(claimString(claims, "name"), claimString(claims, "preferred_username"), id.Email)
	if aud == domain.AudienceStaff {
		groups, present := claimStrings(claims, a.roleClaim)
		teams, tpresent := claimStrings(claims, a.teamClaim)
		if (!present || (len(a.teamMap) > 0 && !tpresent)) && p.UserInfoEndpoint() != "" {
			// Fall back to userinfo when the ID token omits the claim.
			if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
				var uc map[string]any
				if ui.Claims(&uc) == nil {
					if !present {
						groups, _ = claimStrings(uc, a.roleClaim)
					}
					if !tpresent {
						teams, _ = claimStrings(uc, a.teamClaim)
					}
				}
			}
		}
		id.Groups, id.TeamGroups = groups, teams
	}
	m.oidcLogin(w, r, a, id, rawID, st.ReturnTo)
}

// oidcLogin links or creates the user, syncs role/tier/teams (staff, every
// login, same transaction as the session), and redirects into the area.
func (m *Module) oidcLogin(w http.ResponseWriter, r *http.Request, a *oidcAudience, id identityClaims, rawID, returnTo string) {
	ctx := r.Context()
	kind := domain.UserKind(a.audience)
	ip := server.ClientIP(r)
	var (
		refusal string
		sess    newSession
		userID  string
	)
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		refusal = ""
		u, found, err := m.resolveIdentity(ctx, tx, a, id, ip)
		if err != nil {
			var pe *problems.Error
			if errors.As(err, &pe) {
				refusal = pe.Detail
				return nil
			}
			return err
		}
		var role domain.Role
		tier := 0
		if kind == domain.KindStaff {
			var ok bool
			role, tier, ok = a.mapRole(id.Groups)
			if !ok {
				refusal = refusalNoRole
				if found && u.Status != domain.UserDisabled {
					return DisableUser(ctx, tx, u.ID, "", "oidc: no mapped group", ip)
				}
				return nil
			}
		}
		if !found {
			if u, err = m.createOIDCUser(ctx, tx, a, id, role, tier); err != nil {
				return err
			}
		}
		if u.Status == domain.UserDisabled {
			refusal = refusalDisabled
			return nil
		}
		if kind == domain.KindStaff {
			if err := m.syncStaffRole(ctx, tx, u, role, tier, ip); err != nil {
				return err
			}
			if err := m.syncTeams(ctx, tx, a, u.ID, id.TeamGroups); err != nil {
				return err
			}
		} else {
			if !u.IDPManaged { // AUTH-044: an identity makes the account SSO-only
				if _, err := tx.ExecContext(ctx, `UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID); err != nil {
					return err
				}
				if err := markSSOLinked(ctx, tx, u, "idp_sync", ip); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'active', updated_at = ? WHERE id = ? AND status = 'pending_verification'`, m.nowDB(), u.ID); err != nil {
				return err
			}
			if id.EmailVerified {
				if err := joinOrgByDomain(ctx, tx, u.ID, u.Email); err != nil {
					return err
				}
			}
		}
		if err := m.revokePresentedSession(ctx, tx, r); err != nil {
			return err
		}
		if sess, err = m.createSession(ctx, tx, r, u.ID, a.audience, sessionExtra{OIDCIDToken: rawID, OIDCSID: id.SID}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, m.nowDB(), u.ID); err != nil {
			return err
		}
		userID = u.ID
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.login_success", TargetType: "user", TargetID: u.ID, IP: ip,
			Data: map[string]any{"method": "oidc", "audience": a.audience}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if refusal != "" {
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return Audit(ctx, tx, AuditEntry{Action: "auth.login_failed", TargetType: "user", IP: ip,
				Data: map[string]any{"method": "oidc", "audience": a.audience, "reason": refusal, "subject_sha256": hashToken(id.Issuer + "|" + id.Subject)}})
		})
		code := "oidc_failed"
		switch refusal {
		case refusalNoRole:
			code = "no_role"
		case refusalDisabled:
			code = "disabled"
		}
		m.oidcRefused(w, r, a.audience, code, errOIDC(refusal))
		return
	}
	_ = userID
	m.setSessionCookie(w, r, sess, a.audience)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// resolveIdentity finds the user for (issuer, subject), or links an existing
// user by verified email when ST_OIDC_LINK_BY_EMAIL=true (same kind only,
// never a break-glass admin). A link makes the account SSO-only: its local
// password and local sessions end in the same transaction (AUTH-044).
// found=false means "create".
func (m *Module) resolveIdentity(ctx context.Context, tx *store.Tx, a *oidcAudience, id identityClaims, ip string) (userRow, bool, error) {
	kind := domain.UserKind(a.audience)
	var uid string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE issuer = ? AND subject = ?`, id.Issuer, id.Subject).Scan(&uid)
	switch {
	case err == nil:
		u, err := loadUser(ctx, tx, "u.id = ?", uid)
		if err != nil {
			return userRow{}, false, err
		}
		if u.Kind != kind {
			return userRow{}, false, errOIDC("identity belongs to another audience")
		}
		return u, true, nil
	case !errors.Is(store.NotFound(err), store.ErrNotFound):
		return userRow{}, false, err
	}
	if id.Email == "" || !validEmail(id.Email) {
		return userRow{}, false, errOIDC("the identity provider sent no usable email")
	}
	u, err := loadUser(ctx, tx, "u.email = ?", id.Email)
	if errors.Is(err, store.ErrNotFound) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	if !m.opt.LinkByEmail || !id.EmailVerified || u.Kind != kind || u.BreakGlass {
		return userRow{}, false, errOIDC("an account with this email exists and cannot be linked automatically")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_identities (id, user_id, audience, issuer, subject, email_at_link, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), u.ID, string(a.audience), id.Issuer, id.Subject, id.Email, m.nowDB()); err != nil {
		return userRow{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID); err != nil {
		return userRow{}, false, err
	}
	if err := markSSOLinked(ctx, tx, u, "link_by_email", ip); err != nil {
		return userRow{}, false, err
	}
	u.IDPManaged = true
	u.Password = sql.NullString{}
	return u, true, nil
}

func (m *Module) createOIDCUser(ctx context.Context, tx *store.Tx, a *oidcAudience, id identityClaims, role domain.Role, tier int) (userRow, error) {
	uid := store.NewID()
	now := m.nowDB()
	name := truncate(id.Name, 120)
	var err error
	if a.audience == domain.AudienceStaff {
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, tier, status, idp_managed, created_at, updated_at)
			VALUES (?, 'staff', ?, ?, ?, ?, 'active', ?, ?, ?)`, uid, id.Email, name, string(role), tier, true, now, now)
	} else {
		// Customer OIDC always creates a requester, whatever the token says (AUTH-025).
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, status, idp_managed, created_at, updated_at)
			VALUES (?, 'customer', ?, ?, 'requester', 'active', ?, ?, ?)`, uid, id.Email, name, true, now, now)
	}
	if err != nil {
		return userRow{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_identities (id, user_id, audience, issuer, subject, email_at_link, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), uid, string(a.audience), id.Issuer, id.Subject, id.Email, now); err != nil {
		return userRow{}, err
	}
	if err := Audit(ctx, tx, AuditEntry{ActorID: uid, Action: "auth.user_provisioned", TargetType: "user", TargetID: uid,
		Data: map[string]any{"audience": a.audience, "issuer": id.Issuer}}); err != nil {
		return userRow{}, err
	}
	return loadUser(ctx, tx, "u.id = ?", uid)
}

// mapRole: highest mapped role and highest mapped tier win (default tier 1).
func (a *oidcAudience) mapRole(groups []string) (domain.Role, int, bool) {
	var best domain.Role
	tier := domain.MinTier
	for _, g := range groups {
		if r, ok := a.roleMap[g]; ok && domain.RoleRank(r) > domain.RoleRank(best) {
			best = r
		}
		if t, ok := a.tierMap[g]; ok && t > tier {
			tier = t
		}
	}
	return best, tier, best != ""
}

// syncStaffRole applies the IdP role/tier on every login (D-04 §4).
func (m *Module) syncStaffRole(ctx context.Context, tx *store.Tx, u userRow, role domain.Role, tier int, ip string) error {
	if u.Role == role && int(u.Tier.Int64) == tier && u.IDPManaged {
		return nil
	}
	bg := u.BreakGlass && role == domain.RoleAdmin
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, tier = ?, idp_managed = ?, break_glass = ?, updated_at = ? WHERE id = ?`,
		string(role), tier, true, bg, m.nowDB(), u.ID); err != nil {
		return err
	}
	// AUTH-044: the account becomes SSO-only when it turns IdP-managed or
	// loses break-glass; a break-glass admin keeps its local password.
	if !bg && (!u.IDPManaged || breakGlassAdmin(u)) {
		if err := markSSOLinked(ctx, tx, u, "idp_sync", ip); err != nil {
			return err
		}
	}
	if u.Role == role && int(u.Tier.Int64) == tier {
		return nil
	}
	// A lower role or tier may leave assigned tickets outside M-4 (TKT-017).
	if err := membershipChanged(ctx, tx, u.ID, "", []string{u.ID}); err != nil {
		return err
	}
	tx.AfterCommit(func() { InvalidateTeams(u.ID) })
	return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.role_synced", TargetType: "user", TargetID: u.ID, IP: ip,
		Data: map[string]any{"from": map[string]any{"role": u.Role, "tier": u.Tier.Int64}, "to": map[string]any{"role": role, "tier": tier}}})
}

// syncTeams mirrors IdP groups into team_members rows with source='idp'
// (AUTH-031); local memberships are never touched. Off when the map is empty.
func (m *Module) syncTeams(ctx context.Context, tx *store.Tx, a *oidcAudience, userID string, groups []string) error {
	if len(a.teamMap) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, g := range groups {
		ref, ok := a.teamMap[g]
		if !ok {
			continue
		}
		tid, err := resolveTeam(ctx, tx, ref)
		if err != nil {
			return err
		}
		if tid != "" {
			want[tid] = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT team_id, source FROM team_members WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	have := map[string]string{}
	for rows.Next() {
		var t, s string
		if err := rows.Scan(&t, &s); err != nil {
			rows.Close()
			return err
		}
		have[t] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	changed := false
	for t := range want {
		if _, ok := have[t]; !ok {
			if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id, source, created_at) VALUES (?, ?, 'idp', ?)`, t, userID, m.nowDB()); err != nil {
				return err
			}
			changed = true
		}
	}
	for t, s := range have {
		if s == "idp" && !want[t] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ? AND source = 'idp'`, t, userID); err != nil {
				return err
			}
			// Leaving a team unassigns that team's tickets (M-4, TKT-017).
			if err := membershipChanged(ctx, tx, userID, t, []string{userID}); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		tx.AfterCommit(func() { InvalidateTeams(userID) })
		return Audit(ctx, tx, AuditEntry{ActorID: userID, Action: "auth.teams_synced", TargetType: "user", TargetID: userID})
	}
	return nil
}

// resolveTeam matches a TEAM_MAP value against team id, idp_group or name.
func resolveTeam(ctx context.Context, q store.Querier, ref string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM teams WHERE id = ? OR idp_group = ? OR lower(name) = lower(?) ORDER BY id LIMIT 1`, ref, ref, ref).Scan(&id)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return "", nil
	}
	return id, err
}

// validateTeamMap: an unknown team in ST_OIDC_STAFF_TEAM_MAP is a boot error.
func (m *Module) validateTeamMap(ctx context.Context) error {
	a := m.opt.Staff
	if a == nil || len(a.teamMap) == 0 {
		return nil
	}
	var bad []string
	for g, ref := range a.teamMap {
		id, err := resolveTeam(ctx, m.d.DB.Read(), ref)
		if err != nil {
			return err
		}
		if id == "" {
			bad = append(bad, g+"="+ref)
		}
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		return fmt.Errorf("ST_OIDC_STAFF_TEAM_MAP: unknown team(s): %s", strings.Join(bad, ", "))
	}
	return nil
}

// ---- claim helpers ----

func claimString(c map[string]any, k string) string {
	s, _ := c[k].(string)
	return strings.TrimSpace(s)
}

func claimBool(c map[string]any, k string) bool {
	switch v := c[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	}
	return false
}

// claimStrings reads a list claim (array of strings, or one string).
func claimStrings(c map[string]any, k string) ([]string, bool) {
	v, ok := c[k]
	if !ok || v == nil {
		return nil, false
	}
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	case string:
		return []string{x}, true
	}
	return nil, true
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
