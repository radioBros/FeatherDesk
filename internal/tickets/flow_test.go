// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// lifecycle is a ready team + agent + customer + ticket.
type lifecycle struct {
	e        *env
	team     string
	agent    *domain.Principal
	sup      *domain.Principal
	cust     *domain.Principal
	ticketID string
}

func newLifecycle(t *testing.T, db *store.DB) *lifecycle {
	e := newEnv(t, db)
	l := &lifecycle{e: e, team: storetest.CreateTeam(t, db, "")}
	cat := storetest.CreateCategory(t, db, "", l.team)
	l.agent = e.staff(domain.RoleAgent, 1, l.team)
	l.sup = e.staff(domain.RoleSupervisor, 1)
	l.cust = e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
	l.ticketID = e.createTicket(l.cust, CreateInput{CategoryID: &cat})
	return l
}

func (l *lifecycle) row(t *testing.T) *ticketRow {
	t.Helper()
	r, err := lockFreeLoad(context.Background(), l.e.svc, l.ticketID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (l *lifecycle) path(s string) string { return "/api/v1/agent/tickets/" + l.ticketID + s }

func TestAutoOpen_OnFirstStaffPublicReply(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "hello"}).expect(t, 201)
		r := l.row(t)
		if r.Status != domain.StatusOpen || !r.FirstRespondedAt.Valid || !r.LastStaffActivityAt.Valid {
			t.Fatalf("after first public reply: %s responded=%v", r.Status, r.FirstRespondedAt.Valid)
		}
	})
}

func TestAutoOpen_NotOnInternalNote(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "note"}).expect(t, 201)
		if r := l.row(t); r.Status != domain.StatusNew || r.FirstRespondedAt.Valid {
			t.Fatalf("note changed status to %s", r.Status)
		}
	})
}

// TestReply_SetsFirstRespondedOnPublicOnly (TKT-006).
func TestReply_SetsFirstRespondedOnPublicOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "n"}).expect(t, 201)
		if l.row(t).FirstRespondedAt.Valid {
			t.Fatal("internal note set first_responded_at")
		}
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "p"}).expect(t, 201)
		first := l.row(t).FirstRespondedAt.Time
		time.Sleep(2 * time.Millisecond)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "p2"}).expect(t, 201)
		if !l.row(t).FirstRespondedAt.Time.Equal(first) {
			t.Fatal("first_responded_at moved on the second reply")
		}
	})
}

func TestPendingToOpen_OnCustomerReply(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "need info", "set_status": "pending"}).expect(t, 201)
		if r := l.row(t); r.Status != domain.StatusPending || !r.SLAPausedAt.Valid {
			t.Fatalf("status %s paused %v", r.Status, r.SLAPausedAt.Valid)
		}
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/messages", map[string]any{"body_md": "here"}).expect(t, 201)
		if r := l.row(t); r.Status != domain.StatusOpen || r.SLAPausedAt.Valid {
			t.Fatalf("customer reply: status %s", r.Status)
		}
	})
}

// TestCustomerReply_OnPendingReopens is the TKT-006 name of the same rule.
func TestCustomerReply_OnPendingReopens(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "on_hold"}).expect(t, 200)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/messages", map[string]any{"body_md": "any news?"}).expect(t, 201)
		if r := l.row(t); r.Status != domain.StatusOpen {
			t.Fatalf("on_hold + customer reply: %s", r.Status)
		}
	})
}

func TestInvalidTransition409ListsAllowed(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		p := l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "closed"}).expect(t, 409).problem(t)
		if p["type"] != problems.BaseURI+problems.SlugInvalidTransition {
			t.Fatalf("type %v", p["type"])
		}
		allowed := p["allowed"].([]any)
		if len(allowed) != 4 || allowed[0] != "open" {
			t.Fatalf("allowed %v", allowed)
		}
	})
}

func TestClosedImmutable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "resolved"}).expect(t, 200)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "closed"}).expect(t, 200)
		before := l.row(t)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "late"}).expect(t, 403)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"priority": "high"}).expect(t, 403)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "open"}).expect(t, 403)
		if after := l.row(t); !after.UpdatedAt.Equal(before.UpdatedAt) || after.Priority != before.Priority {
			t.Fatal("closed ticket changed")
		}
		l.e.do(l.sup, "PATCH", l.path(""), map[string]any{"status": "open"}).expect(t, 200)
	})
}

// ---- TKT-006 messages ----

func TestReply_WithSetStatusAtomic(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		msgs := l.e.count(`SELECT COUNT(*) FROM ticket_messages`)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "x", "set_status": "closed"}).expect(t, 409)
		if l.e.count(`SELECT COUNT(*) FROM ticket_messages`) != msgs || l.row(t).Status != domain.StatusNew {
			t.Fatal("reply was not rolled back with the invalid status")
		}
	})
}

func TestNoteEdit_Window15Min(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		m := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "draft"}).expect(t, 201).m(t)
		mid := m["id"].(string)
		r := l.e.do(l.agent, "PATCH", l.path("/messages/"+mid), map[string]any{"body_md": "fixed"}).expect(t, 200).m(t)
		if r["body_md"] != "fixed" || r["edited_at"] == nil {
			t.Fatalf("edit %v", r)
		}
		l.e.exec(`UPDATE ticket_messages SET created_at = ? WHERE id = ?`, store.Now().Add(-16*time.Minute), mid)
		l.e.do(l.agent, "PATCH", l.path("/messages/"+mid), map[string]any{"body_md": "again"}).expect(t, 403)
		other := l.e.staff(domain.RoleAgent, 1, l.team)
		m2 := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "mine"}).expect(t, 201).m(t)
		l.e.do(other, "PATCH", l.path("/messages/"+m2["id"].(string)), map[string]any{"body_md": "theirs"}).expect(t, 403)
		if ev := l.e.eventsOf(l.ticketID); !slices.Contains(ev, events.MessageEdited) {
			t.Fatalf("no message.edited event: %v", ev)
		}
	})
}

func TestPublicReplyEdit403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		m := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "sent"}).expect(t, 201).m(t)
		l.e.do(l.agent, "PATCH", l.path("/messages/"+m["id"].(string)), map[string]any{"body_md": "changed"}).expect(t, 403)
		l.e.do(l.sup, "PATCH", l.path("/messages/"+m["id"].(string)), map[string]any{"body_md": "changed"}).expect(t, 403)
	})
}

func TestCustomerCannotPostInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/messages", map[string]any{"body_md": "psst", "kind": "internal"}).expect(t, 201)
		if n := l.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND kind <> 'public'`, l.ticketID); n != 0 {
			t.Fatalf("customer created %d non-public messages", n)
		}
		// Customers never reach the agent message route (I-1).
		l.e.do(l.cust, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "x"}).expect(t, 404)
	})
}

// ---- TKT-013 customer resolve / reopen / follow-up ----

func TestReopen_InsideWindow(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		r := l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/reopen", nil).expect(t, 200).m(t)
		if r["status"] != "open" {
			t.Fatalf("status %v", r["status"])
		}
	})
}

func TestReopen_OutsideWindow409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/resolve", nil).expect(t, 200)
		l.e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, store.Now().Add(-8*24*time.Hour), l.ticketID)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/reopen", nil).expect(t, 409)
		setSupport(t, l.e, func(s *settings.Support) { s.ReopenWindowDays = 30 })
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/reopen", nil).expect(t, 200)
	})
}

func TestReplyOnClosed_CreatesFollowup(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "resolved"}).expect(t, 200)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "closed"}).expect(t, 200)
		l.e.do(l.sup, "POST", l.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 403)
		before := l.row(t)
		p := l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+l.ticketID+"/messages", map[string]any{"body_md": "it broke again"}).expect(t, 409).problem(t)
		fid := p["detail"].(string)
		f, err := lockFreeLoad(context.Background(), l.e.svc, fid)
		if err != nil {
			t.Fatal(err)
		}
		if f.FollowupOfID != l.ticketID || f.Tier != 1 || f.TeamID != before.TeamID || f.CategoryID != before.CategoryID || f.RequesterID != l.cust.UserID {
			t.Fatalf("follow-up %+v", f)
		}
		after := l.row(t)
		if after.Status != domain.StatusClosed || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatal("original changed (M-7)")
		}
		if ev := l.e.eventsOf(l.ticketID); ev[len(ev)-1] != events.TicketFollowupCreated {
			t.Fatalf("original events %v", ev)
		}
		if ev := l.e.eventsOf(fid); !slices.Equal(ev, []string{events.TicketCreated, events.TicketFollowupCreated}) {
			t.Fatalf("follow-up events %v", ev)
		}
		c := l.e.do(l.cust, "GET", "/api/v1/portal/tickets/"+fid, nil).expect(t, 200).m(t)
		if c["followup_of_id"] != l.ticketID {
			t.Fatal("followup_of_id missing on portal")
		}
	})
}

// ---- TKT-010 escalation ----

type escFix struct {
	e               *env
	teamA, teamB    string
	l1, l2, l2B, l3 *domain.Principal
	cust            *domain.Principal
	id              string
}

func newEscFix(t *testing.T, db *store.DB) *escFix {
	e := newEnv(t, db)
	f := &escFix{e: e, teamA: storetest.CreateTeam(t, db, ""), teamB: storetest.CreateTeam(t, db, "")}
	f.l1 = e.staff(domain.RoleAgent, 1, f.teamA)
	f.l2 = e.staff(domain.RoleAgent, 2, f.teamA)
	f.l2B = e.staff(domain.RoleAgent, 2, f.teamB)
	f.l3 = e.staff(domain.RoleAgent, 3, f.teamA)
	f.cust = e.customer("", domain.RoleRequester)
	f.id = e.createTicket(f.cust, CreateInput{})
	e.do(e.staff(domain.RoleSupervisor, 1), "PATCH", "/api/v1/agent/tickets/"+f.id, map[string]any{"team_id": f.teamA}).expect(t, 200)
	return f
}

func (f *escFix) path(s string) string { return "/api/v1/agent/tickets/" + f.id + s }

func TestEscalate_L1ToL2ClearsL1Assignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": f.l1.UserID}).expect(t, 200)
		res := f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "needs network access"}).expect(t, 200).m(t)
		if res["visible"] != false {
			t.Fatalf("L1 should lose visibility: %v", res["visible"])
		}
		r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if r.Tier != 2 || r.AssigneeID != "" || r.Status != domain.StatusOpen {
			t.Fatalf("after escalate tier %d assignee %q status %s", r.Tier, r.AssigneeID, r.Status)
		}
		// I-4: the L1 actor gets 404 on re-read.
		f.e.do(f.l1, "GET", f.path(""), nil).expect(t, 404)
		f.e.do(f.l2, "GET", f.path(""), nil).expect(t, 200)
		ev := f.e.eventsOf(f.id)
		if ev[len(ev)-1] != events.TicketEscalated {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestEscalate_L3Max409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "a"}).expect(t, 200)
		f.e.do(f.l2, "POST", f.path("/escalate"), map[string]any{"reason": "b"}).expect(t, 200)
		f.e.do(f.l3, "POST", f.path("/escalate"), map[string]any{"reason": "c"}).expect(t, 409)
	})
}

func TestEscalate_ReasonRequired422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "   "}).expect(t, 422)
	})
}

func TestDeescalate_L1Forbidden403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "a"}).expect(t, 200)
		f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, f.id, f.l1.UserID, store.Now())
		f.e.do(f.l1, "POST", f.path("/deescalate"), map[string]any{"reason": "back"}).expect(t, 403)
	})
}

func TestDeescalate_KeepsAssignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "a"}).expect(t, 200)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		f.e.do(f.l2, "POST", f.path("/deescalate"), map[string]any{"reason": "simple after all"}).expect(t, 200)
		r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if r.Tier != 1 || r.AssigneeID != f.l2.UserID {
			t.Fatalf("tier %d assignee %q", r.Tier, r.AssigneeID)
		}
		if ev := f.e.eventsOf(f.id); ev[len(ev)-1] != events.TicketDeescalated {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestEscalate_SystemNoteInternalNotOnPortal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "CONFIDENTIAL-REASON"}).expect(t, 200)
		staff := f.e.do(f.l2, "GET", f.path(""), nil).expect(t, 200)
		if !containsBytes(staff.body, "CONFIDENTIAL-REASON") {
			t.Fatal("staff thread lacks the system note")
		}
		portal := f.e.do(f.cust, "GET", "/api/v1/portal/tickets/"+f.id, nil).expect(t, 200)
		if containsBytes(portal.body, "CONFIDENTIAL-REASON") {
			t.Fatal("escalation reason leaked to the portal (I-2)")
		}
	})
}

func TestEscalate_WithTeamMove(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		f.e.do(f.l2, "POST", f.path("/escalate"), map[string]any{"reason": "network team", "target_team_id": f.teamB}).expect(t, 200)
		r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if r.Tier != 2 || r.TeamID != f.teamB || r.AssigneeID != "" {
			t.Fatalf("tier %d team %q assignee %q", r.Tier, r.TeamID, r.AssigneeID)
		}
		f.e.do(f.l2B, "GET", f.path(""), nil).expect(t, 200)
		var data string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT data FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.escalated'`, f.id).Scan(&data)
		var d map[string]any
		_ = json.Unmarshal([]byte(data), &d)
		if d["to_team"] != f.teamB || d["from_tier"] != float64(1) || d["to_tier"] != float64(2) || d["reason"] != "network team" {
			t.Fatalf("event data %s", data)
		}
	})
}

// ---- TKT-007 assignment ----

func TestAssign_RejectsLowerTierAssignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l2, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l1.UserID}).expect(t, 422)
	})
}

func TestAssign_RejectsNonTeamMember(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2B.UserID}).expect(t, 422)
	})
}

func TestUnassign_OnlyByAssigneeOrSupervisor(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": nil}).expect(t, 403)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": nil}).expect(t, 200)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		f.e.do(f.e.staff(domain.RoleSupervisor, 1), "PATCH", f.path(""), map[string]any{"assignee_id": nil}).expect(t, 200)
	})
}

// TestAssignable_EqualsM4Set: every listed user passes M-4 and every M-4 user is listed.
func TestAssignable_EqualsM4Set(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		sup := f.e.staff(domain.RoleSupervisor, 1, f.teamA)
		disabled := f.e.staff(domain.RoleAgent, 3, f.teamA)
		f.e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, disabled.UserID)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		var got []map[string]any
		f.e.do(f.l2, "GET", "/api/v1/agent/assignable?ticket_id="+f.id, nil).expect(t, 200).json(t, &got)
		ids := map[string]bool{}
		for _, g := range got {
			ids[g["id"].(string)] = true
		}
		ctx := context.Background()
		row, _ := lockFreeLoad(ctx, f.e.svc, f.id)
		rows, _ := db.Read().QueryContext(ctx, `SELECT id FROM users WHERE kind = 'staff'`)
		var all []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			all = append(all, id)
		}
		rows.Close()
		for _, id := range all {
			st, err := loadStaff(ctx, db.Read(), id)
			if err != nil {
				t.Fatal(err)
			}
			if satisfiesM4(st, row) != ids[id] {
				t.Errorf("user %s (tier %d role %s): M-4 %v listed %v", id, st.Tier, st.Role, satisfiesM4(st, row), ids[id])
			}
		}
		if !ids[sup.UserID] || ids[f.l1.UserID] || ids[disabled.UserID] || ids[f.l2B.UserID] {
			t.Fatalf("assignable %v", ids)
		}
		f.e.do(f.l1, "GET", "/api/v1/agent/assignable?ticket_id="+f.id, nil).expect(t, 404)
	})
}

// ---- TKT-008 field updates ----

func TestPatch_MixedAllowedForbiddenAllRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		before, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		evs := len(f.e.eventsOf(f.id))
		// priority is allowed for L1, unassigning someone else is not.
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"priority": "high", "subject": "new subject", "assignee_id": nil}).expect(t, 403)
		after, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if after.Priority != before.Priority || after.Subject != before.Subject || after.AssigneeID != before.AssigneeID {
			t.Fatal("partial write")
		}
		if len(f.e.eventsOf(f.id)) != evs {
			t.Fatal("events written for a rejected patch")
		}
	})
}

func TestTransfer_ClearsInvalidAssignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		both := f.e.staff(domain.RoleAgent, 1, f.teamA, f.teamB)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": both.UserID}).expect(t, 200)
		f.e.do(both, "PATCH", f.path(""), map[string]any{"team_id": f.teamB}).expect(t, 200)
		if r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id); r.AssigneeID != both.UserID {
			t.Fatal("valid assignee cleared on transfer")
		}
		f.e.do(both, "PATCH", f.path(""), map[string]any{"team_id": f.teamA}).expect(t, 200)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": f.l1.UserID}).expect(t, 200)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"team_id": f.teamB}).expect(t, 200)
		if r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id); r.AssigneeID != "" {
			t.Fatal("invalid assignee kept")
		}
	})
}

func TestTransfer_ResponseSignalsLostVisibility(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		res := f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"team_id": f.teamB}).expect(t, 200).m(t)
		if res["visible"] != false {
			t.Fatal("visible should be false after transferring away")
		}
		tk := res["ticket"].(map[string]any)
		if len(tk["messages"].([]any)) != 0 || len(tk["allowed_actions"].([]any)) != 0 {
			t.Fatal("lost-visibility result must not carry the thread")
		}
	})
}

func TestPatch_UrgentNeedsSupervisorWhenFlagOn(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"priority": "urgent"}).expect(t, 200)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"priority": "normal"}).expect(t, 200)
		setSupport(t, f.e, func(s *settings.Support) { s.UrgentRequiresSupervisor = true })
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"priority": "urgent"}).expect(t, 403)
		f.e.do(f.e.staff(domain.RoleSupervisor, 1), "PATCH", f.path(""), map[string]any{"priority": "urgent"}).expect(t, 200)
	})
}
