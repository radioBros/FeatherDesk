// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"slices"
	"sort"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// ---- helpers shared by the automation tests (D-19 §A) ----

// setStrategy sets a team's assignment strategy through PATCH /admin/teams.
func (e *env) setStrategy(admin *domain.Principal, teamID, strategy string) {
	e.t.Helper()
	var name string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT name FROM teams WHERE id = ?`, teamID).Scan(&name); err != nil {
		e.t.Fatal(err)
	}
	got := e.do(admin, "PATCH", "/api/v1/admin/teams/"+teamID, map[string]any{"name": name, "assignment_strategy": strategy}).expect(e.t, 200).m(e.t)
	if got["assignment_strategy"] != strategy {
		e.t.Fatalf("strategy not saved: %v", got)
	}
}

// assigneeOf reads the current assignee ("" = none).
func (e *env) assigneeOf(id string) string {
	e.t.Helper()
	var a *string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT assignee_id FROM tickets WHERE id = ?`, id).Scan(&a); err != nil {
		e.t.Fatal(err)
	}
	if a == nil {
		return ""
	}
	return *a
}

// portalTicket files a ticket as the customer in category cat.
func (e *env) portalTicket(cust *domain.Principal, cat, subject string) string {
	e.t.Helper()
	return e.createTicket(cust, CreateInput{Subject: subject, BodyMD: "Please help with " + subject, CategoryID: sp(cat)})
}

func sortedIDs(ids ...string) []string {
	out := slices.Clone(ids)
	sort.Strings(out)
	return out
}

// TestAssign_RoundRobinFairness: a round-robin team hands new tickets to its
// eligible agents in turn (id order), each exactly as often, as the
// automation actor; the pointer survives a restart through the lazy flush.
func TestAssign_RoundRobinFairness(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		agents := sortedIDs(e.staff(domain.RoleAgent, 1, team).UserID, e.staff(domain.RoleAgent, 1, team).UserID, e.staff(domain.RoleAgent, 2, team).UserID)
		e.setStrategy(admin, team, StrategyRoundRobin)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)

		counts := map[string]int{}
		var last string
		for i := 0; i < 9; i++ {
			id := e.portalTicket(cust, cat, "rr")
			got := e.assigneeOf(id)
			if want := agents[i%3]; got != want {
				t.Fatalf("ticket %d went to %s, want %s (rotation)", i, got, want)
			}
			counts[got]++
			last = got
			evs, err := events.ForTicket(context.Background(), db.Read(), id)
			if err != nil {
				t.Fatal(err)
			}
			if evs[0].Type != events.TicketCreated || evs[1].Type != events.TicketAssigned || evs[1].ActorID != events.ActorAutomation {
				t.Fatalf("events: %+v", evs)
			}
			if evs[2].Type != events.TicketStatusChanged { // new → open
				t.Fatalf("assignment did not open the ticket: %+v", evs)
			}
		}
		for _, a := range agents {
			if counts[a] != 3 {
				t.Fatalf("unfair distribution: %v", counts)
			}
		}
		// Lazy persistence: one UPDATE per team on flush, read back after a restart.
		if err := e.svc.FlushRoundRobin(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := e.count(`SELECT COUNT(*) FROM teams WHERE id = ? AND rr_last_user_id = ?`, team, last); n != 1 {
			t.Fatal("pointer not persisted")
		}
		e2 := newEnv(t, db)
		e2.register(cust)
		id := e2.portalTicket(cust, cat, "after restart")
		if got := e2.assigneeOf(id); got != agents[0] {
			t.Fatalf("after restart the rotation restarted wrongly: %s", got)
		}
	})
}

// TestAssign_M4TierAndTeamEligibility: only active members of the ticket's
// team with an effective tier ≥ the ticket tier are picked; an escalation
// that clears the assignee re-assigns within the new tier.
func TestAssign_M4TierAndTeamEligibility(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team, other := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		l1 := e.staff(domain.RoleAgent, 1, team)
		l2 := e.staff(domain.RoleAgent, 2, team)
		disabled := e.staff(domain.RoleAgent, 3, team)
		e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, disabled.UserID)
		outsider := e.staff(domain.RoleAgent, 3, other)
		e.setStrategy(admin, team, StrategyRoundRobin)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)

		var onL1 string
		for i := 0; i < 6; i++ {
			id := e.portalTicket(cust, cat, "m4")
			got := e.assigneeOf(id)
			if got != l1.UserID && got != l2.UserID {
				t.Fatalf("picked an ineligible agent %s (disabled %s, outsider %s)", got, disabled.UserID, outsider.UserID)
			}
			if got == l1.UserID {
				onL1 = id
			}
		}
		if onL1 == "" {
			t.Fatal("L1 never picked")
		}
		// Escalating to tier 2 clears the L1 owner; the strategy picks L2.
		sup := e.staff(domain.RoleSupervisor, 3, team)
		if _, err := e.svc.Escalate(ctx, sup, onL1, +1, EscalateInput{Reason: "needs L2"}); err != nil {
			t.Fatal(err)
		}
		if got := e.assigneeOf(onL1); got != l2.UserID && got != sup.UserID {
			t.Fatalf("after escalation assignee is %q, want an L2+ member", got)
		}
		// A tier-3 ticket in a team with only L1/L2 agents: only the
		// supervisor (effective tier 3) qualifies.
		if _, err := e.svc.Escalate(ctx, sup, onL1, +1, EscalateInput{Reason: "needs L3"}); err != nil {
			t.Fatal(err)
		}
		if got := e.assigneeOf(onL1); got != sup.UserID {
			t.Fatalf("tier-3 ticket assigned to %q, want the supervisor", got)
		}
	})
}

// TestAssign_LeastBusy picks the eligible agent with the fewest open
// tickets (ties to the lowest id); resolved and closed tickets don't count.
func TestAssign_LeastBusy(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		ids := sortedIDs(e.staff(domain.RoleAgent, 1, team).UserID, e.staff(domain.RoleAgent, 1, team).UserID)
		busy, idle := ids[0], ids[1]
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		for _, st := range []domain.TicketStatus{domain.StatusOpen, domain.StatusPending, domain.StatusClosed} {
			storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, AssigneeID: busy, Status: st})
		}
		e.setStrategy(admin, team, StrategyLeastBusy)
		want := []string{idle, idle, busy} // 0/2 → 1/2 → 2/2 tie → lowest id
		for i, w := range want {
			id := e.portalTicket(cust, cat, "lb")
			if got := e.assigneeOf(id); got != w {
				t.Fatalf("ticket %d: got %s, want %s", i, got, w)
			}
		}
	})
}

// TestAssign_AwayAgentsSkipped: PATCH /me/availability takes an agent out
// of automatic assignment until it is reset; manual assignment still works.
func TestAssign_AwayAgentsSkipped(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		a, b := e.staff(domain.RoleAgent, 1, team), e.staff(domain.RoleAgent, 1, team)
		e.setStrategy(admin, team, StrategyRoundRobin)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)

		if got := e.do(a, "GET", "/api/v1/me/availability", nil).expect(t, 200).m(t); got["away"] != false || got["away_since"] != nil {
			t.Fatalf("initial availability: %v", got)
		}
		got := e.do(a, "PATCH", "/api/v1/me/availability", map[string]any{"away": true}).expect(t, 200).m(t)
		if got["away"] != true || got["away_since"] == nil {
			t.Fatalf("away: %v", got)
		}
		e.do(a, "PATCH", "/api/v1/me/availability", map[string]any{}).expect(t, 422)
		e.do(cust, "GET", "/api/v1/me/availability", nil).expect(t, 404)
		e.do(cust, "PATCH", "/api/v1/me/availability", map[string]any{"away": true}).expect(t, 404)

		var last string
		for i := 0; i < 3; i++ {
			last = e.portalTicket(cust, cat, "away")
			if got := e.assigneeOf(last); got != b.UserID {
				t.Fatalf("ticket %d went to %q while %s is away", i, got, a.UserID)
			}
		}
		// Manual assignment ignores the flag.
		if _, err := e.svc.Patch(ctx, admin, last, PatchInput{AssigneeID: optStr{true, sp(a.UserID)}}, ""); err != nil {
			t.Fatalf("manual assignment to an away agent: %v", err)
		}
		// Everyone away: the ticket stays unassigned.
		e.do(b, "PATCH", "/api/v1/me/availability", map[string]any{"away": true}).expect(t, 200)
		if got := e.assigneeOf(e.portalTicket(cust, cat, "nobody")); got != "" {
			t.Fatalf("assigned to %s although everyone is away", got)
		}
		e.do(a, "PATCH", "/api/v1/me/availability", map[string]any{"away": false}).expect(t, 200)
		if got := e.assigneeOf(e.portalTicket(cust, cat, "back")); got != a.UserID {
			t.Fatalf("back agent not picked: %q", got)
		}
	})
}

// TestAssign_UnassignAndTeamChangeReassign: unassigning hands the ticket to
// another eligible agent; a transfer to a strategy team assigns there; a
// manual team leaves it unassigned.
func TestAssign_UnassignAndTeamChangeReassign(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		t1, t2, t3 := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", t1)
		a, b := e.staff(domain.RoleAgent, 1, t1), e.staff(domain.RoleAgent, 1, t1)
		c := e.staff(domain.RoleAgent, 1, t2)
		e.staff(domain.RoleAgent, 1, t3)
		e.setStrategy(admin, t1, StrategyRoundRobin)
		e.setStrategy(admin, t2, StrategyLeastBusy)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)

		id := e.portalTicket(cust, cat, "reassign")
		first := e.assigneeOf(id)
		if _, err := e.svc.Patch(ctx, admin, id, PatchInput{AssigneeID: optStr{true, nil}}, ""); err != nil {
			t.Fatal(err)
		}
		other := a.UserID
		if first == a.UserID {
			other = b.UserID
		}
		if got := e.assigneeOf(id); got != other {
			t.Fatalf("after unassign: %q, want the other agent %s", got, other)
		}
		if _, err := e.svc.Patch(ctx, admin, id, PatchInput{TeamID: optStr{true, sp(t2)}}, ""); err != nil {
			t.Fatal(err)
		}
		if got := e.assigneeOf(id); got != c.UserID {
			t.Fatalf("after transfer: %q, want %s", got, c.UserID)
		}
		if _, err := e.svc.Patch(ctx, admin, id, PatchInput{TeamID: optStr{true, sp(t3)}}, ""); err != nil {
			t.Fatal(err)
		}
		if got := e.assigneeOf(id); got != "" {
			t.Fatalf("manual team auto-assigned %q", got)
		}
	})
}

// TestTeams_AssignmentStrategyField: the field defaults to manual, a PATCH
// without it keeps the value, and an unknown strategy is a 422.
func TestTeams_AssignmentStrategyField(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		team := e.do(admin, "POST", "/api/v1/admin/teams", map[string]any{"name": "Strategy team"}).expect(t, 201).m(t)
		if team["assignment_strategy"] != StrategyManual {
			t.Fatalf("default: %v", team)
		}
		id := team["id"].(string)
		e.do(admin, "PATCH", "/api/v1/admin/teams/"+id, map[string]any{"name": "Strategy team", "assignment_strategy": "round_robin"}).expect(t, 200)
		got := e.do(admin, "PATCH", "/api/v1/admin/teams/"+id, map[string]any{"name": "Renamed"}).expect(t, 200).m(t)
		if got["assignment_strategy"] != StrategyRoundRobin {
			t.Fatalf("PATCH without the field reset it: %v", got)
		}
		p := e.do(admin, "PATCH", "/api/v1/admin/teams/"+id, map[string]any{"name": "Renamed", "assignment_strategy": "random"}).expect(t, 422).problem(t)
		if errs := p["errors"].([]any); errs[0].(map[string]any)["field"] != "assignment_strategy" {
			t.Fatalf("problem: %v", p)
		}
		created := e.do(admin, "POST", "/api/v1/admin/teams", map[string]any{"name": "LB team", "assignment_strategy": "least_busy"}).expect(t, 201).m(t)
		if created["assignment_strategy"] != StrategyLeastBusy {
			t.Fatalf("create: %v", created)
		}
	})
}

// TestAssign_MembershipLossReassigns: removing the owner from the team
// unassigns their tickets (M-4) and the team strategy hands them on.
func TestAssign_MembershipLossReassigns(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		a, b := e.staff(domain.RoleAgent, 1, team), e.staff(domain.RoleAgent, 1, team)
		e.setStrategy(admin, team, StrategyRoundRobin)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		id := e.portalTicket(cust, cat, "membership")
		owner, other := a, b
		if e.assigneeOf(id) == b.UserID {
			owner, other = b, a
		}
		e.do(admin, "DELETE", "/api/v1/admin/teams/"+team+"/members/"+owner.UserID, nil).expect(t, 204)
		if got := e.assigneeOf(id); got != other.UserID {
			t.Fatalf("after membership loss: %q, want %s", got, other.UserID)
		}
	})
}
