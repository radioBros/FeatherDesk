// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify/thread"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// portalProblem asserts a 404 `portal-disabled` problem document.
func portalProblem(t *testing.T, what string, code int, body []byte) {
	t.Helper()
	if code != 404 || !strings.Contains(string(body), "/problems/portal-disabled") {
		t.Fatalf("%s: want 404 portal-disabled, got %d %s", what, code, body)
	}
}

// dialClose dials /api/v1/ws and returns the close code the server ends it with.
func dialClose(t *testing.T, s *stack, h http.Header, within time.Duration) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.base, "http")+"/api/v1/ws",
		&websocket.DialOptions{Subprotocols: []string{"simpletickets.v1"}, HTTPHeader: h})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

// TestE2E_PortalToggle covers support.portal_enabled=false on the full app:
// every customer entry point is refused, existing customer sessions and
// sockets die with the switch, staff are unaffected, mail to customers and
// inbound mail replies keep working, and switching back restores sign-in.
func TestE2E_PortalToggle(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()
		ctx := context.Background()

		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, _ := s.staff(domain.RoleAgent, 1, team)
		custID := storetest.CreateCustomer(t, db, "")
		var custEmail string
		if err := s.db.Read().QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, custID).Scan(&custEmail); err != nil {
			t.Fatal(err)
		}
		cust := s.loginFixture(custID, domain.KindCustomer)
		pat, _ := json.Marshal(cust.must(201, "POST", "/api/v1/me/tokens", map[string]any{"name": "t", "scopes": []string{"tickets:read"}}))
		var tok struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(pat, &tok)
		bearer := []string{"Authorization", "Bearer " + tok.Token}

		tk := agent.must(201, "POST", "/api/v1/agent/tickets", map[string]any{
			"subject": "Printer jams", "body_md": "It jams.", "requester_email": custEmail, "team_id": team,
		}, "Idempotency-Key", "portal-toggle-create")
		id, _ := tk["id"].(string)
		mb := admin.must(201, "POST", "/api/v1/admin/inbound/mailboxes", map[string]any{"name": "Inbox", "kind": "webhook", "address": "support@example.test", "team_id": team})
		hook, _ := mb["webhook_url"].(string)
		inbound := hook[strings.LastIndex(hook, "/")+1:]

		// Default: the portal is on.
		anon := s.client()
		if cfg := anon.must(200, "GET", "/api/v1/auth/config", nil); cfg["portal_enabled"] != true {
			t.Fatalf("default portal_enabled: %v", cfg["portal_enabled"])
		}
		if got := admin.must(200, "GET", "/api/v1/admin/settings", nil); got["portal_enabled"] != true {
			t.Fatalf("admin settings default: %v", got["portal_enabled"])
		}
		cust.must(200, "GET", "/api/v1/portal/tickets", nil)

		// Customer socket open before the switch (cookie) and one opened later (PAT).
		closed := make(chan websocket.StatusCode, 1)
		go func() {
			h := http.Header{}
			h.Set("Cookie", authn.CookieName+"="+cust.cookie)
			h.Set("Origin", e2ePublicURL)
			closed <- dialClose(t, s, h, 20*time.Second)
		}()
		time.Sleep(300 * time.Millisecond)

		admin.must(200, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": false})

		select {
		case code := <-closed:
			if code != 4403 {
				t.Fatalf("customer socket close code %d, want 4403", code)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("customer socket not closed by the switch")
		}
		var sessions int
		_ = s.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE audience = 'customer'`).Scan(&sessions)
		if sessions != 0 {
			t.Fatalf("%d customer sessions survived the switch", sessions)
		}

		// /auth/config tells the SPA.
		cfg := anon.must(200, "GET", "/api/v1/auth/config", nil)
		if cfg["portal_enabled"] != false {
			t.Fatalf("portal_enabled after switch: %v", cfg["portal_enabled"])
		}
		if c, _ := cfg["customer"].(map[string]any); c["local"] != false || c["self_registration"] != false || c["magic_link"] != false {
			t.Fatalf("customer methods still advertised: %v", c)
		}

		// Every customer entry point is refused.
		code, body := anon.do("POST", "/api/v1/auth/login", map[string]any{"audience": "customer", "email": custEmail, "password": "correct-horse-battery-9"})
		portalProblem(t, "customer login", code, body)
		code, body = anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "new@customer.test", "name": "New", "password": "correct-horse-battery-9"})
		portalProblem(t, "register", code, body)
		code, body = anon.do("POST", "/api/v1/auth/magic-link", map[string]any{"email": custEmail})
		portalProblem(t, "magic link request", code, body)
		code, body = anon.do("POST", "/api/v1/auth/magic-link", map[string]any{"token": "x"})
		portalProblem(t, "magic link consume", code, body)
		code, body = anon.do("POST", "/api/v1/auth/verify-email", map[string]any{"token": "x"})
		portalProblem(t, "verify email", code, body)
		code, body = anon.do("GET", "/api/v1/auth/oidc/customer/start", nil)
		portalProblem(t, "customer oidc start", code, body)
		code, body = anon.do("GET", "/api/v1/auth/oidc/customer/callback?code=x&state=y", nil)
		portalProblem(t, "customer oidc callback", code, body)
		// The stale customer cookie is anonymous; the PAT is refused outright.
		if code, _ = cust.do("GET", "/api/v1/me", nil); code != 401 {
			t.Fatalf("stale customer cookie: %d, want 401", code)
		}
		code, body = anon.do("GET", "/api/v1/portal/tickets", nil, bearer...)
		portalProblem(t, "customer PAT on /portal", code, body)
		if code, body = anon.do("GET", "/api/v1/me", nil, bearer...); code != 401 {
			t.Fatalf("customer PAT on /me: %d %s, want 401", code, body)
		}
		// A customer socket with the PAT is accepted and closed with 4403.
		h := http.Header{}
		h.Set("Authorization", "Bearer "+tok.Token)
		if c := dialClose(t, s, h, 10*time.Second); c != 4403 {
			t.Fatalf("customer PAT socket close code %d, want 4403", c)
		}
		// Invite acceptance: refused, and the token survives for later.
		invited := storetest.CreateCustomer(t, db, "")
		var invToken string
		if err := s.db.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'pending_verification', password_hash = NULL WHERE id = ?`, invited); err != nil {
				return err
			}
			var err error
			invToken, err = authn.IssueAuthToken(ctx, tx, invited, authn.PurposeInvite, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		accept := map[string]any{"token": invToken, "name": "Invited", "password": "correct-horse-battery-9"}
		code, body = anon.do("POST", "/api/v1/auth/invite/accept", accept)
		portalProblem(t, "invite accept", code, body)

		// Staff are unaffected: sign-in, API, a ticket on a customer's behalf.
		s.login(staffEmail(t, s, agent), domain.KindStaff).must(200, "GET", "/api/v1/agent/tickets", nil)
		agent.must(200, "GET", "/api/v1/agent/tickets/"+id, nil)
		agent.must(201, "POST", "/api/v1/agent/tickets", map[string]any{
			"subject": "Created for you", "body_md": "On behalf.", "requester_email": custEmail, "team_id": team,
		}, "Idempotency-Key", "portal-toggle-behalf")
		w := s.ws(agent)
		w.subscribe("s1", "ticket:"+id)

		// Customers still receive mail and can answer it by email.
		const reply = "PUBLIC-REPLY-portal-off-77"
		agent.must(201, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "public", "body_md": "We are on it. " + reply},
			"Idempotency-Key", "portal-toggle-reply")
		s.waitMail(custEmail, 15*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, reply) })
		const answer = "CUSTOMER-EMAIL-ANSWER-4412"
		raw := "From: Customer <" + custEmail + ">\r\nTo: support@example.test\r\nSubject: Re: Printer jams\r\n" +
			"Message-ID: <cust-1-" + id + "@customer.test>\r\nReferences: " + thread.TicketThreadID(id, "localhost") + "\r\n" +
			"Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + answer + "\r\n"
		anon.must(202, "POST", "/api/v1/inbound/mime/"+inbound, []byte(raw))
		poll(t, 10*time.Second, "the emailed customer answer threaded onto the ticket", func() bool {
			_, b := agent.do("GET", "/api/v1/agent/tickets/"+id, nil)
			return strings.Contains(string(b), answer)
		})

		// Switching back restores everything, and the invite token still works.
		admin.must(200, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": true})
		if cfg := anon.must(200, "GET", "/api/v1/auth/config", nil); cfg["portal_enabled"] != true {
			t.Fatalf("portal_enabled after switching back: %v", cfg["portal_enabled"])
		}
		s.login(custEmail, domain.KindCustomer).must(200, "GET", "/api/v1/portal/tickets", nil)
		anon.must(200, "POST", "/api/v1/auth/invite/accept", accept)
	})
}

func staffEmail(t *testing.T, s *stack, _ *client) string {
	t.Helper()
	var email string
	if err := s.db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE kind = 'staff' AND role = 'agent' LIMIT 1`).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}
