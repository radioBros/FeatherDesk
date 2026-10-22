// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestMe_ReturnsCSRF(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		org := storetest.CreateOrg(t, e.DB, "")
		e.Exec(`UPDATE organizations SET notes = 'internal staff note' WHERE id = ?`, org)
		u := e.Customer(org, domain.RoleOrgManager)
		c := e.Login(u)
		r, b := c.Do("GET", "/api/v1/me", nil)
		me := authntest.JSON(t, b)
		if r.StatusCode != 200 || me["csrf_token"] == "" || me["csrf_token"] != c.CSRF {
			t.Fatalf("csrf: %v", me["csrf_token"])
		}
		if me["scopes"] != nil || me["effective_tier"].(float64) != 0 {
			t.Fatalf("session scopes/tier: %v %v", me["scopes"], me["effective_tier"])
		}
		orgJSON := me["organization"].(map[string]any)
		if orgJSON["id"] != org {
			t.Fatal("organization missing")
		}
		if _, leak := orgJSON["notes"]; leak {
			t.Fatal("staff notes of the organization leaked to a customer")
		}
	})
}

func TestMe_HasPasswordAndIdpManagedFlags(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		u := e.StaffUser(domain.RoleSupervisor, 2, false)
		c := e.Login(u)
		_, b := c.Do("GET", "/api/v1/me", nil)
		me := authntest.JSON(t, b)
		if me["has_password"] != true || me["idp_managed"] != false || me["effective_tier"].(float64) != 3 {
			t.Fatalf("flags: %v", me)
		}
		e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID)
		_, b = c.Do("GET", "/api/v1/me", nil)
		if authntest.JSON(t, b)["idp_managed"] != true {
			t.Fatal("idp_managed not reported")
		}
	})
}

func TestMe_PasswordChangeRevokesOtherSessions(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		a, b := e.Login(u), e.Login(u)
		if r, body := a.Do("PATCH", "/api/v1/me", map[string]any{"current_password": "wrong-one-12345", "new_password": "a-brand-new-secret-7"}); r.StatusCode != 422 {
			t.Fatalf("wrong current password: %d %s", r.StatusCode, body)
		}
		if r, body := a.Do("PATCH", "/api/v1/me", map[string]any{"current_password": authntest.TestPassword, "new_password": "a-brand-new-secret-7"}); r.StatusCode != 200 {
			t.Fatalf("change: %d %s", r.StatusCode, body)
		}
		if r, _ := a.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("current session revoked")
		}
		if r, _ := b.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatal("other session survived a password change")
		}
		if r, _ := login(e.Client(), domain.AudienceCustomer, u.Email, "a-brand-new-secret-7"); r.StatusCode != 200 {
			t.Fatal("new password does not work")
		}
	})
}

func TestMe_IdpManagedCannotSetPassword(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, u.ID)
		if r, _ := c.Do("PATCH", "/api/v1/me", map[string]any{"current_password": authntest.TestPassword, "new_password": "a-brand-new-secret-7"}); r.StatusCode != 403 {
			t.Fatalf("idp-managed password change: %d", r.StatusCode)
		}
	})
}

func TestMe_PatchProfile(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		r, b := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "  New Name ", "locale": "it", "notification_opt_out": []string{"customer.resolved", "customer.resolved"}})
		me := authntest.JSON(t, b)
		if r.StatusCode != 200 || me["name"] != "New Name" || me["locale"] != "it" || len(me["notification_opt_out"].([]any)) != 1 {
			t.Fatalf("patch: %d %v", r.StatusCode, me)
		}
		if r, _ := c.Do("PATCH", "/api/v1/me", map[string]any{"locale": "xx"}); r.StatusCode != 422 {
			t.Fatalf("bad locale: %d", r.StatusCode)
		}
	})
}

// TestMe_OptOutValidated: PATCH /me runs notify.ValidateOptOut (NOT-015):
// auth.* and customer.reply are not_optional, unknown keys invalid.
func TestMe_OptOutValidated(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		for in, code := range map[string]string{"auth.reset_password": "not_optional", "customer.reply": "not_optional", "ticket.nope": "invalid"} {
			r, b := c.Do("PATCH", "/api/v1/me", map[string]any{"notification_opt_out": []string{in}})
			pb := authntest.JSON(t, b)
			errs, _ := pb["errors"].([]any)
			if r.StatusCode != 422 || len(errs) != 1 || errs[0].(map[string]any)["code"] != code {
				t.Fatalf("%s: %d %s", in, r.StatusCode, b)
			}
		}
		var prefs string
		if err := db.R.QueryRow(`SELECT notify_prefs FROM users WHERE id = (SELECT id FROM users WHERE kind = 'customer' LIMIT 1)`).Scan(&prefs); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(prefs, "auth.reset_password") {
			t.Fatalf("refused opt-out was stored: %s", prefs)
		}
	})
}

func TestAuthConfig_NoSecretFields(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{
			"FD_SMTP_HOST": "smtp.example.test", "FD_SMTP_PASSWORD": "smtp-secret-value", "FD_SMTP_USERNAME": "mailer",
		}})
		_, b := e.Client().Do("GET", "/api/v1/auth/config", nil)
		for _, bad := range []string{"smtp-secret-value", "smtp.example.test", `"mailer"`, `"password"`, "password_enc", "secret", strings.Repeat("s", 32)} {
			if strings.Contains(string(b), bad) {
				t.Fatalf("auth config leaks %q: %s", bad, b)
			}
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		allowed := map[string]bool{"staff": true, "customer": true, "branding": true, "tier_labels": true, "upload": true, "message_max_length": true,
			"customer_urgent_allowed": true, "email_enabled": true, "setup_pending": true, "portal_enabled": true, "kb_enabled": true, "locales": true, "default_locale": true, "version": true}
		for k := range m {
			if !allowed[k] {
				t.Fatalf("unexpected key %q", k)
			}
		}
		if m["email_enabled"] != true {
			t.Fatal("email_enabled should be true with FD_SMTP_HOST")
		}
	})
}

func TestAuthConfig_EmailDisabledHidesRegisterAndReset(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		_, b := e.Client().Do("GET", "/api/v1/auth/config", nil)
		m := authntest.JSON(t, b)
		cust := m["customer"].(map[string]any)
		if m["email_enabled"] != false || cust["self_registration"] != false || cust["password_reset"] != false || cust["magic_link"] != false {
			t.Fatalf("config: %v", m)
		}
		if m["setup_pending"] != true {
			t.Fatal("empty DB without bootstrap: setup should be pending")
		}
	})
}

// TestAuthConfig_ZeroDBReadsWhenCached closes the reader pool after warming
// the cache: a cached /auth/config must still answer.
func TestAuthConfig_ZeroDBReadsWhenCached(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		if r, _ := e.Client().Do("GET", "/api/v1/auth/config", nil); r.StatusCode != 200 {
			t.Fatal("warm-up failed")
		}
		_ = e.DB.R.Close()
		c := e.Client()
		for i := 0; i < 5; i++ {
			if r, b := c.Do("GET", "/api/v1/auth/config", nil); r.StatusCode != 200 {
				t.Fatalf("cached config needed the DB: %d %s", r.StatusCode, b)
			}
		}
	})
}

func TestLogout_RevokesSession(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		old := c.Cookie
		r, b := c.Do("POST", "/api/v1/auth/logout", nil)
		if r.StatusCode != 200 {
			t.Fatalf("logout: %d %s", r.StatusCode, b)
		}
		if c.Cookie != "" {
			t.Fatal("cookie not cleared")
		}
		stale := &authntest.Client{E: e, Cookie: old, IP: "192.0.2.9"}
		if r, _ := stale.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("old cookie after logout: %d", r.StatusCode)
		}
	})
}

func TestLogout_LocalReturnsLoginURL(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		_, b := c.Do("POST", "/api/v1/auth/logout", nil)
		if got := authntest.JSON(t, b)["redirect_url"]; got != authntest.PublicURL+"/login?logged_out=1" {
			t.Fatalf("redirect_url = %v", got)
		}
		admin := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		_, b = admin.Do("POST", "/api/v1/auth/logout", nil)
		if got := authntest.JSON(t, b)["redirect_url"]; got != authntest.PublicURL+"/agent/login?logged_out=1" {
			t.Fatalf("staff redirect_url = %v", got)
		}
	})
}

func TestSessions_RevokeOther(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		a, b := e.Login(u), e.Login(u)
		_, body := a.Do("GET", "/api/v1/me/sessions", nil)
		var list []map[string]any
		_ = json.Unmarshal(body, &list)
		if len(list) != 2 {
			t.Fatalf("sessions: %s", body)
		}
		var other string
		for _, s := range list {
			if s["current"] != true {
				other = s["id"].(string)
			}
			if strings.Contains(s["id"].(string), a.Cookie) || len(s["id"].(string)) != 32 {
				t.Fatal("session id exposes the token")
			}
		}
		if r, _ := a.Do("DELETE", "/api/v1/me/sessions/"+other, nil); r.StatusCode != 204 {
			t.Fatalf("revoke: %d", r.StatusCode)
		}
		if r, _ := b.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatal("revoked session still valid")
		}
		if r, _ := a.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("current session lost")
		}
	})
}

func TestSessions_CannotRevokeOthers404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		victim := e.Login(e.Customer("", domain.RoleRequester))
		_, body := victim.Do("GET", "/api/v1/me/sessions", nil)
		var list []map[string]any
		_ = json.Unmarshal(body, &list)
		attacker := e.Login(e.Customer("", domain.RoleRequester))
		if r, _ := attacker.Do("DELETE", "/api/v1/me/sessions/"+list[0]["id"].(string), nil); r.StatusCode != 404 {
			t.Fatalf("revoking another user's session: %d", r.StatusCode)
		}
		if r, _ := victim.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("victim session affected")
		}
	})
}
