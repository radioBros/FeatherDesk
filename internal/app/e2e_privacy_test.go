// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestE2E_Privacy covers the D-19 §C wiring on the full app: consent at
// registration (authn → privacy hook), the export job on the real queue with
// the privacy.export_ready mail (notify), the download, and an admin erasure
// whose redaction reaches the search index (tickets service) and the agent
// ticket list.
func TestE2E_Privacy(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		ctx := context.Background()
		set := s.a.Deps.Settings
		b, err := set.Branding(ctx)
		if err != nil {
			t.Fatal(err)
		}
		b.Legal.PrivacyURL = "https://acme.test/privacy"
		if err := set.Put(ctx, settings.KeyBranding, b, ""); err != nil {
			t.Fatal(err)
		}
		s.run()
		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, _ := s.staff(domain.RoleAgent, 1, team)

		// Consent: required when a privacy policy URL is set, then recorded.
		cust := s.client()
		email := fmt.Sprintf("grace.%d@customer.test", time.Now().UnixNano())
		reg := map[string]any{"email": email, "name": "Grace Privacy", "password": authntest.TestPassword, "locale": "en"}
		if code, body := cust.do("POST", "/api/v1/auth/register", reg); code != 422 || !strings.Contains(string(body), "accept_privacy") {
			t.Fatalf("register without consent: %d %s", code, body)
		}
		reg["accept_privacy"] = true
		cust.must(202, "POST", "/api/v1/auth/register", reg)
		verify := s.waitMail(email, 10*time.Second, func(m sentMail) bool { return tokenURL.MatchString(m.Text) })
		cust.must(200, "POST", "/api/v1/auth/verify-email", map[string]any{"token": authntest.TokenFromURL(tokenURL.FindString(verify.Text))})
		cust = s.login(email, domain.KindCustomer)
		var consents int
		if err := s.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_consents c JOIN users u ON u.id = c.user_id
			WHERE u.email = ? AND c.policy_url = 'https://acme.test/privacy' AND c.ip <> ''`, email).Scan(&consents); err != nil || consents != 1 {
			t.Fatalf("consent rows = %d, %v", consents, err)
		}
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Privacy e2e", "body_md": "my e2esecretword", "category_id": category},
			"Idempotency-Key", "prv-1")
		ticketID, _ := tk["id"].(string)

		// Export through the queue; the ready mail links to the portal page.
		exp := cust.must(202, "POST", "/api/v1/me/privacy/export", nil)
		ready := s.waitMail(email, 15*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "data export is ready") })
		if !strings.Contains(ready.Text, "/profile/data") || !strings.Contains(ready.Text, "7 days") {
			t.Fatalf("ready mail: %s", ready.Text)
		}
		var url string
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && url == ""; time.Sleep(20 * time.Millisecond) {
			mp := cust.must(200, "GET", "/api/v1/me/privacy", nil)
			reqs, _ := mp["requests"].([]any)
			for _, r := range reqs {
				if m, _ := r.(map[string]any); m["id"] == exp["id"] && m["download_url"] != nil {
					url, _ = m["download_url"].(string)
				}
			}
		}
		if url == "" {
			t.Fatal("export never became ready")
		}
		code, zipBytes := cust.do("GET", url, nil)
		if code != 200 {
			t.Fatalf("download: %d", code)
		}
		zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			t.Fatal(err)
		}
		var all strings.Builder
		for _, f := range zr.File {
			rc, _ := f.Open()
			c, _ := io.ReadAll(rc)
			rc.Close()
			all.Write(c)
		}
		for _, want := range []string{"e2esecretword", "https://acme.test/privacy", "Grace Privacy"} {
			if !strings.Contains(all.String(), want) {
				t.Errorf("export lacks %q", want)
			}
		}

		// Admin erasure: the name leaves the agent list, the text leaves search.
		if _, body := agent.do("GET", "/api/v1/agent/tickets", nil); !strings.Contains(string(body), "Grace Privacy") {
			t.Fatalf("precondition: list shows the requester: %s", body)
		}
		var custID string
		if err := s.db.Read().QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&custID); err != nil {
			t.Fatal(err)
		}
		res := admin.must(200, "POST", "/api/v1/admin/privacy/users/"+custID+"/erase", map[string]any{"mode": "anonymize_and_redact"})
		if name, _ := res["name"].(string); !strings.HasPrefix(name, "Deleted user #") {
			t.Fatalf("erase result: %v", res)
		}
		if _, body := agent.do("GET", "/api/v1/agent/tickets", nil); strings.Contains(string(body), "Grace Privacy") || !strings.Contains(string(body), ticketID) {
			t.Fatalf("agent list after erasure: %s", body)
		}
		if _, body := agent.do("GET", "/api/v1/agent/tickets?q=e2esecretword", nil); strings.Contains(string(body), ticketID) {
			t.Fatalf("search still finds redacted text: %s", body)
		}
		// The erased customer's session is gone.
		if code, _ := cust.do("GET", "/api/v1/me/privacy", nil); code != 401 {
			t.Fatalf("erased user's session still works: %d", code)
		}
	})
}
