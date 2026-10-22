// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// AUTH-044: accounts linked to an identity provider sign in only through it.

const testIssuer = "https://idp.example.test/"

// linkIdentity gives u an OIDC identity row for its own audience, as the
// callback would (idp_managed untouched).
func linkIdentity(e *authntest.Env, u authntest.User, issuer, sub string) {
	e.Exec(`INSERT INTO user_identities (id, user_id, audience, issuer, subject, email_at_link, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), u.ID, string(u.Kind), issuer, sub, u.Email, store.Now())
}

func setIDPManaged(e *authntest.Env, u authntest.User) {
	e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID)
}

// issueToken mints a single-use auth token for u directly in the store.
func issueToken(t *testing.T, e *authntest.Env, u authntest.User, purpose string) string {
	t.Helper()
	ctx := context.Background()
	var tok string
	if err := e.DB.Tx(ctx, func(tx *store.Tx) error {
		var err error
		tok, err = authn.IssueAuthToken(ctx, tx, u.ID, purpose, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func countMails(e *authntest.Env, to, template string) int {
	n := 0
	for _, m := range e.Mails() {
		if m.To == to && m.Template == template {
			n++
		}
	}
	return n
}

// TestSSOLinkedUserCannotUseLocalPassword: an account with an OIDC identity
// for its audience, or idp_managed, fails local login with its (correct) old
// password exactly like a wrong password — same status and body, audited as
// sso_only — and every other local way in (password change, reset, sign-in
// link, verify-email) is refused. The refusals feed the account soft lock
// like wrong passwords do.
func TestSSOLinkedUserCannotUseLocalPassword(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		env := map[string]string{"FD_AUTH_STAFF_LOCAL": "true", "FD_SMTP_HOST": "smtp.example.test"}
		e := authntest.New(t, db, authntest.Options{Env: env})
		ref := e.Customer("", domain.RoleRequester)
		_, wrongBody := login(e.Client(), domain.AudienceCustomer, ref.Email, "wrong-password-123")
		identity := func(u authntest.User) { linkIdentity(e, u, testIssuer, "sub-"+u.ID) }
		managed := func(u authntest.User) { setIDPManaged(e, u) }
		cases := []struct {
			name string
			u    authntest.User
			link func(authntest.User)
		}{
			{"customer_idp_managed", e.Customer("", domain.RoleRequester), managed},
			{"customer_identity_only", e.Customer("", domain.RoleRequester), identity},
			{"staff_idp_managed", e.StaffUser(domain.RoleAgent, 1, false), managed},
			{"staff_identity_only", e.StaffUser(domain.RoleAgent, 1, false), identity},
			{"admin_not_break_glass", e.StaffUser(domain.RoleAdmin, 3, false), managed},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				u := tc.u
				aud := domain.Audience(u.Kind)
				old := e.Login(u) // a session from before the link (the link here is a raw SQL change)
				resetTok := issueToken(t, e, u, authn.PurposeResetPassword)
				magicTok := issueToken(t, e, u, authn.PurposeMagicLink)
				verifyTok := issueToken(t, e, u, authn.PurposeVerifyEmail)
				hash := e.Str(`SELECT password_hash FROM users WHERE id = ?`, u.ID)
				tc.link(u)

				c := e.Client()
				r, b := login(c, aud, u.Email, authntest.TestPassword)
				if r.StatusCode != 401 || c.Cookie != "" {
					t.Fatalf("local login of an SSO account: %d %s", r.StatusCode, b)
				}
				if !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(wrongBody)) {
					t.Fatalf("body differs from a wrong password:\n%s\n%s", b, wrongBody)
				}
				if _, wb := login(e.Client(), aud, u.Email, "wrong-password-123"); !bytes.Equal(bytes.TrimSpace(wb), bytes.TrimSpace(b)) {
					t.Fatalf("wrong and right password differ for an SSO account:\n%s\n%s", wb, b)
				}
				if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login_failed' AND actor_id = ? AND CAST(data AS TEXT) LIKE '%sso_only%'`, u.ID); n != 1 {
					t.Fatalf("sso_only audit rows = %d", n)
				}

				// PATCH /me cannot give it a local password.
				if r, b := old.Do("PATCH", "/api/v1/me", map[string]any{"current_password": authntest.TestPassword, "new_password": "a-brand-new-secret-7"}); r.StatusCode != 403 {
					t.Fatalf("password change: %d %s", r.StatusCode, b)
				}
				// Forgot answers with the SSO notice, never a link.
				e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": u.Email})
				if m := e.LastMail(u.Email); m.Template != authn.TplResetPassword || m.Vars["sso"] != "true" || authntest.TokenFromURL(m.Vars["url"]) != "" {
					t.Fatalf("forgot for an SSO account: %+v", m)
				}
				// A reset link issued before the link is dead.
				if r, b := e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": resetTok, "password": "another-good-pass-1"}); r.StatusCode != 400 {
					t.Fatalf("reset: %d %s", r.StatusCode, b)
				}
				if got := e.Str(`SELECT password_hash FROM users WHERE id = ?`, u.ID); got != hash {
					t.Fatal("reset changed the password of an SSO account")
				}
				// Sign-in and verify-email links open no session.
				for path, tok := range map[string]string{"/api/v1/auth/magic-link": magicTok, "/api/v1/auth/verify-email": verifyTok} {
					c := e.Client()
					if r, b := c.Do("POST", path, map[string]any{"token": tok}); r.StatusCode != 400 || c.Cookie != "" {
						t.Fatalf("%s: %d %s", path, r.StatusCode, b)
					}
				}
				if u.Kind == domain.KindCustomer {
					before := countMails(e, u.Email, authn.TplMagicLink)
					if r, _ := e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": u.Email}); r.StatusCode != 200 {
						t.Fatalf("magic-link request: %d", r.StatusCode)
					}
					if countMails(e, u.Email, authn.TplMagicLink) != before {
						t.Fatal("sign-in link mailed to an SSO account")
					}
				}
			})
		}

		// Throttle: ten refused logins with the right password soft-lock the
		// account exactly like ten wrong passwords.
		t.Run("throttle", func(t *testing.T) {
			et := authntest.New(t, db, authntest.Options{Throttle: true})
			u := et.Customer("", domain.RoleRequester)
			linkIdentity(et, u, testIssuer, "sub-throttle-"+u.ID)
			for i := 0; i < 10; i++ {
				c := et.Client()
				c.IP = "203.0.113." + string(rune('1'+i))
				if r, _ := login(c, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 401 {
					t.Fatalf("attempt %d: %d", i, r.StatusCode)
				}
			}
			et.Exec(`DELETE FROM user_identities WHERE user_id = ?`, u.ID) // unlinked again: only the lock remains
			c := et.Client()
			c.IP = "203.0.113.200"
			if r, _ := login(c, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 401 {
				t.Fatalf("not soft-locked after ten SSO-only refusals: %d", r.StatusCode)
			}
			if n := et.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login_failed' AND actor_id = ? AND CAST(data AS TEXT) LIKE '%account_locked%'`, u.ID); n != 1 {
				t.Fatalf("account_locked audits = %d", n)
			}
		})
	})
}

// TestLogin_SSOOnlyTimingSimilar: a refused SSO-only login with the right
// password takes as long as a wrong password (±20 %). SQLite, production
// argon params, skipped under -race.
func TestLogin_SSOOnlyTimingSimilar(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	db := storetest.Open(t, store.SQLite)
	e := authntest.New(t, db, authntest.Options{})
	restore := authn.SetArgonParams(authn.DefaultArgon)
	defer restore()
	plain := e.Customer("", domain.RoleRequester)
	sso := e.Customer("", domain.RoleRequester)
	e.SetPassword(plain.ID) // production-cost hashes
	e.SetPassword(sso.ID)
	linkIdentity(e, sso, testIssuer, "sub-timing")
	wrong, ssoRight := loginMedians(t, e, timingSamples(),
		loginAttempt{domain.AudienceCustomer, plain.Email, "definitely-wrong-pw"},
		loginAttempt{domain.AudienceCustomer, sso.Email, authntest.TestPassword})
	assertTimingSimilar(t, wrong, ssoRight)
}

// TestBreakGlassStillWorksAndRequiresMFA: a break-glass admin keeps local
// login even when it is IdP-managed and has an identity, and that login is
// covered by two-step verification: enrolment works, an enrolled factor is
// challenged before any session, the staff policy forces enrolment, and it
// may still change its local password. A non-break-glass admin in the same
// state is refused.
func TestBreakGlassStillWorksAndRequiresMFA(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{}) // staff local login off
		bgUser := func(sub string) authntest.User {
			u := e.StaffUser(domain.RoleAdmin, 3, true)
			setIDPManaged(e, u)
			linkIdentity(e, u, testIssuer, sub)
			return u
		}
		bg := bgUser("bg-1")
		c := e.Client()
		r, b := login(c, domain.AudienceStaff, bg.Email, authntest.TestPassword)
		if me := authntest.JSON(t, b); r.StatusCode != 200 || me["id"] != bg.ID || me["mfa_required"] != nil || c.Cookie == "" {
			t.Fatalf("break-glass local login: %d %s", r.StatusCode, b)
		}
		if r, b := c.Do("GET", "/api/v1/me/mfa", nil); r.StatusCode != 200 || authntest.JSON(t, b)["available"] != true {
			t.Fatalf("MFA not available to the break-glass admin: %d %s", r.StatusCode, b)
		}

		mu := enrolTOTP(t, e, bg)
		count := func() int {
			return e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.break_glass_login' AND actor_id = ?`, bg.ID)
		}
		before := count()
		pc, ch := passwordStep(t, e, bg, "")
		if pc.Cookie != "" || methodsOf(ch) != "totp,recovery" || count() != before {
			t.Fatalf("challenge: cookie=%q methods=%s audits=%d/%d", pc.Cookie, methodsOf(ch), count(), before)
		}
		if r, b := verify(pc, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 || pc.Cookie == "" {
			t.Fatalf("verify: %d %s", r.StatusCode, b)
		}
		if count() != before+1 {
			t.Fatal("break-glass login not audited after the second step")
		}

		// Policy "staff" forces an un-enrolled break-glass admin to enrol.
		if r, b := mu.C.Do("PATCH", "/api/v1/admin/security", map[string]any{"mfa_required": "staff"}); r.StatusCode != 200 {
			t.Fatalf("policy: %d %s", r.StatusCode, b)
		}
		bg2 := bgUser("bg-2")
		fc := e.Client()
		r, b = login(fc, domain.AudienceStaff, bg2.Email, authntest.TestPassword)
		if ch := authntest.JSON(t, b); r.StatusCode != 200 || ch["enrollment_required"] != true || fc.Cookie != "" {
			t.Fatalf("forced enrolment for break-glass: %d %s", r.StatusCode, b)
		}

		// The break-glass admin keeps its local password (and can change it).
		if r, b := mu.C.Do("PATCH", "/api/v1/me", map[string]any{"current_password": authntest.TestPassword, "new_password": "another-good-pass-1"}); r.StatusCode != 200 {
			t.Fatalf("break-glass password change: %d %s", r.StatusCode, b)
		}

		// Same state without the flag: SSO only.
		plain := e.StaffUser(domain.RoleAdmin, 3, false)
		setIDPManaged(e, plain)
		withLocal := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		if r, _ := login(withLocal.Client(), domain.AudienceStaff, plain.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatalf("non-break-glass IdP-managed admin: %d", r.StatusCode)
		}
	})
}

// TestLinkingInvalidatesPasswordAndSessions: when an account becomes
// SSO-only — linked by email (staff and customer) or turned idp_managed by
// the staff role sync, or a break-glass admin losing its flag — its local
// password hash is removed, its local sessions end (OIDC ones stay), open MFA
// challenges and unused reset links die, and auth.sso_linked is audited. A
// break-glass admin keeps password and sessions through the sync.
func TestLinkingInvalidatesPasswordAndSessions(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_LINK_BY_EMAIL": "true", "FD_AUTH_STAFF_LOCAL": "true"})})
		passwordGone := func(u authntest.User) bool {
			return e.Int(`SELECT COUNT(*) FROM users WHERE id = ? AND password_hash IS NULL`, u.ID) == 1
		}
		linkedVia := func(u authntest.User, via string) int {
			return e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.sso_linked' AND actor_id = ? AND CAST(data AS TEXT) LIKE ?`, u.ID, "%"+via+"%")
		}
		meStatus := func(c *authntest.Client) int { r, _ := c.Do("GET", "/api/v1/me", nil); return r.StatusCode }

		t.Run("staff_link_by_email", func(t *testing.T) {
			u := e.StaffUser(domain.RoleAgent, 1, false)
			a, b := e.Login(u), e.Login(u)
			issueToken(t, e, u, authn.PurposeResetPassword)
			sso := mustLogin(t, op, e, staffFlow(op, "s-agent", u.Email, "fd-agents"))
			if !passwordGone(u) || meStatus(a) != 401 || meStatus(b) != 401 || meStatus(sso) != 200 {
				t.Fatalf("after link: password gone=%v old=%d/%d sso=%d", passwordGone(u), meStatus(a), meStatus(b), meStatus(sso))
			}
			if n := e.Int(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, u.ID); n != 1 {
				t.Fatalf("sessions left = %d", n)
			}
			if n := e.Int(`SELECT COUNT(*) FROM auth_tokens WHERE user_id = ? AND used_at IS NULL`, u.ID); n != 0 {
				t.Fatalf("unused reset links left = %d", n)
			}
			if linkedVia(u, "link_by_email") != 1 {
				t.Fatal("auth.sso_linked not audited")
			}
			if r, _ := login(e.Client(), domain.AudienceStaff, u.Email, authntest.TestPassword); r.StatusCode != 401 {
				t.Fatalf("local login after link: %d", r.StatusCode)
			}
		})

		t.Run("customer_link_by_email_kills_open_challenge", func(t *testing.T) {
			mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
			pc, ch := passwordStep(t, e, mu.User, "")
			mustLogin(t, op, e, flow{aud: domain.AudienceCustomer, claims: op.claims("cust-client", "c-link", mu.Email)})
			if !passwordGone(mu.User) || meStatus(mu.C) != 401 || linkedVia(mu.User, "link_by_email") != 1 {
				t.Fatal("customer link did not invalidate password/sessions")
			}
			if r, b := verify(pc, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 401 || pc.Cookie != "" {
				t.Fatalf("challenge from before the link completed: %d %s", r.StatusCode, b)
			}
		})

		t.Run("staff_idp_managed_sync", func(t *testing.T) {
			u := e.StaffUser(domain.RoleAgent, 1, false)
			local := e.Login(u)
			linkIdentity(e, u, op.issuer(), "s-sync") // identity without idp_managed (pre-AUTH-044 data)
			sso := mustLogin(t, op, e, staffFlow(op, "s-sync", u.Email, "fd-agents", "fd-l2"))
			if !passwordGone(u) || meStatus(local) != 401 || meStatus(sso) != 200 || linkedVia(u, "idp_sync") != 1 {
				t.Fatalf("sync: password gone=%v local=%d sso=%d", passwordGone(u), meStatus(local), meStatus(sso))
			}
			if e.Int(`SELECT COUNT(*) FROM users WHERE id = ? AND idp_managed`, u.ID) != 1 {
				t.Fatal("idp_managed not set")
			}
		})

		t.Run("customer_idp_managed_sync", func(t *testing.T) {
			u := e.Customer("", domain.RoleRequester)
			local := e.Login(u)
			linkIdentity(e, u, op.issuer(), "c-sync")
			mustLogin(t, op, e, flow{aud: domain.AudienceCustomer, claims: op.claims("cust-client", "c-sync", u.Email)})
			if !passwordGone(u) || meStatus(local) != 401 || linkedVia(u, "idp_sync") != 1 {
				t.Fatal("customer sync did not invalidate password/sessions")
			}
		})

		t.Run("break_glass_kept_then_lost", func(t *testing.T) {
			u := e.StaffUser(domain.RoleAdmin, 3, true)
			local := e.Login(u)
			linkIdentity(e, u, op.issuer(), "s-bg")
			mustLogin(t, op, e, staffFlow(op, "s-bg", u.Email, "fd-admins", "fd-l3"))
			if passwordGone(u) || meStatus(local) != 200 || linkedVia(u, "idp_sync") != 0 {
				t.Fatal("break-glass admin lost its local access through the sync")
			}
			if r, _ := login(e.Client(), domain.AudienceStaff, u.Email, authntest.TestPassword); r.StatusCode != 200 {
				t.Fatalf("break-glass local login after sync: %d", r.StatusCode)
			}
			mustLogin(t, op, e, staffFlow(op, "s-bg", u.Email, "fd-agents")) // demoted: break-glass flag removed
			if !passwordGone(u) || meStatus(local) != 401 || linkedVia(u, "idp_sync") != 1 {
				t.Fatal("demoted break-glass admin kept its local password or sessions")
			}
			if r, _ := login(e.Client(), domain.AudienceStaff, u.Email, authntest.TestPassword); r.StatusCode != 401 {
				t.Fatalf("local login after losing break-glass: %d", r.StatusCode)
			}
		})
	})
}

// TestNoLoginPathBypassesMFA: every way to obtain a session from a local
// first factor answers an MfaChallenge (no cookie, /me 401) for a user with
// a second factor, and the challenge then completes with the factor. The
// password-reset flow never signs in (204, no cookie) and the next login is
// challenged; the setup wizard only creates a brand-new account (an existing
// address is 409), so it can never reach an enrolled user. OIDC sign-ins are
// out of scope by design: the IdP owns their second factor.
func TestNoLoginPathBypassesMFA(t *testing.T) {
	type result struct {
		mu authntest.User
		c  *authntest.Client
		r  *http.Response
		b  []byte
	}
	env := map[string]string{"FD_SMTP_HOST": "smtp.example.test"}
	rows := []struct {
		name      string
		challenge bool // else: no session must be created
		run       func(t *testing.T, e *authntest.Env, mu mfaUser) result
	}{
		{"password_customer", true, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			c := e.Client()
			r, b := login(c, domain.AudienceCustomer, mu.Email, authntest.TestPassword)
			return result{mu.User, c, r, b}
		}},
		{"magic_link", true, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": mu.Email})
			tok := authntest.TokenFromURL(e.LastMail(mu.Email).Vars["url"])
			c := e.Client()
			r, b := c.Do("POST", "/api/v1/auth/magic-link", map[string]any{"token": tok})
			return result{mu.User, c, r, b}
		}},
		{"verify_email", true, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			tok := issueToken(t, e, mu.User, authn.PurposeVerifyEmail)
			c := e.Client()
			r, b := c.Do("POST", "/api/v1/auth/verify-email", map[string]any{"token": tok})
			return result{mu.User, c, r, b}
		}},
		{"invite_accept", true, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			e.Exec(`UPDATE users SET status = 'pending_verification' WHERE id = ?`, mu.ID)
			tok := issueToken(t, e, mu.User, authn.PurposeInvite)
			c := e.Client()
			r, b := c.Do("POST", "/api/v1/auth/invite/accept", map[string]any{"token": tok, "name": "Invited Again", "password": "invited-pass-123"})
			return result{mu.User, c, r, b}
		}},
		{"reset_password", false, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			c := e.Client()
			r, b := c.Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": resetToken(t, e, mu.Email), "password": "a-new-good-pass-42"})
			if r.StatusCode != 204 {
				t.Fatalf("reset: %d %s", r.StatusCode, b)
			}
			// The next login with the new password is still challenged.
			lc := e.Client()
			lr, lb := login(lc, domain.AudienceCustomer, mu.Email, "a-new-good-pass-42")
			if ch := authntest.JSON(t, lb); lr.StatusCode != 200 || ch["mfa_required"] != true || lc.Cookie != "" {
				t.Fatalf("login after reset: %d %s", lr.StatusCode, lb)
			}
			return result{mu.User, c, r, b}
		}},
		{"setup_wizard", false, func(t *testing.T, e *authntest.Env, mu mfaUser) result {
			body := setupBody(setupToken(t, e))
			body["admin"].(map[string]any)["email"] = mu.Email
			c := e.Client()
			r, b := c.Do("POST", "/api/v1/setup/complete", body)
			if r.StatusCode != 409 {
				t.Fatalf("setup with an enrolled user's address: %d %s", r.StatusCode, b)
			}
			// A completed wizard signs in a brand-new account with no factor.
			nc := e.Client()
			if r, b := nc.Do("POST", "/api/v1/setup/complete", setupBody(setupToken(t, e))); r.StatusCode != 201 {
				t.Fatalf("setup: %d %s", r.StatusCode, b)
			}
			if n := e.Int(`SELECT COUNT(*) FROM user_mfa m JOIN users u ON u.id = m.user_id WHERE u.email = 'owner@example.test'`); n != 0 {
				t.Fatal("wizard admin has MFA state")
			}
			return result{mu.User, c, r, b}
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			storetest.Engines(t, func(t *testing.T, db *store.DB) {
				e := authntest.New(t, db, authntest.Options{Env: env})
				mu := enrolTOTP(t, e, e.Customer("", domain.RoleRequester))
				res := row.run(t, e, mu)
				if res.c.Cookie != "" {
					t.Fatalf("session cookie set: %d %s", res.r.StatusCode, res.b)
				}
				if r, _ := res.c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
					t.Fatalf("/me without the second factor: %d", r.StatusCode)
				}
				if !row.challenge {
					return
				}
				ch := authntest.JSON(t, res.b)
				if res.r.StatusCode != 200 || ch["mfa_required"] != true || ch["enrollment_required"] != false {
					t.Fatalf("expected an MfaChallenge: %d %s", res.r.StatusCode, res.b)
				}
				if r, b := verify(res.c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 || res.c.Cookie == "" {
					t.Fatalf("verify: %d %s", r.StatusCode, b)
				}
			})
		})
	}

	// Break-glass: an IdP-managed break-glass admin (the one SSO-linked
	// account allowed a local login) is challenged too.
	t.Run("password_break_glass_idp_managed", func(t *testing.T) {
		storetest.Engines(t, func(t *testing.T, db *store.DB) {
			e := authntest.New(t, db, authntest.Options{})
			u := e.StaffUser(domain.RoleAdmin, 3, true)
			setIDPManaged(e, u)
			linkIdentity(e, u, testIssuer, "bg-mfa")
			mu := enrolTOTP(t, e, u)
			c, ch := passwordStep(t, e, u, "")
			if c.Cookie != "" {
				t.Fatal("session before the second step")
			}
			if r, b := verify(c, ch, "totp", totpAt(t, mu.Secret, e.Clock.Now())); r.StatusCode != 200 {
				t.Fatalf("verify: %d %s", r.StatusCode, b)
			}
		})
	})
}
