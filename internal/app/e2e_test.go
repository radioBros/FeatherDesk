// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestE2E_FullFlow drives the full app (every module of app.Registry, a
// fake SMTP sink, a webhook receiver) through one ticket's life:
// register → verify via the mailed link → portal ticket → L1 internal note
// + public reply (customer mail without the note) → escalation (L2 sees it
// on WS, L1 gets ticket.access_lost, staff.escalated mail to L2) → L2
// resolves → customer reopens and resolves → signed webhooks → an inbound
// MIME reply threads onto the ticket via In-Reply-To.
func TestE2E_FullFlow(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()

		admin, _ := s.staff(domain.RoleAdmin, 3)
		l1, l1ID := s.staff(domain.RoleAgent, 1, team)
		l2, l2ID := s.staff(domain.RoleAgent, 2, team)
		// A second L1 member who is not the actor: staff.escalated must skip
		// members below the new tier (NOT-007 "exactly the target-team
		// members with tier ≥ new tier").
		otherL1ID := storetest.CreateStaff(t, s.db, domain.RoleAgent, 1, team)
		var l2Email, otherL1Email string
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT email FROM users WHERE id = ?`, l2ID).Scan(&l2Email); err != nil {
			t.Fatal(err)
		}
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT email FROM users WHERE id = ?`, otherL1ID).Scan(&otherL1Email); err != nil {
			t.Fatal(err)
		}

		// (e) A signed webhook for every event, created by the admin.
		rcv := newReceiver(t)
		wh := admin.must(201, "POST", "/api/v1/admin/webhooks", map[string]any{"url": rcv.srv.URL + "/hook",
			"events": []string{events.TicketCreated, events.MessageCreated, events.NoteCreated, events.TicketEscalated, events.TicketStatusChanged}})
		secret, _ := wh["secret"].(string)
		if secret == "" {
			t.Fatalf("webhook create returned no secret: %v", wh)
		}

		// (a) Self-registration; the verify link comes from the mailed message.
		cust := s.client()
		email := fmt.Sprintf("ada.%d@customer.test", time.Now().UnixNano())
		cust.must(202, "POST", "/api/v1/auth/register", map[string]any{"email": email, "name": "Ada Lovelace", "password": authntest.TestPassword, "locale": "en"})
		verify := s.waitMail(email, 10*time.Second, func(m sentMail) bool { return tokenURL.MatchString(m.Text) })
		link := tokenURL.FindString(verify.Text)
		cust.must(200, "POST", "/api/v1/auth/verify-email", map[string]any{"token": authntest.TokenFromURL(link)})
		cust = s.login(email, domain.KindCustomer)
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Printer offline", "body_md": "The printer on floor 3 is offline.", "category_id": category},
			"Idempotency-Key", "e2e-create-1")
		id, _ := tk["id"].(string)
		if id == "" {
			t.Fatalf("create: %v", tk)
		}
		// The requester filed it, so the actor-exclusion rule sends no
		// customer.ticket_received; staff of the team get staff.ticket_new.
		s.waitMail(l2Email, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "Printer offline") })

		// (b) L1 takes the ticket, writes an internal note (canary), then a public reply.
		l1.must(200, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"assignee_id": l1ID})
		const canary = "CANARY-internal-7f3a9c"
		const public = "POSITIVE-public-reply-b81e"
		l1.must(201, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "internal", "body_md": "Note: " + canary}, "Idempotency-Key", "e2e-note-1")
		l1.must(201, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "public", "body_md": "Hello! " + public}, "Idempotency-Key", "e2e-reply-1")
		reply := s.waitMail(email, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, public) })
		if reply.MessageID == "" {
			t.Fatal("customer.reply has no Message-ID")
		}

		// (c) L1 watches the ticket, L2 its queue; L1 escalates to tier 2.
		wsL1 := s.ws(l1)
		wsL1.subscribe("s1", "ticket:"+id)
		wsL2 := s.ws(l2)
		wsL2.subscribe("s2", "tickets", "queue:"+team)
		esc := l1.must(200, "POST", "/api/v1/agent/tickets/"+id+"/escalate", map[string]any{"reason": "needs network access"})
		if esc["visible"] != false {
			t.Fatalf("L1 should lose visibility after escalating: %v", esc)
		}
		if wsL1.waitFor(5*time.Second, func(f map[string]any) bool { return f["event"] == events.TicketAccessLost }) == nil {
			t.Fatalf("L1 got no ticket.access_lost: %s", wsL1.dump())
		}
		if wsL2.waitFor(5*time.Second, func(f map[string]any) bool { return f["type"] == "event" && f["event"] == events.TicketEscalated }) == nil {
			t.Fatalf("L2 got no ticket.escalated frame: %s", wsL2.dump())
		}
		s.waitMail(l2Email, 10*time.Second, func(m sentMail) bool {
			return strings.Contains(m.Text, "needs network access") || strings.Contains(strings.ToLower(m.Subject), "escalat")
		})
		for _, m := range s.mailsTo(otherL1Email) {
			if strings.Contains(m.Text, "needs network access") || strings.Contains(strings.ToLower(m.Subject), "escalat") {
				t.Fatalf("an L1 member got staff.escalated for a tier-2 ticket: %s", m.Subject)
			}
		}
		if code, _ := l1.do("GET", "/api/v1/agent/tickets/"+id, nil); code != 404 {
			t.Fatalf("L1 still sees the tier-2 ticket: %d", code)
		}

		// (d) L2 resolves; the customer reopens, then resolves.
		l2.must(200, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"assignee_id": l2ID})
		l2.must(200, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"status": "resolved"})
		s.waitMail(email, 10*time.Second, func(m sentMail) bool { return strings.Contains(strings.ToLower(m.Subject), "resolved") })
		cust.must(200, "POST", "/api/v1/portal/tickets/"+id+"/reopen", nil)
		if st := cust.must(200, "GET", "/api/v1/portal/tickets/"+id, nil)["status"]; st != "open" {
			t.Fatalf("after reopen the customer sees %v", st)
		}
		cust.must(200, "POST", "/api/v1/portal/tickets/"+id+"/resolve", nil)
		if st := cust.must(200, "GET", "/api/v1/portal/tickets/"+id, nil)["status"]; st != "resolved" {
			t.Fatalf("after resolve the customer sees %v", st)
		}

		// I-2 / I-7: no mail to the customer ever carries the internal note.
		for _, m := range s.mailsTo(email) {
			if strings.Contains(m.Raw, canary) || strings.Contains(m.Text, canary) {
				t.Fatalf("customer mail %q leaks the internal note:\n%s", m.Subject, m.Text)
			}
		}

		// (e) Signed deliveries; the non-internal hook never gets the note.
		rcv.waitEvent(t, events.TicketCreated, 10*time.Second, nil)
		rcv.waitEvent(t, events.TicketEscalated, 10*time.Second, nil)
		rcv.waitEvent(t, events.TicketStatusChanged, 10*time.Second, nil)
		for _, d := range rcv.deliveries() {
			if !verifySig(secret, d) {
				t.Fatalf("bad signature on %s: %q", d.Event, d.Sig)
			}
			if d.Event == events.NoteCreated || strings.Contains(string(d.Body), canary) {
				t.Fatalf("include_internal=false hook got internal data: %s %s", d.Event, d.Body)
			}
		}

		// (f) Inbound reply through the raw-MIME webhook, threaded by In-Reply-To.
		mb := admin.must(201, "POST", "/api/v1/admin/inbound/mailboxes", map[string]any{"name": "Support inbox", "kind": "webhook", "address": "support@example.test", "team_id": team})
		hookURL, _ := mb["webhook_url"].(string)
		token := hookURL[strings.LastIndex(hookURL, "/")+1:]
		if token == "" {
			t.Fatalf("mailbox create: %v", mb)
		}
		const inboundText = "INBOUND-reply-text-5d2c"
		raw := "From: Ada Lovelace <" + email + ">\r\nTo: support@example.test\r\nSubject: Re: " + reply.Subject + "\r\n" +
			"Message-ID: <inbound-" + id + "@customer.test>\r\nIn-Reply-To: " + reply.MessageID + "\r\nReferences: " + reply.MessageID + "\r\n" +
			"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
			"Still broken after the reboot. " + inboundText + "\r\n"
		anon := s.client()
		anon.must(202, "POST", "/api/v1/inbound/mime/"+token, []byte(raw))
		poll(t, 10*time.Second, "inbound reply on the ticket", func() bool {
			code, b := l2.do("GET", "/api/v1/agent/tickets/"+id, nil)
			if code != 200 {
				return false
			}
			var tkt struct {
				Messages []struct {
					Kind   string `json:"kind"`
					BodyMD string `json:"body_md"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(b, &tkt)
			for _, m := range tkt.Messages {
				if m.Kind == "public" && strings.Contains(m.BodyMD, inboundText) {
					return true
				}
			}
			return false
		})
		var tickets int
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil || tickets != 1 {
			t.Fatalf("inbound reply must not open a new ticket: %d %v", tickets, err)
		}
	})
}
