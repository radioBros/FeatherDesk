// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/notify/thread"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func keysTo(ts []Target) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Key+"→"+t.R.Addr)
	}
	sort.Strings(out)
	return out
}

// TestRecipients: one row per events_spec §2 matrix line plus the global rules.
func TestRecipients(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		ctx := context.Background()
		em := e.email
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team, Tier: 2})
		triageID, triage := e.ticket(storetest.Ticket{})
		msgCust := e.message(tid, e.customer, "public", "help")
		msgStaff := e.message(tid, e.agent2, "public", "answer")
		assigned := snap
		assigned.AssigneeID = e.agent2

		cases := []struct {
			name string
			ev   events.Event
			want []string
		}{
			{"created: requester + tier≥2 team agents (L1 excluded)", events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.TicketCreated, Snapshot: snap},
				[]string{"staff.ticket_new→" + em(e.agent2)}}, // requester is the actor → excluded
			{"created by staff: requester gets confirmation", events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.TicketCreated, Snapshot: snap},
				[]string{"customer.ticket_received→" + em(e.customer)}},
			{"created in triage: supervisors + admins", events.Event{ID: store.NewID(), TicketID: triageID, ActorID: e.customer, Type: events.TicketCreated, Snapshot: triage},
				[]string{"staff.ticket_new→" + em(e.admin), "staff.ticket_new→" + em(e.supervisor)}},
			{"staff public reply → requester", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.MessageCreated, Snapshot: assigned}, events.MessageRef{MessageID: msgStaff, Kind: "public"}),
				[]string{"customer.reply→" + em(e.customer)}},
			{"customer reply → assignee", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.MessageCreated, Snapshot: assigned}, events.MessageRef{MessageID: msgCust, Kind: "public"}),
				[]string{"staff.customer_replied→" + em(e.agent2)}},
			{"customer reply unassigned → team (tier-aware)", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.MessageCreated, Snapshot: snap}, events.MessageRef{MessageID: msgCust, Kind: "public"}),
				[]string{"staff.customer_replied→" + em(e.agent2)}},
			{"assigned to someone else", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.supervisor, Type: events.TicketAssigned, Snapshot: assigned}, events.Assignment{To: e.agent2}),
				[]string{"staff.assigned→" + em(e.agent2)}},
			{"self-assign: nobody", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.TicketAssigned, Snapshot: assigned}, events.Assignment{To: e.agent2}),
				nil},
			{"escalated to team2 tier 3", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.TicketEscalated, Snapshot: withTeam(snap, e.team2, 3)}, events.TierChange{FromTier: 2, ToTier: 3, FromTeam: e.team, ToTeam: e.team2, Reason: "r"}),
				[]string{"staff.escalated→" + em(e.agentOther)}},
			{"resolved → requester", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.TicketStatusChanged, Snapshot: snap}, events.StatusChange{From: "open", To: "resolved"}),
				[]string{"customer.resolved→" + em(e.customer)}},
			{"status other than resolved: nobody", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.TicketStatusChanged, Snapshot: snap}, events.StatusChange{From: "new", To: "open"}),
				nil},
			{"mention (L1 cannot see tier-2 ticket)", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.NoteCreated, Snapshot: snap}, map[string]any{"message_id": "x", "kind": "internal", "mentions": []string{e.agent1, e.customer}, "mentioned": []string{e.supervisor}}),
				[]string{"staff.mentioned→" + em(e.supervisor)}},
			{"SLA breach: assignee + supervisors", mustData(events.Event{ID: store.NewID(), TicketID: tid, Type: events.SLABreached, Snapshot: assigned}, events.SLA{Which: "resolution", DueAt: time.Now()}),
				[]string{"staff.sla→" + em(e.agent2), "staff.sla→" + em(e.supervisor)}},
			{"children resolved → parent assignee", mustData(events.Event{ID: store.NewID(), TicketID: tid, Type: events.TicketChildrenResolved, Snapshot: assigned}, map[string]string{"parent_id": tid}),
				[]string{"staff.children_resolved→" + em(e.agent2)}},
			{"internal message.created: nobody", mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.agent2, Type: events.MessageCreated, Snapshot: snap}, events.MessageRef{MessageID: msgStaff, Kind: "internal"}),
				nil},
		}
		for _, c := range cases {
			got, err := e.m.Recipients(ctx, c.ev)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			want := slices.Clone(c.want)
			sort.Strings(want)
			if g := keysTo(got); !slices.Equal(g, want) {
				t.Errorf("%s:\n got  %v\n want %v", c.name, g, want)
			}
		}

		// A disabled user is never a recipient.
		e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, e.agent2)
		got, _ := e.m.Recipients(ctx, mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.MessageCreated, Snapshot: assigned}, events.MessageRef{MessageID: msgCust, Kind: "public"}))
		if len(got) != 0 {
			t.Errorf("disabled assignee notified: %v", keysTo(got))
		}
		e.exec(`UPDATE users SET status = 'active' WHERE id = ?`, e.agent2)

		// A reassignment committed after the event doesn't change its recipients.
		ev := mustData(events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.MessageCreated, Snapshot: assigned}, events.MessageRef{MessageID: msgCust, Kind: "public"})
		e.exec(`UPDATE tickets SET assignee_id = ? WHERE id = ?`, e.agentOther, tid)
		got, _ = e.m.Recipients(ctx, ev)
		if g := keysTo(got); !slices.Equal(g, []string{"staff.customer_replied→" + em(e.agent2)}) {
			t.Errorf("recipients followed live state: %v", g)
		}
	})
}

func mustData(e events.Event, data any) events.Event {
	b, _ := json.Marshal(data)
	e.Data = b
	return e
}

func withTeam(s events.Snapshot, team string, tier int) events.Snapshot {
	s.TeamID, s.Tier = team, tier
	return s
}

// TestHappyPathNoDBWrites: 20 successful sends write nothing (no deferred
// row, no transaction at all). The recipient turned the in-app channel off:
// in-app rows cost exactly one transaction per event (TestInApp_OneTxPerEvent).
func TestHappyPathNoDBWrites(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		snap.AssigneeID = e.agent1
		e.exec(`UPDATE users SET notify_prefs = ? WHERE id = ?`, `{"in_app_off":["staff.customer_replied"]}`, e.agent1)
		var evs []events.Event
		for i := 0; i < 20; i++ {
			mid := e.message(tid, e.customer, "public", "reply")
			evs = append(evs, e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		}
		before := e.commit.Load()
		for _, ev := range evs {
			e.handle(ev)
		}
		if n := len(e.fake.Mails()); n != 20 {
			t.Fatalf("sent %d mails, want 20", n)
		}
		if d := e.commit.Load() - before; d != 0 {
			t.Errorf("%d write transactions on the happy path", d)
		}
		if n := e.countJobs("1=1"); n != 0 {
			t.Errorf("%d deferred_jobs rows", n)
		}
	})
}

func smtpErr(code string, perm bool) error {
	return &SendFailure{Code: code, Permanent: perm, Err: errors.New(code)}
}

// TestTempFailureDefersOnce: a 421 writes exactly one retrying row, which the
// queue retries and removes.
func TestTempFailureDefersOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		snap.AssigneeID = e.agent1
		mid := e.message(tid, e.customer, "public", "reply")
		ev := e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"})
		e.fake.Fail = func(*Mail) error { return smtpErr("other", false) }
		e.handle(ev)
		if n := e.countJobs("kind = ? AND status = 'retrying' AND attempts = 1", JobSend); n != 1 {
			t.Fatalf("retrying rows = %d", n)
		}
		e.fake.Fail = nil
		q := queue.New(db, queue.Options{Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond})
		e.m.RegisterQueue(q)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Skip replaying the event itself: start the cursor after it.
		e.exec(`UPDATE counters SET text_value = ? WHERE name = 'dispatch_cursor'`, ev.ID)
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := q.Stop(context.Background()); err != nil {
				t.Errorf("q.Stop: %v", err)
			}
		}()
		var id string
		_ = db.Read().QueryRowContext(ctx, `SELECT id FROM deferred_jobs`).Scan(&id)
		if err := q.Retry(ctx, id); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return e.countJobs("1=1") == 0 })
		if n := len(e.fake.Mails()); n != 1 {
			t.Errorf("mails after retry = %d", n)
		}
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// TestPermanentFailureDead: a 550 marks the mail dead after one attempt.
func TestPermanentFailureDead(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		attempts := 0
		e.fake.Fail = func(*Mail) error { attempts++; return smtpErr("rejected", true) }
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{From: "open", To: "resolved"}))
		if attempts != 1 {
			t.Errorf("attempts = %d", attempts)
		}
		if n := e.countJobs("kind = ? AND status = 'dead' AND attempts = 1", JobSend); n != 1 {
			t.Errorf("dead rows = %d", n)
		}
		if e.countJobs("status = 'retrying'") != 0 {
			t.Error("permanent failure scheduled a retry")
		}
	})
}

// TestReplaySameMessageID: a replayed event yields the same Message-ID; the
// same process skips the duplicate, a restarted one sends the same id.
func TestReplaySameMessageID(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		ev := e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"})
		e.handle(ev)
		e.handle(ev) // replay in the same process
		ms := e.fake.Mails()
		if len(ms) != 1 {
			t.Fatalf("mails = %d after replay", len(ms))
		}
		want := thread.MessageID(ev.ID, e.email(e.customer), "help.example.test")
		if ms[0].MessageID != want {
			t.Errorf("Message-ID %s, want %s", ms[0].MessageID, want)
		}
		m2, _ := New(Deps{DB: db, Settings: e.set, Secrets: e.box, Cfg: e.cfg, Getenv: e.getenv})
		f2 := &FakeSender{}
		m2.SetSender(f2)
		if err := m2.handleEvent(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
		if len(f2.Mails()) != 1 || f2.Mails()[0].MessageID != want {
			t.Errorf("restart replay Message-ID differs")
		}
	})
}

// TestSharedReferences: two mails on one ticket share References.
func TestSharedReferences(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketCreated, snap, nil))
		mid := e.message(tid, e.agent1, "public", "hello")
		e.handle(e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		ms := sentTo(e.fake.Mails(), e.email(e.customer))
		if len(ms) != 2 {
			t.Fatalf("customer mails = %d", len(ms))
		}
		ref := thread.TicketThreadID(tid, "help.example.test")
		for _, m := range ms {
			if m.References != ref || m.InReplyTo != ref {
				t.Errorf("References %q / In-Reply-To %q, want %q", m.References, m.InReplyTo, ref)
			}
			if !strings.HasPrefix(m.Subject, "[#") {
				t.Errorf("subject %q", m.Subject)
			}
		}
		if ms[0].MessageID == ms[1].MessageID {
			t.Error("distinct mails share a Message-ID")
		}
	})
}

// TestSenderIdentityByAudience: customer.* uses the customer-facing
// identity, staff.* the staff-facing one.
func TestSenderIdentityByAudience(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketCreated, snap, nil))
		for _, m := range e.fake.Mails() {
			isCustomer := strings.EqualFold(m.To.Address, e.email(e.customer))
			switch {
			case isCustomer && (m.From.Address != "support@acme.test" || m.From.Name != "Acme Support" || m.ReplyTo != "support@acme.test"):
				t.Errorf("customer mail From %v Reply-To %q", m.From, m.ReplyTo)
			case !isCustomer && (m.From.Address != "noreply@acme.test" || m.From.Name != "Acme Helpdesk"):
				t.Errorf("staff mail From %v", m.From)
			}
		}
		if len(e.fake.Mails()) < 2 {
			t.Fatalf("mails = %d", len(e.fake.Mails()))
		}
	})
}

// TestTeamMailboxModes: agents / mailbox / both.
func TestTeamMailboxModes(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		for mode, want := range map[string][]string{
			"agents":  {e.email(e.agent1), e.email(e.agent2)},
			"mailbox": {"billing@acme.test"},
			"both":    {"billing@acme.test", e.email(e.agent1), e.email(e.agent2)},
		} {
			e.exec(`UPDATE teams SET notification_email = 'billing@acme.test', notify_mode = ? WHERE id = ?`, mode, e.team)
			got, err := e.m.Recipients(context.Background(), events.Event{ID: store.NewID(), TicketID: tid, ActorID: e.customer, Type: events.TicketCreated, Snapshot: snap})
			if err != nil {
				t.Fatal(err)
			}
			var addrs []string
			for _, g := range got {
				if g.Key == TplStaffTicketNew {
					addrs = append(addrs, g.R.Addr)
				}
			}
			sort.Strings(addrs)
			sort.Strings(want)
			if !slices.Equal(addrs, want) {
				t.Errorf("mode %s: %v, want %v", mode, addrs, want)
			}
		}
		// The mailbox gets the instance default locale.
		e.exec(`UPDATE teams SET notify_mode = 'both' WHERE id = ?`, e.team)
		e.handle(e.event(tid, e.customer, events.TicketCreated, snap, nil))
		mb := sentTo(e.fake.Mails(), "billing@acme.test")
		if len(mb) != 1 || !strings.Contains(mb[0].Subject, "New ticket") {
			t.Errorf("mailbox mail: %+v", mb)
		}
	})
}

// TestTriageMailbox: an uncategorised (triage) ticket notifies the triage mailbox.
func TestTriageMailbox(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.setEmail(func(em *settings.Email) { em.TriageMailbox = "triage@acme.test" })
		tid, snap := e.ticket(storetest.Ticket{})
		e.handle(e.event(tid, e.customer, events.TicketCreated, snap, nil))
		if len(sentTo(e.fake.Mails(), "triage@acme.test")) != 1 {
			t.Errorf("triage mailbox not notified")
		}
		// Escalation into a team also reaches its mailbox.
		e.exec(`UPDATE teams SET notification_email = 'net@acme.test', notify_mode = 'mailbox' WHERE id = ?`, e.team2)
		e.handle(e.event(tid, e.supervisor, events.TicketEscalated, withTeam(snap, e.team2, 2), events.TierChange{FromTier: 1, ToTier: 2, ToTeam: e.team2, Reason: "x"}))
		if len(sentTo(e.fake.Mails(), "net@acme.test")) != 1 {
			t.Errorf("team mailbox not notified of escalation")
		}
	})
}

// TestTemplateToggle: disabling customer.ticket_received sends 0 confirmations.
func TestTemplateToggle(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.setEmail(func(em *settings.Email) { em.DisabledTemplates = []string{TplCustomerTicketReceived} })
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketCreated, snap, nil))
		if n := len(sentTo(e.fake.Mails(), e.email(e.customer))); n != 0 {
			t.Errorf("confirmation sent %d times while disabled", n)
		}
		if len(e.fake.Mails()) == 0 {
			t.Error("staff notifications were also suppressed")
		}
	})
}

// TestOptOut: a staff user who opts out of staff.ticket_new gets nothing;
// opting out of customer.reply is ignored (not optional).
func TestOptOut(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.exec(`UPDATE users SET notify_prefs = ? WHERE id = ?`, `{"opt_out":["staff.ticket_new"]}`, e.agent2)
		e.exec(`UPDATE users SET notify_prefs = ? WHERE id = ?`, `{"opt_out":["customer.reply"]}`, e.customer)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.customer, events.TicketCreated, snap, nil))
		if n := len(sentTo(e.fake.Mails(), e.email(e.agent2))); n != 0 {
			t.Errorf("opted-out agent got %d mails", n)
		}
		if n := len(sentTo(e.fake.Mails(), e.email(e.agent1))); n != 1 {
			t.Errorf("other agent got %d mails", n)
		}
		mid := e.message(tid, e.agent1, "public", "hi")
		e.handle(e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		if n := len(sentTo(e.fake.Mails(), e.email(e.customer))); n != 1 {
			t.Errorf("customer.reply opt-out honoured (%d mails); it must not be optional", n)
		}
		if fe := ValidateOptOut([]string{"staff.ticket_new", "auth.reset_password", "customer.reply", "nope"}); len(fe) != 3 ||
			fe[0].Code != "not_optional" || fe[1].Code != "not_optional" || fe[2].Code != "invalid" {
			t.Errorf("ValidateOptOut = %+v", fe)
		}
	})
}

// TestEmailDisabledLogsOnly: no SMTP → one INFO line per recipient, no
// deferred row, no body/link/token in the log.
func TestEmailDisabledLogsOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.setSMTP("")
		if Enabled(context.Background()) || EnabledWith(context.Background(), e.set) {
			t.Fatal("Enabled with no SMTP host")
		}
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketCreated, snap, nil))
		if err := e.m.handleAuthJob(context.Background(), mustJSON(AuthMail{Template: TplAuthResetPassword, To: e.email(e.customer), Vars: map[string]string{"url": "https://help.example.test/reset?token=SECRET123"}})); err != nil {
			t.Fatal(err)
		}
		logs := e.logs.String()
		if c := strings.Count(logs, "email disabled"); c != 3 { // requester + agent2 (agent1 is the actor) + reset
			t.Errorf("%d log lines:\n%s", c, logs)
		}
		for _, bad := range []string{"SECRET123", "token=", "https://help.example.test/tickets", "Hello"} {
			if strings.Contains(logs, bad) {
				t.Errorf("log leaks %q", bad)
			}
		}
		if len(e.fake.Mails()) != 0 || e.countJobs("1=1") != 0 {
			t.Errorf("mails %d, deferred %d", len(e.fake.Mails()), e.countJobs("1=1"))
		}
		// Saving a working config enables email without restart.
		e.setSMTP("smtp.acme.test")
		if !Enabled(context.Background()) {
			t.Error("not enabled after saving SMTP")
		}
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))
		if len(e.fake.Mails()) != 1 {
			t.Errorf("mails after enabling = %d", len(e.fake.Mails()))
		}
	})
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// TestConfigSwapNoRestart: saving new SMTP settings changes the next send's host.
func TestConfigSwapNoRestart(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))
		e.setSMTP("smtp2.acme.test")
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))
		if h := e.fake.Hosts; len(h) != 2 || h[0] != "smtp.acme.test" || h[1] != "smtp2.acme.test" {
			t.Errorf("hosts = %v", h)
		}
	})
}

// TestCoalesceReplies: three customer replies within the window → one
// pending row, then exactly one staff.customer_replied mail with all three.
func TestCoalesceReplies(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, map[string]string{"ST_NOTIFY_COALESCE_SECONDS": "60"})
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		snap.AssigneeID = e.agent1
		for _, body := range []string{"first CANARY-1", "second CANARY-2", "third CANARY-3"} {
			mid := e.message(tid, e.customer, "public", body)
			e.handle(e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		}
		if n := e.countJobs("1=1"); n != 1 {
			t.Fatalf("deferred rows = %d, want 1", n)
		}
		if len(e.fake.Mails()) != 0 {
			t.Fatal("coalesced mail sent before the window ended")
		}
		var payload string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT payload FROM deferred_jobs WHERE kind = ?`, JobCoalesced).Scan(&payload)
		if err := e.m.handleCoalescedJob(context.Background(), json.RawMessage(payload)); err != nil {
			t.Fatal(err)
		}
		ms := e.fake.Mails()
		if len(ms) != 1 {
			t.Fatalf("mails = %d", len(ms))
		}
		for _, c := range []string{"CANARY-1", "CANARY-2", "CANARY-3"} {
			if !strings.Contains(ms[0].Text, c) {
				t.Errorf("aggregate lacks %s", c)
			}
		}
		// auth.* is never coalesced: two resets → two mails.
		for i := 0; i < 2; i++ {
			if err := e.m.handleAuthJob(context.Background(), mustJSON(AuthMail{Template: TplAuthResetPassword, To: e.email(e.customer), Vars: map[string]string{"url": "https://x/r"}})); err != nil {
				t.Fatal(err)
			}
		}
		if len(e.fake.Mails()) != 3 {
			t.Errorf("auth mails coalesced: %d", len(e.fake.Mails())-1)
		}
	})
}

// TestCustomerMailNeverContainsInternal (I-7): fire every event with a canary
// in internal notes and the escalation reason; no customer mail carries it.
func TestCustomerMailNeverContainsInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		const canary = "CANARY-INTERNAL-7f3a"
		e.exec(`UPDATE teams SET notification_email = 'billing@acme.test', notify_mode = 'both' WHERE id = ?`, e.team)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		snap.Watchers = []string{e.customer2}
		e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, tid, e.customer2, store.Now())
		note := e.message(tid, e.agent1, "internal", "secret "+canary)
		pub := e.message(tid, e.agent1, "public", "public answer")
		cust := e.message(tid, e.customer, "public", "customer text")
		fire := []events.Event{
			e.event(tid, e.agent1, events.TicketCreated, snap, nil),
			e.event(tid, e.agent1, events.NoteCreated, snap, map[string]any{"message_id": note, "kind": "internal", "mentions": []string{e.agent2}}),
			e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: note, Kind: "public"}), // mislabelled kind
			e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: pub, Kind: "public"}),
			e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: cust, Kind: "public"}),
			e.event(tid, e.agent1, events.TicketEscalated, withTeam(snap, e.team, 2), events.TierChange{FromTier: 1, ToTier: 2, Reason: "reason " + canary}),
			e.event(tid, e.agent1, events.TicketAssigned, snap, events.Assignment{To: e.agent2}),
			e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}),
			e.event(tid, "", events.SLABreached, snap, events.SLA{Which: "resolution"}),
			e.event(tid, "", events.TicketChildrenResolved, snap, map[string]string{"parent_id": tid}),
		}
		for _, ev := range fire {
			e.handle(ev)
		}
		custAddrs := map[string]bool{strings.ToLower(e.email(e.customer)): true, strings.ToLower(e.email(e.customer2)): true}
		nCust, staffSaw := 0, false
		for _, m := range e.fake.Mails() {
			if custAddrs[strings.ToLower(m.To.Address)] {
				nCust++
				if strings.Contains(m.Subject+m.Text+m.HTML, canary) {
					t.Errorf("canary in customer mail %q", m.Subject)
				}
			} else if strings.Contains(m.Text, canary) {
				staffSaw = true
			}
		}
		if nCust < 4 {
			t.Errorf("only %d customer mails rendered", nCust)
		}
		if !staffSaw {
			t.Error("canary never reached staff mail: test is not exercising internal content")
		}
	})
}

// TestAuthMailJob: mail.auth renders auth.* with the right identity and locale.
func TestAuthMailJob(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.exec(`UPDATE users SET locale = 'de' WHERE id = ?`, e.customer)
		if err := e.m.handleAuthJob(context.Background(), mustJSON(AuthMail{Template: TplAuthVerifyEmail, UserID: e.customer, To: e.email(e.customer), Vars: map[string]string{"url": "https://help.example.test/verify?t=abc"}})); err != nil {
			t.Fatal(err)
		}
		if err := e.m.handleAuthJob(context.Background(), mustJSON(AuthMail{Template: TplAuthInvite, To: e.email(e.agent1), Vars: map[string]string{"url": "https://help.example.test/agent/invite?t=x", "inviter": "Boss"}})); err != nil {
			t.Fatal(err)
		}
		ms := e.fake.Mails()
		if len(ms) != 2 {
			t.Fatalf("mails = %d", len(ms))
		}
		if !strings.Contains(ms[0].Subject, "Bestätigen") || !strings.Contains(ms[0].Text, "https://help.example.test/verify?t=abc") || ms[0].From.Address != "support@acme.test" {
			t.Errorf("verify mail: %q from %v", ms[0].Subject, ms[0].From)
		}
		if !strings.Contains(ms[0].Text, "1440") {
			t.Error("expiry not stated")
		}
		if ms[1].From.Address != "noreply@acme.test" || !strings.Contains(ms[1].Text, "Boss") {
			t.Errorf("invite mail from %v", ms[1].From)
		}
		if strings.HasPrefix(ms[0].Subject, "[#") || ms[0].References != "" {
			t.Error("auth mail carries ticket headers")
		}
		// auth.* cannot be disabled by the template toggle.
		e.setEmail(func(em *settings.Email) { em.DisabledTemplates = []string{TplAuthVerifyEmail} })
		_ = e.m.handleAuthJob(context.Background(), mustJSON(AuthMail{Template: TplAuthVerifyEmail, To: "x@y.test", Vars: map[string]string{"url": "u"}}))
		if len(e.fake.Mails()) != 3 {
			t.Error("auth mail suppressed by toggle")
		}
	})
}

// TestAdminMailJob: mail.admins reaches every active admin once.
func TestAdminMailJob(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		admin2 := storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
		if err := e.m.handleAdminJob(context.Background(), mustJSON(AdminMail{Template: TplStaffWebhookDisabled, Vars: map[string]string{"url": "https://hooks.example.test/x", "reason": "failures", "failures": "20"}})); err != nil {
			t.Fatal(err)
		}
		ms := e.fake.Mails()
		got := []string{}
		for _, m := range ms {
			got = append(got, strings.ToLower(m.To.Address))
			if !strings.Contains(m.Subject, "https://hooks.example.test/x") {
				t.Errorf("subject %q", m.Subject)
			}
		}
		sort.Strings(got)
		want := []string{strings.ToLower(e.email(e.admin)), strings.ToLower(e.email(admin2))}
		sort.Strings(want)
		if !slices.Equal(got, want) {
			t.Errorf("admin mails to %v, want %v", got, want)
		}
	})
}

// TestPlusAddressReplyTo: with plus addressing on, Reply-To carries a
// verifiable token; off, it equals the identity's Reply-To.
func TestPlusAddressReplyTo(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))
		e.setEmail(func(em *settings.Email) { em.PlusAddressing = true })
		e.handle(e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"}))
		ms := e.fake.Mails()
		if ms[0].ReplyTo != "support@acme.test" {
			t.Errorf("off: Reply-To %q", ms[0].ReplyTo)
		}
		tok, ok := thread.TokenFromAddress(ms[1].ReplyTo)
		if !ok || !strings.HasPrefix(ms[1].ReplyTo, "support+t-") {
			t.Fatalf("on: Reply-To %q", ms[1].ReplyTo)
		}
		got, rh, ok := thread.VerifyPlusToken(e.box, tok)
		if !ok || got != tid || !thread.RecipientMatches(rh, e.email(e.customer)) {
			t.Errorf("token does not verify: %v %s", ok, got)
		}
	})
}

// TestSignatureAppendedToStaffReplyMail: the author's signature is in the
// customer.reply mail but never stored in the message body.
func TestSignatureAppendedToStaffReplyMail(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.exec(`UPDATE users SET signature_md = ? WHERE id = ?`, "Kind regards, **SIG-MARK**", e.agent1)
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		mid := e.message(tid, e.agent1, "public", "the answer")
		e.handle(e.event(tid, e.agent1, events.MessageCreated, snap, events.MessageRef{MessageID: mid, Kind: "public"}))
		ms := sentTo(e.fake.Mails(), e.email(e.customer))
		if len(ms) != 1 || !strings.Contains(ms[0].Text, "SIG-MARK") || !strings.Contains(ms[0].HTML, "<strong>SIG-MARK</strong>") {
			t.Fatalf("signature missing: %+v", ms)
		}
		var body string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT body_md FROM ticket_messages WHERE id = ?`, mid).Scan(&body)
		if strings.Contains(body, "SIG-MARK") {
			t.Error("signature stored in the message body")
		}
		// Not added to staff notifications or customer-authored mail.
		cm := e.message(tid, e.customer, "public", "thanks")
		snap.AssigneeID = e.agent1
		e.handle(e.event(tid, e.customer, events.MessageCreated, snap, events.MessageRef{MessageID: cm, Kind: "public"}))
		for _, m := range sentTo(e.fake.Mails(), e.email(e.agent1)) {
			if strings.Contains(m.Text, "SIG-MARK") {
				t.Error("signature in staff notification")
			}
		}
	})
}

// TestQueueEndToEnd: an event written to ticket_events reaches the fake SMTP
// sender through the real dispatcher.
func TestQueueEndToEnd(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		q := queue.New(db, queue.Options{Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond})
		e.m.RegisterQueue(q)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := q.Stop(context.Background()); err != nil {
				t.Errorf("q.Stop: %v", err)
			}
		}()
		tid, snap := e.ticket(storetest.Ticket{TeamID: e.team})
		e.event(tid, e.agent1, events.TicketStatusChanged, snap, events.StatusChange{To: "resolved"})
		waitFor(t, func() bool { return len(e.fake.Mails()) == 1 })
		if err := q.Enqueue(ctx, JobAuthMail, AuthMail{Template: TplAuthMagicLink, To: "z@example.test", Vars: map[string]string{"url": "https://x/m"}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return len(e.fake.Mails()) == 2 })
	})
}
