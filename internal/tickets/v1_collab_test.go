// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// ---- TKT-023 canned responses ----

func TestCanned_AgentSeesOnlyGlobalAndOwnTeams(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		teamA, teamB := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 1)
		agent := e.staff(domain.RoleAgent, 1, teamA)
		e.do(sup, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Global hello", "body_md": "Hi {{customer.name}}"}).expect(t, 201)
		e.do(sup, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Team A", "body_md": "A", "team_id": teamA}).expect(t, 201)
		e.do(sup, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Team B", "body_md": "B", "team_id": teamB}).expect(t, 201)
		var list []map[string]any
		e.do(agent, "GET", "/api/v1/agent/canned-responses", nil).expect(t, 200).json(t, &list)
		var titles []string
		for _, c := range list {
			titles = append(titles, c["title"].(string))
		}
		if !slices.Equal(titles, []string{"Global hello", "Team A"}) {
			t.Fatalf("agent sees %v", titles)
		}
		e.do(agent, "GET", "/api/v1/agent/canned-responses?q=team", nil).expect(t, 200).json(t, &list)
		if len(list) != 1 {
			t.Fatalf("search %v", list)
		}
		e.do(agent, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "x", "body_md": "y"}).expect(t, 403)
	})
}

func TestCanned_UnknownPlaceholderLeftLiteral(t *testing.T) {
	got := RenderCanned("Hi {{customer.name}}, ticket #{{ticket.number}} — {{agent.name}} {{ unknown.key }} {{customer.email}}",
		map[string]string{"customer.name": "Ada", "ticket.number": "1042", "agent.name": "Bob"})
	if got != "Hi Ada, ticket #1042 — Bob {{ unknown.key }} {{customer.email}}" {
		t.Fatalf("rendered %q", got)
	}
}

// TestCanned_AgentOwnTeamCRUDAndPreview: agents manage responses of their
// own teams through /agent/canned-responses and preview placeholders for a
// visible ticket (TKT-023).
func TestCanned_AgentOwnTeamCRUDAndPreview(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		e := l.e
		other := e.do(l.sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Other team"}).expect(t, 201).m(t)["id"].(string)
		e.do(l.agent, "POST", "/api/v1/agent/canned-responses", map[string]any{"title": "Global", "body_md": "x"}).expect(t, 403)
		e.do(l.agent, "POST", "/api/v1/agent/canned-responses", map[string]any{"title": "Other", "body_md": "x", "team_id": other}).expect(t, 403)
		e.do(l.agent, "POST", "/api/v1/agent/canned-responses", map[string]any{"title": "", "body_md": "x", "team_id": l.team}).expect(t, 422)
		c := e.do(l.agent, "POST", "/api/v1/agent/canned-responses", map[string]any{"title": "Mine", "body_md": "Hi {{customer.name}}, #{{ticket.number}} from {{agent.name}} {{x.y}} ({{requester.name}})", "team_id": l.team}).expect(t, 201).m(t)
		id := c["id"].(string)
		u := e.do(l.agent, "PATCH", "/api/v1/agent/canned-responses/"+id, map[string]any{"title": "Mine v2", "body_md": c["body_md"], "team_id": l.team}).expect(t, 200).m(t)
		if u["title"] != "Mine v2" {
			t.Fatalf("update %v", u)
		}
		global := e.do(l.sup, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Global one", "body_md": "g"}).expect(t, 201).m(t)
		e.do(l.agent, "PATCH", "/api/v1/agent/canned-responses/"+global["id"].(string), map[string]any{"title": "Taken", "body_md": "g", "team_id": l.team}).expect(t, 403)
		e.do(l.agent, "DELETE", "/api/v1/agent/canned-responses/"+global["id"].(string), nil).expect(t, 403)
		if n := e.count(`SELECT COUNT(*) FROM canned_responses WHERE id = ? AND team_id IS NULL AND title = 'Global one'`, global["id"]); n != 1 {
			t.Fatal("agent changed a global canned response")
		}
		foreign := e.do(l.sup, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Theirs", "body_md": "x", "team_id": other}).expect(t, 201).m(t)
		e.do(l.agent, "PATCH", "/api/v1/agent/canned-responses/"+foreign["id"].(string), map[string]any{"title": "t", "body_md": "x", "team_id": l.team}).expect(t, 404)
		e.do(l.agent, "DELETE", "/api/v1/agent/canned-responses/"+foreign["id"].(string), nil).expect(t, 404)
		e.do(l.agent, "GET", "/api/v1/agent/canned-responses/"+foreign["id"].(string)+"/preview?ticket_id="+l.ticketID, nil).expect(t, 404)

		pv := e.do(l.agent, "GET", "/api/v1/agent/canned-responses/"+id+"/preview?ticket_id="+l.ticketID, nil).expect(t, 200).m(t)
		var num int64
		var cust, agent string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, l.ticketID).Scan(&num)
		_ = db.Read().QueryRowContext(context.Background(), `SELECT name FROM users WHERE id = ?`, l.cust.UserID).Scan(&cust)
		_ = db.Read().QueryRowContext(context.Background(), `SELECT name FROM users WHERE id = ?`, l.agent.UserID).Scan(&agent)
		want := "Hi " + cust + ", #" + strconv.FormatInt(num, 10) + " from " + agent + " {{x.y}} (" + cust + ")"
		if pv["body_md"] != want {
			t.Fatalf("preview %q want %q", pv["body_md"], want)
		}
		e.do(l.agent, "GET", "/api/v1/agent/canned-responses/"+id+"/preview", nil).expect(t, 422)
		e.do(l.agent, "DELETE", "/api/v1/agent/canned-responses/"+id, nil).expect(t, 204)
	})
}

// ---- TKT-024 tags ----

func TestTags_ReplaceSetEmitsDiff(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"tags": []string{"vpn", "Printer"}}).expect(t, 200)
		r := l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"tags": []string{"printer", "urgent-fix"}}).expect(t, 200).m(t)
		tags := r["ticket"].(map[string]any)["tags"].([]any)
		if len(tags) != 2 || tags[0] != "printer" || tags[1] != "urgent-fix" {
			t.Fatalf("tags %v", tags)
		}
		var data string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT data FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.tags_changed' ORDER BY id DESC LIMIT 1`, l.ticketID).Scan(&data)
		if !strings.Contains(data, "vpn") || !strings.Contains(data, "urgent-fix") {
			t.Fatalf("diff %s", data)
		}
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"tags": []string{"Bad Tag!"}}).expect(t, 422)
		var tagList []map[string]any
		l.e.do(l.agent, "GET", "/api/v1/agent/tags?q=pri", nil).expect(t, 200).json(t, &tagList)
		if len(tagList) != 1 || tagList[0]["usage_count"] != float64(1) {
			t.Fatalf("lookup %v", tagList)
		}
		ids, _ := listIDs(t, l.e, l.agent, "/api/v1/agent/tickets?tag=printer")
		if !slices.Equal(ids, []string{l.ticketID}) {
			t.Fatalf("tag filter %v", ids)
		}
	})
}

func TestTags_NotVisibleToCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"tags": []string{"angry-customer"}}).expect(t, 200)
		for _, p := range []string{"/api/v1/portal/tickets/" + l.ticketID, "/api/v1/portal/tickets"} {
			if r := l.e.do(l.cust, "GET", p, nil).expect(t, 200); strings.Contains(string(r.body), "angry-customer") || strings.Contains(string(r.body), `"tags"`) {
				t.Fatalf("%s exposes tags", p)
			}
		}
	})
}

// ---- TKT-025 watchers / CC ----

func TestWatcher_StaffSeesOutsideTier(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		f.e.do(f.l1, "GET", f.path(""), nil).expect(t, 404)
		r := f.e.do(f.l2, "POST", f.path("/watchers"), map[string]any{"user_id": f.l1.UserID}).expect(t, 200).m(t)
		if len(r["watchers"].([]any)) != 1 {
			t.Fatalf("watchers %v", r["watchers"])
		}
		f.e.do(f.l1, "GET", f.path(""), nil).expect(t, 200)
		f.e.do(f.l2, "DELETE", f.path("/watchers/"+f.l1.UserID), nil).expect(t, 204)
		f.e.do(f.l1, "GET", f.path(""), nil).expect(t, 404)
		ev := f.e.eventsOf(f.id)
		if ev[len(ev)-1] != events.TicketWatchersChanged {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestCC_OtherOrgCustomer422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		stranger := l.e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		l.e.do(l.agent, "POST", l.path("/watchers"), map[string]any{"user_id": stranger.UserID}).expect(t, 422)
		var org string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT organization_id FROM tickets WHERE id = ?`, l.ticketID).Scan(&org)
		colleague := l.e.customer(org, domain.RoleRequester)
		l.e.do(l.agent, "POST", l.path("/watchers"), map[string]any{"user_id": colleague.UserID}).expect(t, 200)
		l.e.do(colleague, "GET", "/api/v1/portal/tickets/"+l.ticketID, nil).expect(t, 200)
	})
}

func TestCC_CustomerSeesPublicOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		var org string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT organization_id FROM tickets WHERE id = ?`, l.ticketID).Scan(&org)
		cc := l.e.customer(org, domain.RoleRequester)
		l.e.do(l.agent, "POST", l.path("/watchers"), map[string]any{"user_id": cc.UserID}).expect(t, 200)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "CC-MUST-NOT-SEE"}).expect(t, 201)
		r := l.e.do(cc, "GET", "/api/v1/portal/tickets/"+l.ticketID, nil).expect(t, 200)
		if strings.Contains(string(r.body), "CC-MUST-NOT-SEE") {
			t.Fatal("CC sees an internal note (I-2)")
		}
		// CC is no requester: no CSAT for them.
		if strings.Contains(string(r.body), `"csat"`) {
			t.Fatal("csat offered to a CC")
		}
	})
}

// ---- TKT-026 saved views ----

func TestViews_InvalidFilter422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "x", "filter": map[string]any{"status": "bogus"}}).expect(t, 422)
		e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "x", "filter": map[string]any{"unknown_key": 1}}).expect(t, 422)
		e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "x", "filter": map[string]any{"cf.nope": "1"}}).expect(t, 422)
		v := e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "Mine open", "filter": map[string]any{"assignee": "me", "status": []string{"new", "open"}}}).expect(t, 201).m(t)
		e.do(agent, "GET", "/api/v1/agent/views/"+v["id"].(string), nil).expect(t, 200)
	})
}

func TestViews_SharedRequiresSupervisor(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent, other := e.staff(domain.RoleAgent, 1), e.staff(domain.RoleAgent, 1)
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "x", "filter": map[string]any{}, "shared": true}).expect(t, 403)
		shared := e.do(sup, "POST", "/api/v1/agent/views", map[string]any{"name": "Team queue", "filter": map[string]any{"assignee": "none"}, "shared": true}).expect(t, 201).m(t)
		private := e.do(agent, "POST", "/api/v1/agent/views", map[string]any{"name": "Private", "filter": map[string]any{}}).expect(t, 201).m(t)
		var list []map[string]any
		e.do(other, "GET", "/api/v1/agent/views", nil).expect(t, 200).json(t, &list)
		if len(list) != 1 || list[0]["id"] != shared["id"] {
			t.Fatalf("other agent sees %v", list)
		}
		e.do(other, "GET", "/api/v1/agent/views/"+private["id"].(string), nil).expect(t, 404)
		e.do(other, "DELETE", "/api/v1/agent/views/"+shared["id"].(string), nil).expect(t, 403)
		e.do(agent, "PATCH", "/api/v1/agent/views/"+private["id"].(string), map[string]any{"name": "Renamed", "filter": map[string]any{"priority": "high"}}).expect(t, 200)
		e.do(agent, "DELETE", "/api/v1/agent/views/"+private["id"].(string), nil).expect(t, 204)
	})
}

// TestViews_NeverWiden: applying a shared view's filter still goes through
// the caller's visibility predicate.
func TestViews_NeverWiden(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		f.ticket(t, f.reqA, f.teamB, 1, "")
		v := f.e.do(f.sup, "POST", "/api/v1/agent/views", map[string]any{"name": "Team B", "filter": map[string]any{"team_id": f.teamB}, "shared": true}).expect(t, 201).m(t)
		q := viewQuery(v["filter"].(map[string]any)).Encode()
		if ids, _ := listIDs(t, f.e, f.l1, "/api/v1/agent/tickets?"+q); len(ids) != 0 {
			t.Fatalf("L1 (team A) sees team B tickets through a view: %v", ids)
		}
		if ids, _ := listIDs(t, f.e, f.sup, "/api/v1/agent/tickets?"+q); len(ids) != 1 {
			t.Fatalf("supervisor %v", ids)
		}
	})
}

// ---- TKT-027 CSAT ----

func TestCSAT_OnlyWhenResolvedOrClosed(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		path := "/api/v1/portal/tickets/" + l.ticketID + "/csat"
		l.e.do(l.cust, "POST", path, map[string]any{"score": 5}).expect(t, 409)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		r := l.e.do(l.cust, "POST", path, map[string]any{"score": 4, "comment": "quick fix"}).expect(t, 200).m(t)
		if r["csat_score"] != float64(4) {
			t.Fatalf("score %v", r["csat_score"])
		}
		staff := l.e.do(l.agent, "GET", l.path(""), nil).expect(t, 200)
		if !strings.Contains(string(staff.body), "quick fix") {
			t.Fatal("comment not stored as a system message")
		}
		if ev := l.e.eventsOf(l.ticketID); ev[len(ev)-1] != events.TicketCSAT {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestCSAT_OncePerCycle(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		path := "/api/v1/portal/tickets/" + l.ticketID + "/csat"
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		l.e.do(l.cust, "POST", path, map[string]any{"score": 2}).expect(t, 200)
		l.e.do(l.cust, "POST", path, map[string]any{"score": 5}).expect(t, 409)
		// A new resolution cycle allows a new rating.
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/reopen", nil).expect(t, 200)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		l.e.do(l.cust, "POST", path, map[string]any{"score": 5}).expect(t, 200)
	})
}

func TestCSAT_OrgManagerOfOtherRequester403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		var org string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT organization_id FROM tickets WHERE id = ?`, l.ticketID).Scan(&org)
		mgr := l.e.customer(org, domain.RoleOrgManager)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		l.e.do(mgr, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/csat", map[string]any{"score": 1}).expect(t, 403)
	})
}

func TestCSAT_SignedTokenWithoutSession(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		ctx := context.Background()
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "resolved"}).expect(t, 200)
		tok, err := l.e.svc.CSATToken(ctx, l.ticketID)
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/v1/portal/tickets/" + l.ticketID + "/csat"
		l.e.do(nil, "POST", path, map[string]any{"score": 5, "csat_token": tok + "x"}).expect(t, 401)
		r := l.e.do(nil, "POST", path, map[string]any{"score": 5, "csat_token": tok}).expect(t, 200).m(t)
		if r["csat_score"] != float64(5) {
			t.Fatal("not rated")
		}
		l.e.do(nil, "POST", path, map[string]any{"score": 1, "csat_token": tok}).expect(t, 409) // single use
	})
}

// ---- TKT-030 optimistic concurrency ----

func TestIfMatch_Stale412(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		etag := l.e.do(l.agent, "GET", l.path(""), nil).expect(t, 200).Header.Get("ETag")
		r := l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "high"}, "If-Match", etag).expect(t, 200)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "low"}, "If-Match", etag).expect(t, 412)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "low"}, "If-Match", r.Header.Get("ETag")).expect(t, 200)
	})
}

func TestIfMatch_AbsentStillWorks(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "high"}).expect(t, 200)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "low"}).expect(t, 200)
	})
}

// ---- TKT-033 mentions ----

func TestMention_InvisibleUser422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "internal", "body_md": "ping @[Bee](" + f.l2B.UserID + ")"}).expect(t, 422)
		r := f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "internal", "body_md": "ping @[Two](" + f.l2.UserID + ")"}).expect(t, 201).m(t)
		var data string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT data FROM ticket_events WHERE ticket_id = ? AND type = 'note.created' ORDER BY id DESC LIMIT 1`, f.id).Scan(&data)
		if !strings.Contains(data, f.l2.UserID) || !strings.Contains(data, r["id"].(string)) {
			t.Fatalf("event data %s", data)
		}
		// @handle (e-mail local part) works too.
		var email string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, f.l2.UserID).Scan(&email)
		f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "internal", "body_md": "cc @" + strings.Split(email, "@")[0]}).expect(t, 201)
	})
}

func TestMention_InPublicReplyIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "public", "body_md": "thanks @[Bee](" + f.l2B.UserID + ")"}).expect(t, 201)
		var data string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT data FROM ticket_events WHERE ticket_id = ? AND type = 'message.created' ORDER BY id DESC LIMIT 1`, f.id).Scan(&data)
		if strings.Contains(data, "mentioned") {
			t.Fatal("public reply produced mentions")
		}
	})
}

// ---- TKT-028 reports ----

// reportFixture builds 40 tickets with known timings and returns the
// expected aggregates computed independently.
func TestReports_FixtureNumbers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
		var frt, res []float64
		resolved, csatSum, csatN, breached := 0, 0, 0, 0
		for i := 0; i < 40; i++ {
			created := base.Add(time.Duration(i) * time.Hour)
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Tier: 1 + i%3, AssigneeID: agent.UserID})
			e.exec(`UPDATE tickets SET created_at = ?, resolution_due_at = ? WHERE id = ?`, created, created.Add(10*time.Hour), id)
			if i%2 == 0 {
				fr := time.Duration(10+i) * time.Minute
				e.exec(`UPDATE tickets SET first_responded_at = ? WHERE id = ?`, created.Add(fr), id)
				frt = append(frt, fr.Minutes())
			}
			if i%4 == 0 {
				rd := time.Duration(60*(i+1)) * time.Minute
				e.exec(`UPDATE tickets SET status = 'resolved', resolved_at = ?, csat_score = ? WHERE id = ?`, created.Add(rd), 1+i%5, id)
				res = append(res, rd.Minutes())
				resolved++
				csatSum += 1 + i%5
				csatN++
				if rd > 10*time.Hour {
					breached++
				}
			}
		}
		var rep Report
		e.do(sup, "GET", "/api/v1/agent/reports/summary?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z", nil).expect(t, 200).json(t, &rep)
		slices.Sort(frt)
		slices.Sort(res)
		if rep.Totals.Created != 40 || rep.Totals.Resolved != resolved || rep.SLA.Breached != breached || rep.SLA.Met != resolved-breached {
			t.Fatalf("totals %+v sla %+v", rep.Totals, rep.SLA)
		}
		if *rep.FirstResponseMinutes.Median != frt[9] || *rep.FirstResponseMinutes.P90 != frt[17] {
			t.Fatalf("first response %v/%v want %v/%v", *rep.FirstResponseMinutes.Median, *rep.FirstResponseMinutes.P90, frt[9], frt[17])
		}
		if *rep.ResolutionMinutes.Median != res[4] || *rep.ResolutionMinutes.P90 != res[8] {
			t.Fatalf("resolution %v/%v want %v/%v", *rep.ResolutionMinutes.Median, *rep.ResolutionMinutes.P90, res[4], res[8])
		}
		if rep.CSAT.Count != csatN || *rep.CSAT.Average != float64(int(float64(csatSum)/float64(csatN)*100+0.5))/100 {
			t.Fatalf("csat %+v", rep.CSAT)
		}
		if rep.ByTier[0].Created != 14 || rep.ByTier[1].Created != 13 || rep.ByTier[2].Created != 13 {
			t.Fatalf("by tier %+v", rep.ByTier)
		}
	})
}

func TestReports_AgentOwnStatsOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		a1, a2 := e.staff(domain.RoleAgent, 1, team), e.staff(domain.RoleAgent, 1, team)
		cust := e.customer("", domain.RoleRequester)
		for i := 0; i < 3; i++ {
			storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, AssigneeID: a1.UserID})
		}
		storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, AssigneeID: a2.UserID})
		var rep Report
		e.do(a2, "GET", "/api/v1/agent/reports/summary", nil).expect(t, 200).json(t, &rep)
		if rep.Totals.Created != 1 {
			t.Fatalf("agent sees %d tickets", rep.Totals.Created)
		}
		e.do(e.staff(domain.RoleSupervisor, 1), "GET", "/api/v1/agent/reports/summary", nil).expect(t, 200).json(t, &rep)
		if rep.Totals.Created != 4 {
			t.Fatalf("supervisor sees %d", rep.Totals.Created)
		}
	})
}

func TestReports_UseBusinessMinutes(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		c := e.do(sup, "POST", "/api/v1/admin/calendars", map[string]any{"name": "Weekdays", "timezone": "UTC",
			"week": map[string]any{"mon": []map[string]string{{"start": "09:00", "end": "17:00"}}, "fri": []map[string]string{{"start": "09:00", "end": "17:00"}}}}).expect(t, 201).m(t)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Biz", "calendar_id": c["id"]}).expect(t, 201).m(t)["id"].(string)
		cust := e.customer("", domain.RoleRequester)
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team})
		// Fri 2026-10-23 16:00 → Mon 2026-10-26 10:00: 66 h wall clock, 2 business hours.
		e.exec(`UPDATE tickets SET created_at = ?, first_responded_at = ? WHERE id = ?`,
			time.Date(2026, 10, 23, 16, 0, 0, 0, time.UTC), time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC), id)
		var rep Report
		e.do(sup, "GET", "/api/v1/agent/reports/summary?from=2026-10-20T00:00:00Z&to=2026-10-30T00:00:00Z&calendar_mode=team", nil).expect(t, 200).json(t, &rep)
		if *rep.FirstResponseMinutes.Median != 120 {
			t.Fatalf("business minutes %v", *rep.FirstResponseMinutes.Median)
		}
		e.do(sup, "GET", "/api/v1/agent/reports/summary?from=2026-10-20T00:00:00Z&to=2026-10-30T00:00:00Z", nil).expect(t, 200).json(t, &rep)
		if *rep.FirstResponseMinutes.Median != 66*60 {
			t.Fatalf("24x7 minutes %v", *rep.FirstResponseMinutes.Median)
		}
	})
}
