// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/radioBros/FeatherDesk/internal/app"
	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// dataDirs keeps one FD_DATA_DIR per database, shared by the seeder and
// stackFor (blobs such as export archives live there).
var dataDirs sync.Map // *store.DB → string

func testEnv(t *testing.T, db *store.DB) func(string) string {
	t.Helper()
	dir, _ := dataDirs.LoadOrStore(db, t.TempDir())
	vars := map[string]string{
		"FD_SECRET_KEY": strings.Repeat("d", 32), "FD_DATA_DIR": dir.(string), "FD_PUBLIC_URL": "http://localhost:8080",
		"FD_DATABASE_URL": db.URL, "FD_LOG_FORMAT": "text",
	}
	return func(k string) string { return vars[k] }
}

// fixedNow keeps the simulated history identical between runs.
var fixedNow = time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)

func run(t *testing.T, db *store.DB, opt Options) (*Summary, error) {
	t.Helper()
	authntest.FastArgon()
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	return Run(context.Background(), testEnv(t, db), opt)
}

func column(t *testing.T, db *store.DB, q string) map[string]int {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		out[k] = n
	}
	return out
}

func count(t *testing.T, db *store.DB, q string) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func wantAll(t *testing.T, what string, got map[string]int, want ...string) {
	t.Helper()
	for _, w := range want {
		if got[w] == 0 {
			t.Errorf("%s: %q is missing (have %v)", what, w, got)
		}
	}
}

// stackFor builds a second, read-only-in-spirit app on the seeded database
// (like `serve` would) to look at the data through the real API.
func stackFor(t *testing.T, db *store.DB) (http.Handler, func() *client) {
	t.Helper()
	env := seedEnv(testEnv(t, db))
	cfg, err := config.Load(env)
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Build(context.Background(), cfg, app.BuildOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Getenv: env,
		SPA: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	for _, m := range a.Modules {
		if am, ok := m.(*authn.Module); ok {
			am.SetThrottle(false)
		}
	}
	n := 0
	return a.Server.Handler(), func() *client {
		n++
		return newClient(a.Server.Handler(), cfg.PublicOrigin(), "localhost:8080", fmt.Sprintf("10.99.0.%d", n))
	}
}

func TestSeed_CoversEveryFeature(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sum, err := run(t, db, Options{Now: fixedNow})
		if err != nil {
			t.Fatal(err)
		}
		// Entities.
		for name, min := range map[string]int{"organizations": 3, "staff": 11, "customers": 12, "teams": 3, "business calendars": 2, "categories": 4,
			"custom fields": 7, "SLA policies": 4, "tickets": 60, "internal notes": 20, "attachments": 4, "ticket links": 3, "watchers (CC)": 3,
			"CSAT ratings": 5, "canned responses": 6, "saved views": 3, "KB categories": 5, "KB articles": 15, "KB translations": 30,
			"webhooks": 1, "webhook deliveries": 2, "inbound mailboxes": 1, "inbound log entries": 4, "API tokens": 1, "audit log entries": 30,
			"automation rules": 6, "automation rule runs": 1, "macros": 6, "MFA-enrolled users": 1, "privacy requests": 4, "consent records": 3,
			"notifications": 100, "unread notifications": 20} {
			if got := sum.Count(name); got < min {
				t.Errorf("%s: %d, want at least %d", name, got, min)
			}
		}
		if sum.APIToken == "" || !strings.HasPrefix(sum.APIToken, authn.PATPrefix) {
			t.Errorf("API token = %q", sum.APIToken)
		}
		if len(sum.Accounts) < 20 || sum.PortalURL == "" || sum.AgentURL == "" {
			t.Errorf("summary incomplete: %+v", sum)
		}
		// Ticket dimensions.
		wantAll(t, "status", column(t, db, `SELECT status, COUNT(*) FROM tickets GROUP BY status`), "new", "open", "pending", "on_hold", "resolved", "closed")
		wantAll(t, "priority", column(t, db, `SELECT priority, COUNT(*) FROM tickets GROUP BY priority`), "low", "normal", "high", "urgent")
		wantAll(t, "channel", column(t, db, `SELECT channel, COUNT(*) FROM tickets GROUP BY channel`), "portal", "agent", "api", "email")
		wantAll(t, "tier", column(t, db, `SELECT CAST(tier AS TEXT), COUNT(*) FROM tickets GROUP BY tier`), "1", "2", "3")
		wantAll(t, "field type", column(t, db, `SELECT type, COUNT(*) FROM custom_field_defs GROUP BY type`),
			"text", "textarea", "number", "date", "select", "multiselect", "checkbox")
		wantAll(t, "link type", column(t, db, `SELECT type, COUNT(*) FROM ticket_links GROUP BY type`), "parent_of", "related_to", "duplicate_of")
		wantAll(t, "kb visibility", column(t, db, `SELECT visibility, COUNT(*) FROM kb_articles GROUP BY visibility`), "public", "customers", "internal")
		wantAll(t, "kb status", column(t, db, `SELECT status, COUNT(*) FROM kb_articles GROUP BY status`), "draft", "published", "archived")
		wantAll(t, "kb locale", column(t, db, `SELECT locale, COUNT(*) FROM kb_article_translations GROUP BY locale`), "en", "it", "de", "fr", "es")
		wantAll(t, "message kind", column(t, db, `SELECT kind, COUNT(*) FROM ticket_messages GROUP BY kind`), "public", "internal", "system")
		wantAll(t, "events", column(t, db, `SELECT type, COUNT(*) FROM ticket_events GROUP BY type`),
			"ticket.created", "ticket.escalated", "ticket.deescalated", "ticket.merged", "ticket.split", "ticket.followup_created", "ticket.csat",
			"sla.warning", "sla.breached", "note.created", "message.created", "ticket.assigned", "ticket.watchers_changed", "ticket.tags_changed", "ticket.fields_changed",
			"ticket.automation_applied")
		wantAll(t, "rule event", column(t, db, `SELECT event, COUNT(*) FROM automation_rules GROUP BY event`), "ticket.created", "message.created", "ticket.updated", "time")
		wantAll(t, "assignment strategy", column(t, db, `SELECT assignment_strategy, COUNT(*) FROM teams GROUP BY assignment_strategy`), "manual", "round_robin", "least_busy")
		wantAll(t, "privacy request", column(t, db, `SELECT type || ':' || status, COUNT(*) FROM privacy_requests GROUP BY type, status`),
			"export:ready", "erasure:pending", "erasure:rejected")
		wantAll(t, "notification type", column(t, db, `SELECT type, COUNT(*) FROM notifications GROUP BY type`),
			"staff.ticket_new", "staff.customer_replied", "staff.escalated", "staff.mentioned", "staff.sla", "customer.ticket_received", "customer.reply", "customer.resolved")
		wantAll(t, "notified kind", column(t, db, `SELECT u.kind, COUNT(*) FROM notifications n JOIN users u ON u.id = n.user_id GROUP BY u.kind`), "staff", "customer")
		wantAll(t, "user status", column(t, db, `SELECT status, COUNT(*) FROM users GROUP BY status`), "active", "pending_verification", "disabled")
		wantAll(t, "customer role", column(t, db, `SELECT role, COUNT(*) FROM users WHERE kind = 'customer' GROUP BY role`), "requester", "org_manager")
		wantAll(t, "staff role", column(t, db, `SELECT role, COUNT(*) FROM users WHERE kind = 'staff' GROUP BY role`), "admin", "supervisor", "agent")
		wantAll(t, "inbound status", column(t, db, `SELECT status, COUNT(*) FROM inbound_messages GROUP BY status`), "processed", "quarantined")
		wantAll(t, "attachment type", column(t, db, `SELECT CASE WHEN content_type LIKE 'text/plain%' THEN 'text/plain' ELSE content_type END AS ct, COUNT(*) FROM attachments GROUP BY 1`), "image/png", "text/plain")
		wantAll(t, "notify mode", column(t, db, `SELECT notify_mode, COUNT(*) FROM teams GROUP BY notify_mode`), "agents", "mailbox", "both")
		for q, name := range map[string]string{
			`SELECT COUNT(*) FROM users WHERE idp_managed`:                                       "idp_managed user",
			`SELECT COUNT(*) FROM users WHERE kind = 'staff' AND signature_md <> ''`:             "staff signature",
			`SELECT COUNT(*) FROM tickets WHERE csat_score IS NOT NULL AND csat_score <= 2`:      "low CSAT",
			`SELECT COUNT(*) FROM tickets WHERE followup_of_id IS NOT NULL`:                      "follow-up ticket",
			`SELECT COUNT(*) FROM tickets WHERE team_id IS NULL AND status = 'new'`:              "triage ticket",
			`SELECT COUNT(*) FROM tickets WHERE assignee_id IS NULL`:                             "unassigned ticket",
			`SELECT COUNT(*) FROM tickets WHERE assignee_id IS NOT NULL`:                         "assigned ticket",
			`SELECT COUNT(*) FROM ticket_field_values WHERE value_num IS NOT NULL`:               "numeric field value",
			`SELECT COUNT(*) FROM ticket_field_values WHERE value_date IS NOT NULL`:              "date field value",
			`SELECT COUNT(*) FROM ticket_field_values WHERE value_json IS NOT NULL`:              "json field value",
			`SELECT COUNT(*) FROM ticket_field_values WHERE value_text IS NOT NULL`:              "text field value",
			`SELECT COUNT(*) FROM custom_field_defs WHERE required`:                              "required field",
			`SELECT COUNT(*) FROM custom_field_defs WHERE NOT customer_visible`:                  "staff-only field",
			`SELECT COUNT(*) FROM custom_field_defs WHERE customer_visible`:                      "customer-visible field",
			`SELECT COUNT(*) FROM business_calendars WHERE holidays <> '[]'`:                     "calendar with holidays",
			`SELECT COUNT(*) FROM teams WHERE notification_email IS NOT NULL`:                    "team notification email",
			`SELECT COUNT(*) FROM canned_responses WHERE team_id IS NULL`:                        "global canned response",
			`SELECT COUNT(*) FROM canned_responses WHERE team_id IS NOT NULL`:                    "team canned response",
			`SELECT COUNT(*) FROM saved_views WHERE shared`:                                      "shared view",
			`SELECT COUNT(*) FROM saved_views WHERE NOT shared`:                                  "personal view",
			`SELECT COUNT(*) FROM kb_categories WHERE parent_id IS NOT NULL`:                     "nested KB category",
			`SELECT COUNT(*) FROM kb_articles WHERE helpful_yes > 0`:                             "helpful votes",
			`SELECT COUNT(*) FROM webhooks WHERE NOT active AND url = 'http://localhost:9/demo'`: "disabled demo webhook",
			`SELECT COUNT(*) FROM inbound_mailboxes WHERE NOT active`:                            "inactive mailbox",
			`SELECT COUNT(*) FROM branding_assets`:                                               "branding asset",
			`SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL AND scopes <> '[]'`:        "admin API token",
			`SELECT COUNT(*) FROM automation_rules WHERE enabled`:                                "enabled rule",
			`SELECT COUNT(*) FROM automation_rules WHERE NOT enabled`:                            "disabled rule",
			`SELECT COUNT(*) FROM automation_time_runs`:                                          "time rule run",
			`SELECT COUNT(*) FROM macros WHERE owner_id IS NULL`:                                 "shared macro",
			`SELECT COUNT(*) FROM macros WHERE owner_id IS NOT NULL`:                             "personal macro",
			`SELECT COUNT(*) FROM users WHERE away_since IS NOT NULL`:                            "away agent",
			`SELECT COUNT(*) FROM privacy_requests WHERE status = 'ready' AND file_size > 0`:     "ready export file",
			`SELECT COUNT(*) FROM privacy_requests WHERE include_internal`:                       "admin export",
			`SELECT COUNT(*) FROM user_consents WHERE policy_version <> ''`:                      "versioned consent",
			`SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`:                           "unread notification",
			`SELECT COUNT(*) FROM notifications WHERE read_at IS NOT NULL`:                       "read notification",
			`SELECT COUNT(*) FROM sessions`:                                                      "",
		} {
			n := count(t, db, q)
			if name == "" {
				if n != 0 {
					t.Errorf("%d sessions left behind", n)
				}
				continue
			}
			if n == 0 {
				t.Errorf("no %s (%s)", name, q)
			}
		}
		// Two months of history.
		var oldest, newest time.Time
		var a, b any
		if err := db.Read().QueryRowContext(context.Background(), `SELECT MIN(created_at), MAX(created_at) FROM tickets`).Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		oldest, newest = timeOf(t, a), timeOf(t, b)
		if span := newest.Sub(oldest); span < 50*24*time.Hour || oldest.After(fixedNow.AddDate(0, 0, -50)) || newest.Before(fixedNow.Add(-6*time.Hour)) {
			t.Errorf("ticket history %s .. %s is not a believable two months", oldest, newest)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM ticket_events WHERE created_at > `+futureLiteral(db)); n != 0 {
			t.Errorf("%d events dated after the seeding moment", n)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM notifications WHERE created_at > `+futureLiteral(db)+` OR read_at < created_at`); n != 0 {
			t.Errorf("%d notifications dated after the seeding moment or read before they existed", n)
		}
		// Retention never touches two months of demo history; erasure stays manual.
		var ret, prv string
		for key, dst := range map[string]*string{"retention": &ret, "privacy": &prv} {
			if err := db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = ?`, key).Scan(dst); err != nil {
				t.Fatalf("settings %s: %v", key, err)
			}
		}
		var rv, pv map[string]any
		if err := json.Unmarshal([]byte(ret), &rv); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(prv), &pv); err != nil {
			t.Fatal(err)
		}
		if rv["closed_tickets_months"] != float64(24) || rv["inbound_email_days"] != float64(90) || pv["erasure_auto_approve_days"] != float64(0) || pv["policy_version"] == "" {
			t.Errorf("privacy settings: %s / %s", ret, prv)
		}
		// Settings the demo promises.
		var br, mail, smtp string
		for key, dst := range map[string]*string{"branding": &br, "email": &mail, "smtp": &smtp} {
			if err := db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = ?`, key).Scan(dst); err != nil {
				t.Fatalf("settings %s: %v", key, err)
			}
		}
		if !strings.Contains(br, "FeatherDesk Demo") || !strings.Contains(br, "privacy_url") {
			t.Errorf("branding: %s", br)
		}
		if !strings.Contains(mail, "triage@demo.test") {
			t.Errorf("email settings: %s", mail)
		}
		var sm map[string]any
		if err := json.Unmarshal([]byte(smtp), &sm); err != nil {
			t.Fatal(err)
		}
		if h, _ := sm["host"].(string); h != "" {
			t.Errorf("email must stay disabled without --smtp-mailpit: %s", smtp)
		}
		// The dispatcher cursor is at the end: a server started on this data
		// replays nothing.
		var cur, last string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT text_value FROM counters WHERE name = 'dispatch_cursor'`).Scan(&cur); err != nil {
			t.Fatal(err)
		}
		if err := db.Read().QueryRowContext(context.Background(), `SELECT MAX(id) FROM ticket_events`).Scan(&last); err != nil || cur != last {
			t.Errorf("dispatch cursor %q, last event %q (%v)", cur, last, err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM deferred_jobs`); n != 0 {
			t.Errorf("%d deferred jobs would be sent by a server", n)
		}
		checkMFAAndExports(t, db, sum)
	})
}

// checkMFAAndExports signs in through a fresh app on the seeded data: the
// enrolled user needs the second step (TOTP and recovery codes from the
// summary work), the admin and the supervisor do not, and the admin can
// download a ready export.
func checkMFAAndExports(t *testing.T, db *store.DB, sum *Summary) {
	t.Helper()
	if len(sum.MFA) != 1 || sum.MFA[0].Email != "tomas.tran@demo.test" || sum.MFA[0].Secret == "" || len(sum.MFA[0].RecoveryCodes) != 10 {
		t.Fatalf("summary MFA: %+v", sum.MFA)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM user_mfa m JOIN users u ON u.id = m.user_id WHERE u.email = 'tomas.tran@demo.test' AND m.totp_enabled_at IS NOT NULL`); n != 1 {
		t.Errorf("tomas.tran is not TOTP-enrolled")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM user_mfa`); n != 1 {
		t.Errorf("%d users with an MFA row, want only the demo one", n)
	}
	var sec string
	if err := db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'security'`).Scan(&sec); err == nil {
		var sv map[string]any
		if err := json.Unmarshal([]byte(sec), &sv); err != nil || (sv["mfa_required"] != nil && sv["mfa_required"] != "none") {
			t.Errorf("two-step verification must stay optional: %s", sec)
		}
	}
	_, newClient := stackFor(t, db)
	for _, email := range []string{"admin@demo.test", "supervisor@demo.test"} {
		c := newClient()
		if err := c.login("staff", email, DefaultPassword); err != nil || c.userID == "" {
			t.Errorf("%s must sign in without a second step: %v", email, err)
		}
	}
	m := sum.MFA[0]
	secondStep := func(method, code string) error {
		c := newClient()
		var ch struct {
			MFARequired bool   `json:"mfa_required"`
			MFAToken    string `json:"mfa_token"`
		}
		if err := c.post("/auth/login", map[string]string{"audience": "staff", "email": m.Email, "password": DefaultPassword}, &ch); err != nil {
			return err
		}
		if !ch.MFARequired || ch.MFAToken == "" {
			return fmt.Errorf("no MFA challenge for %s", m.Email)
		}
		var me struct {
			ID string `json:"id"`
		}
		if err := c.post("/auth/mfa/verify", map[string]string{"mfa_token": ch.MFAToken, "method": method, "code": code}, &me); err != nil {
			return err
		}
		if me.ID == "" {
			return fmt.Errorf("%s: no session", method)
		}
		return nil
	}
	// The next 30-second step: the enrolment consumed the current one.
	code, err := authn.TOTPCode(m.Secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := secondStep("totp", code); err != nil {
		t.Errorf("TOTP sign-in: %v", err)
	}
	if err := secondStep("recovery", m.RecoveryCodes[0]); err != nil {
		t.Errorf("recovery code sign-in: %v", err)
	}
	// Every ready export is a real ZIP archive in blob storage (checked on
	// disk: the download link expires relative to the wall clock).
	dir, _ := dataDirs.Load(db)
	rows, err := db.Read().QueryContext(context.Background(), `SELECT file_key FROM privacy_requests WHERE type = 'export' AND status = 'ready'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir.(string), filepath.FromSlash(key)))
		if err != nil || !bytes.HasPrefix(b, []byte("PK")) {
			t.Errorf("export archive %s: %d bytes, %v", key, len(b), err)
		}
		n++
	}
	if n < 2 {
		t.Errorf("%d ready exports, want at least 2", n)
	}
}

func timeOf(t *testing.T, v any) time.Time {
	t.Helper()
	switch x := v.(type) {
	case time.Time:
		return x.UTC()
	case string:
		for _, l := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999"} {
			if ts, err := time.Parse(l, x); err == nil {
				return ts.UTC()
			}
		}
	}
	t.Fatalf("cannot read time %v (%T)", v, v)
	return time.Time{}
}

// futureLiteral is a timestamp literal slightly after "now" for both engines.
func futureLiteral(db *store.DB) string {
	return "'" + time.Now().UTC().Add(2*time.Minute).Format("2006-01-02 15:04:05") + "'"
}

func TestSeed_InternalNotesNeverReachCustomers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		if _, err := run(t, db, Options{Now: fixedNow}); err != nil {
			t.Fatal(err)
		}
		_, newClient := stackFor(t, db)
		type row struct{ id, email, note string }
		rows, err := db.Read().QueryContext(context.Background(), `SELECT t.id, u.email, m.body_md FROM ticket_messages m
			JOIN tickets t ON t.id = m.ticket_id JOIN users u ON u.id = t.requester_id
			WHERE m.kind = 'internal' AND u.status = 'active' ORDER BY t.id, m.created_at`)
		if err != nil {
			t.Fatal(err)
		}
		notes := map[string][]row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.email, &r.note); err != nil {
				t.Fatal(err)
			}
			notes[r.id] = append(notes[r.id], r)
		}
		rows.Close()
		if len(notes) < 10 {
			t.Fatalf("only %d tickets with internal notes", len(notes))
		}
		sessions := map[string]*client{}
		checked := 0
		for id, list := range notes {
			email := list[0].email
			c := sessions[email]
			if c == nil {
				c = newClient()
				if err := c.login("customer", email, DefaultPassword); err != nil {
					t.Fatalf("customer login %s: %v", email, err)
				}
				sessions[email] = c
			}
			var raw json.RawMessage
			if err := c.get("/portal/tickets/"+id, &raw); err != nil {
				t.Fatalf("portal ticket %s: %v", id, err)
			}
			var parsed struct {
				Messages []struct {
					Kind   string `json:"kind"`
					BodyMD string `json:"body_md"`
				} `json:"messages"`
				CustomFields map[string]any `json:"custom_fields"`
			}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatal(err)
			}
			for _, m := range parsed.Messages {
				if m.Kind != "public" {
					t.Errorf("ticket %s: customer API returned a %q message", id, m.Kind)
				}
			}
			for _, n := range list {
				if strings.Contains(string(raw), n.note[:min(len(n.note), 40)]) {
					t.Errorf("ticket %s: internal note leaked to the customer: %q", id, n.note)
				}
			}
			for _, staffOnly := range []string{"root_cause", "refund_amount", "internal_ref"} {
				if _, ok := parsed.CustomFields[staffOnly]; ok {
					t.Errorf("ticket %s: staff-only field %s visible to the customer", id, staffOnly)
				}
			}
			checked++
		}
		t.Logf("checked %d tickets with internal notes", checked)
		// ... and the agent API does show them (the check above is not vacuous).
		agent := newClient()
		if err := agent.login("staff", "supervisor@demo.test", DefaultPassword); err != nil {
			t.Fatal(err)
		}
		for id := range notes {
			var raw json.RawMessage
			if err := agent.get("/agent/tickets/"+id, &raw); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"internal"`) {
				t.Errorf("supervisor does not see the internal notes of %s", id)
			}
			break
		}
		// The agent can sign in with the demo password and finds the data.
		var tickets struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := agent.get("/agent/tickets?limit=50", &tickets); err != nil || len(tickets.Items) < 30 {
			t.Errorf("agent ticket list: %d items, %v", len(tickets.Items), err)
		}
		var found struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := agent.get("/agent/tickets?q=VPN", &found); err != nil || len(found.Items) == 0 {
			t.Errorf("search finds nothing for VPN: %d, %v", len(found.Items), err)
		}
	})
}

func TestSeed_RefusesForeignData(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		// Foreign data: not empty, not from the demo.
		storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
		if _, err := run(t, db, Options{Tickets: 33, Force: true, Now: fixedNow}); !errors.Is(err, ErrNotDemo) {
			t.Fatalf("seeding a foreign database must be refused even with --force, got %v", err)
		}
		if n := count(t, db, `SELECT COUNT(*) FROM tickets`); n != 0 {
			t.Fatalf("a refused run wrote %d tickets", n)
		}
	})
}

func TestSeed_RefusesSecondRunAndForce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		if _, err := run(t, db, Options{Tickets: 33, Now: fixedNow}); err != nil {
			t.Fatal(err)
		}
		before := count(t, db, `SELECT COUNT(*) FROM ticket_events`)
		if _, err := run(t, db, Options{Tickets: 33, Now: fixedNow}); !errors.Is(err, ErrAlreadySeeded) {
			t.Fatalf("second run without --force: %v", err)
		}
		if after := count(t, db, `SELECT COUNT(*) FROM ticket_events`); after != before {
			t.Fatalf("a refused run changed the data (%d -> %d events)", before, after)
		}
		// --force wipes the demo and seeds it again.
		sum, err := run(t, db, Options{Tickets: 36, Force: true, Now: fixedNow, SMTPMailpit: true, Password: "another-demo-pass-77"})
		if err != nil {
			t.Fatalf("--force on a demo database: %v", err)
		}
		if got := count(t, db, `SELECT COUNT(*) FROM tickets`); got != 36 || sum.Count("tickets") != 36 {
			t.Errorf("tickets after --force: %d / %d", got, sum.Count("tickets"))
		}
		// 24 before D-20, plus the Mobile-only agent and two Demo HQ employees.
		if got := count(t, db, `SELECT COUNT(*) FROM users`); got != 27 {
			t.Errorf("users after --force: %d, want 27 (no leftovers)", got)
		}
		for q, want := range map[string]int{
			`SELECT COUNT(*) FROM user_mfa`:           1,
			`SELECT COUNT(*) FROM mfa_recovery_codes`: 10,
			`SELECT COUNT(*) FROM privacy_requests`:   5,
			`SELECT COUNT(*) FROM user_consents`:      4,
			`SELECT COUNT(*) FROM automation_rules`:   10, // two project rules (D-20)
			`SELECT COUNT(*) FROM macros`:             9,  // one Mobile macro (D-20)
		} {
			if got := count(t, db, q); got != want {
				t.Errorf("after --force %s = %d, want %d (no leftovers)", q, got, want)
			}
		}
		if got := count(t, db, `SELECT COUNT(*) FROM notifications`); got != sum.Count("notifications") {
			t.Errorf("notifications after --force: %d, summary %d", got, sum.Count("notifications"))
		}
		if got := count(t, db, `SELECT MIN(number) FROM tickets`); got != 1001 {
			t.Errorf("ticket numbers restart at 1001, got %d", got)
		}
		var smtp string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&smtp); err != nil {
			t.Fatal(err)
		}
		var sm map[string]any
		if err := json.Unmarshal([]byte(smtp), &sm); err != nil {
			t.Fatal(err)
		}
		if sm["host"] != "localhost" || sm["port"] != float64(1025) || sm["auth"] != "none" || sm["security"] != "none" {
			t.Errorf("--smtp-mailpit: %s", smtp)
		}
		_, newClient := stackFor(t, db)
		c := newClient()
		if err := c.login("staff", "admin@demo.test", "another-demo-pass-77"); err != nil {
			t.Errorf("the new password does not work: %v", err)
		}
	})
}

func TestSeed_RejectsWeakPassword(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	if _, err := run(t, db, Options{Password: "short"}); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("weak password accepted: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM users`); n != 0 {
		t.Fatalf("%d users written", n)
	}
}

func TestSeed_Deterministic(t *testing.T) {
	snapshot := func(t *testing.T, db *store.DB) string {
		rows, err := db.Read().QueryContext(context.Background(), `SELECT number, subject, status, priority, channel, tier, created_at FROM tickets ORDER BY number`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var n, tier int
			var sub, st, pr, ch string
			var at any
			if err := rows.Scan(&n, &sub, &st, &pr, &ch, &tier, &at); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%d|%s|%s|%s|%s|%d|%s\n", n, sub, st, pr, ch, tier, timeOf(t, at).Format(time.RFC3339))
		}
		return b.String()
	}
	var first string
	for i := 0; i < 2; i++ {
		db := storetest.Open(t, store.SQLite)
		if _, err := run(t, db, Options{Now: fixedNow}); err != nil {
			t.Fatal(err)
		}
		got := snapshot(t, db)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Errorf("two seeds with the same options differ:\n%s\n---\n%s", first, got)
		}
	}
}

func TestSeed_FewTicketsStillCoversScenarios(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	sum, err := run(t, db, Options{Tickets: 5, Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if n := sum.Count("tickets"); n < 30 {
		t.Errorf("the scripted scenarios are always created, got %d tickets", n)
	}
}

// TestSeed_Projects (D-20): three projects with tickets in each, refs on
// every ticket, organization limits respected, staff-only IT never on the
// portal or by mail, and the Mobile-only agent sees Mobile tickets only
// through the real API (I-11).
func TestSeed_Projects(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sum, err := run(t, db, Options{Now: fixedNow})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Count("projects") < 3 || len(sum.Projects) != 3 {
			t.Fatalf("projects: count %d, summary %+v", sum.Count("projects"), sum.Projects)
		}
		wantPortal := map[string]bool{"WEB": true, "MOB": true, "IT": false}
		for _, p := range sum.Projects {
			if v, ok := wantPortal[p.Key]; !ok || v != p.PortalVisible || p.Tickets == 0 || len(p.SampleRefs) == 0 {
				t.Errorf("project %+v", p)
			}
			for _, r := range p.SampleRefs {
				if !strings.HasPrefix(r, p.Key+"-") {
					t.Errorf("sample ref %s of %s", r, p.Key)
				}
			}
		}
		var out bytes.Buffer
		sum.Print(&out)
		if !strings.Contains(out.String(), "Projects") || !strings.Contains(out.String(), "WEB-") || !strings.Contains(out.String(), "staff only") {
			t.Errorf("printout lacks the projects section:\n%s", out.String())
		}
		wantAll(t, "tickets per project", column(t, db, `SELECT p.key, COUNT(*) FROM tickets t JOIN projects p ON p.id = t.project_id GROUP BY p.key`), "WEB", "MOB", "IT")
		for q, name := range map[string]string{
			`SELECT COUNT(*) FROM tickets t JOIN projects p ON p.id = t.project_id WHERE p.key = 'IT' AND t.channel IN ('portal', 'email')`: "IT ticket on the portal or by mail",
			`SELECT COUNT(*) FROM tickets t JOIN organizations o ON o.id = t.organization_id JOIN projects p ON p.id = t.project_id
				WHERE o.name = 'Globex' AND p.key <> 'WEB'`: "Globex ticket outside WEB",
			`SELECT COUNT(*) FROM tickets t JOIN categories c ON c.id = t.category_id WHERE c.project_id <> t.project_id`: "category of another project",
		} {
			if n := count(t, db, q); n != 0 {
				t.Errorf("%s: %d", name, n)
			}
		}
		for q, name := range map[string]string{
			`SELECT COUNT(*) FROM project_members m JOIN projects p ON p.id = m.project_id JOIN users u ON u.id = m.user_id
				WHERE p.key = 'MOB' AND m.role = 'project_admin' AND u.email = 'freya.fields@demo.test'`: "Mobile project admin",
			`SELECT COUNT(*) FROM sla_policies s JOIN projects p ON p.id = s.project_id WHERE p.key = 'MOB'`:     "Mobile SLA override",
			`SELECT COUNT(*) FROM canned_responses c JOIN projects p ON p.id = c.project_id WHERE p.key = 'MOB'`: "Mobile canned response",
			`SELECT COUNT(*) FROM automation_rules r JOIN projects p ON p.id = r.project_id WHERE p.key = 'MOB'`: "Mobile rule",
			`SELECT COUNT(*) FROM macros m JOIN projects p ON p.id = m.project_id WHERE p.key = 'MOB'`:           "Mobile macro",
			`SELECT COUNT(*) FROM kb_categories k JOIN projects p ON p.id = k.project_id WHERE p.key = 'MOB'`:    "Mobile KB category",
			`SELECT COUNT(*) FROM organization_projects`:                                                         "organization limited to projects",
			`SELECT COUNT(*) FROM custom_field_defs WHERE project_ids IS NOT NULL`:                               "project custom field",
		} {
			if n := count(t, db, q); n == 0 {
				t.Errorf("missing: %s", name)
			}
		}
		// The Mobile-only agent: one membership, Mobile tickets only (I-11).
		if n := count(t, db, `SELECT COUNT(*) FROM project_members m JOIN users u ON u.id = m.user_id WHERE u.email = 'mona.mobile@demo.test'`); n != 1 {
			t.Fatalf("mona.mobile has %d memberships, want 1", n)
		}
		_, newClient := stackFor(t, db)
		mona := newClient()
		if err := mona.login("staff", "mona.mobile@demo.test", DefaultPassword); err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []struct {
				ID, Ref  string
				Project  struct{ Key string } `json:"project"`
				Assignee *struct{ ID string } `json:"assignee"`
			} `json:"items"`
		}
		if err := mona.get("/agent/tickets?limit=200", &page); err != nil {
			t.Fatal(err)
		}
		mine := 0
		for _, it := range page.Items {
			if it.Project.Key != "MOB" || !strings.HasPrefix(it.Ref, "MOB-") {
				t.Errorf("mona.mobile sees %s (%s)", it.Ref, it.Project.Key)
			}
			if it.Assignee != nil && it.Assignee.ID == mona.userID {
				mine++
			}
		}
		if len(page.Items) == 0 || mine == 0 {
			t.Errorf("mona.mobile sees %d tickets, %d assigned to her", len(page.Items), mine)
		}
		var projects []struct{ Key string }
		if err := mona.get("/agent/projects", &projects); err != nil || len(projects) != 1 || projects[0].Key != "MOB" {
			t.Errorf("mona.mobile projects %v: %v", projects, err)
		}
		var webID string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT t.id FROM tickets t JOIN projects p ON p.id = t.project_id WHERE p.key = 'WEB' LIMIT 1`).Scan(&webID); err != nil {
			t.Fatal(err)
		}
		var apiErr *APIError
		if err := mona.get("/agent/tickets/"+webID, nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
			t.Errorf("a WEB ticket must be 404 for mona.mobile, got %v", err)
		}
		// Supervisors see every project; refs resolve.
		sup := newClient()
		if err := sup.login("staff", "supervisor@demo.test", DefaultPassword); err != nil {
			t.Fatal(err)
		}
		var any struct {
			Ref string `json:"ref"`
		}
		if err := sup.get("/agent/tickets/by-ref/"+sum.Projects[2].SampleRefs[0], &any); err != nil || any.Ref != sum.Projects[2].SampleRefs[0] {
			t.Errorf("by-ref %s: %v %v", sum.Projects[2].SampleRefs[0], any.Ref, err)
		}
	})
}
