// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestE2E_StaffInternalEmailReplyNeverReachesCustomer (NOT-028 through the
// NOT-009 canary): on the full app, a staff member answers a ticket thread by
// email with `#internal` on the first line. The pipeline must store it as an
// internal note (prefix stripped) and no mail to the customer may ever carry
// its text. A public staff email reply sent afterwards is the positive
// control: the customer does get that one, so the notification path is live
// and the absence of the canary means something.
func TestE2E_StaffInternalEmailReplyNeverReachesCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		env := e2eEnv(t, db, sink, t.TempDir())
		env["FD_INBOUND_TRUSTED_AUTHSERV_IDS"] = "mx.example.test"
		s := buildStack(t, sink, env)
		s.run()

		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1, team)
		var agentEmail string
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT email FROM users WHERE id = ?`, agentID).Scan(&agentEmail); err != nil {
			t.Fatal(err)
		}
		agentDomain := agentEmail[strings.LastIndex(agentEmail, "@")+1:]

		const customerEmail = "grace@customer.test"
		tk := agent.must(201, "POST", "/api/v1/agent/tickets", map[string]any{
			"subject": "VPN drops every hour", "body_md": "The VPN disconnects every hour.",
			"requester_email": customerEmail, "requester_name": "Grace Hopper", "team_id": team,
		}, "Idempotency-Key", "e2e-not028-create")
		id, _ := tk["id"].(string)
		if id == "" {
			t.Fatalf("create: %v", tk)
		}

		mb := admin.must(201, "POST", "/api/v1/admin/inbound/mailboxes", map[string]any{"name": "Support inbox", "kind": "webhook", "address": "support@example.test", "team_id": team})
		hookURL, _ := mb["webhook_url"].(string)
		token := hookURL[strings.LastIndex(hookURL, "/")+1:]
		if token == "" {
			t.Fatalf("mailbox create: %v", mb)
		}
		anon := s.client()
		staffMail := func(n, body string) {
			raw := "From: Agent <" + agentEmail + ">\r\nTo: support@example.test\r\nSubject: Re: VPN drops every hour\r\n" +
				"Message-ID: <staff-" + n + "-" + id + "@" + agentDomain + ">\r\nReferences: " + thread.TicketThreadID(id, "localhost") + "\r\n" +
				"Authentication-Results: mx.example.test; dkim=pass header.d=" + agentDomain + " header.s=s1\r\n" +
				"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
			anon.must(202, "POST", "/api/v1/inbound/mime/"+token, []byte(raw))
		}

		const canary = "CANARY-internal-mail-91c4e"
		const public = "POSITIVE-public-mail-3b7d"
		staffMail("1", "#internal\r\nCustomer's laptop has an old client. "+canary+"\r\n")

		type msg struct {
			Kind   string `json:"kind"`
			BodyMD string `json:"body_md"`
		}
		messages := func() []msg {
			code, b := agent.do("GET", "/api/v1/agent/tickets/"+id, nil)
			if code != 200 {
				t.Fatalf("GET ticket: %d %s", code, b)
			}
			var tkt struct {
				Messages []msg `json:"messages"`
			}
			_ = json.Unmarshal(b, &tkt)
			return tkt.Messages
		}
		poll(t, 10*time.Second, "the #internal email stored as an internal note", func() bool {
			for _, m := range messages() {
				if strings.Contains(m.BodyMD, canary) {
					if m.Kind != "internal" {
						t.Fatalf("#internal email stored as %q, want internal", m.Kind)
					}
					if strings.Contains(m.BodyMD, "#internal") {
						t.Fatalf("the #internal prefix was kept: %q", m.BodyMD)
					}
					return true
				}
			}
			return false
		})

		// Positive control: a public email reply from the same agent reaches the customer.
		staffMail("2", "We pushed a new VPN client to your laptop. "+public+"\r\n")
		s.waitMail(customerEmail, 15*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, public) })

		// I-2 / I-7: no customer mail carries the internal note, in any part.
		for _, m := range s.mailsTo(customerEmail) {
			if strings.Contains(m.Raw, canary) || strings.Contains(m.Text, canary) {
				t.Fatalf("customer mail %q leaks the #internal email reply:\n%s", m.Subject, m.Text)
			}
		}
		// Nor does the customer's own view of the ticket.
		var requester string
		if err := s.db.Read().QueryRowContext(t.Context(), `SELECT requester_id FROM tickets WHERE id = ?`, id).Scan(&requester); err != nil {
			t.Fatal(err)
		}
		cust := s.loginFixture(requester, domain.KindCustomer)
		if code, b := cust.do("GET", "/api/v1/portal/tickets/"+id, nil); code != 200 || strings.Contains(string(b), canary) || !strings.Contains(string(b), public) {
			t.Fatalf("portal view: %d, canary leaked=%v, public reply present=%v", code, strings.Contains(string(b), canary), strings.Contains(string(b), public))
		}
	})
}
