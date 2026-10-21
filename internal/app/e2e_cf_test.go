// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// cfFixture: a running full app with a staff-only field (canary value set
// by an agent) and a customer-visible field (positive control).
type cfFixture struct {
	s                *stack
	cust, agent      *client
	custEmail, id    string
	internal, public *receiver
	secretInt        string
	secretPub        string
}

const (
	cfStaffKey    = "risk_score_internal"
	cfStaffCanary = "CANARY-staff-only-value-91d0"
	cfPublicKey   = "order_ref"
	cfPublicValue = "ORDER-POSITIVE-4411"
)

func newCFFixture(t *testing.T, db *store.DB) *cfFixture {
	t.Helper()
	sink := newSink(t)
	team := storetest.CreateTeam(t, db, "Support")
	category := storetest.CreateCategory(t, db, "Orders", team)
	custID := storetest.CreateCustomer(t, db, "")
	s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
	s.run()
	admin, _ := s.staff(domain.RoleAdmin, 3)
	agent, agentID := s.staff(domain.RoleAgent, 3, team)
	f := &cfFixture{s: s, agent: agent, internal: newReceiver(t), public: newReceiver(t)}
	if err := s.db.Read().QueryRowContext(t.Context(), `SELECT email FROM users WHERE id = ?`, custID).Scan(&f.custEmail); err != nil {
		t.Fatal(err)
	}

	admin.must(201, "POST", "/api/v1/admin/custom-fields", map[string]any{"key": cfStaffKey, "label": map[string]string{"en": "Risk"}, "type": "text",
		"customer_visible": false, "customer_editable": false})
	admin.must(201, "POST", "/api/v1/admin/custom-fields", map[string]any{"key": cfPublicKey, "label": map[string]string{"en": "Order"}, "type": "text",
		"customer_visible": true, "customer_editable": true})
	evs := []string{events.TicketCreated, events.TicketFieldsChanged, events.MessageCreated, events.TicketStatusChanged, events.TicketAssigned}
	wi := admin.must(201, "POST", "/api/v1/admin/webhooks", map[string]any{"url": f.internal.srv.URL, "events": evs, "include_internal": true})
	wp := admin.must(201, "POST", "/api/v1/admin/webhooks", map[string]any{"url": f.public.srv.URL, "events": evs, "include_internal": false})
	f.secretInt, _ = wi["secret"].(string)
	f.secretPub, _ = wp["secret"].(string)

	f.cust = s.loginFixture(custID, domain.KindCustomer)
	tk := f.cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Where is my order", "body_md": "Order missing.",
		"category_id": category, "custom_fields": map[string]any{cfPublicKey: cfPublicValue}}, "Idempotency-Key", "cf-1")
	f.id = tk["id"].(string)
	agent.must(200, "PATCH", "/api/v1/agent/tickets/"+f.id, map[string]any{"assignee_id": agentID})
	agent.must(200, "PATCH", "/api/v1/agent/tickets/"+f.id, map[string]any{"custom_fields": map[string]any{cfStaffKey: cfStaffCanary, cfPublicKey: cfPublicValue + "-2"}})
	agent.must(201, "POST", "/api/v1/agent/tickets/"+f.id+"/messages", map[string]any{"kind": "public", "body_md": "We are on it."}, "Idempotency-Key", "cf-2")
	agent.must(200, "PATCH", "/api/v1/agent/tickets/"+f.id, map[string]any{"status": "resolved"})
	return f
}

// TestCF_StaffOnlyHiddenInCustomerEmailPayload (TKT-040, I-9): a staff-only
// custom field value never reaches a customer email (customer.reply,
// customer.resolved). Positive controls: the customer sees the visible field
// over REST, and the mails themselves are delivered.
func TestCF_StaffOnlyHiddenInCustomerEmailPayload(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFixture(t, db)
		s := f.s
		s.waitMail(f.custEmail, 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, "We are on it.") })
		s.waitMail(f.custEmail, 10*time.Second, func(m sentMail) bool { return strings.Contains(strings.ToLower(m.Subject), "resolved") })
		pt := f.cust.must(200, "GET", "/api/v1/portal/tickets/"+f.id, nil)
		b, _ := json.Marshal(pt)
		if !strings.Contains(string(b), cfPublicValue+"-2") || strings.Contains(string(b), cfStaffCanary) || strings.Contains(string(b), cfStaffKey) {
			t.Fatalf("portal ticket custom fields wrong: %s", b)
		}
		at := f.agent.must(200, "GET", "/api/v1/agent/tickets/"+f.id, nil)
		if b, _ := json.Marshal(at); !strings.Contains(string(b), cfStaffCanary) {
			t.Fatalf("staff should see the staff-only value: %s", b)
		}
		mails := s.mailsTo(f.custEmail)
		if len(mails) < 2 {
			t.Fatalf("expected customer.reply and customer.resolved, got %d mails", len(mails))
		}
		for _, m := range mails {
			if strings.Contains(m.Raw, cfStaffCanary) || strings.Contains(m.Text, cfStaffCanary) || strings.Contains(m.Text, cfStaffKey) {
				t.Fatalf("customer mail %q leaks the staff-only field:\n%s", m.Subject, m.Text)
			}
		}
	})
}

// TestCF_StaffOnlyHiddenInCustomerWebhookPayload (TKT-040, I-9): webhooks
// with include_internal=false are the customer-scoped integration surface
// (Webhook.include_internal: "staff integrations only"). Their
// ticket.fields_changed payload lists only customer-visible keys; the
// include_internal=true hook lists the staff-only key too. No payload ever
// carries field values (events record keys only), so the canary value never
// leaves the server.
func TestCF_StaffOnlyHiddenInCustomerWebhookPayload(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFixture(t, db)
		keysOf := func(d delivery) []string {
			var env struct {
				Data struct {
					Changes struct {
						Keys []string `json:"keys"`
					} `json:"changes"`
				} `json:"data"`
			}
			_ = json.Unmarshal(d.Body, &env)
			return env.Data.Changes.Keys
		}
		hasKey := func(k string) func(map[string]any) bool {
			return func(env map[string]any) bool {
				b, _ := json.Marshal(env["data"])
				return strings.Contains(string(b), `"`+k+`"`)
			}
		}
		pub := f.public.waitEvent(t, events.TicketFieldsChanged, 10*time.Second, hasKey(cfPublicKey))
		if k := keysOf(pub); !slices.Contains(k, cfPublicKey) || slices.Contains(k, cfStaffKey) {
			t.Fatalf("customer-scoped hook keys %v (want %s only)", k, cfPublicKey)
		}
		in := f.internal.waitEvent(t, events.TicketFieldsChanged, 10*time.Second, hasKey(cfStaffKey))
		if k := keysOf(in); !slices.Contains(k, cfStaffKey) || !slices.Contains(k, cfPublicKey) {
			t.Fatalf("internal hook keys %v (want both)", k)
		}
		f.public.waitEvent(t, events.TicketStatusChanged, 10*time.Second, nil)
		time.Sleep(200 * time.Millisecond)
		for _, d := range f.public.deliveries() {
			if !verifySig(f.secretPub, d) {
				t.Fatalf("bad signature on %s", d.Event)
			}
			if strings.Contains(string(d.Body), cfStaffCanary) || strings.Contains(string(d.Body), cfStaffKey) {
				t.Fatalf("customer-scoped hook leaks the staff-only field in %s: %s", d.Event, d.Body)
			}
		}
		for _, d := range f.internal.deliveries() {
			if !verifySig(f.secretInt, d) {
				t.Fatalf("bad signature on %s", d.Event)
			}
			if strings.Contains(string(d.Body), cfStaffCanary) {
				t.Fatalf("webhook payloads carry no field values, got one in %s: %s", d.Event, d.Body)
			}
		}
	})
}
