// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []map[string]any
	raw    []string // every frame except acks
	all    []string // every frame, acks included
	closed chan websocket.StatusCode
}

func (e *env) wsDial(p *domain.Principal) *wsClient {
	e.t.Helper()
	srv := httptest.NewServer(e.hub)
	e.t.Cleanup(srv.Close)
	tok := e.pat(p, domain.ScopeTicketsRead)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		Subprotocols: []string{realtime.Subprotocol},
		HTTPHeader:   http.Header{"Authorization": {"Bearer pat-" + tok.UserID}},
	})
	if err != nil {
		e.t.Fatalf("dial: %v", err)
	}
	w := &wsClient{t: e.t, c: c, closed: make(chan websocket.StatusCode, 1)}
	go func() {
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				w.closed <- websocket.CloseStatus(err)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			w.mu.Lock()
			w.frames = append(w.frames, m)
			w.all = append(w.all, string(b))
			if m["type"] != "ack" {
				w.raw = append(w.raw, string(b))
			}
			w.mu.Unlock()
		}
	}()
	e.t.Cleanup(func() { _ = c.CloseNow() })
	return w
}

func (w *wsClient) send(v any) {
	w.t.Helper()
	b, _ := json.Marshal(v)
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// waitFor polls until pred matches a frame (returns it) or the timeout ends.
func (w *wsClient) waitFor(d time.Duration, pred func(map[string]any) bool) map[string]any {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, f := range w.frames {
			if pred(f) {
				w.mu.Unlock()
				return f
			}
		}
		w.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// subscribe sends a subscribe op and returns the denied topics.
func (w *wsClient) subscribe(id string, topics ...string) []string {
	w.t.Helper()
	w.send(map[string]any{"op": "subscribe", "id": id, "topics": topics})
	ack := w.waitFor(3*time.Second, func(f map[string]any) bool { return f["type"] == "ack" && f["id"] == id })
	if ack == nil {
		w.t.Fatal("no ack")
	}
	var denied []string
	if d, ok := ack["denied"].([]any); ok {
		for _, x := range d {
			denied = append(denied, x.(string))
		}
	}
	return denied
}

func (w *wsClient) reset() {
	w.mu.Lock()
	w.frames, w.raw = nil, nil
	w.mu.Unlock()
}

func (w *wsClient) events() []map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []map[string]any
	for _, f := range w.frames {
		if f["type"] == "event" {
			out = append(out, f)
		}
	}
	return out
}

func (w *wsClient) rawJoined() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.raw, "\n")
}

// dispatch hands every event after *cursor to the hub, like the platform's
// realtime consumer does after commit.
func (e *env) dispatch(cursor *string) {
	e.t.Helper()
	evs, err := events.ReadAfter(context.Background(), e.db.Read(), *cursor, 10000)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, ev := range evs {
		_ = e.hub.HandleEvent(context.Background(), ev)
		*cursor = ev.ID
	}
}

// TestWS_CustomerNeverReceivesInternal fuzzes every event type × customer
// subscriber kind × topic with payloads stuffed with staff-only data (I-2,
// I-7, I-9): customers never get an internal note, an internal event type, or
// a tier/team/assignee field.
func TestWS_CustomerNeverReceivesInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		org := storetest.CreateOrg(t, db, "")
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 2, team)
		req := e.customer(org, domain.RoleRequester)
		mgr := e.customer(org, domain.RoleOrgManager)
		adm := e.staff(domain.RoleAdmin, 1)
		cfCreate(t, e, adm, map[string]any{"key": "staff_secret_field", "label": map[string]string{"en": "S"}, "type": "text"})
		id := e.createTicket(req, CreateInput{Subject: "Public subject", BodyMD: "public body"})
		if _, err := e.svc.Patch(ctx, adm, id, PatchInput{TeamID: optStr{true, sp(team)}, AssigneeID: optStr{true, sp(agent.UserID)},
			CustomFields: map[string]any{"staff_secret_field": "STAFF-ONLY-VALUE"}}, ""); err != nil {
			t.Fatal(err)
		}
		note, err := e.svc.AddStaffMessage(ctx, agent, id, MessageInput{Kind: "internal", BodyMD: "INTERNAL-NOTE-BODY"})
		if err != nil {
			t.Fatal(err)
		}
		pub, err := e.svc.AddStaffMessage(ctx, agent, id, MessageInput{Kind: "public", BodyMD: "public reply"})
		if err != nil {
			t.Fatal(err)
		}
		row, _ := lockFreeLoad(ctx, e.svc, id)
		snap := row.snapshot()

		// Customers may not even ask for counts or queue:* (4403, see
		// TestWS_CustomerPATCannotSubscribeCounts); they hold every topic open to them.
		clients := []*wsClient{e.wsDial(req), e.wsDial(mgr)}
		for i, c := range clients {
			if denied := c.subscribe("s", "tickets", "ticket:"+id, "me"); len(denied) != 0 {
				t.Fatalf("client %d denied %v", i, denied)
			}
		}
		staffWatch := e.wsDial(agent)
		staffWatch.subscribe("s", "ticket:"+id)

		rnd := rand.New(rand.NewSource(7))
		sent := 0
		for round := 0; round < 3; round++ {
			for _, typ := range events.All {
				for _, msg := range []string{note.MessageID, pub.MessageID} {
					data := map[string]any{
						"reason": "SECRET-REASON", "from_tier": 1, "to_tier": 1 + rnd.Intn(3), "from_team": team, "to_team": team,
						"message_id": msg, "kind": "internal", "mentioned": []string{agent.UserID}, "keys": []string{"staff_secret_field"},
						"from": "pending", "to": "on_hold", "to_user": agent.UserID, "new_ticket_id": id,
					}
					b, _ := json.Marshal(data)
					ev := events.Event{ID: store.NewID(), TicketID: id, ActorID: agent.UserID, Type: typ, Data: b, Snapshot: snap, CreatedAt: store.Now()}
					_ = e.hub.HandleEvent(ctx, ev)
					sent++
				}
			}
		}
		if staffWatch.waitFor(3*time.Second, func(f map[string]any) bool { return f["event"] == events.NoteCreated }) == nil {
			t.Fatal("staff never received note.created: the fuzz did not reach the hub")
		}
		time.Sleep(200 * time.Millisecond)
		for i, c := range clients {
			evs := c.events()
			if len(evs) == 0 {
				t.Fatalf("client %d received nothing (expected customer-visible events)", i)
			}
			for _, f := range evs {
				if !events.CustomerVisible(f["event"].(string)) {
					t.Errorf("client %d received %s", i, f["event"])
				}
				if f["event"] == events.TicketStatusChanged {
					ch := f["data"].(map[string]any)["changes"].(map[string]any)
					if ch["from"] != "awaiting_reply" || ch["to"] != "open" {
						t.Errorf("status labels not mapped: %v", ch)
					}
				}
			}
			raw := c.rawJoined()
			for _, leak := range []string{"INTERNAL-NOTE-BODY", "SECRET-REASON", "STAFF-ONLY-VALUE", "staff_secret_field", `"tier"`, `"team_id"`, `"assignee"`, team, `"internal"`} {
				_ = agent
				if idx := strings.Index(raw, leak); idx >= 0 {
					t.Errorf("client %d received %q: …%s…", i, leak, raw[max(0, idx-600):min(len(raw), idx+80)])
				}
			}
		}
		t.Logf("%d synthetic events fanned out", sent)
	})
}

// TestWS_L1NeverReceivesL2 (I-4): an L1 subscriber of tickets and
// queue:<team> receives nothing about a tier-2 ticket, and gets
// ticket.access_lost when its ticket is escalated.
func TestWS_L1NeverReceivesL2(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		cursor := ""
		f.e.dispatch(&cursor)
		l1 := f.e.wsDial(f.l1)
		if d := l1.subscribe("s", "tickets", "queue:"+f.teamA, "ticket:"+f.id); len(d) != 0 {
			t.Fatalf("denied %v", d)
		}
		l2 := f.e.wsDial(f.l2)
		l2.subscribe("s", "tickets", "queue:"+f.teamA)
		ctx := context.Background()
		if _, err := f.e.svc.Escalate(ctx, f.l1, f.id, +1, EscalateInput{Reason: "x"}); err != nil {
			t.Fatal(err)
		}
		f.e.dispatch(&cursor)
		if l1.waitFor(3*time.Second, func(fr map[string]any) bool { return fr["event"] == events.TicketAccessLost }) == nil {
			t.Fatal("no ticket.access_lost")
		}
		if l2.waitFor(3*time.Second, func(fr map[string]any) bool {
			return fr["event"] == events.TicketEscalated && fr["topic"] == "queue:"+f.teamA
		}) == nil {
			t.Fatal("L2 did not see the escalation in its queue")
		}
		l1.reset()
		other := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.cust.UserID, TeamID: f.teamA, Tier: 2})
		if _, err := f.e.svc.AddStaffMessage(ctx, f.l2, f.id, MessageInput{Kind: "internal", BodyMD: "deep dive"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.svc.AddStaffMessage(ctx, f.l2, other, MessageInput{Kind: "public", BodyMD: "hi"}); err != nil {
			t.Fatal(err)
		}
		f.e.dispatch(&cursor)
		time.Sleep(300 * time.Millisecond)
		if evs := l1.events(); len(evs) != 0 {
			t.Fatalf("L1 received %v", evs)
		}
	})
}

// TestWS_CanSeeEventAgreesWithPredicate: CanSeeEvent on the stored snapshot
// agrees with the SQL predicate for 1 000 random (principal, ticket) pairs.
func TestWS_CanSeeEventAgreesWithPredicate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		ctx := context.Background()
		rnd := rand.New(rand.NewSource(99))
		staffs := []*domain.Principal{f.l1, f.l2, f.l1B, f.sup}
		custs := []*domain.Principal{f.reqA, f.reqA2, f.mgr, f.reqB}
		var ids []string
		for i := 0; i < 30; i++ {
			c := custs[rnd.Intn(len(custs))]
			id := f.e.createTicket(c, CreateInput{})
			team := []string{"", f.teamA, f.teamB}[rnd.Intn(3)]
			f.e.exec(`UPDATE tickets SET team_id = ?, tier = ? WHERE id = ?`, store.Nullable(team), 1+rnd.Intn(3), id)
			if rnd.Intn(4) == 0 {
				f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, id, staffs[rnd.Intn(len(staffs))].UserID, store.Now())
			}
			// Write a fresh event so its stored snapshot reflects the row.
			if _, err := f.e.svc.AddStaffMessage(ctx, f.sup, id, MessageInput{Kind: "internal", BodyMD: "snap"}); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		all := append(append([]*domain.Principal{}, staffs...), custs...)
		pol := f.e.svc.Policy(ctx)
		for i := 0; i < 1000; i++ {
			p := all[rnd.Intn(len(all))]
			id := ids[rnd.Intn(len(ids))]
			evs, _ := events.ForTicket(ctx, db.Read(), id)
			snap := evs[len(evs)-1].Snapshot
			mem := authz.CanSeeEvent(p, snap, events.TicketStatusChanged, pol)
			pred, args := authz.TicketPredicate(p, pol, "t")
			sql := f.e.count(`SELECT COUNT(*) FROM tickets t WHERE t.id = ? AND `+pred, append([]any{id}, args...)...) == 1
			if mem != sql {
				t.Fatalf("%s on %s: CanSeeEvent=%v SQL=%v", p.UserID, id, mem, sql)
			}
		}
	})
}

func TestWS_ResumeReplaysOnlyVisible(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		ctx := context.Background()
		hidden := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.cust.UserID, TeamID: f.teamA, Tier: 2})
		var start string
		_ = db.Read().QueryRowContext(ctx, `SELECT MAX(id) FROM ticket_events`).Scan(&start)
		if _, err := f.e.svc.AddStaffMessage(ctx, f.l2, hidden, MessageInput{Kind: "public", BodyMD: "L2 only"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.e.svc.AddStaffMessage(ctx, f.l1, f.id, MessageInput{Kind: "public", BodyMD: "visible"}); err != nil {
			t.Fatal(err)
		}
		l1 := f.e.wsDial(f.l1)
		l1.subscribe("s", "tickets")
		l1.send(map[string]any{"op": "resume", "id": "r", "last_event_id": start})
		if l1.waitFor(3*time.Second, func(fr map[string]any) bool { return fr["type"] == "ack" && fr["id"] == "r" }) == nil {
			t.Fatal("no resume ack")
		}
		evs := l1.events()
		if len(evs) == 0 {
			t.Fatal("nothing replayed")
		}
		for _, ev := range evs {
			tk := ev["data"].(map[string]any)["ticket"].(map[string]any)
			if tk["id"] != f.id {
				t.Fatalf("replayed an invisible ticket: %v", tk["id"])
			}
		}
	})
}

func TestWS_ResumeGapTooLargeResync(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{})
		old := store.NewID()
		e.exec(`INSERT INTO ticket_events (id, ticket_id, type, data, snapshot, created_at) VALUES (?, ?, 'ticket.created', '{}', '{}', ?)`,
			old, id, store.Now().Add(-25*time.Hour))
		c := e.wsDial(cust)
		c.subscribe("s", "tickets")
		for _, last := range []string{old, "not-an-id"} {
			c.reset()
			c.send(map[string]any{"op": "resume", "id": "r", "last_event_id": last})
			if c.waitFor(3*time.Second, func(f map[string]any) bool { return f["type"] == "resync" && f["reason"] == "gap_too_large" }) == nil {
				t.Fatalf("no resync for %s", last)
			}
		}
	})
}

func TestWS_CountsDebounced(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		cursor := ""
		c := f.e.wsDial(f.l1)
		c.subscribe("s", "counts")
		ctx := context.Background()
		for i := 0; i < 15; i++ {
			if _, err := f.e.svc.AddStaffMessage(ctx, f.l1, f.id, MessageInput{Kind: "public", BodyMD: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		f.e.dispatch(&cursor)
		time.Sleep(1600 * time.Millisecond)
		n := 0
		c.mu.Lock()
		var last map[string]any
		for _, fr := range c.frames {
			if fr["type"] == "counts" {
				n++
				last = fr
			}
		}
		c.mu.Unlock()
		if n != 1 {
			t.Fatalf("%d counts frames for a burst of events, want 1", n)
		}
		if last["data"].(map[string]any)["tier"] == nil {
			t.Fatalf("counts frame %v", last)
		}
	})
}

// BenchmarkFanout_1000Subscribers measures the in-memory part of fan-out: one
// event decided and serialised for 1 000 subscribers, with the payload built
// once beforehand (no DB access inside the loop).
func BenchmarkFanout_1000Subscribers(b *testing.B) {
	snap := events.Snapshot{Number: 1, Subject: "s", Status: "open", Priority: "normal", Tier: 1, TeamID: "team-a", RequesterID: "c1", OrganizationID: "o1"}
	ev := events.Event{ID: store.NewID(), TicketID: "t1", Type: events.MessageCreated, Snapshot: snap, CreatedAt: time.Now()}
	pl := &payload{staffTicket: &ticketSummaryDTO{ID: "t1"}, customerTicket: &customerTicketSummaryDTO{ID: "t1"},
		customerMsg: &customerMessageDTO{ID: "m1"}, staffMsg: &messageDTO{ID: "m1"}, staffChanges: map[string]any{}, custChanges: map[string]any{}}
	subs := make([]*domain.Principal, 1000)
	for i := range subs {
		switch i % 3 {
		case 0:
			subs[i] = &domain.Principal{UserID: "s", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1 + i%3, Teams: []string{"team-a"}}
		case 1:
			subs[i] = &domain.Principal{UserID: "c1", Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: "o1"}
		default:
			subs[i] = &domain.Principal{UserID: "x", Kind: domain.KindCustomer, Role: domain.RoleOrgManager, OrganizationID: "o2"}
		}
	}
	pol := authz.DefaultTicketPolicy()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for _, p := range subs {
			if f, ok := frameFor("tickets", ev, pl, p, pol); ok {
				_, _ = json.Marshal(f)
			}
		}
	}
}
