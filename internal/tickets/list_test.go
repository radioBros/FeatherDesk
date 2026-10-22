// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func listIDs(t *testing.T, e *env, p *domain.Principal, path string) ([]string, *string) {
	t.Helper()
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	e.do(p, "GET", path, nil).expect(t, 200).json(t, &page)
	var ids []string
	for _, it := range page.Items {
		ids = append(ids, it.ID)
	}
	return ids, page.NextCursor
}

func listAll(t *testing.T, e *env, p *domain.Principal, base string) []string {
	t.Helper()
	var all []string
	cursor := ""
	for i := 0; i < 1000; i++ {
		path := base
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		ids, next := listIDs(t, e, p, path)
		all = append(all, ids...)
		if next == nil {
			return all
		}
		cursor = *next
	}
	t.Fatal("pagination did not end")
	return nil
}

func TestList_CursorStableUnderInserts(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		prios := []domain.Priority{"low", "normal", "high", "urgent"}
		var orig []string
		for i := 0; i < 23; i++ {
			orig = append(orig, storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: prios[i%4],
				Status: domain.TicketStatuses[i%6], UpdatedAt: store.Now().Add(-time.Duration(i) * time.Minute)}))
		}
		for _, sort := range []string{"attention", "-updated_at", "updated_at", "priority", "-number", "due", "created_at"} {
			seen := map[string]int{}
			cursor := ""
			for page := 0; page < 50; page++ {
				path := "/api/v1/agent/tickets?limit=5&sort=" + url.QueryEscape(sort)
				if cursor != "" {
					path += "&cursor=" + url.QueryEscape(cursor)
				}
				ids, next := listIDs(t, e, sup, path)
				for _, id := range ids {
					seen[id]++
				}
				// Insert while paging.
				storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: prios[page%4]})
				if next == nil {
					break
				}
				cursor = *next
			}
			for _, id := range orig {
				if seen[id] != 1 {
					t.Fatalf("sort %s: ticket seen %d times", sort, seen[id])
				}
			}
		}
	})
}

func TestList_AttentionOrder(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		mk := func(st domain.TicketStatus, pr domain.Priority, staffAfterCustomer bool) string {
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Status: st, Priority: pr})
			if staffAfterCustomer {
				e.exec(`UPDATE tickets SET last_staff_activity_at = ? WHERE id = ?`, store.Now().Add(time.Minute), id)
			}
			return id
		}
		resolved := mk(domain.StatusResolved, "urgent", false)
		answered := mk(domain.StatusOpen, "urgent", true)
		waitingNormal := mk(domain.StatusOpen, "normal", false)
		waitingUrgent := mk(domain.StatusNew, "urgent", false)
		pending := mk(domain.StatusPending, "high", false)
		ids, _ := listIDs(t, e, sup, "/api/v1/agent/tickets")
		// Working statuses first; among the rest, priority still orders.
		want := []string{waitingUrgent, waitingNormal, answered, resolved, pending}
		if !slices.Equal(ids, want) {
			t.Fatalf("attention order\n got %v\nwant %v", ids, want)
		}
	})
}

func TestCounts_MatchListTotals(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		teamA, teamB := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		l2 := e.staff(domain.RoleAgent, 2, teamA)
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		for i := 0; i < 30; i++ {
			tk := storetest.Ticket{Requester: cust.UserID, Tier: 1 + i%3, Status: domain.TicketStatuses[i%6]}
			switch i % 4 {
			case 0:
				tk.TeamID = teamA
			case 1:
				tk.TeamID = teamB
			case 2:
				tk.TeamID = teamA
				if tk.Tier <= 2 {
					tk.AssigneeID = l2.UserID
				}
			}
			id := storetest.CreateTicket(t, db, tk)
			if i%5 == 0 {
				e.exec(`UPDATE tickets SET resolution_due_at = ? WHERE id = ?`, store.Now().Add(-time.Hour), id)
			}
		}
		open := "&status=new,open,pending,on_hold"
		for _, p := range []*domain.Principal{l2, sup} {
			var c Counts
			e.do(p, "GET", "/api/v1/agent/tickets/counts", nil).expect(t, 200).json(t, &c)
			check := func(name string, got int, q string) {
				if n := len(listAll(t, e, p, "/api/v1/agent/tickets?limit=200"+open+q)); n != got {
					t.Errorf("%s %s: count %d, list %d", p.Role, name, got, n)
				}
			}
			check("mine", c.Mine, "&assignee=me")
			check("unassigned", c.Unassigned, "&assignee=none")
			check("triage", c.Triage, "&team_id=none")
			check("breached", c.Breached, "&breached=true")
			for tier := 1; tier <= 3; tier++ {
				check(fmt.Sprint("tier", tier), c.Tier[fmt.Sprint(tier)], fmt.Sprintf("&tier=%d", tier))
			}
		}
	})
}

func TestList_CustomerFiltersIgnoreStaffParams(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		e.createTicket(cust, CreateInput{})
		for _, q := range []string{"assignee=me", "tier=2", "team_id=none", "sort=priority"} {
			e.do(cust, "GET", "/api/v1/portal/tickets?"+q, nil).expect(t, 422)
		}
		e.do(cust, "GET", "/api/v1/portal/tickets?status=open", nil).expect(t, 200)
	})
}

func TestList_FiltersAndSearchParams(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer("", domain.RoleRequester)
		a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: "high"})
		storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: "low"})
		ids, _ := listIDs(t, e, agent, "/api/v1/agent/tickets?priority=high,urgent")
		if !slices.Equal(ids, []string{a}) {
			t.Fatalf("priority filter %v", ids)
		}
		e.do(agent, "GET", "/api/v1/agent/tickets?bogus=1", nil).expect(t, 422)
		e.do(agent, "GET", "/api/v1/agent/tickets?cursor=not-a-cursor", nil).expect(t, 422)
		e.do(agent, "GET", "/api/v1/agent/tickets?status=nope", nil).expect(t, 422)
	})
}

// ---- TKT-005 detail ----

func TestDetail_AllowedActionsMatchCan(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		teamA, teamB := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		org := storetest.CreateOrg(t, db, "")
		var ps []*domain.Principal
		for tier := 1; tier <= 3; tier++ {
			ps = append(ps, e.staff(domain.RoleAgent, tier, teamA), e.staff(domain.RoleAgent, tier, teamB), e.staff(domain.RoleAgent, tier, teamA, teamB))
		}
		ps = append(ps, e.staff(domain.RoleSupervisor, 1), e.staff(domain.RoleAdmin, 1), e.staff(domain.RoleSupervisor, 2, teamA), e.staff(domain.RoleAdmin, 3, teamB))
		req := e.customer(org, domain.RoleRequester)
		ps = append(ps, req, e.customer(org, domain.RoleOrgManager), e.customer(org, domain.RoleRequester), e.customer("", domain.RoleRequester))
		ps = append(ps, e.staff(domain.RoleAgent, 2), e.pat(ps[0], domain.ScopeTicketsRead), e.pat(req, domain.ScopeTicketsRead))
		if len(ps) != 20 {
			t.Fatalf("%d principals", len(ps))
		}
		ctx := context.Background()
		pol := e.svc.Policy(ctx)
		checked := 0
		for i, st := range domain.TicketStatuses {
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: req.UserID, OrgID: org, TeamID: teamA, Tier: 1 + i%2, Status: st,
				AssigneeID: ps[i%3].UserID})
			e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, store.Now(), id)
			row, _ := lockFreeLoad(ctx, e.svc, id)
			for _, p := range ps {
				path := "/api/v1/agent/tickets/" + id
				if p.IsCustomer() {
					path = "/api/v1/portal/tickets/" + id
				}
				r := e.do(p, "GET", path, nil)
				if r.StatusCode == 404 {
					if authz.CanSeeTicket(p, row.view(pol, time.Now())) {
						t.Fatalf("%s got 404 on a visible ticket", p.UserID)
					}
					continue
				}
				var d struct {
					AllowedActions []string `json:"allowed_actions"`
				}
				r.expect(t, 200).json(t, &d)
				for _, a := range authz.TicketActions {
					can := authz.Can(p, a, row.view(pol, time.Now())) == nil
					if can != slices.Contains(d.AllowedActions, string(a)) {
						t.Errorf("%s %s %s: Can=%v listed=%v", p.Role, st, a, can, !can)
					}
				}
				checked++
			}
		}
		if checked < 60 {
			t.Fatalf("only %d visible combinations checked", checked)
		}
	})
}

func TestDetail_ByNumberVisibility404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		teamA, teamB := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		l1 := e.staff(domain.RoleAgent, 1, teamA)
		cust := e.customer("", domain.RoleRequester)
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: teamB})
		var n int64
		_ = db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, id).Scan(&n)
		e.do(l1, "GET", fmt.Sprintf("/api/v1/agent/tickets/by-number/%d", n), nil).expect(t, 404)
		r := e.do(e.staff(domain.RoleAgent, 1, teamB), "GET", fmt.Sprintf("/api/v1/agent/tickets/by-number/%d", n), nil).expect(t, 200)
		if r.Header.Get("ETag") == "" {
			t.Fatal("no ETag")
		}
		e.do(l1, "GET", "/api/v1/agent/tickets/by-number/999999", nil).expect(t, 404)
	})
}

func TestPortalDetail_StatusLabelMapping(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		want := map[domain.TicketStatus]string{"new": "open", "open": "open", "on_hold": "open", "pending": "awaiting_reply", "resolved": "resolved", "closed": "closed"}
		for st, label := range want {
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, Status: st})
			got := e.do(cust, "GET", "/api/v1/portal/tickets/"+id, nil).expect(t, 200).m(t)["status"]
			if got != label {
				t.Errorf("%s → %v, want %s", st, got, label)
			}
		}
		ids, _ := listIDs(t, e, cust, "/api/v1/portal/tickets?status=awaiting_reply")
		if len(ids) != 1 {
			t.Fatalf("awaiting_reply filter %v", ids)
		}
	})
}

// ---- TKT-009 events ----

// TestEveryMutationEmitsEvent: each service mutation writes exactly one event
// of its expected type (plus documented automatic transitions).
func TestEveryMutationEmitsEvent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		e := f.e
		ctx := context.Background()
		sup := e.staff(domain.RoleSupervisor, 1)
		other := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.cust.UserID, TeamID: f.teamA})
		cat := storetest.CreateCategory(t, db, "", "")
		e.svc.dir.Invalidate()
		steps := []struct {
			name string
			typ  string
			run  func() error
		}{
			{"assign", events.TicketAssigned, func() error {
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{AssigneeID: optStr{true, sp(f.l2.UserID)}}, "")
				return err
			}},
			{"priority", events.TicketPriorityChanged, func() error {
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{Priority: optStr{true, sp("high")}}, "")
				return err
			}},
			{"category", events.TicketCategoryChanged, func() error {
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{CategoryID: optStr{true, sp(cat)}}, "")
				return err
			}},
			{"subject", events.TicketSubjectChanged, func() error {
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{Subject: optStr{true, sp("New subject")}}, "")
				return err
			}},
			{"tags", events.TicketTagsChanged, func() error {
				tags := []string{"vpn"}
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{Tags: &tags}, "")
				return err
			}},
			{"reply", events.MessageCreated, func() error {
				_, err := e.svc.AddStaffMessage(ctx, f.l2, f.id, MessageInput{Kind: "public", BodyMD: "x"})
				return err
			}},
			{"note", events.NoteCreated, func() error {
				_, err := e.svc.AddStaffMessage(ctx, f.l2, f.id, MessageInput{Kind: "internal", BodyMD: "x"})
				return err
			}},
			{"status", events.TicketStatusChanged, func() error {
				_, err := e.svc.Patch(ctx, f.l2, f.id, PatchInput{Status: optStr{true, sp("pending")}}, "")
				return err
			}},
			{"customer reply", events.MessageCreated, func() error {
				_, err := e.svc.AddCustomerReply(ctx, f.cust, f.id, MessageInput{BodyMD: "y"})
				return err
			}},
			{"escalate", events.TicketEscalated, func() error { _, err := e.svc.Escalate(ctx, f.l2, f.id, 1, EscalateInput{Reason: "r"}); return err }},
			{"deescalate", events.TicketDeescalated, func() error { _, err := e.svc.Escalate(ctx, sup, f.id, -1, EscalateInput{Reason: "r"}); return err }},
			{"transfer", events.TicketTeamChanged, func() error {
				_, err := e.svc.Patch(ctx, sup, f.id, PatchInput{TeamID: optStr{true, sp(f.teamB)}}, "")
				return err
			}},
			{"link", events.TicketLinked, func() error {
				_, err := e.svc.CreateLink(ctx, sup, f.id, LinkInput{ToTicketID: other, Type: "related_to"})
				return err
			}},
			{"customer resolve", events.TicketStatusChanged, func() error { return e.svc.CustomerResolve(ctx, f.cust, f.id) }},
			{"customer reopen", events.TicketStatusChanged, func() error { return e.svc.CustomerReopen(ctx, f.cust, f.id) }},
		}
		for _, s := range steps {
			before := len(e.eventsOf(f.id))
			if err := s.run(); err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			got := e.eventsOf(f.id)[before:]
			n := 0
			for _, g := range got {
				if g == s.typ {
					n++
				}
			}
			if n != 1 {
				t.Errorf("%s: events %v, want exactly one %s", s.name, got, s.typ)
			}
		}
	})
}

func TestEvents_AppendOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		id := e.createTicket(e.customer("", domain.RoleRequester), CreateInput{})
		ctx := context.Background()
		for _, q := range []string{`UPDATE ticket_events SET type = 'x' WHERE ticket_id = ?`, `DELETE FROM ticket_events WHERE ticket_id = ?`} {
			err := db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q, id); return err })
			if err == nil {
				t.Fatalf("%q succeeded (M-5)", q)
			}
		}
	})
}

// TestEmit_OnlyEventRowInTx: a mutation changes domain tables and appends
// ticket_events rows, and writes nothing to the queue/outbox/index tables.
func TestEmit_OnlyEventRowInTx(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		tables := []string{"deferred_jobs", "idempotency_keys", "audit_log", "webhook_deliveries", "settings", "inbound_messages"}
		if db.Dialect == store.SQLite {
			tables = append(tables, "tickets_fts")
		} else {
			tables = append(tables, "ticket_search")
		}
		snap := func() map[string]int {
			m := map[string]int{}
			for _, tb := range tables {
				m[tb] = f.e.count(`SELECT COUNT(*) FROM ` + tb)
			}
			return m
		}
		cursor, _ := store.Cursor(context.Background(), db.Read(), "dispatch_cursor")
		before, evs := snap(), f.e.count(`SELECT COUNT(*) FROM ticket_events`)
		f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "public", "body_md": "hi", "set_status": "pending"}).expect(t, 201)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		after := snap()
		for k, v := range before {
			if after[k] != v {
				t.Errorf("table %s changed %d → %d", k, v, after[k])
			}
		}
		if f.e.count(`SELECT COUNT(*) FROM ticket_events`) <= evs {
			t.Fatal("no event written")
		}
		if c, _ := store.Cursor(context.Background(), db.Read(), "dispatch_cursor"); c != cursor {
			t.Fatal("the mutation touched the dispatch cursor")
		}
	})
}

func TestEmit_RollbackNoSignal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		var mu sync.Mutex
		commits := 0
		db.OnCommit(func() { mu.Lock(); commits++; mu.Unlock() })
		evs := f.e.count(`SELECT COUNT(*) FROM ticket_events`)
		f.e.do(f.l1, "POST", f.path("/messages"), map[string]any{"kind": "public", "body_md": "hi", "set_status": "closed"}).expect(t, 409)
		mu.Lock()
		defer mu.Unlock()
		if commits != 0 || f.e.count(`SELECT COUNT(*) FROM ticket_events`) != evs {
			t.Fatalf("rolled-back mutation signalled %d commits", commits)
		}
	})
}

func TestEmit_SnapshotMatchesTicketAfterChange(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		sup := f.e.staff(domain.RoleSupervisor, 1)
		f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, f.id, f.l2B.UserID, store.Now())
		ctx := context.Background()
		check := func(name string) {
			row, _ := lockFreeLoad(ctx, f.e.svc, f.id)
			f.e.svc.fillKey(ctx, row)
			evs, err := events.ForTicket(ctx, db.Read(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			got := evs[len(evs)-1].Snapshot
			want := row.snapshot()
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s: snapshot %+v, ticket %+v", name, got, want)
			}
		}
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		check("assign")
		f.e.do(f.l2, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		check("escalate")
		f.e.do(sup, "PATCH", f.path(""), map[string]any{"team_id": f.teamB}).expect(t, 200)
		check("transfer")
	})
}
