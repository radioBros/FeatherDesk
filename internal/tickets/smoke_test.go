// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"testing"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func TestRoutesDocumented(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	e := newEnv(t, db)
	apispec.AssertRoutes(t, e.srv.Router())
}

func TestSmoke_CreateReplyList(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		org := storetest.CreateOrg(t, db, "")
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer(org, domain.RoleRequester)

		r := e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Printer", "body_md": "Broken", "category_id": cat}).expect(t, 201)
		tk := r.m(t)
		id := tk["id"].(string)
		if tk["status"] != "open" {
			t.Fatalf("customer status %v", tk["status"])
		}
		d := e.do(agent, "GET", "/api/v1/agent/tickets/"+id, nil).expect(t, 200).m(t)
		if d["status"] != "new" || d["team_id"] != team {
			t.Fatalf("detail %v", d)
		}
		e.do(agent, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "public", "body_md": "On it"}).expect(t, 201)
		e.do(agent, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "internal", "body_md": "secret note"}).expect(t, 201)
		d = e.do(agent, "GET", "/api/v1/agent/tickets/"+id, nil).expect(t, 200).m(t)
		if d["status"] != "open" || len(d["messages"].([]any)) != 3 {
			t.Fatalf("after reply %v", d)
		}
		c := e.do(cust, "GET", "/api/v1/portal/tickets/"+id, nil).expect(t, 200).m(t)
		if n := len(c["messages"].([]any)); n != 2 {
			t.Fatalf("customer sees %d messages", n)
		}
		page := e.do(agent, "GET", "/api/v1/agent/tickets", nil).expect(t, 200).m(t)
		if n := len(page["items"].([]any)); n != 1 {
			t.Fatalf("list %d", n)
		}
		e.do(cust, "GET", "/api/v1/portal/tickets", nil).expect(t, 200)
		e.do(agent, "GET", "/api/v1/agent/tickets/counts", nil).expect(t, 200)
		e.do(agent, "GET", "/api/v1/agent/tickets/"+id+"/events", nil).expect(t, 200)
		e.do(agent, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"priority": "high", "assignee_id": agent.UserID}).expect(t, 200)
		e.do(agent, "POST", "/api/v1/agent/tickets/"+id+"/escalate", map[string]any{"reason": "needs L2"}).expect(t, 200)
	})
}
