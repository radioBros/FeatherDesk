// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/notify/smtptest"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return m
}

func TestEmailRoutesInSpec(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		apispec.AssertRoutes(t, e.server().Router())
	})
}

// TestSMTPPasswordWriteOnly: GET never returns it; the DB holds ciphertext.
func TestSMTPPasswordWriteOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		body := map[string]any{
			"smtp":    map[string]any{"host": "smtp.acme.test", "port": 587, "security": "starttls", "username": "mailer", "password": "Sup3rS3cret!", "auth": "plain", "timeout_seconds": 10},
			"senders": map[string]any{"customer": map[string]any{"from_name": "Acme", "from_address": "support@acme.test"}, "staff": map[string]any{"from_address": "noreply@acme.test", "reply_to": nil}},
		}
		resp, b := e.do("PUT", "/api/v1/admin/email", e.admin, body)
		if resp.StatusCode != 200 {
			t.Fatalf("PUT %d %s", resp.StatusCode, b)
		}
		_, b = e.do("GET", "/api/v1/admin/email", e.admin, nil)
		if strings.Contains(string(b), "Sup3rS3cret") || strings.Contains(string(b), "password_enc") {
			t.Fatalf("GET leaks the password: %s", b)
		}
		doc := decode(t, b)
		if doc["smtp"].(map[string]any)["password_set"] != true {
			t.Errorf("password_set not true: %s", b)
		}
		var raw string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw)
		if strings.Contains(raw, "Sup3rS3cret") || !strings.Contains(raw, "enc:v1:") {
			t.Errorf("stored smtp document: %s", raw)
		}
		var auditData string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT data FROM audit_log WHERE action = 'email.settings_updated'`).Scan(&auditData)
		if auditData == "" || strings.Contains(auditData, "Sup3rS3cret") {
			t.Errorf("audit row: %q", auditData)
		}
		// Omitting the password keeps it.
		body["smtp"].(map[string]any)["password"] = nil
		delete(body["smtp"].(map[string]any), "password")
		e.do("PUT", "/api/v1/admin/email", e.admin, body)
		if pw, _ := e.set.SMTPPassword(context.Background()); pw != "Sup3rS3cret!" {
			t.Errorf("password not kept: %q", pw)
		}
	})
}

// TestEnvLocksField: FD_SMTP_HOST locks host; changing it is 422, echoing it is fine.
func TestEnvLocksField(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, map[string]string{"FD_SMTP_HOST": "env-smtp.acme.test"})
		_, b := e.do("GET", "/api/v1/admin/email", e.admin, nil)
		doc := decode(t, b)
		if !strings.Contains(string(b), `"smtp.host"`) || doc["smtp"].(map[string]any)["host"] != "env-smtp.acme.test" {
			t.Fatalf("lock not reported: %s", b)
		}
		resp, b := e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"smtp": map[string]any{"host": "other.acme.test"}, "senders": map[string]any{}})
		if resp.StatusCode != 422 || !strings.Contains(string(b), "locked_by_env") {
			t.Fatalf("PUT locked host: %d %s", resp.StatusCode, b)
		}
		resp, b = e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"smtp": map[string]any{"host": "env-smtp.acme.test", "port": 2525}, "senders": map[string]any{}})
		if resp.StatusCode != 200 {
			t.Fatalf("echoing a locked value: %d %s", resp.StatusCode, b)
		}
	})
}

// TestTemplateTogglesAPI: auth.* can't be disabled (422 not_optional).
func TestTemplateTogglesAPI(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		resp, b := e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"smtp": map[string]any{}, "senders": map[string]any{}, "templates_enabled": map[string]bool{"auth.verify_email": false}})
		if resp.StatusCode != 422 || !strings.Contains(string(b), "not_optional") {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		resp, b = e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"smtp": map[string]any{}, "senders": map[string]any{}, "templates_enabled": map[string]bool{"customer.ticket_received": false}, "triage_mailbox": "triage@acme.test", "plus_addressing": true})
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		doc := decode(t, b)
		if doc["templates_enabled"].(map[string]any)["customer.ticket_received"] != false || doc["triage_mailbox"] != "triage@acme.test" || doc["plus_addressing"] != true {
			t.Errorf("not saved: %s", b)
		}
		resp, b = e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"smtp": map[string]any{"security": "ssl"}, "senders": map[string]any{"staff": map[string]any{"from_address": "not-an-email"}}})
		if resp.StatusCode != 422 {
			t.Errorf("invalid values accepted: %d %s", resp.StatusCode, b)
		}
	})
}

// TestEmailTestUnsavedConfig: a wrong password against a fake SMTP with auth
// → auth_failed, saved settings unchanged; the right one → ok.
func TestEmailTestUnsavedConfig(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.m.SetSender(SMTPSender{})
		srv, err := smtptest.Start(smtptest.Options{Mode: smtptest.StartTLS, Username: "mailer", Password: "right"})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		before, _ := e.set.GetRaw(context.Background(), settings.KeySMTP)
		cfg := func(pw string) map[string]any {
			return map[string]any{"to": "admin@acme.test", "config": map[string]any{
				"smtp":    map[string]any{"host": srv.Host(), "port": srv.Port(), "security": "starttls", "username": "mailer", "password": pw, "auth": "plain", "allow_insecure_tls": true},
				"senders": map[string]any{"staff": map[string]any{"from_address": "noreply@acme.test"}}}}
		}
		resp, b := e.do("POST", "/api/v1/admin/email/test", e.admin, cfg("wrong"))
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		doc := decode(t, b)
		if doc["ok"] != false || doc["error_code"] != "auth_failed" {
			t.Errorf("wrong password: %s", b)
		}
		after, _ := e.set.GetRaw(context.Background(), settings.KeySMTP)
		if string(before) != string(after) {
			t.Error("test endpoint changed saved settings")
		}
		_, b = e.do("POST", "/api/v1/admin/email/test", e.admin, cfg("right"))
		if doc := decode(t, b); doc["ok"] != true || doc["error_code"] != nil {
			t.Errorf("right password: %s", b)
		}
		if len(srv.Messages()) != 1 {
			t.Errorf("test mail not delivered")
		}
		// A refused port reports connection_refused.
		bad := cfg("right")
		bad["config"].(map[string]any)["smtp"].(map[string]any)["port"] = 1
		_, b = e.do("POST", "/api/v1/admin/email/test", e.admin, bad)
		if doc := decode(t, b); doc["error_code"] != "connection_refused" {
			t.Errorf("closed port: %s", b)
		}
		resp, _ = e.do("POST", "/api/v1/admin/email/test", e.admin, map[string]any{"to": "nope"})
		if resp.StatusCode != 422 {
			t.Errorf("invalid to: %d", resp.StatusCode)
		}
	})
}

// TestPreviewEveryTemplate: 200 for every key × locale, 404 for unknown keys
// and non-admins.
func TestPreviewEveryTemplate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		for _, loc := range e.m.Templates().Locales() {
			for _, key := range TemplateKeys {
				resp, b := e.do("GET", "/api/v1/admin/email/templates/"+key+"/preview?locale="+loc, e.admin, nil)
				if resp.StatusCode != 200 || !strings.Contains(string(b), "<html") {
					t.Errorf("%s/%s: %d", loc, key, resp.StatusCode)
				}
			}
		}
		if resp, _ := e.do("GET", "/api/v1/admin/email/templates/nope.key/preview", e.admin, nil); resp.StatusCode != 404 {
			t.Errorf("unknown key: %d", resp.StatusCode)
		}
		if resp, _ := e.do("GET", "/api/v1/admin/email/templates/customer.reply/preview", e.customer, nil); resp.StatusCode != 404 {
			t.Errorf("customer: %d", resp.StatusCode)
		}
		// Product name change applies to the next render without restart.
		br := settings.DefaultBranding()
		br.ProductName = "Renamed Desk"
		_ = e.set.Put(context.Background(), settings.KeyBranding, br, "")
		_, b := e.do("GET", "/api/v1/admin/email/templates/auth.verify_email/preview", e.admin, nil)
		if !strings.Contains(string(b), "Renamed Desk") {
			t.Error("branding change not applied")
		}
	})
}

// TestEmailAdminOnly: non-admins never reach the email admin API.
func TestEmailAdminOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		for who, want := range map[string]int{e.customer: 404, e.agent1: 403, e.supervisor: 403, "": 401, e.admin: 200} {
			if resp, _ := e.do("GET", "/api/v1/admin/email", who, nil); resp.StatusCode != want {
				t.Errorf("%s: %d, want %d", who, resp.StatusCode, want)
			}
		}
	})
}

// TestSignatureAPI: staff GET/PUT; customers get 404; too long → 422.
func TestSignatureAPI(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		resp, b := e.do("PUT", "/api/v1/me/signature", e.agent1, map[string]string{"signature_md": "Grace\n**Support**"})
		if resp.StatusCode != 200 || decode(t, b)["signature_md"] != "Grace\n**Support**" {
			t.Fatalf("%d %s", resp.StatusCode, b)
		}
		_, b = e.do("GET", "/api/v1/me/signature", e.agent1, nil)
		if decode(t, b)["signature_md"] != "Grace\n**Support**" {
			t.Errorf("GET %s", b)
		}
		if resp, _ := e.do("PUT", "/api/v1/me/signature", e.customer, map[string]string{"signature_md": "x"}); resp.StatusCode != 404 {
			t.Errorf("customer PUT: %d", resp.StatusCode)
		}
		if resp, _ := e.do("GET", "/api/v1/me/signature", e.customer, nil); resp.StatusCode != 404 {
			t.Errorf("customer GET: %d", resp.StatusCode)
		}
		if resp, _ := e.do("PUT", "/api/v1/me/signature", e.agent1, map[string]string{"signature_md": strings.Repeat("x", 4001)}); resp.StatusCode != 422 {
			t.Errorf("too long: %d", resp.StatusCode)
		}
	})
}

// TestReportSystem: /admin/system smtp block.
func TestReportSystem(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		doc := map[string]any{}
		e.m.ReportSystem(context.Background(), doc)
		s := doc["smtp"].(map[string]any)
		if s["configured"] != true || s["ok"] != true || s["last_error"] != nil {
			t.Errorf("smtp = %v", s)
		}
		e.m.recordFailure(smtpErr("auth_failed", false))
		e.m.ReportSystem(context.Background(), doc)
		if s := doc["smtp"].(map[string]any); s["ok"] != false || s["last_error"] == nil {
			t.Errorf("after failure smtp = %v", s)
		}
	})
}

// TestAdminEmail_InboundBlocklist: the inbound sender blocklist the mailin
// pipeline enforces (NOT-028) is editable through PUT /admin/email:
// normalised (lower-case, deduplicated, sorted), invalid entries are 422.
func TestAdminEmail_InboundBlocklist(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		resp, b := e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"inbound_blocklist": []string{" Spam@Bad.Example ", "@junk.example", "junk.example", "@junk.example"}})
		if resp.StatusCode != 200 {
			t.Fatalf("put: %d %s", resp.StatusCode, b)
		}
		_, b = e.do("GET", "/api/v1/admin/email", e.admin, nil)
		var got struct {
			InboundBlocklist []string `json:"inbound_blocklist"`
		}
		_ = json.Unmarshal(b, &got)
		if want := []string{"@junk.example", "junk.example", "spam@bad.example"}; !slices.Equal(got.InboundBlocklist, want) {
			t.Fatalf("blocklist %v, want %v", got.InboundBlocklist, want)
		}
		em, _ := e.set.Email(context.Background())
		if len(em.InboundBlocklist) != 3 {
			t.Fatalf("stored %v", em.InboundBlocklist)
		}
		resp, b = e.do("PUT", "/api/v1/admin/email", e.admin, map[string]any{"inbound_blocklist": []string{"not a domain", "ok.example"}})
		if resp.StatusCode != 422 || !strings.Contains(string(b), "inbound_blocklist[0]") {
			t.Fatalf("invalid entry: %d %s", resp.StatusCode, b)
		}
	})
}
