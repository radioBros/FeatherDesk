// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

var smtpOn = map[string]string{"FD_SMTP_HOST": "smtp.example.test"}

func withEnv(extra map[string]string) authntest.Options {
	env := map[string]string{"FD_SMTP_HOST": "smtp.example.test"}
	for k, v := range extra {
		env[k] = v
	}
	return authntest.Options{Env: env}
}

func register(c *authntest.Client, email string) (int, []byte) {
	r, b := c.Do("POST", "/api/v1/auth/register", map[string]any{"email": email, "name": "New Person", "password": "a-fine-password-42"})
	return r.StatusCode, b
}

func TestRegister_ExistingEmailSame202(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		s1, b1 := register(e.Client(), u.Email)
		s2, b2 := register(e.Client(), "brand-new@example.test")
		if s1 != 202 || s2 != 202 || string(b1) != string(b2) {
			t.Fatalf("responses differ: %d %s / %d %s", s1, b1, s2, b2)
		}
		if m := e.LastMail(u.Email); m.Template != authn.TplAlreadyRegistered {
			t.Fatalf("existing address mail: %s", m.Template)
		}
		if m := e.LastMail("brand-new@example.test"); m.Template != authn.TplVerifyEmail || m.Vars["url"] == "" {
			t.Fatalf("verification mail: %+v", m)
		}
		if st := e.Str(`SELECT status FROM users WHERE email = 'brand-new@example.test'`); st != "pending_verification" {
			t.Fatalf("status %s", st)
		}
	})
}

func TestRegister_DisabledSetting403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, withEnv(map[string]string{"FD_SELF_REGISTRATION": "false"}))
		if s, _ := register(e.Client(), "x@example.test"); s != 403 {
			t.Fatalf("env off: %d", s)
		}
		e2 := authntest.New(t, storetest.Open(t, db.Dialect), authntest.Options{Env: smtpOn})
		e2.Exec(`UPDATE settings SET value = ? WHERE key = 'support'`, store.MustJSON(map[string]any{"self_registration": false}))
		e2.App.Deps.Settings.Invalidate()
		if s, _ := register(e2.Client(), "x@example.test"); s != 403 {
			t.Fatalf("setting off: %d", s)
		}
	})
}

func TestRegister_EmailDisabled409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		s, b := register(e.Client(), "x@example.test")
		if s != 409 || !contains(b, "email-disabled") {
			t.Fatalf("register without SMTP: %d %s", s, b)
		}
		if n := e.Int(`SELECT COUNT(*) FROM users WHERE email = 'x@example.test'`); n != 0 {
			t.Fatal("user created without a working mail path")
		}
	})
}

func TestRegister_NoOrgJoinBeforeVerification(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		org := storetest.CreateOrg(t, e.DB, "Acme")
		e.Exec(`UPDATE organizations SET email_domains = ? WHERE id = ?`, `["acme.test"]`, org)
		register(e.Client(), "jane@acme.test")
		if got := e.Str(`SELECT organization_id FROM users WHERE email = 'jane@acme.test'`); got != "" {
			t.Fatal("joined the organization before verification")
		}
	})
}

func TestVerify_ActivatesAndJoinsOrgByDomain(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		org := storetest.CreateOrg(t, e.DB, "Acme")
		e.Exec(`UPDATE organizations SET email_domains = ? WHERE id = ?`, `["acme.test"]`, org)
		register(e.Client(), "jane@ACME.test")
		tok := authntest.TokenFromURL(e.LastMail("jane@acme.test").Vars["url"])
		c := e.Client()
		r, b := c.Do("POST", "/api/v1/auth/verify-email", map[string]any{"token": tok})
		if r.StatusCode != 200 {
			t.Fatalf("verify: %d %s", r.StatusCode, b)
		}
		me := authntest.JSON(t, b)
		if me["status"] != "active" || me["organization_id"] != org || c.Cookie == "" {
			t.Fatalf("after verify: %v", me)
		}
		if r, _ := e.Client().Do("POST", "/api/v1/auth/verify-email", map[string]any{"token": tok}); r.StatusCode != 400 {
			t.Fatalf("token reuse: %d", r.StatusCode)
		}
	})
}

func TestForgot_Always202(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		r1, b1 := e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": u.Email})
		r2, b2 := e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": "nobody@example.test"})
		if r1.StatusCode != 202 || r2.StatusCode != 202 || string(b1) != string(b2) {
			t.Fatalf("%d %s / %d %s", r1.StatusCode, b1, r2.StatusCode, b2)
		}
		if len(e.Mails()) != 1 || e.LastMail(u.Email).Template != authn.TplResetPassword {
			t.Fatalf("mails: %+v", e.Mails())
		}
	})
}

func TestForgot_EmailDisabled409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		if r, b := e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": "a@example.test"}); r.StatusCode != 409 || !contains(b, "email-disabled") {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	})
}

func TestForgot_SSOUserGetsSSONotice(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID)
		e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": u.Email})
		m := e.LastMail(u.Email)
		if m.Vars["sso"] != "true" || authntest.TokenFromURL(m.Vars["url"]) != "" {
			t.Fatalf("SSO notice: %+v", m)
		}
		if n := e.Int(`SELECT COUNT(*) FROM auth_tokens WHERE user_id = ?`, u.ID); n != 0 {
			t.Fatal("reset token issued for an SSO account")
		}
	})
}

func resetToken(t *testing.T, e *authntest.Env, email string) string {
	t.Helper()
	e.Client().Do("POST", "/api/v1/auth/password/forgot", map[string]any{"email": email})
	return authntest.TokenFromURL(e.LastMail(email).Vars["url"])
}

func TestReset_RevokesAllSessions(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		a, b := e.Login(u), e.Login(u)
		tok := resetToken(t, e, u.Email)
		if r, body := e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": tok, "password": "another-good-pass-1"}); r.StatusCode != 204 {
			t.Fatalf("reset: %d %s", r.StatusCode, body)
		}
		for _, c := range []*authntest.Client{a, b} {
			if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
				t.Fatal("session survived a password reset")
			}
		}
		if r, _ := login(e.Client(), domain.AudienceCustomer, u.Email, "another-good-pass-1"); r.StatusCode != 200 {
			t.Fatal("new password rejected")
		}
	})
}

func TestReset_TokenSingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		tok := resetToken(t, e, u.Email)
		e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": tok, "password": "another-good-pass-1"})
		if r, _ := e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": tok, "password": "yet-another-pass-22"}); r.StatusCode != 400 {
			t.Fatalf("second use: %d", r.StatusCode)
		}
		if r, _ := e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": "x", "password": "short"}); r.StatusCode != 422 {
			t.Fatalf("weak password: %d", r.StatusCode)
		}
	})
}

func TestMagicLink_Always202(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		r1, b1 := e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": u.Email})
		r2, b2 := e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": "ghost@example.test"})
		if r1.StatusCode != 200 || r2.StatusCode != 200 || string(b1) != string(b2) {
			t.Fatalf("%d %s / %d %s", r1.StatusCode, b1, r2.StatusCode, b2)
		}
		if e.LastMail(u.Email).Template != authn.TplMagicLink || len(e.Mails()) != 1 {
			t.Fatalf("mails %+v", e.Mails())
		}
	})
}

func TestMagicLink_StaffEmailNoMail(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		s := e.StaffUser(domain.RoleAgent, 1, false)
		if r, _ := e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": s.Email}); r.StatusCode != 200 {
			t.Fatal("status")
		}
		if len(e.Mails()) != 0 {
			t.Fatal("magic link sent to a staff address")
		}
	})
}

func TestMagicLink_SingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		u := e.Customer("", domain.RoleRequester)
		e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"email": u.Email})
		tok := authntest.TokenFromURL(e.LastMail(u.Email).Vars["url"])
		c := e.Client()
		r, b := c.Do("POST", "/api/v1/auth/magic-link", map[string]any{"token": tok})
		if r.StatusCode != 200 || authntest.JSON(t, b)["id"] != u.ID || c.Cookie == "" {
			t.Fatalf("consume: %d %s", r.StatusCode, b)
		}
		if r, _ := e.Client().Do("POST", "/api/v1/auth/magic-link", map[string]any{"token": tok}); r.StatusCode != 400 {
			t.Fatalf("reuse: %d", r.StatusCode)
		}
	})
}

func TestInvite_OrgManagerCannotInviteOtherOrg(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		orgA, orgB := storetest.CreateOrg(t, e.DB, ""), storetest.CreateOrg(t, e.DB, "")
		mgr := e.Login(e.Customer(orgA, domain.RoleOrgManager))
		r, b := mgr.Do("POST", "/api/v1/portal/organization/members", map[string]any{"email": "colleague@example.test", "name": "Colleague", "organization_id": orgB})
		if r.StatusCode != 201 {
			t.Fatalf("invite: %d %s", r.StatusCode, b)
		}
		if got := authntest.JSON(t, b)["organization_id"]; got != orgA {
			t.Fatalf("invitee placed in %v, want the manager's org", got)
		}
		outsider := e.Customer(orgB, domain.RoleRequester)
		if r, _ := mgr.Do("PATCH", "/api/v1/portal/organization/members/"+outsider.ID, map[string]any{"status": "disabled"}); r.StatusCode != 404 {
			t.Fatalf("patching another org's member: %d", r.StatusCode)
		}
		req := e.Login(e.Customer(orgA, domain.RoleRequester))
		if r, _ := req.Do("POST", "/api/v1/portal/organization/members", map[string]any{"email": "x@example.test", "name": "X"}); r.StatusCode != 403 {
			t.Fatalf("requester inviting: %d", r.StatusCode)
		}
		if r, _ := req.Do("GET", "/api/v1/portal/organization/members", nil); r.StatusCode != 403 {
			t.Fatalf("requester listing: %d", r.StatusCode)
		}
		r, b = mgr.Do("GET", "/api/v1/portal/organization/members", nil)
		var list []map[string]any
		_ = json.Unmarshal(b, &list)
		if r.StatusCode != 200 || len(list) != 3 {
			t.Fatalf("members: %d %s", r.StatusCode, b)
		}
	})
}

func TestInvite_AcceptSetsKindFromInviteNotInput(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		org := storetest.CreateOrg(t, e.DB, "")
		mgr := e.Login(e.Customer(org, domain.RoleOrgManager))
		mgr.Do("POST", "/api/v1/portal/organization/members", map[string]any{"email": "inv@example.test", "name": "Inv"})
		tok := authntest.TokenFromURL(e.LastMail("inv@example.test").Vars["url"])
		c := e.Client()
		r, b := c.Do("POST", "/api/v1/auth/invite/accept", map[string]any{"token": tok, "name": "Invited Person", "password": "invited-pass-123", "kind": "staff", "role": "admin"})
		if r.StatusCode != 200 {
			t.Fatalf("accept: %d %s", r.StatusCode, b)
		}
		me := authntest.JSON(t, b)
		if me["kind"] != "customer" || me["role"] != "requester" || me["status"] != "active" || me["organization_id"] != org {
			t.Fatalf("accepted user: %v", me)
		}
	})
}

func TestInvite_RevokedTokenRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		org := storetest.CreateOrg(t, e.DB, "")
		mgr := e.Login(e.Customer(org, domain.RoleOrgManager))
		_, b := mgr.Do("POST", "/api/v1/portal/organization/members", map[string]any{"email": "inv@example.test", "name": "Inv"})
		id := authntest.JSON(t, b)["id"].(string)
		tok := authntest.TokenFromURL(e.LastMail("inv@example.test").Vars["url"])
		// Revoking a pending invite = disabling the invited account.
		if r, body := mgr.Do("PATCH", "/api/v1/portal/organization/members/"+id, map[string]any{"status": "disabled"}); r.StatusCode != 200 {
			t.Fatalf("revoke: %d %s", r.StatusCode, body)
		}
		if r, _ := e.Client().Do("POST", "/api/v1/auth/invite/accept", map[string]any{"token": tok, "name": "X", "password": "invited-pass-123"}); r.StatusCode != 400 {
			t.Fatalf("revoked invite accepted: %d", r.StatusCode)
		}
	})
}

// TestMailJob_EnqueuedToQueue checks the real "mail.auth" job path.
func TestMailJob_EnqueuedToQueue(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		o := authntest.Options{Env: smtpOn, KeepMail: true}
		e := authntest.New(t, db, o)
		var mu sync.Mutex
		var got []authn.MailPayload
		q := e.App.Deps.Queue
		q.Handle(authn.MailJobKind, func(ctx context.Context, raw json.RawMessage) error {
			var p authn.MailPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
			return nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() {
			sctx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
			defer c2()
			_ = q.Stop(sctx)
		}()
		register(e.Client(), "queued@example.test")
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(got)
			mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 || got[0].Template != authn.TplVerifyEmail || got[0].To != "queued@example.test" || got[0].Locale != "en" || got[0].UserID == "" {
			t.Fatalf("queued mail: %+v", got)
		}
	})
}

func contains(b []byte, s string) bool {
	return len(b) > 0 && string(b) != "" && stringsContains(string(b), s)
}
