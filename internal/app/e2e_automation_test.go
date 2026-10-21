// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestE2E_AutomationRuleAndMacro (D-19 §A) on the full app with the real
// queue: a ticket.created rule assigns by round robin and tags a new portal
// ticket through the durable automation consumer; its internal note never
// reaches the customer (WebSocket, e-mail, portal); an agent applies a macro.
func TestE2E_AutomationRuleAndMacro(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Automation")
		category := storetest.CreateCategory(t, db, "Hardware", team)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()

		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1, team)
		admin.must(200, "PATCH", "/api/v1/admin/teams/"+team, map[string]any{"name": "Automation", "assignment_strategy": "round_robin"})
		const canary = "AUTOMATION-CANARY-9d1e"
		const ack = "AUTO-ACK-5c2f"
		canned := admin.must(201, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Ack", "body_md": "Hello {{customer.name}}, " + ack + " for #{{ticket.number}}."})
		admin.must(201, "POST", "/api/v1/admin/automation/rules", map[string]any{"name": "Hardware triage", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "printer"}},
			"actions": []map[string]any{{"type": "add_tag", "value": "hardware"}, {"type": "add_note", "body_md": "Checklist: " + canary},
				{"type": "set_priority", "value": "high"}, {"type": "send_reply", "canned_response_id": canned["id"]}}})

		custID := storetest.CreateCustomer(t, s.db, storetest.CreateOrg(t, s.db, ""))
		cust := s.loginFixture(custID, domain.KindCustomer)
		var custEmail string
		if err := s.db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, custID).Scan(&custEmail); err != nil {
			t.Fatal(err)
		}
		ws := s.ws(cust)
		ws.subscribe("c1", "tickets")
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "The printer jams", "body_md": "Every page.", "category_id": category},
			"Idempotency-Key", "auto-1")
		id := tk["id"].(string)

		var detail map[string]any
		poll(t, 10*time.Second, "automation applied", func() bool {
			detail = agent.must(200, "GET", "/api/v1/agent/tickets/"+id, nil)
			tags, _ := detail["tags"].([]any)
			return len(tags) == 1 && tags[0] == "hardware" && detail["priority"] == "high"
		})
		if a, _ := detail["assignee"].(map[string]any); a == nil || a["id"] != agentID {
			t.Fatalf("not assigned by round robin: %v", detail["assignee"])
		}
		var agentEmail string
		if err := s.db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, agentID).Scan(&agentEmail); err != nil {
			t.Fatal(err)
		}
		s.waitMail(agentEmail, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "Assigned to you") })
		page := agent.must(200, "GET", "/api/v1/agent/tickets/"+id+"/events", nil)
		found := false
		for _, it := range page["items"].([]any) {
			if it.(map[string]any)["type"] == events.TicketAutomationApplied {
				found = true
			}
		}
		if !found {
			t.Fatal("no ticket.automation_applied in the timeline")
		}

		// The automated public reply (author NULL) reaches the customer: portal
		// (spec-validated CustomerMessage with a null author) and e-mail.
		_, portal := cust.do("GET", "/api/v1/portal/tickets/"+id, nil)
		if !strings.Contains(string(portal), ack) || strings.Contains(string(portal), "{{") {
			t.Fatalf("portal lacks the automated reply: %s", portal)
		}
		s.waitMail(custEmail, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, ack) })

		// The macro: the agent answers and waits for the customer.
		mac := agent.must(201, "POST", "/api/v1/agent/macros", map[string]any{"name": "Ask for model", "reply_md": "Which model, {{customer.name}}?", "status": "pending"})
		res := agent.must(200, "POST", "/api/v1/agent/tickets/"+id+"/macros/"+mac["id"].(string)+"/apply", nil, "Idempotency-Key", "macro-1")
		if res["ticket"].(map[string]any)["status"] != "pending" {
			t.Fatalf("macro: %v", res)
		}
		s.waitMail(custEmail, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, "Which model") })

		// I-7: nothing the customer received carries the automation note or event.
		if _, p := cust.do("GET", "/api/v1/portal/tickets/"+id, nil); strings.Contains(string(p), canary) {
			t.Fatal("portal shows the automation note")
		}
		time.Sleep(200 * time.Millisecond)
		if d := ws.dump(); strings.Contains(d, canary) || strings.Contains(d, events.TicketAutomationApplied) {
			t.Fatalf("customer socket leak: %s", d)
		}
		for _, m := range s.mailsTo(custEmail) {
			if strings.Contains(m.Raw, canary) || strings.Contains(m.Text, canary) {
				t.Fatalf("customer mail %q leaks the automation note", m.Subject)
			}
		}
		// The automation actor (no users row) never breaks a durable consumer:
		// nothing was spilled to deferred_jobs (notify, webhooks, automation).
		var spilled int
		if err := s.db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM deferred_jobs`).Scan(&spilled); err != nil {
			t.Fatal(err)
		}
		if spilled != 0 {
			t.Fatalf("%d deferred jobs after the automation flow", spilled)
		}
	})
}
