// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// totpAt is an independent RFC 6238 generator (the test does not trust the
// implementation under test).
func totpAt(t testing.TB, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	h := hmac.New(sha1.New, key)
	h.Write(msg[:])
	s := h.Sum(nil)
	o := s[19] & 15
	v := (binary.BigEndian.Uint32(s[o:o+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", v)
}

type mfaUser struct {
	authntest.User
	Secret   string
	Recovery []string
	C        *authntest.Client // signed-in client from enrolment
}

func reauth(t testing.TB, c *authntest.Client) {
	t.Helper()
	if r, b := c.Do("POST", "/api/v1/me/mfa/reauth", map[string]any{"password": authntest.TestPassword}); r.StatusCode != 200 {
		t.Fatalf("reauth: %d %s", r.StatusCode, b)
	}
}

// enrolTOTP signs u in, enrols TOTP through /me/mfa and advances the clock
// one step so the next code is not a replay of the confirmation code.
func enrolTOTP(t testing.TB, e *authntest.Env, u authntest.User) mfaUser {
	t.Helper()
	c := e.Login(u)
	reauth(t, c)
	r, b := c.Do("POST", "/api/v1/me/mfa/totp/setup", nil)
	if r.StatusCode != 200 {
		t.Fatalf("setup: %d %s", r.StatusCode, b)
	}
	secret := authntest.JSON(t, b)["secret"].(string)
	r, b = c.Do("POST", "/api/v1/me/mfa/totp/confirm", map[string]any{"code": totpAt(t, secret, e.Clock.Now())})
	if r.StatusCode != 200 {
		t.Fatalf("confirm: %d %s", r.StatusCode, b)
	}
	var res struct {
		Status        map[string]any `json:"status"`
		RecoveryCodes []string       `json:"recovery_codes"`
	}
	_ = json.Unmarshal(b, &res)
	if len(res.RecoveryCodes) != 10 || res.Status["totp_enabled"] != true || res.Status["recovery_codes_remaining"].(float64) != 10 {
		t.Fatalf("confirm result: %s", b)
	}
	e.Clock.Advance(30 * time.Second)
	return mfaUser{User: u, Secret: secret, Recovery: res.RecoveryCodes, C: c}
}

// passwordStep runs POST /auth/login and expects an MfaChallenge.
func passwordStep(t testing.TB, e *authntest.Env, u authntest.User, ip string) (*authntest.Client, map[string]any) {
	t.Helper()
	c := e.Client()
	if ip != "" {
		c.IP = ip
	}
	r, b := login(c, domain.Audience(u.Kind), u.Email, authntest.TestPassword)
	if r.StatusCode != 200 {
		t.Fatalf("login: %d %s", r.StatusCode, b)
	}
	ch := authntest.JSON(t, b)
	if ch["mfa_required"] != true || ch["mfa_token"] == "" {
		t.Fatalf("expected a challenge, got %s", b)
	}
	return c, ch
}

func verify(c *authntest.Client, ch map[string]any, method, code string) (*http.Response, []byte) {
	return c.Do("POST", "/api/v1/auth/mfa/verify", map[string]any{"mfa_token": ch["mfa_token"], "method": method, "code": code})
}

func methodsOf(ch map[string]any) string {
	var out []string
	for _, m := range ch["methods"].([]any) {
		out = append(out, m.(string))
	}
	return strings.Join(out, ",")
}

// TestMFA_PasswordOnlyNoSession: with MFA on, a correct password yields a
// challenge and no session at all (no cookie, /me stays 401).
func TestMFA_PasswordOnlyNoSession(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		sessions := e.Int(`SELECT count(*) FROM sessions WHERE user_id = ?`, mu.ID)
		c, ch := passwordStep(t, e, mu.User, "")
		if c.Cookie != "" {
			t.Fatal("cookie set after the password step")
		}
		if got := methodsOf(ch); got != "totp,recovery" || ch["enrollment_required"] != false {
			t.Fatalf("challenge %v", ch)
		}
		if n := e.Int(`SELECT count(*) FROM sessions WHERE user_id = ?`, mu.ID); n != sessions {
			t.Fatalf("sessions %d → %d", sessions, n)
		}
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("/me without second factor: %d", r.StatusCode)
		}
		if e.Int(`SELECT count(*) FROM mfa_challenges WHERE token_hash = ?`, ch["mfa_token"]) != 0 {
			t.Fatal("challenge token stored in clear")
		}
		r, b := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now()))
		if r.StatusCode != 200 || authntest.JSON(t, b)["id"] != mu.ID || c.Cookie == "" {
			t.Fatalf("verify: %d %s", r.StatusCode, b)
		}
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatalf("/me after verify: %d", r.StatusCode)
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.login_success' AND actor_id = ? AND CAST(data AS TEXT) LIKE '%totp%'`, mu.ID) != 1 {
			t.Fatal("login_success audit lacks the second factor")
		}
	})
}

// TestMFA_TOTPReplayRejected: a code accepted once is refused on the next
// login, also within its window; the next step's code works.
func TestMFA_TOTPReplayRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		code := totpAt(t, mu.Secret, e.Clock.Now())
		c, ch := passwordStep(t, e, mu.User, "")
		if r, b := verify(c, ch, "totp", code); r.StatusCode != 200 {
			t.Fatalf("first use: %d %s", r.StatusCode, b)
		}
		c, ch = passwordStep(t, e, mu.User, "")
		r, b := verify(c, ch, "totp", code)
		if r.StatusCode != 422 || !strings.Contains(string(b), `"code"`) || c.Cookie != "" {
			t.Fatalf("replay: %d %s", r.StatusCode, b)
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_failed' AND CAST(data AS TEXT) LIKE '%replay%'`) != 1 {
			t.Fatal("replay not audited")
		}
		// The previous step (−1) is older than the last accepted one: refused.
		if r, _ := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now().Add(-30*time.Second))); r.StatusCode != 422 {
			t.Fatalf("older step accepted: %d", r.StatusCode)
		}
		// +1 step is inside the window and newer: accepted.
		if r, b := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now().Add(30*time.Second))); r.StatusCode != 200 {
			t.Fatalf("+1 step: %d %s", r.StatusCode, b)
		}
	})
}

// TestMFA_WindowPlusMinusOne: −1 step accepted (clock skew), −2 refused.
func TestMFA_WindowPlusMinusOne(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		e.Clock.Advance(5 * time.Minute)
		c, ch := passwordStep(t, e, mu.User, "")
		if r, _ := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now().Add(-60*time.Second))); r.StatusCode != 422 {
			t.Fatalf("−2 steps accepted: %d", r.StatusCode)
		}
		if r, b := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now().Add(-30*time.Second))); r.StatusCode != 200 {
			t.Fatalf("−1 step: %d %s", r.StatusCode, b)
		}
	})
}

// TestMFA_ChallengeExpiry: five minutes, then 401 and a fresh login.
func TestMFA_ChallengeExpiry(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		c, ch := passwordStep(t, e, mu.User, "")
		e.Clock.Advance(5*time.Minute + time.Second)
		r, b := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now()))
		if r.StatusCode != 401 || c.Cookie != "" {
			t.Fatalf("expired challenge: %d %s", r.StatusCode, b)
		}
		// A used challenge is dead too.
		c, ch = passwordStep(t, e, mu.User, "")
		if r, _ := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 {
			t.Fatal("fresh challenge")
		}
		if r, _ := verify(e.Client(), ch, "recovery", mu.Recovery[0]); r.StatusCode != 401 {
			t.Fatalf("used challenge: %d", r.StatusCode)
		}
		// A new login retires the previous open challenge.
		_, ch1 := passwordStep(t, e, mu.User, "")
		_, _ = passwordStep(t, e, mu.User, "")
		if r, _ := verify(e.Client(), ch1, "recovery", mu.Recovery[0]); r.StatusCode != 401 {
			t.Fatalf("superseded challenge: %d", r.StatusCode)
		}
	})
}

// TestMFA_AttemptLimitFeedsThrottle: 5 wrong codes kill a challenge; every
// wrong code counts on the account's login throttle, so 10 lock the
// account against IPs that never completed a login.
func TestMFA_AttemptLimitFeedsThrottle(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Throttle: true})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		good := totpAt(t, mu.Secret, e.Clock.Now())
		for round, ip := range []string{"198.51.100.1", "198.51.100.2"} {
			c, ch := passwordStep(t, e, mu.User, ip)
			for i := 1; i <= 5; i++ {
				r, b := verify(c, ch, "totp", "000000")
				want := 422
				if i == 5 {
					want = 401
				}
				if r.StatusCode != want {
					t.Fatalf("round %d attempt %d: %d %s", round, i, r.StatusCode, b)
				}
			}
			if r, _ := verify(c, ch, "totp", good); r.StatusCode != 401 {
				t.Fatalf("exhausted challenge accepted a good code: %d", r.StatusCode)
			}
		}
		// 10 failures → the password step is refused from a new IP.
		c := e.Client()
		c.IP = "198.51.100.3"
		if r, _ := login(c, domain.AudienceCustomer, mu.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatalf("locked account still challenged: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_failed' AND actor_id = ?`, mu.ID); n != 10 {
			t.Fatalf("mfa_failed audits: %d", n)
		}
	})
}

// TestMFA_EmailCodeSingleUse: the mailed code works once, only for its
// challenge; resends are rate-limited.
func TestMFA_EmailCodeSingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		if r, _ := c.Do("POST", "/api/v1/me/mfa/email", nil); r.StatusCode != 403 {
			t.Fatalf("enable without re-auth: %d", r.StatusCode)
		}
		reauth(t, c)
		r, b := c.Do("POST", "/api/v1/me/mfa/email", nil)
		if r.StatusCode != 200 || len(authntest.JSON(t, b)["recovery_codes"].([]any)) != 10 {
			t.Fatalf("enable email: %d %s", r.StatusCode, b)
		}
		lc, ch := passwordStep(t, e, u, "")
		if methodsOf(ch) != "email,recovery" {
			t.Fatalf("methods %v", ch["methods"])
		}
		if r, b := verify(lc, ch, "email", "123456"); r.StatusCode != 422 {
			t.Fatalf("code before send: %d %s", r.StatusCode, b)
		}
		send := func(ch map[string]any) (*http.Response, []byte) {
			return lc.Do("POST", "/api/v1/auth/mfa/email/send", map[string]any{"mfa_token": ch["mfa_token"]})
		}
		if r, b := send(ch); r.StatusCode != 200 {
			t.Fatalf("send: %d %s", r.StatusCode, b)
		}
		if r, _ := send(ch); r.StatusCode != 429 {
			t.Fatalf("immediate resend: %d", r.StatusCode)
		}
		m := e.LastMail(u.Email)
		code := m.Vars["code"]
		if m.Template != authn.TplMFACode || len(code) != 6 || m.Vars["expires_minutes"] != "10" {
			t.Fatalf("mail %+v", m)
		}
		if e.Int(`SELECT count(*) FROM mfa_challenges WHERE email_code_hash LIKE ?`, "%"+code+"%") != 0 {
			t.Fatal("e-mail code stored in clear")
		}
		if r, b := verify(lc, ch, "email", code); r.StatusCode != 200 {
			t.Fatalf("verify email: %d %s", r.StatusCode, b)
		}
		lc2, ch2 := passwordStep(t, e, u, "")
		if r, _ := verify(lc2, ch2, "email", code); r.StatusCode != 422 {
			t.Fatalf("code reused on another challenge: %d", r.StatusCode)
		}
		// Codes expire after 10 minutes even though the challenge waits for them.
		lc3, ch3 := passwordStep(t, e, u, "")
		lc = lc3
		if r, _ := send(ch3); r.StatusCode != 200 {
			t.Fatal("send 3")
		}
		code = e.LastMail(u.Email).Vars["code"]
		e.Clock.Advance(10*time.Minute + time.Second)
		if r, _ := verify(lc3, ch3, "email", code); r.StatusCode != 401 {
			t.Fatalf("expired e-mail code: %d", r.StatusCode)
		}
	})
}

// TestMFA_EmailRefusedWhenSMTPOff: email-disabled for enabling the method
// and for sending a code; the method is not offered.
func TestMFA_EmailRefusedWhenSMTPOff(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		ac := e.Login(admin)
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		reauth(t, c)
		r, b := c.Do("POST", "/api/v1/me/mfa/email", nil)
		if r.StatusCode != 409 || !strings.Contains(string(b), "email-disabled") {
			t.Fatalf("enable without SMTP: %d %s", r.StatusCode, b)
		}
		if r, b := c.Do("GET", "/api/v1/me/mfa", nil); r.StatusCode != 200 || authntest.JSON(t, b)["email_available"] != false {
			t.Fatalf("status: %s", b)
		}
		if r, _ := ac.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "all"}); r.StatusCode != 200 {
			t.Fatal("policy")
		}
		lc, ch := passwordStep(t, e, u, "")
		if methodsOf(ch) != "totp" || ch["enrollment_required"] != true {
			t.Fatalf("enrol challenge %v", ch)
		}
		r, b = lc.Do("POST", "/api/v1/auth/mfa/email/send", map[string]any{"mfa_token": ch["mfa_token"]})
		if r.StatusCode != 409 || !strings.Contains(string(b), "email-disabled") {
			t.Fatalf("send without SMTP: %d %s", r.StatusCode, b)
		}
		if len(e.Mails()) != 0 {
			t.Fatal("mail queued without SMTP")
		}
	})
}

// TestMFA_RecoveryCodeSingleUse
func TestMFA_RecoveryCodeSingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		code := strings.ToUpper(mu.Recovery[3]) // case and dashes do not matter
		c, ch := passwordStep(t, e, mu.User, "")
		if r, b := verify(c, ch, "recovery", code); r.StatusCode != 200 {
			t.Fatalf("recovery: %d %s", r.StatusCode, b)
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_recovery_used' AND actor_id = ?`, mu.ID) != 1 {
			t.Fatal("recovery use not audited")
		}
		c, ch = passwordStep(t, e, mu.User, "")
		if r, _ := verify(c, ch, "recovery", mu.Recovery[3]); r.StatusCode != 422 {
			t.Fatalf("recovery code reused: %d", r.StatusCode)
		}
		if r, _ := c.Do("GET", "/api/v1/me/mfa", nil); r.StatusCode != 401 {
			t.Fatal("not signed in")
		}
		r, b := mu.C.Do("GET", "/api/v1/me/mfa", nil)
		if r.StatusCode != 200 || authntest.JSON(t, b)["recovery_codes_remaining"].(float64) != 9 {
			t.Fatalf("remaining: %s", b)
		}
		// Regenerating replaces every code.
		reauth(t, mu.C)
		r, b = mu.C.Do("POST", "/api/v1/me/mfa/recovery-codes", nil)
		if r.StatusCode != 200 {
			t.Fatalf("regenerate: %d %s", r.StatusCode, b)
		}
		if r, _ := verify(c, ch, "recovery", mu.Recovery[4]); r.StatusCode != 422 {
			t.Fatalf("old code after regenerate: %d", r.StatusCode)
		}
		fresh := authntest.JSON(t, b)["recovery_codes"].([]any)
		if r, _ := verify(c, ch, "recovery", fresh[0].(string)); r.StatusCode != 200 {
			t.Fatalf("new code: %d", r.StatusCode)
		}
	})
}

// TestMFA_ForcedEnrolmentUnderPolicy: policy staff covers staff only; all
// covers customers; enrolment needs the challenge, then yields the session
// and the recovery codes once.
func TestMFA_ForcedEnrolmentUnderPolicy(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		ac := e.Login(admin)
		if r, b := ac.Do("GET", "/api/v1/admin/security", nil); r.StatusCode != 200 || authntest.JSON(t, b)["mfa_required"] != "none" {
			t.Fatalf("default policy: %s", b)
		}
		if r, _ := ac.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "sometimes"}); r.StatusCode != 422 {
			t.Fatal("bad policy accepted")
		}
		if r, _ := ac.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "staff"}); r.StatusCode != 200 {
			t.Fatal("policy staff")
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_policy_changed'`) != 1 {
			t.Fatal("policy change not audited")
		}
		cust := e.Customer("", domain.RoleRequester)
		if r, b := login(e.Client(), domain.AudienceCustomer, cust.Email, authntest.TestPassword); r.StatusCode != 200 || authntest.JSON(t, b)["mfa_required"] != nil {
			t.Fatalf("customer challenged under staff policy: %s", b)
		}
		agent := e.StaffUser(domain.RoleAgent, 1, false)
		c, ch := passwordStep(t, e, agent, "")
		if ch["enrollment_required"] != true {
			t.Fatalf("challenge %v", ch)
		}
		// Not a login challenge: verify refuses it.
		if r, _ := verify(c, ch, "totp", "123456"); r.StatusCode != 401 {
			t.Fatalf("verify on enrol challenge: %d", r.StatusCode)
		}
		c, ch = passwordStep(t, e, agent, "")
		tok := ch["mfa_token"]
		r, b := c.Do("POST", "/api/v1/auth/mfa/enroll/totp", map[string]any{"mfa_token": tok})
		if r.StatusCode != 200 {
			t.Fatalf("enroll totp: %d %s", r.StatusCode, b)
		}
		setup := authntest.JSON(t, b)
		secret := setup["secret"].(string)
		if !strings.HasPrefix(setup["otpauth_uri"].(string), "otpauth://totp/") || c.Cookie != "" {
			t.Fatalf("setup %v", setup)
		}
		if r, _ := c.Do("POST", "/api/v1/auth/mfa/enroll/confirm", map[string]any{"mfa_token": tok, "method": "totp", "code": "000000"}); r.StatusCode != 422 {
			t.Fatalf("wrong enrol code: %d", r.StatusCode)
		}
		r, b = c.Do("POST", "/api/v1/auth/mfa/enroll/confirm", map[string]any{"mfa_token": tok, "method": "totp", "code": totpAt(t, secret, e.Clock.Now())})
		if r.StatusCode != 200 || c.Cookie == "" {
			t.Fatalf("enroll confirm: %d %s", r.StatusCode, b)
		}
		var res struct {
			Me            map[string]any `json:"me"`
			RecoveryCodes []string       `json:"recovery_codes"`
		}
		_ = json.Unmarshal(b, &res)
		if res.Me["id"] != agent.ID || len(res.RecoveryCodes) != 10 {
			t.Fatalf("result %s", b)
		}
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("no session after enrolment")
		}
		// Next login is an ordinary MFA login; the policy forbids removing the last factor.
		e.Clock.Advance(30 * time.Second)
		c2, ch2 := passwordStep(t, e, agent, "")
		if ch2["enrollment_required"] != false {
			t.Fatal("still enrolling")
		}
		if r, _ := verify(c2, ch2, "totp", totpAt(t, secret, e.Clock.Now())); r.StatusCode != 200 {
			t.Fatal("login after enrolment")
		}
		reauth(t, c2)
		if r, b := c2.Do("DELETE", "/api/v1/me/mfa/totp", nil); r.StatusCode != 409 {
			t.Fatalf("last factor removed under policy: %d %s", r.StatusCode, b)
		}
		if r, b := c2.Do("GET", "/api/v1/me/mfa", nil); authntest.JSON(t, b)["required"] != true || r.StatusCode != 200 {
			t.Fatalf("status %s", b)
		}
		// Policy all now covers the customer.
		if r, _ := ac.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "all"}); r.StatusCode != 200 {
			t.Fatal("policy all")
		}
		if _, ch := passwordStep(t, e, cust, ""); ch["enrollment_required"] != true {
			t.Fatal("customer not forced under policy all")
		}
	})
}

// TestMFA_GateCoversEmailLogins: magic link and invite acceptance never
// bypass a second factor.
func TestMFA_GateCoversEmailLogins(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		c := e.Client()
		if r, _ := c.Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": mu.Email}); r.StatusCode != 200 {
			t.Fatal("magic link request")
		}
		tok := authntest.TokenFromURL(e.LastMail(mu.Email).Vars["url"])
		r, b := c.Do("POST", "/api/v1/auth/magic-link", map[string]any{"token": tok})
		if r.StatusCode != 200 || authntest.JSON(t, b)["mfa_required"] != true || c.Cookie != "" {
			t.Fatalf("magic link with MFA: %d %s", r.StatusCode, b)
		}
		ch := authntest.JSON(t, b)
		if r, _ := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 {
			t.Fatal("verify after magic link")
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.login_success' AND actor_id = ? AND CAST(data AS TEXT) LIKE '%magic_link%'`, mu.ID) != 1 {
			t.Fatal("first factor not kept in the audit")
		}
	})
}

// TestMFA_BreakGlassAuditKept: the break-glass audit still fires after MFA.
func TestMFA_BreakGlassAuditKept(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.StaffUser(domain.RoleAdmin, 3, true))
		before := e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.break_glass_login'`)
		c, ch := passwordStep(t, e, mu.User, "")
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.break_glass_login'`) != before {
			t.Fatal("break-glass login audited before the second factor")
		}
		if r, _ := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 {
			t.Fatal("verify")
		}
		if e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.break_glass_login'`) != before+1 {
			t.Fatal("break-glass login not audited")
		}
	})
}

// TestMFA_AdminResetAudited
func TestMFA_AdminResetAudited(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		ac := e.Login(admin)
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		sup := e.Login(e.StaffUser(domain.RoleSupervisor, 3, false))
		if r, _ := sup.Do("POST", "/api/v1/admin/users/"+mu.ID+"/mfa/reset", nil); r.StatusCode != 403 && r.StatusCode != 404 {
			t.Fatalf("supervisor reset: %d", r.StatusCode)
		}
		if r, _ := mu.C.Do("POST", "/api/v1/admin/users/"+mu.ID+"/mfa/reset", nil); r.StatusCode != 404 {
			t.Fatalf("customer reset: %d", r.StatusCode)
		}
		r, b := ac.Do("GET", "/api/v1/admin/users/"+mu.ID+"/mfa", nil)
		if r.StatusCode != 200 || authntest.JSON(t, b)["totp_enabled"] != true {
			t.Fatalf("admin status: %s", b)
		}
		if r, _ := ac.Do("POST", "/api/v1/admin/users/"+store.NewID()+"/mfa/reset", nil); r.StatusCode != 404 {
			t.Fatalf("unknown user: %d", r.StatusCode)
		}
		if r, b := ac.Do("POST", "/api/v1/admin/users/"+mu.ID+"/mfa/reset", nil); r.StatusCode != 204 {
			t.Fatalf("reset: %d %s", r.StatusCode, b)
		}
		if n := e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_reset' AND actor_id = ? AND target_id = ?`, admin.ID, mu.ID); n != 1 {
			t.Fatalf("reset audits: %d", n)
		}
		if e.Int(`SELECT count(*) FROM user_mfa WHERE user_id = ?`, mu.ID)+e.Int(`SELECT count(*) FROM mfa_recovery_codes WHERE user_id = ?`, mu.ID) != 0 {
			t.Fatal("factors left after reset")
		}
		if r, b := login(e.Client(), domain.AudienceCustomer, mu.Email, authntest.TestPassword); r.StatusCode != 200 || authntest.JSON(t, b)["mfa_required"] != nil {
			t.Fatalf("login after reset: %s", b)
		}
	})
}

// TestMFA_ReauthRequired: every change needs the password on this session
// within 10 minutes; wrong passwords are refused.
func TestMFA_ReauthRequired(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		for _, op := range [][2]string{{"POST", "/me/mfa/totp/setup"}, {"DELETE", "/me/mfa/totp"}, {"DELETE", "/me/mfa/email"}, {"POST", "/me/mfa/recovery-codes"}} {
			if r, _ := c.Do(op[0], "/api/v1"+op[1], nil); r.StatusCode != 403 {
				t.Fatalf("%s %s without re-auth: %d", op[0], op[1], r.StatusCode)
			}
		}
		if r, _ := c.Do("POST", "/api/v1/me/mfa/totp/confirm", map[string]any{"code": "123456"}); r.StatusCode != 403 {
			t.Fatalf("confirm without re-auth: %d", r.StatusCode)
		}
		if r, _ := c.Do("POST", "/api/v1/me/mfa/reauth", map[string]any{"password": "wrong-password-123"}); r.StatusCode != 422 {
			t.Fatalf("wrong password: %d", r.StatusCode)
		}
		reauth(t, c)
		r, b := c.Do("GET", "/api/v1/me/mfa", nil)
		if r.StatusCode != 200 || authntest.JSON(t, b)["reauth_expires_at"] == nil {
			t.Fatalf("status %s", b)
		}
		if r, _ := c.Do("POST", "/api/v1/me/mfa/totp/setup", nil); r.StatusCode != 200 {
			t.Fatal("setup after re-auth")
		}
		// The window is per session.
		other := e.Login(u)
		if r, _ := other.Do("POST", "/api/v1/me/mfa/totp/setup", nil); r.StatusCode != 403 {
			t.Fatalf("re-auth leaked to another session: %d", r.StatusCode)
		}
		e.Clock.Advance(10*time.Minute + time.Second)
		if r, _ := c.Do("POST", "/api/v1/me/mfa/totp/setup", nil); r.StatusCode != 403 {
			t.Fatalf("expired re-auth: %d", r.StatusCode)
		}
		if r, _ := c.Do("POST", "/api/v1/me/mfa/recovery-codes", nil); r.StatusCode != 403 {
			t.Fatal("expired re-auth (codes)")
		}
	})
}

// TestMFA_SSOAndPATUnaffected: SSO-managed accounts never reach the local
// challenge (their password login is refused outright, AUTH-044), are never
// forced and cannot manage MFA here; PATs keep working and cannot touch MFA.
func TestMFA_SSOAndPATUnaffected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		ac := e.Login(admin)
		sso := e.Customer("", domain.RoleRequester)
		pc := e.Login(sso)
		pat := pc.PAT("tickets:read", "tickets:write")
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		mpat := mu.C.PAT("tickets:read", "tickets:write")
		e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, sso.ID)
		if r, _ := ac.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "all"}); r.StatusCode != 200 {
			t.Fatal("policy")
		}
		if r, b := login(e.Client(), domain.AudienceCustomer, sso.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatalf("SSO-managed account signed in with a local password: %d %s", r.StatusCode, b)
		}
		r, b := pc.Do("GET", "/api/v1/me/mfa", nil)
		if st := authntest.JSON(t, b); r.StatusCode != 200 || st["available"] != false || st["required"] != false {
			t.Fatalf("SSO status %s", b)
		}
		if r, _ := pc.Do("POST", "/api/v1/me/mfa/reauth", map[string]any{"password": authntest.TestPassword}); r.StatusCode != 403 {
			t.Fatalf("SSO re-auth: %d", r.StatusCode)
		}
		// PATs: unaffected by MFA (and by the policy), but cannot read or change it.
		for _, p := range []*authntest.Client{pat, mpat} {
			if r, _ := p.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
				t.Fatalf("PAT /me: %d", r.StatusCode)
			}
			if r, _ := p.Do("GET", "/api/v1/me/mfa", nil); r.StatusCode != 403 {
				t.Fatalf("PAT reads MFA: %d", r.StatusCode)
			}
			if r, _ := p.Do("DELETE", "/api/v1/me/mfa/totp", nil); r.StatusCode != 403 {
				t.Fatalf("PAT disables MFA: %d", r.StatusCode)
			}
		}
	})
}

// TestMFA_NoSecretInResponsesOrLogs: after setup, no response, audit row or
// log line carries the TOTP secret, a recovery code or an e-mail code; the
// stored secret is sealed.
func TestMFA_NoSecretInResponsesOrLogs(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		reauth(t, mu.C)
		if r, _ := mu.C.Do("POST", "/api/v1/me/mfa/email", nil); r.StatusCode != 200 {
			t.Fatal("email")
		}
		var bodies bytes.Buffer
		rec := func(_ *http.Response, b []byte) { bodies.Write(b) }
		rec(mu.C.Do("GET", "/api/v1/me/mfa", nil))
		rec(mu.C.Do("GET", "/api/v1/me", nil))
		c, ch := passwordStep(t, e, mu.User, "")
		fmt.Fprint(&bodies, ch)
		rec(c.Do("POST", "/api/v1/auth/mfa/email/send", map[string]any{"mfa_token": ch["mfa_token"]}))
		code := e.LastMail(mu.Email).Vars["code"]
		rec(verify(c, ch, "email", "999999"))
		rec(verify(c, ch, "email", code))
		rec(c.Do("GET", "/api/v1/me/mfa", nil))
		ac := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		rec(ac.Do("GET", "/api/v1/admin/users/"+mu.ID+"/mfa", nil))
		rec(ac.Do("GET", "/api/v1/admin/audit-log", nil))
		secrets := append([]string{mu.Secret, code}, mu.Recovery...)
		var audit bytes.Buffer
		rows, err := e.DB.Read().QueryContext(context.Background(), `SELECT action, data FROM audit_log`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var a, d string
			_ = rows.Scan(&a, &d)
			audit.WriteString(a + d + "\n")
		}
		rows.Close()
		stored := e.Str(`SELECT totp_secret_enc FROM user_mfa WHERE user_id = ?`, mu.ID)
		if !crypto.IsSealed(stored) {
			t.Fatalf("secret not sealed: %q", stored)
		}
		for _, s := range secrets {
			for name, hay := range map[string]string{"responses": bodies.String(), "audit": audit.String(), "logs": e.Logs.String(), "db": stored} {
				if strings.Contains(hay, s) || strings.Contains(strings.ToUpper(hay), strings.ToUpper(s)) {
					t.Errorf("%s contains a secret or code (%q…)", name, s[:3])
				}
			}
		}
		if e.Int(`SELECT count(*) FROM mfa_recovery_codes WHERE code_hash IN (?, ?)`, mu.Recovery[0], strings.ReplaceAll(mu.Recovery[0], "-", "")) != 0 {
			t.Fatal("recovery code stored in clear")
		}
	})
}

// TestRotateSecrets_ResealsMFA: rotate-secrets re-encrypts TOTP secrets, so
// dropping FD_SECRET_KEY_PREVIOUS afterwards keeps authenticators working.
func TestRotateSecrets_ResealsMFA(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
		oldKey := []byte(strings.Repeat("s", 32))
		newKey := []byte(strings.Repeat("n", 32))
		box, err := crypto.NewBox(newKey, oldKey)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := settings.RotateSecrets(context.Background(), e.DB, box)
		if err != nil || rep.MFA != 1 {
			t.Fatalf("rotate: %+v %v", rep, err)
		}
		only, _ := crypto.NewBox(newKey)
		got, err := only.OpenString(settings.PurposeMFA, e.Str(`SELECT totp_secret_enc FROM user_mfa WHERE user_id = ?`, mu.ID))
		if err != nil || got != mu.Secret {
			t.Fatalf("resealed secret does not open with the new key alone: %v", err)
		}
	})
}

// TestMFA_CLIResetMFA: the offline break-glass path.
func TestMFA_CLIResetMFA(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		mu := enrolTOTP(t, e, e.StaffUser(domain.RoleAdmin, 3, true))
		var out bytes.Buffer
		restore := authn.SetCLIIO(strings.NewReader(""), &out, func(k string) string { return e.Vars[k] })
		defer restore()
		if err := authn.RunCLI(context.Background(), "reset-mfa", []string{"--email", strings.ToUpper(mu.Email)}); err != nil {
			t.Fatal(err)
		}
		if err := authn.RunCLI(context.Background(), "reset-mfa", []string{"--email", "nobody@example.test"}); err == nil {
			t.Fatal("unknown user accepted")
		}
		if e.Int(`SELECT count(*) FROM user_mfa WHERE user_id = ?`, mu.ID) != 0 || e.Int(`SELECT count(*) FROM audit_log WHERE action = 'auth.mfa_reset' AND target_id = ? AND CAST(data AS TEXT) LIKE '%cli%'`, mu.ID) != 1 {
			t.Fatal("CLI reset")
		}
		if r, b := login(e.Client(), domain.AudienceStaff, mu.Email, authntest.TestPassword); r.StatusCode != 200 || authntest.JSON(t, b)["mfa_required"] != nil {
			t.Fatalf("break-glass login after CLI reset: %s", b)
		}
	})
}
