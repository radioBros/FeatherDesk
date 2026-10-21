// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// fakeClock drives Service.now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func withClock(e *env) *fakeClock {
	c := &fakeClock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	e.svc.now = c.now
	return c
}

func TestSLA_DueDatesOnCreate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		clk := withClock(e)
		id := e.createTicket(e.customer("", domain.RoleRequester), CreateInput{})
		r, _ := lockFreeLoad(context.Background(), e.svc, id)
		if !r.FirstResponseDueAt.Time.Equal(clk.now().Add(480*time.Minute)) || !r.ResolutionDueAt.Time.Equal(clk.now().Add(2880*time.Minute)) {
			t.Fatalf("due %s / %s", r.FirstResponseDueAt.Time, r.ResolutionDueAt.Time)
		}
	})
}

func TestSLA_PauseResumeExtendsResolution(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		clk := withClock(l.e)
		ctx := context.Background()
		id, err := l.e.svc.Create(ctx, l.cust, CreateInput{Subject: "s", BodyMD: "b"})
		if err != nil {
			t.Fatal(err)
		}
		created := clk.now()
		l.e.do(l.sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"team_id": l.team}).expect(t, 200)
		clk.add(time.Hour)
		l.e.do(l.agent, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "public", "body_md": "need logs", "set_status": "pending"}).expect(t, 201)
		clk.add(2 * time.Hour)
		l.e.do(l.cust, "POST", "/api/v1/portal/tickets/"+id+"/messages", map[string]any{"body_md": "logs attached"}).expect(t, 201)
		r, _ := lockFreeLoad(ctx, l.e.svc, id)
		if r.SLAPausedMinutes != 120 || !r.ResolutionDueAt.Time.Equal(created.Add((2880+120)*time.Minute)) {
			t.Fatalf("paused %d, resolution due %s", r.SLAPausedMinutes, r.ResolutionDueAt.Time)
		}
		if !r.FirstResponseDueAt.Time.Equal(created.Add(480 * time.Minute)) {
			t.Fatal("first-response due moved")
		}
	})
}

func TestSLA_PriorityChangeRecomputesFromCreated(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		clk := withClock(l.e)
		id, _ := l.e.svc.Create(context.Background(), l.cust, CreateInput{Subject: "s", BodyMD: "b"})
		created := clk.now()
		clk.add(5 * time.Hour)
		l.e.do(l.sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"priority": "urgent"}).expect(t, 200)
		r, _ := lockFreeLoad(context.Background(), l.e.svc, id)
		if !r.FirstResponseDueAt.Time.Equal(created.Add(30*time.Minute)) || !r.ResolutionDueAt.Time.Equal(created.Add(240*time.Minute)) {
			t.Fatalf("due %s / %s", r.FirstResponseDueAt.Time, r.ResolutionDueAt.Time)
		}
	})
}

func TestSLA_EscalationDoesNotReset(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		before, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x", "target_team_id": f.teamB}).expect(t, 200)
		after, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if !after.ResolutionDueAt.Time.Equal(before.ResolutionDueAt.Time) || !after.FirstResponseDueAt.Time.Equal(before.FirstResponseDueAt.Time) {
			t.Fatal("escalation changed the SLA clock")
		}
	})
}

func TestSLA_TeamCalendarMode(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		c := e.do(sup, "POST", "/api/v1/admin/calendars", map[string]any{"name": "UTC office", "timezone": "UTC",
			"week": map[string]any{"wed": []map[string]string{{"start": "09:00", "end": "17:00"}}}}).expect(t, 201).m(t)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Office", "calendar_id": c["id"]}).expect(t, 201).m(t)["id"].(string)
		e.do(sup, "PUT", "/api/v1/admin/sla-policies", map[string]any{"policies": []map[string]any{
			{"priority": "normal", "first_response_minutes": 60, "resolution_minutes": 600, "calendar_mode": "team"}}}).expect(t, 200)
		clk := withClock(e) // Wed 2026-10-21 08:00 UTC
		id := e.createTicket(sup, CreateInput{TeamID: &team, RequesterEmail: "c@example.org"})
		r, _ := lockFreeLoad(context.Background(), e.svc, id)
		if want := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC); !r.FirstResponseDueAt.Time.Equal(want) {
			t.Fatalf("first response due %s, want %s", r.FirstResponseDueAt.Time, want)
		}
		// 600 business minutes = Wed 09–17 (480) + next Wed 09:00–11:00.
		if want := time.Date(2026, 10, 28, 11, 0, 0, 0, time.UTC); !r.ResolutionDueAt.Time.Equal(want) {
			t.Fatalf("resolution due %s, want %s", r.ResolutionDueAt.Time, want)
		}
		// Editing the calendar recomputes open tickets (TKT-035).
		e.do(sup, "PATCH", "/api/v1/admin/calendars/"+c["id"].(string), map[string]any{"name": "UTC office", "timezone": "UTC",
			"week": map[string]any{"wed": []map[string]string{{"start": "09:00", "end": "17:00"}}, "thu": []map[string]string{{"start": "09:00", "end": "17:00"}}}}).expect(t, 200)
		n, err := e.svc.RecomputeDue(context.Background(), c["id"].(string))
		if err != nil || n != 1 {
			t.Fatalf("recompute %d %v", n, err)
		}
		r, _ = lockFreeLoad(context.Background(), e.svc, id)
		if want := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC); !r.ResolutionDueAt.Time.Equal(want) {
			t.Fatalf("after calendar edit due %s, want %s", r.ResolutionDueAt.Time, want)
		}
		_ = clk
	})
}

func TestSLA_PoliciesAPI(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		var pols []map[string]any
		e.do(sup, "GET", "/api/v1/admin/sla-policies", nil).expect(t, 200).json(t, &pols)
		if len(pols) != 4 {
			t.Fatalf("policies %v", pols)
		}
		e.do(sup, "PUT", "/api/v1/admin/sla-policies", map[string]any{"policies": []map[string]any{
			{"priority": "high", "first_response_minutes": 500, "resolution_minutes": 100, "calendar_mode": "24x7"}}}).expect(t, 422)
		e.do(e.staff(domain.RoleAgent, 3), "GET", "/api/v1/admin/sla-policies", nil).expect(t, 403)
	})
}

// ---- TKT-022 breach job ----

func TestBreach_WarnThenBreachOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		clk := withClock(e)
		id := e.createTicket(e.customer("", domain.RoleRequester), CreateInput{Priority: "high"}) // 120 / 1440
		ctx := context.Background()
		count := func(typ string) int {
			return e.count(`SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ? AND type = ?`, id, typ)
		}
		for i := 0; i < 5; i++ {
			if _, err := e.svc.CheckSLA(ctx, clk.now().Add(100*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		if count(events.SLAWarning) != 1 || count(events.SLABreached) != 0 {
			t.Fatalf("at 83%%: warnings %d breaches %d", count(events.SLAWarning), count(events.SLABreached))
		}
		for i := 0; i < 5; i++ {
			if _, err := e.svc.CheckSLA(ctx, clk.now().Add(121*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		if count(events.SLAWarning) != 1 || count(events.SLABreached) != 1 {
			t.Fatalf("after due: warnings %d breaches %d", count(events.SLAWarning), count(events.SLABreached))
		}
		// Nothing more to write: the job is quiet.
		if n, _ := e.svc.CheckSLA(ctx, clk.now().Add(122*time.Minute)); n != 0 {
			t.Fatalf("rerun wrote %d events", n)
		}
	})
}

func TestBreach_PausedSkipped(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		clk := withClock(e)
		id := e.createTicket(e.customer("", domain.RoleRequester), CreateInput{Priority: "high"})
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"status": "on_hold"}).expect(t, 200)
		if n, _ := e.svc.CheckSLA(context.Background(), clk.now().Add(48*time.Hour)); n != 0 {
			t.Fatalf("paused ticket produced %d SLA events", n)
		}
	})
}

// ---- TKT-020 auto-close ----

func TestAutoClose_OnlyPastThreshold(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		old := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, Status: domain.StatusResolved})
		recent := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, Status: domain.StatusResolved})
		open := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID})
		e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, store.Now().Add(-8*24*time.Hour), old)
		e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, store.Now().Add(-6*24*time.Hour), recent)
		n, err := e.svc.AutoClose(context.Background(), time.Now())
		if err != nil || n != 1 {
			t.Fatalf("closed %d %v", n, err)
		}
		for id, want := range map[string]domain.TicketStatus{old: "closed", recent: "resolved", open: "new"} {
			if r, _ := lockFreeLoad(context.Background(), e.svc, id); r.Status != want {
				t.Errorf("%s: %s, want %s", id, r.Status, want)
			}
		}
		var actor *string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT actor_id FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.status_changed'`, old).Scan(&actor)
		if actor != nil {
			t.Fatalf("actor %v, want system (NULL)", *actor)
		}
	})
}

func TestAutoClose_IdempotentRerun(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		for i := 0; i < 3; i++ {
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, Status: domain.StatusResolved})
			e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, store.Now().Add(-30*24*time.Hour), id)
		}
		if n, _ := e.svc.AutoClose(context.Background(), time.Now()); n != 3 {
			t.Fatalf("first run %d", n)
		}
		if n, _ := e.svc.AutoClose(context.Background(), time.Now()); n != 0 {
			t.Fatalf("rerun %d", n)
		}
		if e.count(`SELECT COUNT(*) FROM ticket_events WHERE type = 'ticket.status_changed'`) != 3 {
			t.Fatal("rerun wrote events")
		}
	})
}
