// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// UserJSON renders a users row as the OpenAPI `User` schema. teamIDs is
// included for staff (nil = load them).
func UserJSON(ctx context.Context, q store.Querier, id string) (map[string]any, error) {
	u, err := loadUser(ctx, q, "u.id = ?", id)
	if err != nil {
		return nil, err
	}
	return userJSON(ctx, q, u)
}

func userJSON(ctx context.Context, q store.Querier, u userRow) (map[string]any, error) {
	out := map[string]any{
		"id": u.ID, "kind": u.Kind, "email": u.Email, "name": u.Name, "role": u.Role, "status": u.Status,
		"locale": u.Locale, "idp_managed": u.IDPManaged, "break_glass": u.BreakGlass, "has_password": u.hasPassword(),
		"tier": nil, "organization_id": nil, "last_login_at": nil,
		"created_at": u.CreatedAt.UTC(), "updated_at": u.UpdatedAt.UTC(),
	}
	if u.Tier.Valid {
		out["tier"] = u.Tier.Int64
	}
	if u.OrgID.Valid {
		out["organization_id"] = u.OrgID.String
	}
	if u.LastLogin.Valid {
		out["last_login_at"] = u.LastLogin.Time.UTC()
	}
	if u.Kind == domain.KindStaff {
		teams, err := teamIDs(ctx, q, u.ID)
		if err != nil {
			return nil, err
		}
		out["team_ids"] = teams
		projects, current, err := projectMemberships(ctx, q, u.ID)
		if err != nil {
			return nil, err
		}
		out["projects"], out["current_project_id"] = projects, current
	}
	return out, nil
}

// projectMemberships returns the user's project memberships (D-20) as
// ProjectMembership objects and the persisted project switcher choice.
func projectMemberships(ctx context.Context, q store.Querier, userID string) ([]map[string]string, *string, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, role FROM project_members WHERE user_id = ? ORDER BY project_id`, userID)
	if err != nil {
		return nil, nil, err
	}
	out := []map[string]string{}
	for rows.Next() {
		var pid, role string
		if err := rows.Scan(&pid, &role); err != nil {
			rows.Close()
			return nil, nil, err
		}
		out = append(out, map[string]string{"project_id": pid, "role": role})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var cur sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT current_project_id FROM users WHERE id = ?`, userID).Scan(&cur); err != nil {
		return nil, nil, err
	}
	if !cur.Valid {
		return out, nil, nil
	}
	return out, &cur.String, nil
}

func teamIDs(ctx context.Context, q store.Querier, userID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT team_id FROM team_members WHERE user_id = ? ORDER BY team_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type notifyPrefs struct {
	OptOut []string `json:"opt_out"`
}

func (u *userRow) prefs() map[string]any {
	m := map[string]any{}
	if len(u.NotifyPrefs) > 0 {
		_ = json.Unmarshal(u.NotifyPrefs, &m)
	}
	return m
}

func (u *userRow) optOut() []string {
	var p notifyPrefs
	if len(u.NotifyPrefs) > 0 {
		_ = json.Unmarshal(u.NotifyPrefs, &p)
	}
	if p.OptOut == nil {
		return []string{}
	}
	return p.OptOut
}

// meJSON renders the `Me` schema for user u authenticated by session idHash
// (csrf) or by a token with scopes.
func (m *Module) meJSON(ctx context.Context, q store.Querier, u userRow, idHash string, scopes []string) (map[string]any, error) {
	out, err := userJSON(ctx, q, u)
	if err != nil {
		return nil, err
	}
	p := &domain.Principal{UserID: u.ID, Kind: u.Kind, Role: u.Role, Tier: int(u.Tier.Int64), Scopes: scopes}
	if ms, ok := out["projects"].([]map[string]string); ok {
		for _, m := range ms {
			if m["role"] == string(domain.ProjectAdmin) {
				p.AdminProjects = append(p.AdminProjects, m["project_id"])
			}
		}
	}
	out["csrf_token"] = ""
	if idHash != "" {
		out["csrf_token"] = m.csrfFor(idHash)
	}
	out["effective_tier"] = p.EffectiveTier()
	out["audience"] = string(u.Kind)
	if scopes != nil {
		out["scopes"] = scopes
	} else {
		out["scopes"] = nil
	}
	out["notification_opt_out"] = u.optOut()
	out["notification_prefs"] = notify.PrefsView(string(u.Kind), u.NotifyPrefs) // NOT-037
	out["allowed_areas"] = authz.AllowedAreas(p)
	out["organization"] = nil
	if u.OrgID.Valid {
		org, err := orgJSON(ctx, q, u.OrgID.String, u.Kind == domain.KindStaff)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if org != nil {
			out["organization"] = org
		}
	}
	return out, nil
}

// orgJSON renders `Organization`; staff notes are never shown to customers.
func orgJSON(ctx context.Context, q store.Querier, id string, withNotes bool) (map[string]any, error) {
	var (
		name, status, notes string
		domains             store.JSON[[]string]
		created, updated    time.Time
	)
	err := q.QueryRowContext(ctx, `SELECT name, email_domains, status, notes, created_at, updated_at FROM organizations WHERE id = ?`, id).
		Scan(&name, &domains, &status, &notes, &created, &updated)
	if err != nil {
		return nil, store.NotFound(err)
	}
	if domains.V == nil {
		domains.V = []string{}
	}
	out := map[string]any{"id": id, "name": name, "email_domains": domains.V, "status": status, "created_at": created.UTC(), "updated_at": updated.UTC()}
	if withNotes {
		out["notes"] = notes
	}
	return out, nil
}

// ---- GET /me ----

func (m *Module) handleGetMe(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	u, err := loadUser(r.Context(), m.d.DB.Read(), "u.id = ?", p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	me, err := m.meJSON(r.Context(), m.d.DB.Read(), u, p.SessionID, p.Scopes)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// ---- PATCH /me ----

type mePatch struct {
	Name               *string                         `json:"name"`
	Locale             *string                         `json:"locale"`
	CurrentPassword    *string                         `json:"current_password"`
	NewPassword        *string                         `json:"new_password"`
	NotificationOptOut *[]string                       `json:"notification_opt_out"`
	NotificationPrefs  map[string]notify.ChannelsPatch `json:"notification_prefs"` // NOT-037
	// CurrentProjectID is the agent panel's project switcher (D-20), staff
	// only: a project id, or null for "All projects".
	CurrentProjectID optProject `json:"current_project_id"`
}

// optProject distinguishes an absent current_project_id from null.
type optProject struct {
	Set bool
	ID  *string
}

func (o *optProject) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.ID = &s
	return nil
}

func (m *Module) handlePatchMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	var in mePatch
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	u, err := loadUser(ctx, m.d.DB.Read(), "u.id = ?", p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var fields []problems.FieldError
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if l := utf8.RuneCountInString(n); l < 1 || l > 120 {
			fields = append(fields, problems.Field("name", "length", "1 to 120 characters"))
		}
		in.Name = &n
	}
	if in.Locale != nil && !domain.ValidLocale(*in.Locale) {
		fields = append(fields, problems.Field("locale", "invalid", "unsupported locale"))
	}
	if in.NotificationOptOut != nil {
		// NOT-015: unknown keys are "invalid"; auth.* and customer.reply are
		// "not_optional".
		fields = append(fields, notify.ValidateOptOut(*in.NotificationOptOut)...)
	}
	if in.NotificationPrefs != nil {
		fields = append(fields, notify.ValidatePrefs(string(u.Kind), in.NotificationPrefs)...)
	}
	if in.CurrentProjectID.Set && in.CurrentProjectID.ID != nil {
		var one int
		switch {
		case !p.IsStaff():
			fields = append(fields, problems.Field("current_project_id", "staff", "only staff have a project switcher"))
		case m.d.DB.Read().QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, *in.CurrentProjectID.ID).Scan(&one) != nil:
			fields = append(fields, problems.Field("current_project_id", "not_found", "unknown project"))
		case !p.InProject(*in.CurrentProjectID.ID):
			fields = append(fields, problems.Field("current_project_id", "not_allowed", "you are not a member of this project"))
		}
	}
	var newHash string
	if in.NewPassword != nil {
		sso, err := ssoOnly(ctx, m.d.DB.Read(), u)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		if sso { // AUTH-044: SSO-only accounts have no local password
			problems.Write(w, r, problems.Forbidden("password is managed by the identity provider"))
			return
		}
		if c := PasswordProblem(*in.NewPassword); c != "" {
			fields = append(fields, problems.Field("new_password", c, ""))
		}
		if u.hasPassword() {
			ok := false
			if in.CurrentPassword != nil {
				ok, _ = VerifyPassword(ctx, *in.CurrentPassword, u.Password.String)
			} else {
				_, _ = VerifyPassword(ctx, "", dummy())
			}
			if !ok {
				fields = append(fields, problems.Field("current_password", "invalid", "current password is wrong"))
			}
		}
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	if in.NewPassword != nil {
		if newHash, err = HashPassword(ctx, *in.NewPassword); err != nil {
			problems.Write(w, r, err)
			return
		}
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		if in.Name != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, updated_at = ? WHERE id = ?`, *in.Name, now, u.ID); err != nil {
				return err
			}
		}
		if in.Locale != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET locale = ?, updated_at = ? WHERE id = ?`, *in.Locale, now, u.ID); err != nil {
				return err
			}
		}
		if in.CurrentProjectID.Set && p.IsStaff() {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET current_project_id = ? WHERE id = ?`, in.CurrentProjectID.ID, u.ID); err != nil {
				return err
			}
		}
		if in.NotificationOptOut != nil || in.NotificationPrefs != nil {
			prefs := u.prefs()
			if in.NotificationOptOut != nil {
				opt := slices.Compact(slices.Sorted(slices.Values(*in.NotificationOptOut)))
				if opt == nil {
					opt = []string{}
				}
				prefs["opt_out"] = opt
			}
			if in.NotificationPrefs != nil {
				prefs = notify.ApplyPrefs(prefs, in.NotificationPrefs) // NOT-037: email ⇔ opt_out
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET notify_prefs = ?, updated_at = ? WHERE id = ?`, store.MustJSON(prefs), now, u.ID); err != nil {
				return err
			}
		}
		if newHash != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, newHash, now, u.ID); err != nil {
				return err
			}
			// Other sessions end; the current one stays (AUTH-016).
			if err := RevokeUserSessions(ctx, tx, u.ID, p.SessionID); err != nil {
				return err
			}
			return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.password_changed", TargetType: "user", TargetID: u.ID, IP: server.ClientIP(r)})
		}
		return nil
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.handleGetMe(w, r)
}

// ---- POST /auth/login ----

type loginRequest struct {
	Audience domain.Audience `json:"audience"`
	Email    string          `json:"email"`
	Password string          `json:"password"`
}

// errLoginFailed is the one body every login failure returns (no enumeration).
func errLoginFailed() error {
	return problems.New(problems.SlugUnauthenticated, "invalid email or password")
}

func (m *Module) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in loginRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Email = normEmail(in.Email)
	var fields []problems.FieldError
	if in.Audience != domain.AudienceStaff && in.Audience != domain.AudienceCustomer {
		fields = append(fields, problems.Field("audience", "invalid", "staff or customer"))
	}
	if !validEmail(in.Email) {
		fields = append(fields, problems.Field("email", "email", "invalid email"))
	}
	if in.Password == "" || len(in.Password) > 1024 {
		fields = append(fields, problems.Field("password", "required", ""))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	if in.Audience == domain.AudienceCustomer && m.portalRefused(w, r) {
		return
	}
	ip := server.ClientIP(r)
	if ok, wait := m.thr.allowIP("login", ip); !ok {
		problems.Write(w, r, problems.RateLimited(wait))
		return
	}
	u, uerr := loadUser(ctx, m.d.DB.Read(), "u.email = ?", in.Email)
	if uerr != nil && !errors.Is(uerr, store.ErrNotFound) {
		problems.Write(w, r, uerr)
		return
	}
	found := uerr == nil
	hash := dummy()
	if found && u.hasPassword() {
		hash = u.Password.String
	}
	ok, _ := VerifyPassword(ctx, in.Password, hash)
	// AUTH-044: an SSO-linked account signs in only through its IdP. The
	// lookup runs for every attempt (unknown address included) and the
	// refusal takes the wrong-password tail below: same body, same timing,
	// same throttle accounting.
	linked, lerr := hasIdentity(ctx, m.d.DB.Read(), u.ID, u.Kind)
	if lerr != nil {
		problems.Write(w, r, lerr)
		return
	}
	reason := ""
	switch {
	case !found:
		reason = "unknown_user"
	case !u.hasPassword():
		reason = "no_local_password"
	case !ok:
		reason = "bad_password"
	case ssoOnlyWith(u, linked):
		reason = "sso_only"
	case string(u.Kind) != string(in.Audience):
		reason = "audience_mismatch"
	case u.Status != domain.UserActive:
		reason = "status_" + string(u.Status)
	case u.Kind == domain.KindStaff && !m.opt.StaffLocal && !u.BreakGlass:
		reason = "staff_local_disabled"
	case m.thr.locked(in.Email, ip, m.now()):
		reason = "account_locked"
	}
	if reason != "" {
		m.thr.fail(in.Email, m.now())
		actor := ""
		if found {
			actor = u.ID
		}
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return Audit(ctx, tx, AuditEntry{ActorID: actor, Action: "auth.login_failed", TargetType: "user", TargetID: actor, IP: ip,
				Data: map[string]any{"email_sha256": hashToken(in.Email), "reason": reason, "audience": in.Audience}})
		})
		problems.Write(w, r, errLoginFailed())
		return
	}
	if NeedsRehash(u.Password.String) {
		if nh, err := HashPassword(ctx, in.Password); err == nil {
			u.Password.String = nh
			_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, nh, u.ID)
				return err
			})
		}
	}
	// D-19 §B: with a second factor (or a policy forcing enrolment) the
	// password alone yields a challenge, never a session; the throttle only
	// counts the login as a success once the second factor passed.
	if m.mfaGate(w, r, u, "local") {
		return
	}
	m.thr.success(in.Email, ip)
	m.startSession(w, r, u, http.StatusOK, sessionExtra{}, "local")
}

// startSession is the common tail of every login: revoke the presented
// session (fixation), create a new one, stamp last_login_at, audit, set the
// cookie and answer with `Me`.
func (m *Module) startSession(w http.ResponseWriter, r *http.Request, u userRow, status int, extra sessionExtra, method string) {
	me, err := m.openSession(w, r, u, extra, method)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, status, me)
}

// openSession creates the session, sets the cookie and returns `Me`.
func (m *Module) openSession(w http.ResponseWriter, r *http.Request, u userRow, extra sessionExtra, method string) (map[string]any, error) {
	ctx := r.Context()
	aud := domain.Audience(u.Kind)
	if err := m.portalGateUser(u); err != nil {
		return nil, err
	}
	var s newSession
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		if err := m.revokePresentedSession(ctx, tx, r); err != nil {
			return err
		}
		var err error
		if s, err = m.createSession(ctx, tx, r, u.ID, aud, extra); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, m.nowDB(), u.ID); err != nil {
			return err
		}
		ip := server.ClientIP(r)
		data := map[string]any{"method": method, "audience": aud}
		if extra.MFA != "" {
			data["mfa"] = extra.MFA
		}
		if err := Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.login_success", TargetType: "user", TargetID: u.ID, IP: ip, Data: data}); err != nil {
			return err
		}
		if u.Kind == domain.KindStaff && u.BreakGlass && method == "local" {
			m.log.Warn("break-glass admin login", "user_id", u.ID, "ip", ip)
			return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.break_glass_login", TargetType: "user", TargetID: u.ID, IP: ip})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	fresh, err := loadUser(ctx, m.d.DB.Read(), "u.id = ?", u.ID)
	if err != nil {
		return nil, err
	}
	me, err := m.meJSON(ctx, m.d.DB.Read(), fresh, s.IDHash, nil)
	if err != nil {
		return nil, err
	}
	m.setSessionCookie(w, r, s, aud)
	return me, nil
}

// ---- POST /auth/logout ----

func (m *Module) handleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	if p.SessionID == "" {
		problems.Write(w, r, problems.Forbidden("not available with an API token"))
		return
	}
	var idToken store.NullString
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT oidc_id_token FROM sessions WHERE id_hash = ?`, p.SessionID).Scan(&idToken); err != nil {
			return store.NotFound(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, p.SessionID); err != nil {
			return err
		}
		tx.AfterCommit(func() { closeSessionSockets(p.SessionID, "logged out") })
		return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.logout", TargetType: "user", TargetID: p.UserID, IP: server.ClientIP(r)})
	})
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, err)
		return
	}
	m.clearSessionCookie(w, r)
	login := m.loginURL(p.Kind)
	redirect := login
	if idToken.Valid && idToken.String != "" {
		if a := m.oidcFor(domain.Audience(p.Kind)); a != nil {
			if end := a.endSessionURL(ctx); end != "" {
				q := url.Values{"id_token_hint": {idToken.String}, "post_logout_redirect_uri": {login}, "client_id": {a.clientID}}
				sep := "?"
				if strings.Contains(end, "?") {
					sep = "&"
				}
				redirect = end + sep + q.Encode()
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"redirect_url": redirect})
}

func (m *Module) loginURL(kind domain.UserKind) string {
	q := url.Values{"logged_out": {"1"}}
	if kind == domain.KindStaff {
		return m.agentURL("/login", q)
	}
	return m.portalURL("/login", q)
}
