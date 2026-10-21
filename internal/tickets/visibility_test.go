// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// fixture is a small world used by the TKT-001 visibility tests.
type fixture struct {
	e                *env
	orgA, orgB       string
	teamA, teamB     string
	l1, l2, l1B, sup *domain.Principal
	reqA, reqA2, mgr *domain.Principal
	reqB             *domain.Principal
}

func newFixture(t *testing.T, db *store.DB) *fixture {
	e := newEnv(t, db)
	f := &fixture{e: e}
	f.orgA, f.orgB = storetest.CreateOrg(t, db, ""), storetest.CreateOrg(t, db, "")
	f.teamA, f.teamB = storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
	f.l1 = e.staff(domain.RoleAgent, 1, f.teamA)
	f.l2 = e.staff(domain.RoleAgent, 2, f.teamA)
	f.l1B = e.staff(domain.RoleAgent, 1, f.teamB)
	f.sup = e.staff(domain.RoleSupervisor, 1)
	f.reqA = e.customer(f.orgA, domain.RoleRequester)
	f.reqA2 = e.customer(f.orgA, domain.RoleRequester)
	f.mgr = e.customer(f.orgA, domain.RoleOrgManager)
	f.reqB = e.customer(f.orgB, domain.RoleRequester)
	return f
}

// ticket inserts a ticket with a real event-free fixture row.
func (f *fixture) ticket(t *testing.T, req *domain.Principal, team string, tier int, assignee string) string {
	return storetest.CreateTicket(t, f.e.db, storetest.Ticket{Requester: req.UserID, OrgID: req.OrganizationID, TeamID: team, Tier: tier, AssigneeID: assignee})
}

func (f *fixture) agentGet(p *domain.Principal, id string) int {
	return f.e.do(p, "GET", "/api/v1/agent/tickets/"+id, nil).StatusCode
}

func (f *fixture) portalGet(p *domain.Principal, id string) int {
	return f.e.do(p, "GET", "/api/v1/portal/tickets/"+id, nil).StatusCode
}

func TestVisibility_L1CannotSeeL2Ticket(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		id := f.ticket(t, f.reqA, f.teamA, 2, "")
		if c := f.agentGet(f.l1, id); c != 404 {
			t.Fatalf("L1 on tier-2 ticket: %d, want 404", c)
		}
		if c := f.agentGet(f.l2, id); c != 200 {
			t.Fatalf("L2: %d", c)
		}
	})
}

func TestVisibility_AgentSeesAssignedOutsideTeam(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		id := f.ticket(t, f.reqA, f.teamB, 1, f.l1.UserID)
		if c := f.agentGet(f.l1, id); c != 200 {
			t.Fatalf("assignee outside team: %d", c)
		}
	})
}

func TestVisibility_AgentOtherTeam404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		id := f.ticket(t, f.reqA, f.teamB, 1, "")
		if c := f.agentGet(f.l1, id); c != 404 {
			t.Fatalf("other team: %d", c)
		}
		if c := f.agentGet(f.sup, id); c != 200 {
			t.Fatalf("supervisor: %d", c)
		}
	})
}

func setSupport(t *testing.T, e *env, mod func(*settings.Support)) {
	t.Helper()
	sup, err := e.set.Support(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mod(&sup)
	if err := e.set.Put(context.Background(), settings.KeySupport, sup, ""); err != nil {
		t.Fatal(err)
	}
}

func TestVisibility_TriageFlag(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		id := f.ticket(t, f.reqA, "", 1, "")
		if c := f.agentGet(f.l1, id); c != 200 {
			t.Fatalf("triage visible by default: %d", c)
		}
		setSupport(t, f.e, func(s *settings.Support) { s.TriageVisibleToAgents = false })
		if c := f.agentGet(f.l1, id); c != 404 {
			t.Fatalf("triage hidden when flag off: %d", c)
		}
		if c := f.agentGet(f.sup, id); c != 200 {
			t.Fatalf("supervisor always sees triage (I-8): %d", c)
		}
	})
}

func TestVisibility_RequesterOnlyOwn(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		own := f.ticket(t, f.reqA, f.teamA, 1, "")
		other := f.ticket(t, f.reqA2, f.teamA, 1, "")
		if c := f.portalGet(f.reqA, own); c != 200 {
			t.Fatalf("own: %d", c)
		}
		if c := f.portalGet(f.reqA, other); c != 404 {
			t.Fatalf("colleague's ticket: %d", c)
		}
		page := f.e.do(f.reqA, "GET", "/api/v1/portal/tickets", nil).expect(t, 200).m(t)
		if n := len(page["items"].([]any)); n != 1 {
			t.Fatalf("requester list has %d items", n)
		}
	})
}

func TestVisibility_OrgManagerSeesOrg(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		f.ticket(t, f.reqA, f.teamA, 1, "")
		other := f.ticket(t, f.reqA2, f.teamA, 2, "")
		if c := f.portalGet(f.mgr, other); c != 200 {
			t.Fatalf("org manager: %d", c)
		}
		page := f.e.do(f.mgr, "GET", "/api/v1/portal/tickets", nil).expect(t, 200).m(t)
		if n := len(page["items"].([]any)); n != 2 {
			t.Fatalf("org manager list %d", n)
		}
		mine := f.e.do(f.mgr, "GET", "/api/v1/portal/tickets?mine_only=true", nil).expect(t, 200).m(t)
		if n := len(mine["items"].([]any)); n != 0 {
			t.Fatalf("mine_only %d", n)
		}
	})
}

func TestVisibility_OrgManagerOtherOrg404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		id := f.ticket(t, f.reqB, f.teamA, 1, "")
		if c := f.portalGet(f.mgr, id); c != 404 {
			t.Fatalf("other org: %d", c)
		}
	})
}

// TestPortalMessages_NeverInternal (I-2): internal notes and system messages
// (escalation reasons) never appear through any portal endpoint.
func TestPortalMessages_NeverInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		ctx := context.Background()
		id := f.e.createTicket(f.reqA, CreateInput{Subject: "Mail", BodyMD: "public question"})
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"team_id": f.teamA}).expect(t, 200)
		if _, err := f.e.svc.AddStaffMessage(ctx, f.l1, id, MessageInput{Kind: "internal", BodyMD: "SECRET-NOTE"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.svc.Escalate(ctx, f.l1, id, +1, EscalateInput{Reason: "SECRET-REASON"}); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/v1/portal/tickets/" + id, "/api/v1/portal/tickets", "/api/v1/portal/tickets?q=SECRET"} {
			r := f.e.do(f.reqA, "GET", path, nil).expect(t, 200)
			for _, w := range []string{"SECRET-NOTE", "SECRET-REASON", "internal", "system"} {
				if containsBytes(r.body, w) {
					t.Fatalf("%s leaks %q: %s", path, w, r.body)
				}
			}
		}
		r := f.e.do(f.reqA, "POST", "/api/v1/portal/tickets/"+id+"/messages", map[string]any{"body_md": "thanks"}).expect(t, 201)
		if containsBytes(r.body, "SECRET") {
			t.Fatal("reply response leaks")
		}
	})
}

func containsBytes(b []byte, s string) bool { return len(s) > 0 && bytesIndex(b, s) >= 0 }

func bytesIndex(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}

// TestCanRead_AgreesWithVisibilityPredicate (AUTH-011 / TKT-001): the in-memory
// CanSeeTicket and the SQL predicate agree on 1 000 random (principal, ticket)
// pairs, staff and customer (CC) watchers, PAT scopes and the triage flag
// included; Can() answers 404 exactly when the predicate matches no row.
func TestCanRead_AgreesWithVisibilityPredicate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		ctx := context.Background()
		rnd := rand.New(rand.NewSource(42))
		staffs := []*domain.Principal{f.l1, f.l2, f.l1B, f.sup, f.e.staff(domain.RoleAgent, 3, f.teamA, f.teamB)}
		custs := []*domain.Principal{f.reqA, f.reqA2, f.mgr, f.reqB, f.e.customer("", domain.RoleRequester)}
		// PATs: tickets:write implies reading what it changes; kb:read alone sees no ticket (I-6).
		tokens := []*domain.Principal{f.e.pat(f.l2, domain.ScopeKBRead), f.e.pat(f.l1, domain.ScopeTicketsWrite),
			f.e.pat(f.mgr, domain.ScopeTicketsRead), f.e.pat(f.reqA, domain.ScopeKBRead)}
		teams := []string{"", f.teamA, f.teamB}
		var ids []string
		for i := 0; i < 40; i++ {
			req := custs[rnd.Intn(len(custs))]
			asg := ""
			if rnd.Intn(3) == 0 {
				asg = staffs[rnd.Intn(len(staffs))].UserID
			}
			id := f.ticket(t, req, teams[rnd.Intn(len(teams))], 1+rnd.Intn(3), asg)
			if rnd.Intn(4) == 0 {
				w := staffs[rnd.Intn(len(staffs))].UserID
				f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, id, w, store.Now())
			}
			if rnd.Intn(3) == 0 { // customer CC watcher (V1): a colleague of the requester
				for _, c := range custs {
					if c != req && c.OrganizationID != "" && c.OrganizationID == req.OrganizationID {
						f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, id, c.UserID, store.Now())
						break
					}
				}
			}
			ids = append(ids, id)
		}
		all := append(append(append([]*domain.Principal{}, staffs...), custs...), tokens...)
		checked := 0
		for _, triage := range []bool{true, false} {
			pol := authz.DefaultTicketPolicy()
			pol.TriageVisibleToAgents = triage
			for checked < 500*(map[bool]int{true: 1, false: 2}[triage]) {
				p := all[rnd.Intn(len(all))]
				id := ids[rnd.Intn(len(ids))]
				t0, err := lockFreeLoad(ctx, f.e.svc, id)
				if err != nil {
					t.Fatal(err)
				}
				mem := authz.CanSeeTicket(p, t0.view(pol, f.e.svc.clock()))
				pred, args := authz.TicketPredicate(p, pol, "t")
				n := f.e.count(`SELECT COUNT(*) FROM tickets t WHERE t.id = ? AND `+pred, append([]any{id}, args...)...)
				if mem != (n == 1) {
					t.Fatalf("disagree for %s on %s (triage=%v): memory=%v sql=%d", p.UserID, id, triage, mem, n)
				}
				// Can() answers 404 for every action exactly when SQL sees no row.
				for _, a := range authz.TicketActions {
					err := authz.Can(p, a, t0.view(pol, f.e.svc.clock()))
					if notFound := err != nil && problems.From(err).Status() == 404; notFound != (n == 0) {
						t.Fatalf("%s %s on %s: Can=%v but sql=%d", p.UserID, a, id, err, n)
					}
				}
				checked++
			}
		}
		if checked != 1000 {
			t.Fatal(fmt.Sprint("checked ", checked))
		}
	})
}

func lockFreeLoad(ctx context.Context, s *Service, id string) (*ticketRow, error) {
	q := s.db.Read()
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`, id))
	if err != nil {
		return nil, err
	}
	return t, loadWatchers(ctx, q, t)
}
