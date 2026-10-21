// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestE2E_NotificationCentre (D-19 §D, NOT-036…NOT-038) on the full app:
// a supervisor assigns a ticket through the API and the agent's two open
// sockets get notification.created on `me` with unread_count 1 (no reload);
// marking it read through REST pushes notification.read to both; PATCH /me
// edits the type × channel matrix (email mirrors notification_opt_out,
// customer.reply email stays forced) and an in_app-off type writes no row.
// Every frame validates against WsServerFrame.
func TestE2E_NotificationCentre(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		custID := storetest.CreateCustomer(t, db, "")
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()
		sup, _ := s.staff(domain.RoleSupervisor, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1, team)
		cust := s.loginFixture(custID, domain.KindCustomer)

		// Defaults: in_app and email on, browser off; only the caller's audience.
		me := agent.must(200, "GET", "/api/v1/me", nil)
		prefs, _ := me["notification_prefs"].(map[string]any)
		if a, _ := prefs["staff.assigned"].(map[string]any); len(prefs) != 7 || a["in_app"] != true || a["email"] != true || a["browser"] != false {
			t.Fatalf("staff default prefs %v", prefs)
		}
		if cp, _ := cust.must(200, "GET", "/api/v1/me", nil)["notification_prefs"].(map[string]any); len(cp) != 3 || cp["customer.reply"] == nil {
			t.Fatalf("customer prefs %v", cp)
		}
		if code, body := cust.do("PATCH", "/api/v1/me", map[string]any{"notification_prefs": map[string]any{"customer.reply": map[string]any{"email": false}}}); code != 422 || !strings.Contains(string(body), "not_optional") {
			t.Fatalf("customer.reply email off: %d %s", code, body)
		}
		if code, body := cust.do("PATCH", "/api/v1/me", map[string]any{"notification_prefs": map[string]any{"staff.assigned": map[string]any{"in_app": false}}}); code != 422 || !strings.Contains(string(body), "invalid") {
			t.Fatalf("customer patching a staff type: %d %s", code, body)
		}
		me = agent.must(200, "PATCH", "/api/v1/me", map[string]any{"notification_prefs": map[string]any{
			"staff.assigned":   map[string]any{"browser": true},
			"staff.ticket_new": map[string]any{"in_app": false, "email": false},
		}})
		prefs = me["notification_prefs"].(map[string]any)
		if a := prefs["staff.assigned"].(map[string]any); a["browser"] != true || a["in_app"] != true {
			t.Errorf("browser not saved: %v", a)
		}
		if n := prefs["staff.ticket_new"].(map[string]any); n["in_app"] != false || n["email"] != false {
			t.Errorf("ticket_new not saved: %v", n)
		}
		if oo, _ := me["notification_opt_out"].([]any); len(oo) != 1 || oo[0] != "staff.ticket_new" {
			t.Errorf("email off must mirror notification_opt_out: %v", me["notification_opt_out"])
		}

		tabA, tabB := s.ws(agent), s.ws(agent)
		tabA.subscribe("a", "me")
		tabB.subscribe("b", "me")
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Bell check", "body_md": "x", "category_id": category}, "Idempotency-Key", "bell-1")
		id := tk["id"].(string)
		sup.must(200, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"assignee_id": agentID})

		created := func(f map[string]any) bool { return f["event"] == "notification.created" }
		var nid string
		for _, tab := range []*wsClient{tabA, tabB} {
			f := tab.waitFor(10*time.Second, created)
			if f == nil {
				t.Fatalf("no notification.created: %s", tab.dump())
			}
			d := f["data"].(map[string]any)
			n := d["notification"].(map[string]any)
			if n["type"] != "staff.assigned" || d["unread_count"] != float64(1) || d["browser"] != true || n["ticket_subject"] != "Bell check" || f["topic"] != "me" {
				t.Errorf("frame %v", f)
			}
			nid = n["id"].(string)
		}
		// staff.ticket_new is in_app off: the creation wrote no row for the agent.
		var rows int
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM notifications WHERE user_id = ?`, agentID).Scan(&rows); err != nil || rows != 1 {
			t.Errorf("agent rows %d (%v), want 1 (ticket_new is in_app off)", rows, err)
		}
		if c := agent.must(200, "GET", "/api/v1/me/notifications/unread-count", nil); c["unread_count"] != float64(1) {
			t.Errorf("unread-count %v", c)
		}
		list := agent.must(200, "GET", "/api/v1/me/notifications?unread=true", nil)
		if items, _ := list["items"].([]any); len(items) != 1 {
			t.Fatalf("list %v", list)
		}
		agent.must(200, "POST", "/api/v1/me/notifications/"+nid+"/read", nil)
		for _, tab := range []*wsClient{tabA, tabB} {
			f := tab.waitFor(5*time.Second, func(f map[string]any) bool { return f["event"] == "notification.read" })
			if f == nil {
				t.Fatalf("no notification.read: %s", tab.dump())
			}
			if d := f["data"].(map[string]any); d["unread_count"] != float64(0) {
				t.Errorf("read frame %v", d)
			}
		}
		if code, _ := sup.do("POST", "/api/v1/me/notifications/"+nid+"/read", nil); code != 404 {
			t.Errorf("another user's notification: %d", code)
		}
		for _, tab := range []*wsClient{tabA, tabB} {
			tab.mu.Lock()
			for _, f := range tab.frames {
				b, _ := json.Marshal(f)
				apispec.ValidateSchema(t, "WsServerFrame", b)
			}
			tab.mu.Unlock()
		}
	})
}
