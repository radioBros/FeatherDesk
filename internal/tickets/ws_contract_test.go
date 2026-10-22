// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// harnessRecheck re-reads the registered principal like authn's Rechecker
// re-reads the user: a changed role, tier or team set comes back on the next
// recheck tick; an unregistered user fails it (4401).
type harnessRecheck struct{ e *env }

func (r harnessRecheck) Recheck(_ context.Context, p *domain.Principal) (*domain.Principal, error) {
	key := p.UserID
	if p.TokenID != "" {
		key = "pat-" + p.UserID
	}
	r.e.mu.Lock()
	defer r.e.mu.Unlock()
	np, ok := r.e.users[key]
	if !ok {
		return nil, problems.Unauthenticated()
	}
	cp := *np
	return &cp, nil
}

// waitClose returns the close code of the client's socket.
func (w *wsClient) waitClose(d time.Duration) websocket.StatusCode {
	select {
	case code := <-w.closed:
		return code
	case <-time.After(d):
		return -1
	}
}

// validateFrames checks every frame the client received against the
// WsServerFrame schema of api/openapi.yaml and returns the frame kinds seen
// ("ack", "event:<name>", "resync:<reason>", …).
func (w *wsClient) validateFrames(t *testing.T) map[string]bool {
	t.Helper()
	w.mu.Lock()
	all := append([]string(nil), w.all...)
	w.mu.Unlock()
	seen := map[string]bool{}
	for _, raw := range all {
		apispec.ValidateSchema(t, "WsServerFrame", []byte(raw))
		var f map[string]any
		_ = json.Unmarshal([]byte(raw), &f)
		kind := fmt.Sprint(f["type"])
		switch kind {
		case "event":
			kind += ":" + fmt.Sprint(f["event"])
		case "resync":
			kind += ":" + fmt.Sprint(f["reason"])
		case "ack":
			if f["ok"] == false {
				kind += ":nack"
			} else if f["denied"] != nil {
				kind += ":denied"
			}
		}
		seen[kind] = true
	}
	return seen
}

// TestWS_CustomerPATCannotSubscribeCounts (AUTH-032, realtime_spec §7): a
// customer token asking for counts (or any staff-only topic class) is closed
// with 4403; a staff token gets counts.
func TestWS_CustomerPATCannotSubscribeCounts(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		for _, topic := range []string{"counts", "queue:" + team} {
			c := e.wsDial(cust) // PAT with tickets:read
			c.send(map[string]any{"op": "subscribe", "id": "s", "topics": []string{"tickets", topic}})
			if code := c.waitClose(3 * time.Second); code != realtime.CloseForbidden {
				t.Fatalf("customer PAT subscribing %s: close %d, want 4403", topic, code)
			}
		}
		staff := e.wsDial(e.staff(domain.RoleAgent, 1, team))
		if d := staff.subscribe("s", "counts", "queue:"+team); len(d) != 0 {
			t.Fatalf("staff PAT denied %v", d)
		}
	})
}

// TestWS_DemotionDropsTopics (AUTH-032): when an L2 team member is demoted to
// L1 and leaves the team, the next re-check re-authorises every topic: the
// ticket and queue topics are dropped with resync{permissions_changed}, and
// no further frame about the tier-2 ticket reaches the socket.
func TestWS_DemotionDropsTopics(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnvHub(t, db, 1, realtime.Options{RecheckInterval: 100 * time.Millisecond})
		e.hub.SetRechecker(harnessRecheck{e})
		ctx := context.Background()
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 1)
		l2 := e.staff(domain.RoleAgent, 2, team)
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{Subject: "Deep issue"})
		e.exec(`UPDATE tickets SET team_id = ?, tier = 2 WHERE id = ?`, team, id)
		cursor := ""
		e.dispatch(&cursor)

		c := e.wsDial(l2)
		if d := c.subscribe("s", "tickets", "ticket:"+id, "queue:"+team); len(d) != 0 {
			t.Fatalf("denied before demotion: %v", d)
		}
		// Demotion: tier 2 → 1 and out of the team (AUTH-006 / TKT-017).
		e.mu.Lock()
		np := *e.users["pat-"+l2.UserID]
		np.Tier, np.Teams = 1, nil
		e.users["pat-"+l2.UserID] = &np
		e.mu.Unlock()
		if c.waitFor(3*time.Second, func(f map[string]any) bool {
			return f["type"] == "resync" && f["reason"] == "permissions_changed"
		}) == nil {
			t.Fatal("no resync{permissions_changed} after the demotion")
		}
		c.validateFrames(t)
		c.reset()
		if _, err := e.svc.AddStaffMessage(ctx, sup, id, MessageInput{Kind: "public", BodyMD: "after demotion"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AddStaffMessage(ctx, sup, id, MessageInput{Kind: "internal", BodyMD: "internal after demotion"}); err != nil {
			t.Fatal(err)
		}
		e.dispatch(&cursor)
		time.Sleep(300 * time.Millisecond)
		if evs := c.events(); len(evs) != 0 {
			t.Fatalf("demoted agent still receives %v", evs)
		}
		// The socket stays open: re-subscribing the dropped topics is denied.
		if d := c.subscribe("s2", "ticket:"+id, "queue:"+team); len(d) != 2 {
			t.Fatalf("re-subscribe after demotion denied %v", d)
		}
	})
}

// TestWSEnvelopeMatchesSchema (CON-013): every server frame the real hub and
// the tickets publisher send — ack (ok, denied, nack), event (staff and
// customer payloads, ticket.access_lost, assigned_to_you), counts, pong,
// presence and resync with each reason — validates against the
// WsServerFrame schema of api/openapi.yaml.
func TestWSEnvelopeMatchesSchema(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnvHub(t, db, 1, realtime.Options{RecheckInterval: 100 * time.Millisecond})
		e.hub.SetRechecker(harnessRecheck{e})
		ctx := context.Background()
		team := storetest.CreateTeam(t, db, "")
		org := storetest.CreateOrg(t, db, "")
		l1 := e.staff(domain.RoleAgent, 1, team)
		l2 := e.staff(domain.RoleAgent, 2, team)
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer(org, domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{Subject: "Schema", BodyMD: "hello"})
		cursor := ""
		e.dispatch(&cursor)

		staff := e.wsDial(l1)
		if d := staff.subscribe("s1", "tickets", "ticket:"+id, "counts", "me", "queue:"+team, "ticket:"+store.NewID()); len(d) != 1 {
			t.Fatalf("staff denied %v", d)
		}
		watcher := e.wsDial(l2) // demoted below: permissions_changed
		watcher.subscribe("s1", "ticket:"+id)
		customer := e.wsDial(cust)
		if d := customer.subscribe("s1", "tickets", "ticket:"+id, "me"); len(d) != 0 {
			t.Fatalf("customer denied %v", d)
		}
		staff.send(map[string]any{"op": "ping", "id": "p1"})
		staff.send(map[string]any{"op": "bogus", "id": "x1"})
		staff.send(map[string]any{"op": "presence", "id": "pr", "topic": "ticket:" + id, "state": "typing"})
		staff.send(map[string]any{"op": "resume", "id": "r1", "last_event_id": "not-an-id"})

		if _, err := e.svc.Patch(ctx, sup, id, PatchInput{TeamID: optStr{true, sp(team)}, AssigneeID: optStr{true, sp(l1.UserID)}}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AddStaffMessage(ctx, l1, id, MessageInput{Kind: "public", BodyMD: "public reply"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.AddStaffMessage(ctx, l1, id, MessageInput{Kind: "internal", BodyMD: "a note"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Patch(ctx, l1, id, PatchInput{Status: optStr{true, sp("pending")}}, ""); err != nil {
			t.Fatal(err)
		}
		e.dispatch(&cursor)
		// Resume from the ticket's first event: a real replay with frames.
		var first string
		_ = db.Read().QueryRowContext(ctx, `SELECT MIN(id) FROM ticket_events WHERE ticket_id = ?`, id).Scan(&first)
		customer.send(map[string]any{"op": "resume", "id": "r2", "last_event_id": first})
		if customer.waitFor(3*time.Second, func(f map[string]any) bool { return f["type"] == "ack" && f["id"] == "r2" }) == nil {
			t.Fatal("no resume ack")
		}
		// Escalation past L1: ticket.access_lost for the L1 subscriber.
		if _, err := e.svc.Escalate(ctx, l1, id, +1, EscalateInput{Reason: "needs L2"}); err != nil {
			t.Fatal(err)
		}
		e.dispatch(&cursor)
		// Demote the watcher: resync{permissions_changed}.
		e.mu.Lock()
		np := *e.users["pat-"+l2.UserID]
		np.Tier = 1
		e.users["pat-"+l2.UserID] = &np
		e.mu.Unlock()
		// Queue overflow: resync{slow_consumer} to everyone.
		e.hub.Overflow(events.Event{})

		waits := []struct {
			c    *wsClient
			kind func(map[string]any) bool
		}{
			{staff, func(f map[string]any) bool { return f["type"] == "counts" }},
			{staff, func(f map[string]any) bool { return f["event"] == events.TicketAccessLost }},
			{staff, func(f map[string]any) bool { return f["event"] == "assigned_to_you" }},
			{watcher, func(f map[string]any) bool { return f["reason"] == "permissions_changed" }},
			{customer, func(f map[string]any) bool { return f["reason"] == "slow_consumer" }},
		}
		for i, w := range waits {
			if w.c.waitFor(5*time.Second, w.kind) == nil {
				t.Fatalf("wait %d: frame never arrived", i)
			}
		}
		seen := map[string]bool{}
		for _, c := range []*wsClient{staff, watcher, customer} {
			for k := range c.validateFrames(t) {
				seen[k] = true
			}
		}
		for _, want := range []string{"ack", "ack:denied", "ack:nack", "pong", "presence", "counts",
			"event:" + events.MessageCreated, "event:" + events.NoteCreated, "event:" + events.TicketStatusChanged,
			"event:" + events.TicketAssigned, "event:" + events.TicketAccessLost, "event:assigned_to_you",
			"resync:gap_too_large", "resync:permissions_changed", "resync:slow_consumer"} {
			if !seen[want] {
				t.Errorf("no %s frame was validated (seen %v)", want, seen)
			}
		}
	})
}

// TestWS_ResumeBeyondSendBuffer (TKT-032): a resume of more events than the
// 64-frame send buffer replays every visible frame, in order, then acks; the
// old 48-frame cap answered resync instead.
func TestWS_ResumeBeyondSendBuffer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{})
		e.exec(`UPDATE tickets SET team_id = ? WHERE id = ?`, team, id)
		var start string
		_ = db.Read().QueryRowContext(ctx, `SELECT MAX(id) FROM ticket_events`).Scan(&start)
		const n = 120
		for i := 0; i < n; i++ {
			if _, err := e.svc.AddStaffMessage(ctx, agent, id, MessageInput{Kind: "public", BodyMD: fmt.Sprint("reply ", i)}); err != nil {
				t.Fatal(err)
			}
		}
		c := e.wsDial(agent)
		c.subscribe("s", "tickets", "ticket:"+id) // two frames per event
		c.send(map[string]any{"op": "resume", "id": "r", "last_event_id": start})
		if c.waitFor(10*time.Second, func(f map[string]any) bool { return f["type"] == "ack" && f["id"] == "r" }) == nil {
			t.Fatalf("no resume ack: %d frames", len(c.events()))
		}
		evs := c.events()
		if len(evs) < 2*n {
			t.Fatalf("replayed %d frames, want ≥ %d", len(evs), 2*n)
		}
		last := ""
		for _, f := range evs {
			if f["type"] == "resync" {
				t.Fatal("resync instead of a replay")
			}
			if id := fmt.Sprint(f["event_id"]); f["topic"] == "tickets" {
				if id <= last {
					t.Fatalf("replay out of order: %s after %s", id, last)
				}
				last = id
			}
		}
	})
}
