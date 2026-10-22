// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

func errEmailDisabled() error {
	return problems.New(problems.SlugEmailDisabled, "email is not configured on this instance")
}

func errBadToken() error { return problems.BadRequest("the link is invalid or has expired") }

// throttled applies the per-IP bucket of an anonymous auth action (AUTH-012).
func (m *Module) throttled(w http.ResponseWriter, r *http.Request, action string) bool {
	if ok, wait := m.thr.allowIP(action, server.ClientIP(r)); !ok {
		problems.Write(w, r, problems.RateLimited(wait))
		return true
	}
	return false
}

// requireEmail answers 409 email-disabled when SMTP is not configured.
func (m *Module) requireEmail(w http.ResponseWriter, r *http.Request) bool {
	ok, err := m.emailEnabled(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return false
	}
	if !ok {
		problems.Write(w, r, errEmailDisabled())
		return false
	}
	return true
}

// FindOrgByEmailDomain returns the active organization whose email_domains
// contains the domain of email ("" when none). Same rule for registration,
// customer OIDC and inbound mail (AUTH-014, NOT-028).
func FindOrgByEmailDomain(ctx context.Context, q store.Querier, email string) (string, error) {
	d := emailDomain(email)
	if d == "" {
		return "", nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id, email_domains FROM organizations WHERE status = 'active' ORDER BY created_at, id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id      string
			domains store.JSON[[]string]
		)
		if err := rows.Scan(&id, &domains); err != nil {
			return "", err
		}
		for _, x := range domains.V {
			if strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(x, "@")), d) {
				return id, nil
			}
		}
	}
	return "", rows.Err()
}

// joinOrgByDomain sets organization_id for a customer without one.
func joinOrgByDomain(ctx context.Context, tx *store.Tx, userID, email string) error {
	org, err := FindOrgByEmailDomain(ctx, tx, email)
	if err != nil || org == "" {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET organization_id = ?, updated_at = ? WHERE id = ? AND kind = 'customer' AND organization_id IS NULL`, org, store.Now(), userID)
	return err
}

// ---- POST /auth/register (AUTH-014) ----

type registerRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Locale   string `json:"locale"`
	// AcceptPrivacy is required when a privacy policy URL is configured (D-19 §C).
	AcceptPrivacy bool `json:"accept_privacy"`
}

func (m *Module) handleRegister(w http.ResponseWriter, r *http.Request) {
	if m.portalRefused(w, r) {
		return
	}
	ctx := r.Context()
	var in registerRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Email, in.Name = normEmail(in.Email), strings.TrimSpace(in.Name)
	var fields []problems.FieldError
	if !validEmail(in.Email) {
		fields = append(fields, problems.Field("email", "email", "invalid email"))
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 120 {
		fields = append(fields, problems.Field("name", "length", "1 to 120 characters"))
	}
	if c := PasswordProblem(in.Password); c != "" {
		fields = append(fields, problems.Field("password", c, ""))
	}
	if in.Locale != "" && !domain.ValidLocale(in.Locale) {
		fields = append(fields, problems.Field("locale", "invalid", ""))
	}
	policyURL := m.privacyPolicyURL(ctx)
	if policyURL != "" && !in.AcceptPrivacy {
		fields = append(fields, problems.Field("accept_privacy", "required", "the privacy policy must be accepted"))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	if m.throttled(w, r, "register") {
		return
	}
	sup := settings.DefaultSupport()
	if m.d.Settings != nil {
		var err error
		if sup, err = m.d.Settings.Support(ctx); err != nil {
			problems.Write(w, r, err)
			return
		}
	}
	if !m.selfRegistration(sup) {
		problems.Write(w, r, problems.Forbidden("self-registration is disabled"))
		return
	}
	if !m.requireEmail(w, r) {
		return
	}
	if in.Locale == "" {
		_, in.Locale = publicLocales(sup)
	}
	// Hash first: an existing address must cost the same as a new one.
	hash, err := HashPassword(ctx, in.Password)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	existing, err := loadUser(ctx, m.d.DB.Read(), "u.email = ?", in.Email)
	if err == nil {
		if existing.Status != domain.UserDisabled {
			m.sendMail(ctx, MailPayload{Template: TplAlreadyRegistered, UserID: existing.ID, To: existing.Email, Locale: existing.Locale,
				Vars: map[string]string{"url": m.areaURL(existing.Kind, "/login", nil), "reset_url": m.areaURL(existing.Kind, "/forgot-password", nil)}})
		}
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, err)
		return
	}
	id := store.NewID()
	var tok string
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, status, password_hash, locale, created_at, updated_at)
			VALUES (?, 'customer', ?, ?, 'requester', 'pending_verification', ?, ?, ?, ?)`, id, in.Email, in.Name, hash, in.Locale, now, now); err != nil {
			return err
		}
		var err error
		if tok, err = IssueAuthToken(ctx, tx, id, PurposeVerifyEmail, nil); err != nil {
			return err
		}
		if err := Audit(ctx, tx, AuditEntry{ActorID: id, Action: "auth.registered", TargetType: "user", TargetID: id, IP: server.ClientIP(r)}); err != nil {
			return err
		}
		if policyURL != "" {
			return recordConsent(ctx, tx, id, policyURL, server.ClientIP(r))
		}
		return nil
	})
	if err != nil {
		if store.IsUniqueViolation(err) { // lost a race with another registration
			writeJSON(w, http.StatusAccepted, accepted)
			return
		}
		problems.Write(w, r, err)
		return
	}
	m.sendMail(ctx, MailPayload{Template: TplVerifyEmail, UserID: id, To: in.Email, Locale: in.Locale,
		Vars: map[string]string{"url": LinkURL(m.d.Cfg, domain.KindCustomer, "/verify-email", tok), "name": in.Name}})
	writeJSON(w, http.StatusAccepted, accepted)
}

// ---- POST /auth/verify-email ----

type tokenRequest struct {
	Token string `json:"token"`
}

func (m *Module) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if m.portalRefused(w, r) {
		return
	}
	ctx := r.Context()
	var in tokenRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.Token == "" {
		problems.Write(w, r, problems.Validation(problems.Field("token", "required", "")))
		return
	}
	var uid string
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if uid, _, err = ConsumeAuthToken(ctx, tx, in.Token, PurposeVerifyEmail); err != nil {
			return err
		}
		u, err := loadUser(ctx, tx, "u.id = ?", uid)
		if err != nil {
			return err
		}
		if u.Status == domain.UserDisabled {
			return ErrTokenInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'active', updated_at = ? WHERE id = ? AND status = 'pending_verification'`, m.nowDB(), uid); err != nil {
			return err
		}
		// Organization auto-join happens only after verification (AUTH-014).
		if u.Kind == domain.KindCustomer {
			if err := joinOrgByDomain(ctx, tx, uid, u.Email); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, AuditEntry{ActorID: uid, Action: "auth.email_verified", TargetType: "user", TargetID: uid, IP: server.ClientIP(r)})
	})
	if errors.Is(err, ErrTokenInvalid) || errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, errBadToken())
		return
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.loginUserID(w, r, uid, "verify_email")
}

// loginUserID starts a session for a freshly verified/accepted user.
func (m *Module) loginUserID(w http.ResponseWriter, r *http.Request, uid, method string) {
	u, err := loadUser(r.Context(), m.d.DB.Read(), "u.id = ?", uid)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if u.Status != domain.UserActive {
		problems.Write(w, r, errBadToken())
		return
	}
	// AUTH-044: a link issued before the account was linked to SSO never
	// opens a local session for it.
	if sso, err := ssoOnly(r.Context(), m.d.DB.Read(), u); err != nil || sso {
		if err == nil {
			err = errBadToken()
		}
		problems.Write(w, r, err)
		return
	}
	if m.mfaGate(w, r, u, method) { // D-19 §B: a mailbox alone never bypasses a second factor
		return
	}
	m.startSession(w, r, u, http.StatusOK, sessionExtra{}, method)
}

// ---- POST /auth/password/forgot (AUTH-015) ----

type emailRequest struct {
	Email string `json:"email"`
}

func (m *Module) handleForgot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in emailRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Email = normEmail(in.Email)
	if !validEmail(in.Email) {
		problems.Write(w, r, problems.Validation(problems.Field("email", "email", "invalid email")))
		return
	}
	if m.throttled(w, r, "forgot") || !m.requireEmail(w, r) {
		return
	}
	u, err := loadUser(ctx, m.d.DB.Read(), "u.email = ?", in.Email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			m.log.Error("forgot password lookup", "err", err)
		}
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	sso, err := ssoOnly(ctx, m.d.DB.Read(), u)
	if err != nil {
		m.log.Error("forgot password lookup", "err", err)
		writeJSON(w, http.StatusAccepted, accepted)
		return
	}
	switch {
	case u.Status == domain.UserDisabled, u.Kind == domain.KindCustomer && !m.portalOn.Load():
		// nothing (a customer has no portal to reset a password for)
	case sso || (u.Kind == domain.KindStaff && !m.opt.StaffLocal && !u.BreakGlass):
		// SSO account: tell the user to sign in with the identity provider.
		m.sendMail(ctx, MailPayload{Template: TplResetPassword, UserID: u.ID, To: u.Email, Locale: u.Locale,
			Vars: map[string]string{"sso": "true", "url": m.areaURL(u.Kind, "/login", nil)}})
	default:
		var tok string
		err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = IssueAuthToken(ctx, tx, u.ID, PurposeResetPassword, nil)
			return err
		})
		if err != nil {
			m.log.Error("forgot password token", "err", err)
			break
		}
		m.sendMail(ctx, MailPayload{Template: TplResetPassword, UserID: u.ID, To: u.Email, Locale: u.Locale,
			Vars: map[string]string{"url": LinkURL(m.d.Cfg, u.Kind, "/reset-password", tok), "name": u.Name}})
	}
	writeJSON(w, http.StatusAccepted, accepted)
}

// ---- POST /auth/password/reset ----

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (m *Module) handleReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in resetRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if c := PasswordProblem(in.Password); c != "" {
		problems.Write(w, r, problems.Validation(problems.Field("password", c, "")))
		return
	}
	if m.throttled(w, r, "reset") {
		return
	}
	hash, err := HashPassword(ctx, in.Password)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		uid, _, err := ConsumeAuthToken(ctx, tx, in.Token, PurposeResetPassword)
		if err != nil {
			return err
		}
		u, err := loadUser(ctx, tx, "u.id = ?", uid)
		if err != nil {
			return err
		}
		if u.Status == domain.UserDisabled {
			return ErrTokenInvalid
		}
		if sso, err := ssoOnly(ctx, tx, u); err != nil || sso { // AUTH-044
			if err == nil {
				err = ErrTokenInvalid
			}
			return err
		}
		// The link proved control of the mailbox: a pending account is verified.
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, status = 'active', updated_at = ? WHERE id = ?`, hash, m.nowDB(), uid); err != nil {
			return err
		}
		if err := RevokeUserSessions(ctx, tx, uid, ""); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: uid, Action: "auth.password_reset", TargetType: "user", TargetID: uid, IP: server.ClientIP(r)})
	})
	if errors.Is(err, ErrTokenInvalid) || errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, errBadToken())
		return
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	noContent(w)
}

// ---- POST /auth/magic-link (AUTH-026) ----

type magicRequest struct {
	Email *string `json:"email"`
	Token *string `json:"token"`
}

func (m *Module) handleMagicLink(w http.ResponseWriter, r *http.Request) {
	if m.portalRefused(w, r) {
		return
	}
	ctx := r.Context()
	var in magicRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.Token != nil {
		// POST consumes; a GET of the emailed link (prefetchers) never does.
		var uid string
		err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			var err error
			uid, _, err = ConsumeAuthToken(ctx, tx, *in.Token, PurposeMagicLink)
			return err
		})
		if errors.Is(err, ErrTokenInvalid) {
			problems.Write(w, r, errBadToken())
			return
		}
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		m.loginUserID(w, r, uid, "magic_link")
		return
	}
	if in.Email == nil {
		problems.Write(w, r, problems.Validation(problems.Field("email", "required", "")))
		return
	}
	email := normEmail(*in.Email)
	if !validEmail(email) {
		problems.Write(w, r, problems.Validation(problems.Field("email", "email", "invalid email")))
		return
	}
	if m.throttled(w, r, "magic") || !m.requireEmail(w, r) {
		return
	}
	u, err := loadUser(ctx, m.d.DB.Read(), "u.email = ?", email)
	sso := false
	if err == nil {
		sso, err = ssoOnly(ctx, m.d.DB.Read(), u)
	}
	// AUTH-044: no sign-in link for an SSO-only account (same answer).
	if err == nil && !sso && u.Kind == domain.KindCustomer && u.Status == domain.UserActive {
		var tok string
		err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = IssueAuthToken(ctx, tx, u.ID, PurposeMagicLink, nil)
			return err
		})
		if err == nil {
			m.sendMail(ctx, MailPayload{Template: TplMagicLink, UserID: u.ID, To: u.Email, Locale: u.Locale,
				Vars: map[string]string{"url": LinkURL(m.d.Cfg, domain.KindCustomer, "/magic-link", tok), "name": u.Name}})
		}
	}
	writeJSON(w, http.StatusOK, accepted)
}

// ---- POST /auth/invite/accept (AUTH-027) ----

type inviteAccept struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (m *Module) handleInviteAccept(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in inviteAccept
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	var fields []problems.FieldError
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 120 {
		fields = append(fields, problems.Field("name", "length", "1 to 120 characters"))
	}
	if c := PasswordProblem(in.Password); c != "" {
		fields = append(fields, problems.Field("password", c, ""))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	if m.throttled(w, r, "invite") {
		return
	}
	hash, err := HashPassword(ctx, in.Password)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var uid string
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if uid, _, err = ConsumeAuthToken(ctx, tx, in.Token, PurposeInvite); err != nil {
			return err
		}
		u, err := loadUser(ctx, tx, "u.id = ?", uid)
		if err != nil {
			return err
		}
		// The kind comes from the invited row, never from the request.
		if err := m.portalGateUser(u); err != nil {
			return err
		}
		if u.Status != domain.UserPendingVerification {
			return ErrTokenInvalid
		}
		// AUTH-044: an invitee who already signed in through SSO gets no
		// local password.
		if sso, err := ssoOnly(ctx, tx, u); err != nil || sso {
			if err == nil {
				err = ErrTokenInvalid
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, password_hash = ?, status = 'active', updated_at = ? WHERE id = ?`, in.Name, hash, m.nowDB(), uid); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: uid, Action: "auth.invite_accepted", TargetType: "user", TargetID: uid, IP: server.ClientIP(r)})
	})
	if errors.Is(err, ErrTokenInvalid) || errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, errBadToken())
		return
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.loginUserID(w, r, uid, "invite")
}

var customerRoles = []domain.Role{domain.RoleRequester, domain.RoleOrgManager}

func validCustomerRole(r domain.Role) bool { return slices.Contains(customerRoles, r) }
