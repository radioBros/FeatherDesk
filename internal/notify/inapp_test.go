// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// ---- harness: a real hub with `me` sockets ----

// wsAuth maps "Bearer <user id>" to a token principal with tickets:read.
type wsAuth struct{ db *store.DB }

func (a wsAuth) Authenticate(r *http.Request) (*domain.Principal, error) {
	p, err := userAuth(a).Authenticate(r)
	if p != nil {
		p.Scopes = []string{string(domain.ScopeTicketsRead)}
	}
	return p, err
}

type meSocket struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []json.RawMessage
}

// withHub gives the module a real hub (the `me` topic open to everyone, as
// tickets registers it) and returns a dialer for a user's sockets.
func (e *env) withHub() func(userID string) *meSocket {
	h := realtime.NewHub(realtime.Options{})
	h.SetAuthenticator(wsAuth{db: e.db})
	h.Authorize("me", realtime.TopicAuthorizerFunc(func(_ context.Context, p *domain.Principal, _ string) bool { return p != nil }))
	e.m.RegisterRealtime(h)
	srv := httptest.NewServer(h)
	e.t.Cleanup(func() { h.Shutdown(); srv.Close() })
	return func(userID string) *meSocket {
		t := e.t
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hdr := http.Header{"Authorization": {"Bearer " + userID}}
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{Subprotocols: []string{realtime.Subprotocol}, HTTPHeader: hdr})
		if err != nil {
			t.Fatalf("ws dial: %v", err)
		}
		s := &meSocket{t: t, c: conn}
		go func() {
			for {
				_, b, err := conn.Read(context.Background())
				if err != nil {
					return
				}
				s.mu.Lock()
				s.frames = append(s.frames, b)
				s.mu.Unlock()
			}
		}()
		t.Cleanup(func() { _ = conn.CloseNow() })
		b, _ := json.Marshal(map[string]any{"op": "subscribe", "id": "s", "topics": []string{"me"}})
		if err := conn.Write(context.Background(), websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
		if s.wait(func(f map[string]any) bool { return f["type"] == "ack" }) == nil {
			t.Fatal("no subscribe ack")
		}
		return s
	}
}

func (s *meSocket) all() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.frames))
	for _, b := range s.frames {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
	return out
}

func (s *meSocket) wait(pred func(map[string]any) bool) map[string]any {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range s.all() {
			if pred(f) {
				return f
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// events returns the frames of one event name, in order.
func (s *meSocket) named(name string) []map[string]any {
	var out []map[string]any
	for _, f := range s.all() {
		if f["event"] == name {
			out = append(out, f)
		}
	}
	return out
}

// validate checks every frame received against WsServerFrame (CON-013).
func (s *meSocket) validate() {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.frames {
		apispec.ValidateSchema(s.t, "WsServerFrame", b)
	}
}

func isCreated(f map[string]any) bool { return f["event"] == FrameNotificationCreated }

// rowsOf lists "type→email" of the notifications written for an event.
func (e *env) rowsOf(eventID string) []string {
	e.t.Helper()
	rows, err := e.db.Read().QueryContext(context.Background(), `SELECT n.type, u.email FROM notifications n JOIN users u ON u.id = n.user_id WHERE n.event_id = ?`, eventID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, em string
		if err := rows.Scan(&typ, &em); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, typ+"→"+em)
	}
	sort.Strings(out)
	return out
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// userTargets keeps the user targets of a recipient list ("type→email").
func userTargets(ts []Target) []string {
	var out []string
	for _, t := range ts {
		if !t.R.Mailbox {
			out = append(out, t.Key+"→"+t.R.Addr)
		}
	}
	sort.Strings(out)
	return out
}

// TestInApp_RecipientsMatchEmailMatrix (NOT-036): for every row of the
// events_spec §2 matrix, the users who get an in-app notification are exactly
// the users who get the email (team and triage mailboxes are email-only), the
// actor is never notified, and the rows of one event carry its event_id.
func TestInApp_RecipientsMatchEmailMatrix(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		ctx := context.Background()
		e.exec(`UPDATE teams SET notification_email = 'billing@acme.test', notify_mode = 'both' WHERE id = ?`, e.team)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 2})
		triageID, triage := e.ticket(storetest.Ticket{})
		msgCust := e.message(tid, e.customer, "public", "help")
		msgStaff := e.message(tid, e.agent2, "public", "answer")
		assigned := snap
		assigned.AssigneeID = e.agent2
		withWatcher := assigned
		withWatcher.Watchers = []string{e.customer2}
		cases := []struct {
			name               string
			ticket, actor, typ string
			snap               events.Snapshot
			data               any
			min                int
		}{
			{"created by customer", tid, e.customer, events.TicketCreated, snap, nil, 1},
			{"created by staff", tid, e.agent2, events.TicketCreated, snap, nil, 1},
			{"created in triage", triageID, e.customer, events.TicketCreated, triage, nil, 2},
			{"staff public reply (+ customer watcher)", tid, e.agent2, events.MessageCreated, withWatcher, events.MessageRef{MessageID: msgStaff, Kind: "public"}, 2},
			{"customer reply → assignee", tid, e.customer, events.MessageCreated, assigned, events.MessageRef{MessageID: msgCust, Kind: "public"}, 1},
			{"customer reply → team", tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: msgCust, Kind: "public"}, 1},
			{"assigned", tid, e.supervisor, events.TicketAssigned, assigned, events.Assignment{To: e.agent2}, 1},
			{"self-assign", tid, e.agent2, events.TicketAssigned, assigned, events.Assignment{To: e.agent2}, 0},
			{"escalated", tid, e.agent2, events.TicketEscalated, withTeam(snap, e.team2, 3), events.TierChange{FromTier: 2, ToTier: 3, ToTeam: e.team2}, 1},
			{"resolved", tid, e.agent2, events.TicketStatusChanged, snap, events.StatusChange{From: "open", To: "resolved"}, 1},
			{"mention", tid, e.agent2, events.NoteCreated, snap, map[string]any{"message_id": "x", "kind": "internal", "mentions": []string{e.agent1, e.customer}, "mentioned": []string{e.supervisor}}, 1},
			{"SLA breach", tid, "", events.SLABreached, assigned, events.SLA{Which: "resolution", DueAt: time.Now()}, 2},
			{"children resolved", tid, "", events.TicketChildrenResolved, assigned, map[string]string{"parent_id": tid}, 1},
			{"internal message.created", tid, e.agent2, events.MessageCreated, snap, events.MessageRef{MessageID: msgStaff, Kind: "internal"}, 0},
		}
		for _, c := range cases {
			ev := e.event(c.ticket, c.actor, c.typ, c.snap, c.data)
			emailTo, err := e.m.Recipients(ctx, ev)
			if err != nil {
				t.Fatal(err)
			}
			want := userTargets(emailTo)
			e.handle(ev)
			got := e.rowsOf(ev.ID)
			if !slices.Equal(got, want) {
				t.Errorf("%s:\n in-app %v\n email  %v", c.name, got, want)
			}
			if len(got) < c.min {
				t.Errorf("%s: %d in-app rows, want ≥ %d", c.name, len(got), c.min)
			}
			if c.actor != "" && e.count(`SELECT COUNT(*) FROM notifications WHERE event_id = ? AND user_id = ?`, ev.ID, c.actor) != 0 {
				t.Errorf("%s: the actor notified themselves", c.name)
			}
		}
		if n := e.count(`SELECT COUNT(*) FROM notifications n JOIN users u ON u.id = n.user_id WHERE u.email = 'billing@acme.test'`); n != 0 {
			t.Errorf("mailbox got %d in-app rows", n)
		}
	})
}

// TestInApp_PrefsRespectedPerChannel (NOT-037): in_app off → mail but no
// row; email off → row but no mail; browser on → the frame says so; defaults
// are in_app on, email on, browser off.
func TestInApp_PrefsRespectedPerChannel(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		dial := e.withHub()
		third := storetest.CreateStaff(t, db, domain.RoleAgent, 1, e.team)
		e.exec(`UPDATE users SET notify_prefs = ? WHERE id = ?`, `{"in_app_off":["staff.ticket_new"]}`, e.agent2)
		e.exec(`UPDATE users SET notify_prefs = ? WHERE id = ?`, `{"opt_out":["staff.ticket_new"],"browser_on":["staff.ticket_new"]}`, e.agent1)
		s1, s3 := dial(e.agent1), dial(third)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1})
		ev := e.event(tid, e.customer, events.TicketCreated, snap, nil)
		e.handle(ev)

		if got, want := e.rowsOf(ev.ID), []string{"staff.ticket_new→" + e.email(e.agent1), "staff.ticket_new→" + e.email(third)}; !slices.Equal(got, sortedCopy(want)) {
			t.Errorf("in-app rows %v, want %v", got, sortedCopy(want))
		}
		mails := e.fake.Mails()
		if len(sentTo(mails, e.email(e.agent1))) != 0 {
			t.Error("email opt-out ignored")
		}
		if len(sentTo(mails, e.email(e.agent2))) != 1 || len(sentTo(mails, e.email(third))) != 1 {
			t.Errorf("mail recipients wrong: %d to agent2, %d to third", len(sentTo(mails, e.email(e.agent2))), len(sentTo(mails, e.email(third))))
		}
		f1, f3 := s1.wait(isCreated), s3.wait(isCreated)
		if f1 == nil || f3 == nil {
			t.Fatalf("frames missing: %v / %v", s1.all(), s3.all())
		}
		if b := f1["data"].(map[string]any)["browser"]; b != true {
			t.Errorf("browser_on not in frame: %v", f1)
		}
		if b := f3["data"].(map[string]any)["browser"]; b != false {
			t.Errorf("browser is opt-in, frame says %v", b)
		}
		view := PrefsView("staff", []byte(`{"opt_out":["staff.ticket_new"],"in_app_off":["staff.sla"],"browser_on":["staff.assigned"]}`))
		if (view[TplStaffTicketNew] != Channels{InApp: true, Email: false}) || (view[TplStaffSLA] != Channels{Email: true}) ||
			(view[TplStaffAssigned] != Channels{InApp: true, Email: true, Browser: true}) || len(view) != len(StaffInAppTypes) {
			t.Errorf("PrefsView = %+v", view)
		}
		s1.validate()
		s3.validate()
	})
}

func sortedCopy(s []string) []string { c := slices.Clone(s); sort.Strings(c); return c }

// TestPrefsValidateAndApply (NOT-037): audience and not_optional rules, and
// the merge keeps foreign keys and the email ⇔ opt_out mirror.
func TestPrefsValidateAndApply(t *testing.T) {
	f, tr := false, true
	fe := ValidatePrefs("customer", map[string]ChannelsPatch{
		TplStaffAssigned: {InApp: &f}, "bogus": {}, TplCustomerReply: {Email: &f}, TplCustomerResolved: {Email: &f, Browser: &tr},
	})
	var got []string
	for _, x := range fe {
		got = append(got, x.Field+":"+x.Code)
	}
	want := []string{"notification_prefs.bogus:invalid", "notification_prefs.customer.reply.email:not_optional", "notification_prefs.staff.assigned:invalid"}
	if !slices.Equal(got, want) {
		t.Errorf("ValidatePrefs = %v, want %v", got, want)
	}
	if fe := ValidatePrefs("staff", map[string]ChannelsPatch{TplStaffSLA: {InApp: &f, Email: &f, Browser: &tr}}); len(fe) != 0 {
		t.Errorf("valid staff patch rejected: %v", fe)
	}
	prefs := map[string]any{"opt_out": []any{"staff.sla", "staff.assigned"}, "digest": "weekly"}
	out := ApplyPrefs(prefs, map[string]ChannelsPatch{TplStaffAssigned: {Email: &tr, InApp: &f}, TplStaffMentioned: {Browser: &tr, Email: &f}})
	b, _ := json.Marshal(out)
	if string(b) != `{"browser_on":["staff.mentioned"],"digest":"weekly","in_app_off":["staff.assigned"],"opt_out":["staff.mentioned","staff.sla"]}` {
		t.Errorf("ApplyPrefs = %s", b)
	}
	view := PrefsView("staff", b)
	if (view[TplStaffAssigned] != Channels{Email: true}) || (view[TplStaffMentioned] != Channels{InApp: true, Browser: true}) {
		t.Errorf("round trip = %+v", view)
	}
	// customer.reply email stays on in the view even if a stored list says otherwise.
	if v := PrefsView("customer", []byte(`{"opt_out":["customer.reply"]}`)); !v[TplCustomerReply].Email || len(v) != 3 {
		t.Errorf("customer view = %+v", v)
	}
}

// TestInApp_CustomerNeverReceivesInternal (NOT-036, I-7/I-9): over every
// event type, with an internal-note canary everywhere, customer rows are only
// customer.* types with empty params and no staff actor except on a public
// reply; neither the frame nor the API listing of the customer carries the
// canary, a tier, a team or an escalation reason.
func TestInApp_CustomerNeverReceivesInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		dial := e.withHub()
		const canary = "INTERNAL-CANARY-7f3a"
		cs := dial(e.customer)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1, Subject: "Printer jam"})
		snap.Watchers = []string{e.customer2}
		note := e.message(tid, e.agent1, "internal", "secret "+canary)
		pub := e.message(tid, e.agent1, "public", "public answer")
		var evs []events.Event
		for _, typ := range events.All {
			var data any = map[string]any{"message_id": note, "kind": "internal", "reason": "reason " + canary, "mentions": []string{e.customer, e.agent2}}
			if typ == events.MessageCreated {
				data = events.MessageRef{MessageID: note, Kind: "internal"}
			}
			evs = append(evs, e.event(tid, e.agent1, typ, withTeam(snap, e.team, 2), data))
		}
		evs = append(evs,
			e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: pub, Kind: "public"}),
			e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}),
			e.event(tid, e.agent1, events.TicketEscalated, withTeam(snap, e.team2, 3), events.TierChange{FromTier: 1, ToTier: 3, ToTeam: e.team2, Reason: canary}),
			e.event(tid, e.agent1, events.NoteCreated, snap, map[string]any{"message_id": note, "kind": "internal", "mentions": []string{e.customer}}),
		)
		for _, ev := range evs {
			e.handle(ev)
		}
		rows, err := db.Read().QueryContext(context.Background(), `SELECT n.type, n.params, n.actor_id FROM notifications n JOIN users u ON u.id = n.user_id WHERE u.kind = 'customer'`)
		if err != nil {
			t.Fatal(err)
		}
		types := map[string]bool{}
		for rows.Next() {
			var typ string
			var params store.RawJSON
			var actor store.NullString
			if err := rows.Scan(&typ, &params, &actor); err != nil {
				t.Fatal(err)
			}
			types[typ] = true
			if !slices.Contains(CustomerInAppTypes, typ) {
				t.Errorf("customer got %s", typ)
			}
			if string(params) != "{}" {
				t.Errorf("customer %s params %s", typ, params)
			}
			if actor.Valid && typ != TplCustomerReply {
				t.Errorf("customer %s names the staff actor", typ)
			}
		}
		rows.Close()
		if !types[TplCustomerReply] || !types[TplCustomerResolved] {
			t.Fatalf("expected customer.reply and customer.resolved rows, got %v", types)
		}
		if cs.wait(isCreated) == nil {
			t.Fatal("customer got no frame")
		}
		time.Sleep(100 * time.Millisecond)
		for _, f := range cs.named(FrameNotificationCreated) {
			b, _ := json.Marshal(f)
			for _, bad := range []string{canary, "from_tier", "to_tier", "\"sla\"", "team"} {
				if strings.Contains(string(b), bad) {
					t.Errorf("customer frame carries %q: %s", bad, b)
				}
			}
		}
		_, body := e.do("GET", "/api/v1/me/notifications", e.customer, nil)
		for _, bad := range []string{canary, "from_tier", "to_tier", "\"sla\""} {
			if strings.Contains(string(body), bad) {
				t.Errorf("customer listing carries %q: %s", bad, body)
			}
		}
		cs.validate()
	})
}

// TestNotificationDTOAllowList (I-7): adding a field to the wire shape (which
// customers receive) is a reviewed change of this list.
func TestNotificationDTOAllowList(t *testing.T) {
	fields := func(v any) []string {
		var out []string
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			out = append(out, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
		}
		return out
	}
	if got, want := fields(NotificationDTO{}), []string{"id", "type", "ticket_id", "ticket_number", "ticket_subject", "ticket_ref", "project", "actor_name", "params", "read_at", "created_at"}; !slices.Equal(got, want) {
		t.Errorf("NotificationDTO fields %v, want %v", got, want)
	}
	if got, want := fields(ProjectRefDTO{}), []string{"id", "key", "name", "color"}; !slices.Equal(got, want) {
		t.Errorf("ProjectRefDTO fields %v, want %v (the ProjectRef chip customers already see on their tickets)", got, want)
	}
	if got, want := fields(NotificationParams{}), []string{"from_tier", "to_tier", "sla", "breached"}; !slices.Equal(got, want) {
		t.Errorf("NotificationParams fields %v, want %v (customer rows must keep them empty)", got, want)
	}
}

// TestInApp_OneTxPerEvent (NOT-036): an event with three recipients writes
// its rows in exactly one transaction (one multi-row INSERT and one grouped
// unread-count read); a replay inserts nothing and sends no frame.
func TestInApp_OneTxPerEvent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		dial := e.withHub()
		s := dial(e.agent1)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1})
		ev := e.event(tid, e.admin, events.TicketCreated, snap, nil) // customer + agent1 + agent2
		before := db.WriteStats()
		e.handle(ev)
		after := db.WriteStats()
		if d := after.Txs - before.Txs; d != 1 {
			t.Errorf("%d write transactions for one event, want 1", d)
		}
		if d := after.Statements - before.Statements; d != 2 {
			t.Errorf("%d statements in the event's transaction, want 2 (insert + counts)", d)
		}
		if n := len(e.rowsOf(ev.ID)); n != 3 {
			t.Fatalf("%d rows, want 3", n)
		}
		if s.wait(isCreated) == nil {
			t.Fatal("no frame")
		}
		e.handle(ev) // replay (at-least-once queue)
		time.Sleep(150 * time.Millisecond)
		if n := len(e.rowsOf(ev.ID)); n != 3 {
			t.Errorf("replay duplicated rows: %d", n)
		}
		if n := len(s.named(FrameNotificationCreated)); n != 1 {
			t.Errorf("replay sent %d frames, want 1", n)
		}
	})
}

// TestInApp_WSFrameAndReadSync (NOT-038): two tabs of one user get
// notification.created with the right unread_count; read, read-all and
// delete through the API push notification.read to every tab; all frames
// validate against WsServerFrame.
func TestInApp_WSFrameAndReadSync(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		dial := e.withHub()
		tabA, tabB := dial(e.agent2), dial(e.agent2)
		other := dial(e.agent1)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1, Subject: "VPN down"})
		snap.AssigneeID = e.agent2
		e.handle(e.event(tid, e.supervisor, events.TicketAssigned, snap, events.Assignment{To: e.agent2}))
		e.handle(e.event(tid, e.supervisor, events.TicketEscalated, withTeam(snap, e.team, 2), events.TierChange{FromTier: 1, ToTier: 2, Reason: "x"}))
		waitN := func(s *meSocket, name string, n int) []map[string]any {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if fs := s.named(name); len(fs) >= n {
					return fs
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("want %d %s frames, got %v", n, name, s.all())
			return nil
		}
		for _, tab := range []*meSocket{tabA, tabB} {
			fs := waitN(tab, FrameNotificationCreated, 2)
			d0, d1 := fs[0]["data"].(map[string]any), fs[1]["data"].(map[string]any)
			if d0["unread_count"] != float64(1) || d1["unread_count"] != float64(2) {
				t.Errorf("unread counts %v, %v", d0["unread_count"], d1["unread_count"])
			}
			n := d0["notification"].(map[string]any)
			if n["type"] != TplStaffAssigned || n["ticket_subject"] != "VPN down" || n["actor_name"] == nil || n["read_at"] != nil {
				t.Errorf("notification %v", n)
			}
			if p := d1["notification"].(map[string]any)["params"].(map[string]any); p["from_tier"] != float64(1) || p["to_tier"] != float64(2) {
				t.Errorf("escalation params %v", p)
			}
			if _, ok := fs[0]["event_id"]; ok {
				t.Error("notification frames must not carry event_id (resume cursor)")
			}
		}
		if len(other.named(FrameNotificationCreated)) != 0 {
			t.Error("another user received the frames")
		}
		first := waitN(tabA, FrameNotificationCreated, 2)[0]["data"].(map[string]any)["notification"].(map[string]any)["id"].(string)

		resp, body := e.do("POST", "/api/v1/me/notifications/"+first+"/read", e.agent2, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"unread_count":1`) {
			t.Fatalf("read: %d %s", resp.StatusCode, body)
		}
		for _, tab := range []*meSocket{tabA, tabB} {
			d := waitN(tab, FrameNotificationRead, 1)[0]["data"].(map[string]any)
			if d["unread_count"] != float64(1) || fmt.Sprint(d["ids"]) != "["+first+"]" {
				t.Errorf("read frame %v", d)
			}
		}
		if resp, _ := e.do("POST", "/api/v1/me/notifications/"+first+"/read", e.agent2, nil); resp.StatusCode != 200 {
			t.Errorf("read twice: %d", resp.StatusCode)
		}
		resp, body = e.do("POST", "/api/v1/me/notifications/read-all", e.agent2, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"unread_count":0`) {
			t.Fatalf("read-all: %d %s", resp.StatusCode, body)
		}
		for _, tab := range []*meSocket{tabA, tabB} {
			if d := waitN(tab, FrameNotificationRead, 2)[1]["data"].(map[string]any); d["all"] != true || d["unread_count"] != float64(0) {
				t.Errorf("read-all frame %v", d)
			}
		}
		if resp, _ := e.do("DELETE", "/api/v1/me/notifications/"+first, e.agent2, nil); resp.StatusCode != 204 {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
		waitN(tabB, FrameNotificationRead, 3)
		if len(other.named(FrameNotificationRead)) != 0 {
			t.Error("another user received read frames")
		}
		for _, s := range []*meSocket{tabA, tabB, other} {
			s.validate()
		}
	})
}

// TestNotificationsAPI (NOT-038): keyset pages newest first, unread and
// type filters, the unread count, 404 on another user's row, 422 on bad
// parameters; every response validates against the OpenAPI document.
func TestNotificationsAPI(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1})
		snap.AssigneeID = e.agent1
		for i := 0; i < 4; i++ {
			mid := e.message(tid, e.customer, "public", fmt.Sprint("reply ", i))
			e.handle(e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		}
		e.handle(e.event(tid, e.supervisor, events.TicketAssigned, snap, events.Assignment{To: e.agent1}))
		type page struct {
			Items []struct {
				ID, Type     string
				TicketNumber *int64  `json:"ticket_number"`
				ActorName    *string `json:"actor_name"`
			}
			NextCursor  *string `json:"next_cursor"`
			UnreadCount int     `json:"unread_count"`
		}
		get := func(q string) page {
			t.Helper()
			resp, body := e.do("GET", "/api/v1/me/notifications"+q, e.agent1, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("GET %s: %d %s", q, resp.StatusCode, body)
			}
			var p page
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatal(err)
			}
			return p
		}
		var ids []string
		cursor := ""
		for range 3 {
			q := "?limit=2"
			if cursor != "" {
				q += "&cursor=" + cursor
			}
			p := get(q)
			for _, it := range p.Items {
				ids = append(ids, it.ID)
			}
			if p.NextCursor == nil {
				break
			}
			cursor = *p.NextCursor
		}
		if len(ids) != 5 || !slices.IsSortedFunc(ids, func(a, b string) int { return strings.Compare(b, a) }) {
			t.Fatalf("paged ids %v", ids)
		}
		if p := get("?type=staff.assigned"); len(p.Items) != 1 || p.Items[0].Type != TplStaffAssigned || p.UnreadCount != 5 || p.Items[0].TicketNumber == nil {
			t.Errorf("type filter %+v", p)
		}
		if resp, _ := e.do("POST", "/api/v1/me/notifications/"+ids[0]+"/read", e.agent1, nil); resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		if p := get("?unread=true"); len(p.Items) != 4 || p.UnreadCount != 4 || slices.ContainsFunc(p.Items, func(it struct {
			ID, Type     string
			TicketNumber *int64  `json:"ticket_number"`
			ActorName    *string `json:"actor_name"`
		}) bool {
			return it.ID == ids[0]
		}) {
			t.Errorf("unread filter %+v", p)
		}
		resp, body := e.do("GET", "/api/v1/me/notifications/unread-count", e.agent1, nil)
		if resp.StatusCode != 200 || string(body) != "{\"unread_count\":4}\n" {
			t.Errorf("unread-count %d %q", resp.StatusCode, body)
		}
		for _, c := range []struct{ method, path string }{
			{"POST", "/api/v1/me/notifications/" + ids[1] + "/read"},
			{"DELETE", "/api/v1/me/notifications/" + ids[1]},
			{"POST", "/api/v1/me/notifications/" + store.NewID() + "/read"},
		} {
			if resp, _ := e.do(c.method, c.path, e.agent2, nil); resp.StatusCode != 404 {
				t.Errorf("%s %s as another user: %d", c.method, c.path, resp.StatusCode)
			}
		}
		for _, q := range []string{"?unread=yes", "?type=auth.invite", "?limit=0", "?limit=500", "?cursor=../x"} {
			if resp, _ := e.do("GET", "/api/v1/me/notifications"+q, e.agent1, nil); resp.StatusCode != 422 {
				t.Errorf("GET %s: %d, want 422", q, resp.StatusCode)
			}
		}
		if resp, _ := e.do("GET", "/api/v1/me/notifications", "", nil); resp.StatusCode != 401 {
			t.Errorf("anonymous: %d", resp.StatusCode)
		}
		if resp, _ := e.do("DELETE", "/api/v1/me/notifications/"+ids[1], e.agent1, nil); resp.StatusCode != 204 {
			t.Errorf("delete own: %d", resp.StatusCode)
		}
		if p := get(""); len(p.Items) != 4 || p.UnreadCount != 3 {
			t.Errorf("after delete %+v", p)
		}
		// A customer has a centre too (empty here).
		if resp, body := e.do("GET", "/api/v1/me/notifications", e.customer2, nil); resp.StatusCode != 200 || !strings.Contains(string(body), `"items":[]`) {
			t.Errorf("customer list %d %s", resp.StatusCode, body)
		}
	})
}

// TestInApp_RefAndProject (D-20): ticket notifications carry the ticket ref
// (KEY-123) and the project chip, in the notification.created frame (from
// the event snapshot) and in the list (joined at read time, so a moved ticket
// shows its current ref). An event written before projects maps to the
// Default project with no ref in the frame; a row without a ticket has both
// null. Every frame and response validates against the OpenAPI document.
func TestInApp_RefAndProject(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		ctx := context.Background()
		dial := e.withHub()
		web := storetest.CreateProject(t, db, "WEB")
		e.exec(`UPDATE projects SET name = 'Web App', color = '#1E88E5' WHERE id = ?`, web)
		storetest.MapProjectTeam(t, db, web, e.team)
		storetest.AddProjectMember(t, db, web, e.agent1, domain.ProjectMember)
		agent, cust := dial(e.agent1), dial(e.customer)

		// A D-20 event: the snapshot carries project_id and ref.
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1, ProjectID: web, Subject: "Login broken"})
		var seq int64
		if err := db.Read().QueryRowContext(ctx, `SELECT project_seq FROM tickets WHERE id = ?`, tid).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		snap.ProjectID, snap.Ref = web, domain.TicketRef("WEB", seq)
		snap.AssigneeID = e.agent1
		e.handle(e.event(tid, e.supervisor, events.TicketAssigned, snap, events.Assignment{To: e.agent1}))
		e.handle(e.event(tid, e.supervisor, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))

		wantProject := map[string]any{"id": web, "key": "WEB", "name": "Web App", "color": "#1E88E5"}
		frameOf := func(s *meSocket, ticketID string) map[string]any {
			t.Helper()
			f := s.wait(func(f map[string]any) bool {
				if !isCreated(f) {
					return false
				}
				n, _ := f["data"].(map[string]any)["notification"].(map[string]any)
				return n["ticket_id"] == ticketID
			})
			if f == nil {
				t.Fatalf("no notification.created for %s: %v", ticketID, s.all())
			}
			return f["data"].(map[string]any)["notification"].(map[string]any)
		}
		for who, s := range map[string]*meSocket{"agent": agent, "customer": cust} {
			n := frameOf(s, tid)
			if n["ticket_ref"] != snap.Ref || !reflect.DeepEqual(n["project"], wantProject) {
				t.Errorf("%s frame: ref %v project %v, want %s %v", who, n["ticket_ref"], n["project"], snap.Ref, wantProject)
			}
		}

		// An event written before projects: no project or ref in the snapshot.
		tid2, old := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 1, Subject: "Old one"})
		old.AssigneeID = e.agent1
		e.handle(e.event(tid2, e.supervisor, events.TicketAssigned, old, events.Assignment{To: e.agent1}))
		n := frameOf(agent, tid2)
		if n["ticket_ref"] != nil {
			t.Errorf("pre-projects frame ref %v, want null", n["ticket_ref"])
		}
		if p, _ := n["project"].(map[string]any); p["id"] != domain.DefaultProjectID || p["key"] != "HD" {
			t.Errorf("pre-projects frame project %v, want the Default project", n["project"])
		}

		type item struct {
			TicketID  *string        `json:"ticket_id"`
			TicketRef *string        `json:"ticket_ref"`
			Project   map[string]any `json:"project"`
		}
		list := func(userID string) []item {
			t.Helper()
			resp, body := e.do("GET", "/api/v1/me/notifications", userID, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("list: %d %s", resp.StatusCode, body)
			}
			var p struct{ Items []item }
			if err := json.Unmarshal(body, &p); err != nil {
				t.Fatal(err)
			}
			return p.Items
		}
		byTicket := func(items []item, id string) item {
			t.Helper()
			for _, it := range items {
				if it.TicketID != nil && *it.TicketID == id {
					return it
				}
			}
			t.Fatalf("no notification of ticket %s in %+v", id, items)
			return item{}
		}
		if it := byTicket(list(e.customer), tid); it.TicketRef == nil || *it.TicketRef != snap.Ref || !reflect.DeepEqual(it.Project, wantProject) {
			t.Errorf("customer list item %+v", it)
		}
		items := list(e.agent1)
		if it := byTicket(items, tid); it.TicketRef == nil || *it.TicketRef != snap.Ref || !reflect.DeepEqual(it.Project, wantProject) {
			t.Errorf("agent list item %+v", it)
		}
		var seq2 int64
		if err := db.Read().QueryRowContext(ctx, `SELECT project_seq FROM tickets WHERE id = ?`, tid2).Scan(&seq2); err != nil {
			t.Fatal(err)
		}
		if it := byTicket(items, tid2); it.TicketRef == nil || *it.TicketRef != domain.TicketRef("HD", seq2) || it.Project["id"] != domain.DefaultProjectID {
			t.Errorf("Default project list item %+v", it)
		}

		// The list follows a move: the ticket's current ref and project.
		e.exec(`UPDATE tickets SET project_id = ?, project_seq = ? WHERE id = ?`, web, seq+1, tid2)
		if it := byTicket(list(e.agent1), tid2); it.TicketRef == nil || *it.TicketRef != domain.TicketRef("WEB", seq+1) || it.Project["key"] != "WEB" {
			t.Errorf("moved ticket list item %+v", it)
		}

		// A row without a ticket: both null (and still valid against the spec).
		e.exec(`INSERT INTO notifications (id, user_id, type, ticket_id, actor_id, event_id, params, read_at, created_at) VALUES (?, ?, ?, NULL, NULL, ?, '{}', NULL, ?)`,
			store.NewID(), e.agent2, TplStaffSLA, store.NewID(), store.Now())
		resp, body := e.do("GET", "/api/v1/me/notifications", e.agent2, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"ticket_ref":null,"project":null`) {
			t.Errorf("ticketless row: %d %s", resp.StatusCode, body)
		}
		agent.validate()
		cust.validate()
	})
}

// TestPruneNotifications (NOT-039): 90 days, then the newest 500 per user.
func TestPruneNotifications(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		now := store.Now()
		old := now.Add(-91 * 24 * time.Hour)
		var newest string
		err := db.Tx(context.Background(), func(tx *store.Tx) error {
			ins := func(uid string, at time.Time) (string, error) {
				id := store.NewID()
				_, err := tx.ExecContext(context.Background(), `INSERT INTO notifications (id, user_id, type, event_id, params, created_at) VALUES (?, ?, 'staff.assigned', ?, '{}', ?)`, id, uid, store.NewID(), at)
				return id, err
			}
			for i := 0; i < 3; i++ {
				if _, err := ins(e.agent2, old); err != nil {
					return err
				}
			}
			if _, err := ins(e.agent2, now); err != nil {
				return err
			}
			for i := 0; i < NotificationMaxPerUser+12; i++ {
				id, err := ins(e.agent1, now)
				if err != nil {
					return err
				}
				newest = id
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		n, err := PruneNotifications(context.Background(), db, now)
		if err != nil {
			t.Fatal(err)
		}
		if n != 15 {
			t.Errorf("pruned %d rows, want 15", n)
		}
		if c := e.count(`SELECT COUNT(*) FROM notifications WHERE user_id = ?`, e.agent1); c != NotificationMaxPerUser {
			t.Errorf("agent1 keeps %d", c)
		}
		if c := e.count(`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND id = ?`, e.agent1, newest); c != 1 {
			t.Error("the newest row was pruned")
		}
		if c := e.count(`SELECT COUNT(*) FROM notifications WHERE user_id = ?`, e.agent2); c != 1 {
			t.Errorf("agent2 keeps %d, want 1 (the recent one)", c)
		}
		if n, _ := PruneNotifications(context.Background(), db, now); n != 0 {
			t.Errorf("second prune removed %d", n)
		}
	})
}

// TestNotificationTypeEnumMatchesSpec: NotificationType in api/openapi.yaml
// is exactly InAppTypes.
func TestNotificationTypeEnumMatchesSpec(t *testing.T) {
	s, err := apispec.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Components.Schemas["NotificationType"].Enum; !slices.Equal(got, InAppTypes) {
		t.Errorf("NotificationType enum %v, want %v", got, InAppTypes)
	}
}
