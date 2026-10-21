// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestSettingsNeverReturnsSecret (PLT-019): on the full app (default
// Registry), an SMTP password saved through PUT /admin/email is stored sealed
// (enc:v1:… ciphertext) and is never echoed: neither the PUT answer nor GET
// /admin/email nor GET /admin/system carries the plaintext, the ciphertext or
// any password value; the API exposes only password_set: true. Every response
// is validated against the OpenAPI spec by the e2e client. Both engines.
func TestSettingsNeverReturnsSecret(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		vars := e2eEnv(t, db, sink, t.TempDir())
		for k := range vars {
			if strings.HasPrefix(k, "ST_SMTP_") { // env would lock the SMTP fields
				delete(vars, k)
			}
		}
		s := buildStack(t, sink, vars)
		admin, _ := s.staff(domain.RoleAdmin, 3)
		const secret = "Pl41ntext-SMTP-s3cret!"
		body := map[string]any{
			"smtp": map[string]any{"host": "smtp.example.test", "port": 587, "security": "starttls", "username": "mailer",
				"password": secret, "auth": "plain", "timeout_seconds": 10},
			"senders": map[string]any{
				"customer": map[string]any{"from_name": "Help", "from_address": "support@example.test"},
				"staff":    map[string]any{"from_address": "desk@example.test", "reply_to": nil},
			},
		}
		code, putBody := admin.do("PUT", "/api/v1/admin/email", body)
		if code != 200 {
			t.Fatalf("PUT /admin/email: %d %s", code, putBody)
		}

		var stored string
		if err := s.db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored, secret) {
			t.Fatalf("plaintext SMTP password stored: %s", stored)
		}
		cipher := regexp.MustCompile(`enc:v1:[A-Za-z0-9+/=_-]+`).FindString(stored)
		if cipher == "" {
			t.Fatalf("stored smtp document holds no enc:v1: ciphertext: %s", stored)
		}

		_, getBody := admin.do("GET", "/api/v1/admin/email", nil)
		_, sysBody := admin.do("GET", "/api/v1/admin/system", nil)
		for name, b := range map[string][]byte{"PUT /admin/email": putBody, "GET /admin/email": getBody, "GET /admin/system": sysBody} {
			text := string(b)
			for _, leak := range []string{secret, cipher, "enc:v1:", "password_enc"} {
				if strings.Contains(text, leak) {
					t.Fatalf("%s leaks %q: %s", name, leak, text)
				}
			}
		}
		var doc struct {
			SMTP map[string]any `json:"smtp"`
		}
		if err := json.Unmarshal(getBody, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.SMTP["password_set"] != true {
			t.Fatalf("password_set = %v: %s", doc.SMTP["password_set"], getBody)
		}
		if v, ok := doc.SMTP["password"]; ok && v != nil && v != "" {
			t.Fatalf("GET /admin/email returns a password value %v", v)
		}
	})
}
