// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestAudit_EveryAuthEventWritten drives each auth event once and checks
// its audit_log row; then no row carries a password or token field/value.
func TestAudit_EveryAuthEventWritten(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: smtpOn})
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		ac := e.Login(admin)                                                     // login_success, break_glass_login
		login(e.Client(), domain.AudienceStaff, admin.Email, "wrong-password-1") // login_failed
		cust := e.Customer("", domain.RoleRequester)
		cc := e.Login(cust)
		pat := cc.PAT("tickets:read") // token_created
		id := e.Str(`SELECT id FROM api_tokens WHERE token_hash = ?`, sha(pat.Bearer))
		cc.Do("DELETE", "/api/v1/me/tokens/"+id, nil) // token_revoked
		tok := resetToken(t, e, cust.Email)
		e.Client().Do("POST", "/api/v1/auth/password/reset", map[string]any{"token": tok, "password": "fresh-password-42"}) // password_reset
		ac.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "new@corp.test", "name": "New", "role": "agent"})      // staff_changed
		staffID := e.Str(`SELECT id FROM users WHERE email = 'new@corp.test'`)
		ac.Do("PATCH", "/api/v1/admin/staff/"+staffID, map[string]any{"status": "disabled"}) // user_disabled
		ac.Do("POST", "/api/v1/auth/logout", nil)                                            // logout
		for _, a := range []string{"auth.login_success", "auth.login_failed", "auth.break_glass_login", "auth.logout", "auth.token_created",
			"auth.token_revoked", "auth.password_reset", "auth.staff_changed", "auth.user_disabled"} {
			if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, a); n == 0 {
				t.Errorf("no audit row for %s", a)
			}
		}
		rows, err := e.DB.Read().QueryContext(context.Background(), `SELECT action, CAST(data AS TEXT) FROM audit_log`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, d string
			_ = rows.Scan(&a, &d)
			for _, bad := range []string{`"password"`, `"token"`, authntest.TestPassword, "wrong-password-1", "fresh-password-42", pat.Bearer, tok} {
				if strings.Contains(d, bad) {
					t.Fatalf("audit %s data leaks %q: %s", a, bad, d)
				}
			}
		}
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login_failed' AND CAST(data AS TEXT) LIKE '%email_sha256%'`); n == 0 {
			t.Fatal("failed login should record the email hash")
		}
	})
}
