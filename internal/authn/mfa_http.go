// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// TplMFACode is the auth mail carrying a one-time sign-in code.
const TplMFACode = "auth.mfa_code"

// mountMFA registers the MFA routes (called from Mount).
func (m *Module) mountMFA(g server.Groups) {
	g.Public.Post("/auth/mfa/verify", m.handleMFAVerify)
	g.Public.Post("/auth/mfa/email/send", m.handleMFAEmailSend)
	g.Public.Post("/auth/mfa/enroll/totp", m.handleMFAEnrollTOTP)
	g.Public.Post("/auth/mfa/enroll/confirm", m.handleMFAEnrollConfirm)

	me := g.Authed.With(RequireSession)
	me.Get("/me/mfa", m.handleMFAStatus)
	me.Post("/me/mfa/reauth", m.handleMFAReauth)
	me.Post("/me/mfa/totp/setup", m.handleMFATOTPSetup)
	me.Post("/me/mfa/totp/confirm", m.handleMFATOTPConfirm)
	me.Delete("/me/mfa/totp", m.handleMFATOTPDisable)
	me.Post("/me/mfa/email", m.handleMFAEmailEnable)
	me.Delete("/me/mfa/email", m.handleMFAEmailDisable)
	me.Post("/me/mfa/recovery-codes", m.handleMFARecoveryRegenerate)

	adm := g.Admin.With(RequireScope(domain.ScopeAdmin))
	adm.Get("/users/{id}/mfa", m.handleAdminMFAStatus)
	adm.Post("/users/{id}/mfa/reset", m.handleAdminMFAReset)
	adm.Get("/security", m.handleGetSecurity)
	adm.Patch("/security", m.handlePatchSecurity)
}

// registerMFAQueue prunes old challenges and stale TOTP setups hourly.
func (m *Module) registerMFAQueue(q interface {
	Every(name string, interval time.Duration, fn func(context.Context) error)
}) {
	q.Every("authn.mfa_prune", time.Hour, func(ctx context.Context) error { return PruneMFAChallenges(ctx, m.d.DB, m.now()) })
}

// errMFAExpired: the challenge is unknown, used, expired or out of attempts.
func errMFAExpired() error {
	return problems.New(problems.SlugUnauthenticated, "the sign-in attempt expired; sign in again")
}

func errMFACode() error {
	return problems.Validation(problems.Field("code", "invalid", "the code is wrong or has expired"))
}

func errReauth() error {
	return problems.Forbidden("confirm your password first (POST /me/mfa/reauth)")
}

var errCodeRejected = errors.New("authn: code rejected")

// ---- the login gate ----

// mfaGate runs after a successful first factor of a non-SSO login. It
// answers with an MFA challenge (and returns true) when u has a second
// factor or the policy forces u to enrol; otherwise the caller starts the
// session. method is the first factor ("local", "magic_link", …) and is
// carried to the session audit once the challenge completes.
func (m *Module) mfaGate(w http.ResponseWriter, r *http.Request, u userRow, method string) bool {
	if !mfaApplies(u) {
		return false
	}
	ctx := r.Context()
	st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		problems.Write(w, r, err)
		return true
	}
	purpose := ""
	if st.enabled() {
		purpose = "login"
	} else {
		policy, err := m.mfaPolicy(ctx)
		if err != nil {
			problems.Write(w, r, err)
			return true
		}
		if mfaRequiredFor(policy, u) {
			purpose = "enroll"
		}
	}
	if purpose == "" {
		return false
	}
	methods, err := m.challengeMethods(ctx, u.ID, purpose, st)
	if err != nil {
		problems.Write(w, r, err)
		return true
	}
	ip := server.ClientIP(r)
	var (
		tok string
		exp time.Time
	)
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if tok, exp, err = m.issueChallenge(ctx, tx, u.ID, purpose, method, ip); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_challenged", TargetType: "user", TargetID: u.ID, IP: ip,
			Data: map[string]any{"purpose": purpose, "method": method}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mfa_required": true, "mfa_token": tok, "methods": methods,
		"enrollment_required": purpose == "enroll", "expires_at": exp.UTC(),
	})
	return true
}

// challengeMethods lists what the user can answer with.
func (m *Module) challengeMethods(ctx context.Context, userID, purpose string, st mfaState) ([]string, error) {
	mailOn, err := m.emailEnabled(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	if purpose == "enroll" {
		out = append(out, MFAMethodTOTP)
		if mailOn {
			out = append(out, MFAMethodEmail)
		}
		return out, nil
	}
	if st.totp() {
		out = append(out, MFAMethodTOTP)
	}
	if st.EmailEnabled && mailOn {
		out = append(out, MFAMethodEmail)
	}
	n, err := recoveryRemaining(ctx, m.d.DB.Read(), userID)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		out = append(out, MFAMethodRecovery)
	}
	return out, nil
}

// ---- challenge steps (public) ----

type mfaTokenRequest struct {
	MFAToken string `json:"mfa_token"`
	Method   string `json:"method"`
	Code     string `json:"code"`
}

// openChallenge decodes the body, applies the per-IP bucket and loads the
// challenge and its (still active, still local) user.
func (m *Module) openChallenge(w http.ResponseWriter, r *http.Request, bucket string, needCode bool, methods ...string) (mfaTokenRequest, mfaChallenge, userRow, bool) {
	var (
		in mfaTokenRequest
		c  mfaChallenge
		u  userRow
	)
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return in, c, u, false
	}
	var fields []problems.FieldError
	if in.MFAToken == "" {
		fields = append(fields, problems.Field("mfa_token", "required", ""))
	}
	if len(methods) > 0 {
		ok := false
		for _, x := range methods {
			ok = ok || in.Method == x
		}
		if !ok {
			fields = append(fields, problems.Field("method", "enum", ""))
		}
	}
	in.Code = normCode(in.Code)
	if needCode && (in.Code == "" || len(in.Code) > 64) {
		fields = append(fields, problems.Field("code", "required", ""))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return in, c, u, false
	}
	if m.throttled(w, r, bucket) {
		return in, c, u, false
	}
	ctx := r.Context()
	c, err := m.loadChallenge(ctx, m.d.DB.Read(), in.MFAToken)
	if errors.Is(err, errChallengeInvalid) {
		problems.Write(w, r, errMFAExpired())
		return in, c, u, false
	}
	if err != nil {
		problems.Write(w, r, err)
		return in, c, u, false
	}
	u, err = loadUser(ctx, m.d.DB.Read(), "u.id = ?", c.UserID)
	if err != nil || u.Status != domain.UserActive || !mfaApplies(u) {
		problems.Write(w, r, errMFAExpired())
		return in, c, u, false
	}
	// AUTH-044: linked to SSO while the challenge was open.
	if sso, err := ssoOnly(ctx, m.d.DB.Read(), u); err != nil || sso {
		if err == nil {
			err = errMFAExpired()
		}
		problems.Write(w, r, err)
		return in, c, u, false
	}
	if m.thr.locked(u.Email, server.ClientIP(r), m.now()) {
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error { _, err := m.consumeChallenge(ctx, tx, c.ID); return err })
		problems.Write(w, r, errMFAExpired())
		return in, c, u, false
	}
	return in, c, u, true
}

// mfaFailed records a wrong code: one attempt on the challenge, one failure
// on the account's login throttle (AUTH-012), an audit row. The answer is a
// field error while attempts remain, then 401 (start again).
func (m *Module) mfaFailed(w http.ResponseWriter, r *http.Request, c mfaChallenge, u userRow, method, reason string) {
	ctx := r.Context()
	exhausted, err := m.failChallenge(ctx, c.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, err)
		return
	}
	m.thr.fail(u.Email, m.now())
	ip := server.ClientIP(r)
	_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_failed", TargetType: "user", TargetID: u.ID, IP: ip,
			Data: map[string]any{"method": method, "reason": reason, "purpose": c.Purpose}})
	})
	if exhausted || errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, errMFAExpired())
		return
	}
	problems.Write(w, r, errMFACode())
}

func (m *Module) emailCodeValid(c mfaChallenge, code string) bool {
	return c.EmailExpiresAt.Valid && m.now().Before(c.EmailExpiresAt.Time) && m.emailCodeOK(c.ID, code, c.EmailCodeHash)
}

// POST /auth/mfa/verify: the second step of a login.
func (m *Module) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, c, u, ok := m.openChallenge(w, r, "mfa", true, MFAMethodTOTP, MFAMethodEmail, MFAMethodRecovery)
	if !ok {
		return
	}
	if c.Purpose != "login" {
		problems.Write(w, r, errMFAExpired())
		return
	}
	st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var (
		ctr    uint64
		passed bool
		reason = "wrong_code"
	)
	switch in.Method {
	case MFAMethodTOTP:
		if !st.totp() {
			problems.Write(w, r, problems.Validation(problems.Field("method", "not_allowed", "TOTP is not enabled for this account")))
			return
		}
		if key, ok := m.openTOTP(st.TOTPSecretEnc); ok {
			ctr, passed = totpMatch(key, in.Code, m.now())
		}
	case MFAMethodEmail:
		if !st.EmailEnabled {
			problems.Write(w, r, problems.Validation(problems.Field("method", "not_allowed", "e-mail codes are not enabled for this account")))
			return
		}
		passed = m.emailCodeValid(c, in.Code)
	case MFAMethodRecovery:
		passed = true // checked by the single-use UPDATE below
	}
	if passed {
		remaining := -1
		err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			now := m.nowDB()
			switch in.Method {
			case MFAMethodTOTP:
				used, err := useTOTPCounter(ctx, tx, u.ID, ctr, now)
				if err != nil {
					return err
				}
				if !used {
					reason = "replay"
					return errCodeRejected
				}
			case MFAMethodRecovery:
				used, err := useRecoveryCode(ctx, tx, u.ID, in.Code, now)
				if err != nil {
					return err
				}
				if !used {
					return errCodeRejected
				}
				if remaining, err = recoveryRemaining(ctx, tx, u.ID); err != nil {
					return err
				}
			}
			won, err := m.consumeChallenge(ctx, tx, c.ID)
			if err != nil {
				return err
			}
			if !won {
				return errChallengeInvalid
			}
			if in.Method == MFAMethodRecovery {
				return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_recovery_used", TargetType: "user", TargetID: u.ID,
					IP: server.ClientIP(r), Data: map[string]any{"remaining": remaining}})
			}
			return nil
		})
		switch {
		case errors.Is(err, errCodeRejected):
			passed = false
		case errors.Is(err, errChallengeInvalid):
			problems.Write(w, r, errMFAExpired())
			return
		case err != nil:
			problems.Write(w, r, err)
			return
		}
	}
	if !passed {
		m.mfaFailed(w, r, c, u, in.Method, reason)
		return
	}
	m.thr.success(u.Email, server.ClientIP(r))
	m.startSession(w, r, u, http.StatusOK, sessionExtra{MFA: in.Method}, c.LoginMethod)
}

// POST /auth/mfa/email/send: mails a 6-digit code for a login challenge
// (e-mail method enabled) or for a forced enrolment.
func (m *Module) handleMFAEmailSend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, c, u, ok := m.openChallenge(w, r, "mfa_email", false)
	if !ok {
		return
	}
	if c.Purpose == "login" {
		st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		if !st.EmailEnabled {
			problems.Write(w, r, problems.Conflict("e-mail codes are not enabled for this account"))
			return
		}
	}
	if !m.requireEmail(w, r) {
		return
	}
	now := m.nowDB()
	if c.EmailSends >= mfaEmailMaxSends {
		problems.Write(w, r, problems.RateLimited(c.ExpiresAt.Sub(now)))
		return
	}
	if c.EmailSentAt.Valid {
		if d := now.Sub(c.EmailSentAt.Time); d < mfaEmailResend {
			problems.Write(w, r, problems.RateLimited(mfaEmailResend-d))
			return
		}
	}
	code := newEmailCode()
	codeExp := now.Add(mfaEmailCodeTTL)
	exp := c.ExpiresAt
	if codeExp.After(exp) {
		exp = codeExp // the code lives 10 minutes; the challenge waits for it
	}
	var sent bool
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE mfa_challenges SET email_code_hash = ?, email_expires_at = ?, email_sent_at = ?, email_sends = email_sends + 1, expires_at = ?
			WHERE id = ? AND used_at IS NULL AND email_sends < ?`, m.emailCodeMAC(c.ID, code), codeExp, now, exp, c.ID, mfaEmailMaxSends)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		sent = n == 1
		return err
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if !sent {
		problems.Write(w, r, problems.RateLimited(mfaEmailResend))
		return
	}
	m.sendMail(ctx, MailPayload{Template: TplMFACode, UserID: u.ID, To: u.Email, Locale: u.Locale,
		Vars: map[string]string{"code": code, "expires_minutes": "10", "name": u.Name}})
	writeJSON(w, http.StatusOK, accepted)
}

// POST /auth/mfa/enroll/totp: a new TOTP secret for a forced enrolment.
func (m *Module) handleMFAEnrollTOTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, c, u, ok := m.openChallenge(w, r, "mfa", false)
	if !ok {
		return
	}
	if c.Purpose != "enroll" {
		problems.Write(w, r, errMFAExpired())
		return
	}
	secret := newTOTPSecret()
	sealed, err := m.sealTOTP(secret)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE mfa_challenges SET totp_pending_enc = ? WHERE id = ? AND used_at IS NULL`, sealed, c.ID)
		return err
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_uri": otpauthURI(m.totpIssuer(ctx), u.Email, secret)})
}

// POST /auth/mfa/enroll/confirm: completes a forced enrolment with a TOTP or
// e-mail code; issues the recovery codes and starts the session.
func (m *Module) handleMFAEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, c, u, ok := m.openChallenge(w, r, "mfa", true, MFAMethodTOTP, MFAMethodEmail)
	if !ok {
		return
	}
	if c.Purpose != "enroll" {
		problems.Write(w, r, errMFAExpired())
		return
	}
	var (
		ctr    uint64
		passed bool
	)
	switch in.Method {
	case MFAMethodTOTP:
		if c.PendingEnc == "" {
			problems.Write(w, r, problems.Validation(problems.Field("method", "not_allowed", "request a TOTP secret first")))
			return
		}
		if key, ok := m.openTOTP(c.PendingEnc); ok {
			ctr, passed = totpMatch(key, in.Code, m.now())
		}
	case MFAMethodEmail:
		passed = m.emailCodeValid(c, in.Code)
	}
	if !passed {
		m.mfaFailed(w, r, c, u, in.Method, "wrong_code")
		return
	}
	var codes []string
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		won, err := m.consumeChallenge(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if !won {
			return errChallengeInvalid
		}
		if err := ensureMFARow(ctx, tx, u.ID, now); err != nil {
			return err
		}
		if in.Method == MFAMethodTOTP {
			_, err = tx.ExecContext(ctx, `UPDATE user_mfa SET totp_secret_enc = ?, totp_enabled_at = ?, totp_last_counter = ?, totp_pending_enc = '', totp_pending_at = NULL, updated_at = ? WHERE user_id = ?`,
				c.PendingEnc, now, int64(ctr), now, u.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE user_mfa SET email_enabled = ?, updated_at = ? WHERE user_id = ?`, true, now, u.ID)
		}
		if err != nil {
			return err
		}
		if codes, err = replaceRecoveryCodes(ctx, tx, u.ID, now); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_enabled", TargetType: "user", TargetID: u.ID, IP: server.ClientIP(r),
			Data: map[string]any{"method": in.Method, "forced": true}})
	})
	if errors.Is(err, errChallengeInvalid) {
		problems.Write(w, r, errMFAExpired())
		return
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.thr.success(u.Email, server.ClientIP(r))
	me, err := m.openSession(w, r, u, sessionExtra{MFA: in.Method}, c.LoginMethod)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"me": me, "recovery_codes": codes})
}

// ---- self-service (/me/mfa, session only, re-auth for every change) ----

// mfaSelf loads the caller and refuses accounts MFA does not apply to.
func (m *Module) mfaSelf(w http.ResponseWriter, r *http.Request, mutate bool) (userRow, mfaState, bool) {
	ctx := r.Context()
	p := principal(r)
	u, err := loadUser(ctx, m.d.DB.Read(), "u.id = ?", p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return u, mfaState{}, false
	}
	if mutate {
		if !mfaApplies(u) || !u.hasPassword() {
			problems.Write(w, r, problems.Forbidden("two-factor authentication is managed by your identity provider"))
			return u, mfaState{}, false
		}
		if _, ok := m.reauth.until(p.SessionID, m.now()); !ok {
			problems.Write(w, r, errReauth())
			return u, mfaState{}, false
		}
	}
	st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		problems.Write(w, r, err)
		return u, st, false
	}
	return u, st, true
}

// mfaStatusJSON renders `MfaStatus` for u (never a secret or a code).
func (m *Module) mfaStatusJSON(ctx context.Context, u userRow, idHash string) (map[string]any, error) {
	st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		return nil, err
	}
	policy, err := m.mfaPolicy(ctx)
	if err != nil {
		return nil, err
	}
	mailOn, err := m.emailEnabled(ctx)
	if err != nil {
		return nil, err
	}
	n, err := recoveryRemaining(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"available": mfaApplies(u) && u.hasPassword(), "enabled": st.enabled(), "required": mfaRequiredFor(policy, u),
		"totp_enabled": st.totp(), "totp_enabled_at": nil, "email_enabled": st.EmailEnabled, "email_available": mailOn,
		"recovery_codes_remaining": n, "reauth_expires_at": nil,
	}
	if st.TOTPEnabledAt.Valid {
		out["totp_enabled_at"] = st.TOTPEnabledAt.Time.UTC()
	}
	if t, ok := m.reauth.until(idHash, m.now()); ok {
		out["reauth_expires_at"] = t.UTC()
	}
	return out, nil
}

func (m *Module) writeMFAStatus(w http.ResponseWriter, r *http.Request, u userRow) {
	out, err := m.mfaStatusJSON(r.Context(), u, principal(r).SessionID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /me/mfa
func (m *Module) handleMFAStatus(w http.ResponseWriter, r *http.Request) {
	u, _, ok := m.mfaSelf(w, r, false)
	if !ok {
		return
	}
	m.writeMFAStatus(w, r, u)
}

// POST /me/mfa/reauth {password}: opens a 10-minute window for MFA changes
// on this session. Wrong passwords feed the login throttle.
func (m *Module) handleMFAReauth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	var in struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.Password == "" || len(in.Password) > 1024 {
		problems.Write(w, r, problems.Validation(problems.Field("password", "required", "")))
		return
	}
	u, _, ok := m.mfaSelf(w, r, false)
	if !ok {
		return
	}
	if !mfaApplies(u) || !u.hasPassword() {
		problems.Write(w, r, problems.Forbidden("two-factor authentication is managed by your identity provider"))
		return
	}
	if m.throttled(w, r, "reauth") {
		return
	}
	ip := server.ClientIP(r)
	good, _ := VerifyPassword(ctx, in.Password, u.Password.String)
	if !good || m.thr.locked(u.Email, ip, m.now()) {
		m.thr.fail(u.Email, m.now())
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.reauth_failed", TargetType: "user", TargetID: u.ID, IP: ip})
		})
		problems.Write(w, r, problems.Validation(problems.Field("password", "invalid", "the password is wrong")))
		return
	}
	m.reauth.mark(p.SessionID, m.now().Add(mfaReauthTTL))
	m.writeMFAStatus(w, r, u)
}

// POST /me/mfa/totp/setup: a new secret, pending until confirmed.
func (m *Module) handleMFATOTPSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, _, ok := m.mfaSelf(w, r, true)
	if !ok {
		return
	}
	secret := newTOTPSecret()
	sealed, err := m.sealTOTP(secret)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		if err := ensureMFARow(ctx, tx, u.ID, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE user_mfa SET totp_pending_enc = ?, totp_pending_at = ?, updated_at = ? WHERE user_id = ?`, sealed, now, now, u.ID)
		return err
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "otpauth_uri": otpauthURI(m.totpIssuer(ctx), u.Email, secret)})
}

// POST /me/mfa/totp/confirm {code}: activates the pending secret. The first
// factor enabled on an account also issues the recovery codes.
func (m *Module) handleMFATOTPConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Code = normCode(in.Code)
	if in.Code == "" || len(in.Code) > 64 {
		problems.Write(w, r, problems.Validation(problems.Field("code", "required", "")))
		return
	}
	u, st, ok := m.mfaSelf(w, r, true)
	if !ok {
		return
	}
	if st.PendingEnc == "" || !st.PendingAt.Valid || m.now().Sub(st.PendingAt.Time) > mfaPendingTTL {
		problems.Write(w, r, problems.Conflict("no TOTP setup in progress; start again"))
		return
	}
	if m.throttled(w, r, "mfa") {
		return
	}
	key, ok := m.openTOTP(st.PendingEnc)
	var ctr uint64
	if ok {
		ctr, ok = totpMatch(key, in.Code, m.now())
	}
	if !ok {
		problems.Write(w, r, errMFACode())
		return
	}
	m.enableMethod(w, r, u, st, MFAMethodTOTP, func(ctx context.Context, tx *store.Tx, now time.Time) error {
		_, err := tx.ExecContext(ctx, `UPDATE user_mfa SET totp_secret_enc = totp_pending_enc, totp_enabled_at = ?, totp_last_counter = ?, totp_pending_enc = '', totp_pending_at = NULL, updated_at = ?
			WHERE user_id = ? AND totp_pending_enc = ?`, now, int64(ctr), now, u.ID, st.PendingEnc)
		return err
	})
}

// POST /me/mfa/email: enables e-mail codes (needs working SMTP).
func (m *Module) handleMFAEmailEnable(w http.ResponseWriter, r *http.Request) {
	u, st, ok := m.mfaSelf(w, r, true)
	if !ok || !m.requireEmail(w, r) {
		return
	}
	m.enableMethod(w, r, u, st, MFAMethodEmail, func(ctx context.Context, tx *store.Tx, now time.Time) error {
		_, err := tx.ExecContext(ctx, `UPDATE user_mfa SET email_enabled = ?, updated_at = ? WHERE user_id = ?`, true, now, u.ID)
		return err
	})
}

// enableMethod runs set in a transaction, issues recovery codes when this is
// the account's first factor (or none are left), audits and answers
// `MfaEnrollResult`.
func (m *Module) enableMethod(w http.ResponseWriter, r *http.Request, u userRow, before mfaState, method string, set func(context.Context, *store.Tx, time.Time) error) {
	ctx := r.Context()
	codes := []string{}
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		if err := ensureMFARow(ctx, tx, u.ID, now); err != nil {
			return err
		}
		if err := set(ctx, tx, now); err != nil {
			return err
		}
		n, err := recoveryRemaining(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if !before.enabled() || n == 0 {
			if codes, err = replaceRecoveryCodes(ctx, tx, u.ID, now); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_enabled", TargetType: "user", TargetID: u.ID, IP: server.ClientIP(r),
			Data: map[string]any{"method": method}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	st, err := m.mfaStatusJSON(ctx, u, principal(r).SessionID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "recovery_codes": codes})
}

// disableMethod turns one factor off. The policy may forbid removing the
// last one; with no factor left the recovery codes go too.
func (m *Module) disableMethod(w http.ResponseWriter, r *http.Request, method string) {
	ctx := r.Context()
	u, st, ok := m.mfaSelf(w, r, true)
	if !ok {
		return
	}
	after := st
	if method == MFAMethodTOTP {
		after.TOTPSecretEnc = ""
	} else {
		after.EmailEnabled = false
	}
	if st.enabled() && !after.enabled() {
		policy, err := m.mfaPolicy(ctx)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		if mfaRequiredFor(policy, u) {
			problems.Write(w, r, problems.Conflict("the instance policy requires a second factor; enable another method first"))
			return
		}
	}
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		var err error
		if method == MFAMethodTOTP {
			_, err = tx.ExecContext(ctx, `UPDATE user_mfa SET totp_secret_enc = '', totp_enabled_at = NULL, totp_last_counter = NULL, updated_at = ? WHERE user_id = ?`, now, u.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE user_mfa SET email_enabled = ?, updated_at = ? WHERE user_id = ?`, false, now, u.ID)
		}
		if err != nil {
			return err
		}
		if !after.enabled() {
			if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = ?`, u.ID); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_disabled", TargetType: "user", TargetID: u.ID, IP: server.ClientIP(r),
			Data: map[string]any{"method": method}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.writeMFAStatus(w, r, u)
}

// DELETE /me/mfa/totp
func (m *Module) handleMFATOTPDisable(w http.ResponseWriter, r *http.Request) {
	m.disableMethod(w, r, MFAMethodTOTP)
}

// DELETE /me/mfa/email
func (m *Module) handleMFAEmailDisable(w http.ResponseWriter, r *http.Request) {
	m.disableMethod(w, r, MFAMethodEmail)
}

// POST /me/mfa/recovery-codes: replaces every recovery code.
func (m *Module) handleMFARecoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, st, ok := m.mfaSelf(w, r, true)
	if !ok {
		return
	}
	if !st.enabled() {
		problems.Write(w, r, problems.Conflict("enable a second factor first"))
		return
	}
	var codes []string
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var err error
		if codes, err = replaceRecoveryCodes(ctx, tx, u.ID, m.nowDB()); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.mfa_recovery_regenerated", TargetType: "user", TargetID: u.ID, IP: server.ClientIP(r)})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// ---- admin ----

// GET /admin/users/{id}/mfa
func (m *Module) handleAdminMFAStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := loadUser(ctx, m.d.DB.Read(), "u.id = ?", chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	st, err := loadMFA(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	n, err := recoveryRemaining(ctx, m.d.DB.Read(), u.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"applies": mfaApplies(u), "enabled": st.enabled(), "totp_enabled": st.totp(), "email_enabled": st.EmailEnabled,
		"recovery_codes_remaining": n,
	})
}

// POST /admin/users/{id}/mfa/reset: removes every factor, recovery code and
// open challenge of the user (lost device). Audited; sessions stay.
func (m *Module) handleAdminMFAReset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	id := chi.URLParam(r, "id")
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		if _, err := loadUser(ctx, tx, "u.id = ?", id); err != nil {
			return err
		}
		return ResetMFA(ctx, tx, id, p.UserID, server.ClientIP(r), "admin")
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	noContent(w)
}

// ResetMFA deletes the second factors of userID inside tx and audits it
// (admin reset and the `reset-mfa` CLI).
func ResetMFA(ctx context.Context, tx *store.Tx, userID, actorID, ip, via string) error {
	var had int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM user_mfa WHERE user_id = ? AND (totp_secret_enc <> '' OR email_enabled)`, userID).Scan(&had); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM user_mfa WHERE user_id = ?`,
		`DELETE FROM mfa_recovery_codes WHERE user_id = ?`,
		`DELETE FROM mfa_challenges WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return Audit(ctx, tx, AuditEntry{ActorID: actorID, Action: "auth.mfa_reset", TargetType: "user", TargetID: userID, IP: ip,
		Data: map[string]any{"via": via, "had_mfa": had > 0}})
}

// GET /admin/security
func (m *Module) handleGetSecurity(w http.ResponseWriter, r *http.Request) {
	policy, err := m.mfaPolicy(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, SecuritySettings{MFARequired: policy})
}

// PATCH /admin/security {mfa_required}
func (m *Module) handlePatchSecurity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		MFARequired *string `json:"mfa_required"`
	}
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.MFARequired != nil && !ValidMFAPolicy(*in.MFARequired) {
		problems.Write(w, r, problems.Validation(problems.Field("mfa_required", "enum", "none, staff or all")))
		return
	}
	policy, err := m.mfaPolicy(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.MFARequired != nil && *in.MFARequired != policy {
		p := principal(r)
		if err := m.d.Settings.Put(ctx, KeySecurity, SecuritySettings{MFARequired: *in.MFARequired}, p.UserID); err != nil {
			problems.Write(w, r, err)
			return
		}
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.mfa_policy_changed", TargetType: "settings", TargetID: KeySecurity,
				IP: server.ClientIP(r), Data: map[string]any{"from": policy, "to": *in.MFARequired}})
		})
		policy = *in.MFARequired
	}
	writeJSON(w, http.StatusOK, SecuritySettings{MFARequired: policy})
}
