// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"sync"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestCreate_NumberUniqueUnderConcurrency(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		var wg sync.WaitGroup
		ids := make([]string, 50)
		errs := make([]error, 50)
		for i := range 50 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ids[i], errs[i] = e.svc.Create(context.Background(), cust, CreateInput{Subject: "c", BodyMD: "b"})
			}(i)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n := e.count(`SELECT COUNT(DISTINCT number) FROM tickets`); n != 50 {
			t.Fatalf("%d distinct numbers", n)
		}
	})
}

func TestCreate_RoutesByCategory(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		e.exec(`UPDATE categories SET default_priority = 'high' WHERE id = ?`, cat)
		e.svc.dir.Invalidate()
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{CategoryID: sp(cat)})
		row, _ := lockFreeLoad(context.Background(), e.svc, id)
		if row.TeamID != team || row.Priority != domain.PriorityHigh || row.CategoryID != cat {
			t.Fatalf("routing: team %q priority %s", row.TeamID, row.Priority)
		}
	})
}

func TestCreate_UncategorisedToTriage(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{})
		row, _ := lockFreeLoad(context.Background(), e.svc, id)
		if row.TeamID != "" || row.Channel != domain.ChannelPortal || row.Status != domain.StatusNew {
			t.Fatalf("triage: %+v", row)
		}
		if ev := e.eventsOf(id); len(ev) != 1 || ev[0] != "ticket.created" {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestCreate_AgentFindOrCreateCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		org := storetest.CreateOrg(t, db, "")
		e.exec(`UPDATE organizations SET email_domains = ? WHERE id = ?`, `["acme.test"]`, org)
		agent := e.staff(domain.RoleAgent, 1)
		body := map[string]any{"subject": "Phone call", "body_md": "Customer called", "requester_email": "Ada@Acme.test", "requester_name": "Ada"}
		r := e.do(agent, "POST", "/api/v1/agent/tickets", body).expect(t, 201).m(t)
		if r["channel"] != "agent" || r["organization_id"] != org {
			t.Fatalf("created %v", r)
		}
		if n := e.count(`SELECT COUNT(*) FROM users WHERE email = 'ada@acme.test' AND kind = 'customer' AND status = 'active' AND password_hash IS NULL`); n != 1 {
			t.Fatalf("customer rows %d", n)
		}
		// Second time: found, not duplicated.
		e.do(agent, "POST", "/api/v1/agent/tickets", body).expect(t, 201)
		if n := e.count(`SELECT COUNT(*) FROM users WHERE email = 'ada@acme.test'`); n != 1 {
			t.Fatalf("duplicate customer %d", n)
		}
		// A staff address is never turned into a customer.
		staffEmail := ""
		_ = db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, agent.UserID).Scan(&staffEmail)
		e.do(agent, "POST", "/api/v1/agent/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_email": staffEmail}).expect(t, 422)
	})
}

func TestCreate_RequesterMustBeCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		r := e.do(agent, "POST", "/api/v1/agent/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_id": agent.UserID}).expect(t, 422)
		if e.count(`SELECT COUNT(*) FROM tickets`) != 0 {
			t.Fatalf("ticket created: %s", r.body)
		}
	})
}

func TestCreate_CustomerUrgentRefusedByDefault(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "priority": "urgent"}).expect(t, 422)
		e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "priority": "high"}).expect(t, 201)
	})
}

func TestCreate_OrgManagerCannotFileForOtherOrg(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		orgA, orgB := storetest.CreateOrg(t, db, ""), storetest.CreateOrg(t, db, "")
		mgr := e.customer(orgA, domain.RoleOrgManager)
		colleague := e.customer(orgA, domain.RoleRequester)
		stranger := e.customer(orgB, domain.RoleRequester)
		e.do(mgr, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_id": stranger.UserID}).expect(t, 422)
		r := e.do(mgr, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_id": colleague.UserID}).expect(t, 201).m(t)
		if r["requester"].(map[string]any)["id"] != colleague.UserID {
			t.Fatal("requester not the colleague")
		}
		req := e.customer(orgA, domain.RoleRequester)
		e.do(req, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_id": colleague.UserID}).expect(t, 422)
	})
}

func TestCreate_ChannelApiForPAT(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		tok := e.pat(cust, domain.ScopeTicketsRead, domain.ScopeTicketsWrite)
		r := e.do(tok, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y"}).expect(t, 201).m(t)
		if r["channel"] != "api" {
			t.Fatalf("channel %v", r["channel"])
		}
		ro := e.pat(cust, domain.ScopeTicketsRead)
		e.do(ro, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y"}).expect(t, 403)
	})
}

// TestCreate_RollbackLeavesNoEventNothingDispatched: a failure after the
// ticket insert (someone else's draft attached) rolls everything back: no
// ticket, no ticket_events row, no commit signal for the dispatcher.
func TestCreate_RollbackLeavesNoEventNothingDispatched(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust, other := e.customer("", domain.RoleRequester), e.customer("", domain.RoleRequester)
		att := store.NewID()
		e.exec(`INSERT INTO attachments (id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at) VALUES (?, ?, 'a.txt', 'text/plain', 1, 'x', 'k', ?)`, att, other.UserID, store.Now())
		var commits int
		var mu sync.Mutex
		db.OnCommit(func() { mu.Lock(); commits++; mu.Unlock() })
		cursorBefore, _ := store.Cursor(context.Background(), db.Read(), "dispatch_cursor")
		_, err := e.svc.Create(context.Background(), cust, CreateInput{Subject: "x", BodyMD: "y", AttachmentIDs: []string{att}})
		if err == nil {
			t.Fatal("expected failure")
		}
		if n := e.count(`SELECT COUNT(*) FROM ticket_events`); n != 0 {
			t.Fatalf("%d events after rollback", n)
		}
		if n := e.count(`SELECT COUNT(*) FROM tickets`); n != 0 {
			t.Fatalf("%d tickets after rollback", n)
		}
		mu.Lock()
		defer mu.Unlock()
		if commits != 0 {
			t.Fatalf("%d commit signals", commits)
		}
		cursorAfter, _ := store.Cursor(context.Background(), db.Read(), "dispatch_cursor")
		if cursorAfter != cursorBefore {
			t.Fatal("dispatch cursor moved")
		}
	})
}
