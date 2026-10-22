// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestE2E_OptOutAndEmailEnabled covers the authn ↔ notify wiring:
// PATCH /me validates opt-outs with notify.ValidateOptOut (NOT-015: 422
// not_optional for auth.*), a stored opt-out suppresses the mail end to end,
// and /auth/config reports email_enabled from notify.EnabledWith (NOT-022).
func TestE2E_OptOutAndEmailEnabled(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		custID := storetest.CreateCustomer(t, db, "")
		vars := e2eEnv(t, db, sink, t.TempDir())
		s := buildStack(t, sink, vars)
		s.run()
		if cfg := s.client().must(200, "GET", "/api/v1/auth/config", nil); cfg["email_enabled"] != true {
			t.Fatalf("email_enabled with SMTP configured: %v", cfg["email_enabled"])
		}
		_, aID := s.staff(domain.RoleAgent, 1, team)
		b, bID := s.staff(domain.RoleAgent, 1, team)
		code, body := b.do("PATCH", "/api/v1/me", map[string]any{"notification_opt_out": []string{"auth.reset_password"}})
		if code != 422 || !strings.Contains(string(body), "not_optional") {
			t.Fatalf("opt-out of auth.reset_password: %d %s", code, body)
		}
		b.must(200, "PATCH", "/api/v1/me", map[string]any{"notification_opt_out": []string{"staff.ticket_new"}})

		cust := s.loginFixture(custID, domain.KindCustomer)
		cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Opt-out check", "body_md": "x", "category_id": category}, "Idempotency-Key", "opt-1")
		emailOf := func(id string) string {
			var e string
			if err := s.db.Read().QueryRowContext(t.Context(), `SELECT email FROM users WHERE id = ?`, id).Scan(&e); err != nil {
				t.Fatal(err)
			}
			return e
		}
		s.waitMail(emailOf(aID), 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "Opt-out check") })
		time.Sleep(300 * time.Millisecond)
		if n := len(s.mailsTo(emailOf(bID))); n != 0 {
			t.Fatalf("opted-out agent got %d mails", n)
		}
	})
}

// TestE2E_EmailDisabledReported: without SMTP the full app reports
// email_enabled=false on /auth/config (NOT-022 via notify.EnabledWith).
func TestE2E_EmailDisabledReported(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		vars := e2eEnv(t, db, sink, t.TempDir())
		for _, k := range []string{"FD_SMTP_HOST", "FD_SMTP_PORT", "FD_SMTP_SECURITY", "FD_SMTP_AUTH"} {
			delete(vars, k)
		}
		s := buildStack(t, sink, vars)
		defer s.close()
		if cfg := s.client().must(200, "GET", "/api/v1/auth/config", nil); cfg["email_enabled"] != false {
			t.Fatalf("email_enabled without SMTP: %v", cfg["email_enabled"])
		}
		// Saving a working SMTP config in the admin UI switches it on without
		// a restart (NOT-022): /auth/config is rebuilt after the settings write.
		admin, _ := s.staff(domain.RoleAdmin, 3)
		admin.must(200, "PUT", "/api/v1/admin/email", map[string]any{"smtp": map[string]any{"host": sink.Host(), "port": sink.Port(), "security": "none", "auth": "none"}})
		if cfg := s.client().must(200, "GET", "/api/v1/auth/config", nil); cfg["email_enabled"] != true {
			t.Fatalf("email_enabled after saving SMTP: %v", cfg["email_enabled"])
		}
	})
}
