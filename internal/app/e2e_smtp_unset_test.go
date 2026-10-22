// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestReady_SMTPUnset (NOT-001): the full app with no SMTP configured is ready
// (/readyz 200), says email is off in /auth/config, and reports the SMTP status
// as not configured in /admin/system.
func TestReady_SMTPUnset(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		vars := e2eEnv(t, db, sink, t.TempDir())
		for k := range vars {
			if strings.HasPrefix(k, "FD_SMTP_") {
				delete(vars, k)
			}
		}
		s := buildStack(t, sink, vars)
		s.run()
		resp, err := http.Get(s.base + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "ready") {
			t.Fatalf("/readyz without SMTP: %d %q", resp.StatusCode, body)
		}
		resp, err = http.Get(s.base + "/api/v1/auth/config")
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&cfg)
		resp.Body.Close()
		if cfg["email_enabled"] != false {
			t.Fatalf("email_enabled = %v", cfg["email_enabled"])
		}
		admin, _ := s.staff(domain.RoleAdmin, 3)
		sys := admin.must(200, "GET", "/api/v1/admin/system", nil)
		smtp, _ := sys["smtp"].(map[string]any)
		if smtp == nil || smtp["configured"] != false {
			t.Fatalf("admin/system smtp = %v", sys["smtp"])
		}
	})
}
