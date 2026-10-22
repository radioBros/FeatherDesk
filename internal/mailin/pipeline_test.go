// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func ticketNumber(t *testing.T, db *store.DB, id string) int64 {
	var n int64
	if err := db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewTicketFromKnownCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		team := storetest.CreateTeam(t, db, "")
		cust := customer(t, db, "alice@client.example.org", "")
		mbID, _ := e.webhookMailbox(team, "")
		id := e.receive(mbID, mkMail("Alice <alice@client.example.org>", "Printer broken", "It does not print.\n\nOn Mon, Bob wrote:\n> old", nil))
		st, reason, _ := e.msgStatus(id)
		if st != "processed" {
			t.Fatalf("status %s/%s", st, reason)
		}
		if len(e.sink.creates) != 1 {
			t.Fatalf("creates = %d", len(e.sink.creates))
		}
		c := e.sink.creates[0]
		if c.RequesterID != cust || c.TeamID != team || c.Subject != "Printer broken" || c.InboundMessageID != id {
			t.Fatalf("bad create %+v", c)
		}
		if strings.Contains(c.BodyMD, "old") {
			t.Fatalf("quote not stripped: %q", c.BodyMD)
		}
		var orig bool
		for _, a := range c.Attachments {
			if a.Filename == "original.eml" && len(a.Data) > 0 {
				orig = true
			}
		}
		if !orig {
			t.Fatal("original.eml missing")
		}
	})
}

func TestDuplicateMessageProcessedOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "dup@client.example.org", "")
		hookID, tok := e.webhookMailbox("", "")
		// a second (IMAP) mailbox receives the same message
		resp, data := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{
			"name": "IMAP", "kind": "imap", "address": "imap@example.test",
			"imap": map[string]any{"host": "127.0.0.1", "port": 1143, "security": "none", "username": "u", "password": "p"},
		})
		if resp.StatusCode != 201 {
			t.Fatalf("%d %s", resp.StatusCode, data)
		}
		var imapID string
		mbs, _ := e.m.listMailboxes(context.Background(), false)
		for _, mb := range mbs {
			if mb.Kind == "imap" {
				imapID = mb.ID
			}
		}
		raw := mkMail("dup@client.example.org", "Same", "hello", map[string]string{"Message-ID": "<same-1@client.example.org>"})
		// IMAP path (deliver func) twice + webhook path twice
		deliver := e.m.deliverFor(e.mailbox(imapID))
		for range 2 {
			if err := deliver(context.Background(), raw, Meta{}); err != nil {
				t.Fatal(err)
			}
			r, d := e.do("POST", "/api/v1/inbound/mime/"+tok, "", raw)
			if r.StatusCode != 202 {
				t.Fatalf("webhook %d %s", r.StatusCode, d)
			}
		}
		if c, _ := e.sink.counts(); c != 1 {
			t.Fatalf("tickets created = %d, want 1", c)
		}
		var n int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM inbound_messages`).Scan(&n)
		if n != 2 {
			t.Fatalf("rows = %d, want 2 (one per mailbox)", n)
		}
		var ign int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM inbound_messages WHERE status = 'ignored' AND reason = 'duplicate'`).Scan(&ign)
		if ign != 1 {
			t.Fatalf("duplicate rows = %d", ign)
		}
		_ = hookID
	})
}

func TestCrashAfterRecordReprocesses(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		data := t.TempDir()
		e := newEnv(t, db, map[string]string{"FD_DATA_DIR": data})
		customer(t, db, "crash@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		// "crash": recorded, never processed
		rec, err := e.m.Record(context.Background(), e.mailbox(mbID), mkMail("crash@client.example.org", "Crash", "x", nil))
		if err != nil {
			t.Fatal(err)
		}
		if st, reason, _ := e.msgStatus(rec.ID); st != "failed" || reason != "pending" {
			t.Fatalf("recorded as %s/%s", st, reason)
		}
		// restart: a new module instance recovers pending rows
		e2 := newEnv(t, db, map[string]string{"FD_DATA_DIR": data})
		if err := e2.m.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := e2.m.Recover(context.Background()); err != nil { // idempotent
			t.Fatal(err)
		}
		if st, _, _ := e2.msgStatus(rec.ID); st != "processed" {
			t.Fatalf("status %s", st)
		}
		if c, _ := e2.sink.counts(); c != 1 {
			t.Fatalf("creates = %d, want exactly 1", c)
		}
	})
}

func TestQuarantineRetry(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		setSelfReg(t, e.set, false)
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("stranger@nowhere.example.org", "Hi", "help", nil))
		if st, reason, _ := e.msgStatus(id); st != "quarantined" || reason != "unknown_sender" {
			t.Fatalf("got %s/%s", st, reason)
		}
		customer(t, db, "stranger@nowhere.example.org", "") // admin fixes the cause
		resp, data := e.do("POST", "/api/v1/admin/inbound/messages/"+id+"/retry", "staff:admin", nil)
		if resp.StatusCode != 200 || !strings.Contains(string(data), `"processed"`) {
			t.Fatalf("retry %d %s", resp.StatusCode, data)
		}
		// list shows it
		resp, data = e.do("GET", "/api/v1/admin/inbound/messages?status=processed", "staff:admin", nil)
		if resp.StatusCode != 200 || !strings.Contains(string(data), id) {
			t.Fatalf("list %d %s", resp.StatusCode, data)
		}
		for who, want := range map[string]int{"staff:agent": 403, "customer:requester": 404} {
			if r, _ := e.do("POST", "/api/v1/admin/inbound/messages/"+id+"/retry", who, nil); r.StatusCode != want {
				t.Fatalf("%s → %d, want %d", who, r.StatusCode, want)
			}
			if r, _ := e.do("GET", "/api/v1/admin/inbound/messages", who, nil); r.StatusCode != want {
				t.Fatalf("%s list → %d, want %d", who, r.StatusCode, want)
			}
		}
	})
}

func TestInboundPrune90Days(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		setSelfReg(t, e.set, false)
		customer(t, db, "old@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		q := e.receive(mbID, mkMail("unknown@x.example.org", "Q", "q", nil))
		p := e.receive(mbID, mkMail("old@client.example.org", "P", "p", nil))
		old := store.Now().Add(-91 * 24 * time.Hour)
		exec(t, db, `UPDATE inbound_messages SET received_at = ?`, old)
		n, keys, err := store.PruneInboundMessages(context.Background(), db, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || len(keys) != 1 {
			t.Fatalf("pruned %d (%d keys), want 1", n, len(keys))
		}
		var left string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT id FROM inbound_messages`).Scan(&left)
		if left != q {
			t.Fatalf("kept %s, want quarantined %s (processed %s pruned)", left, q, p)
		}
	})
}

func TestThreadByReferences(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		cust := customer(t, db, "ref@client.example.org", "")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mbID, _ := e.webhookMailbox("", "")
		// ticket thread id
		id := e.receive(mbID, mkMail("ref@client.example.org", "Re: something", "reply 1", map[string]string{"References": thread.TicketThreadID(tk, "help.example.test")}))
		if st, _, tid := e.msgStatus(id); st != "processed" || tid != tk {
			t.Fatalf("thread id: %s %s", st, tid)
		}
		// event message id
		var ev events.Event
		ev.TicketID, ev.Type = tk, events.MessageCreated
		ev.Snapshot.RequesterID = cust
		if err := db.Tx(context.Background(), func(tx *store.Tx) error { return events.Write(context.Background(), tx, &ev, nil) }); err != nil {
			t.Fatal(err)
		}
		id = e.receive(mbID, mkMail("ref@client.example.org", "Re: x", "reply 2", map[string]string{"In-Reply-To": thread.MessageID(ev.ID, "ref@client.example.org", "help.example.test")}))
		if st, _, tid := e.msgStatus(id); st != "processed" || tid != tk {
			t.Fatalf("event id: %s %s", st, tid)
		}
		if c, r := e.sink.counts(); c != 0 || r != 2 {
			t.Fatalf("creates=%d replies=%d", c, r)
		}
		// a foreign host never matches
		id = e.receive(mbID, mkMail("ref@client.example.org", "Re: y", "x", map[string]string{"References": thread.TicketThreadID(tk, "evil.example.org")}))
		if c, _ := e.sink.counts(); c != 1 {
			t.Fatalf("foreign reference should create a new ticket")
		}
		_ = id
	})
}

func TestThreadByPlusToken(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		cust := customer(t, db, "plus@client.example.org", "")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Plus"})
		other := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Other"})
		mbID, _ := e.webhookMailbox("", "")
		tok, err := thread.PlusToken(e.box, tk, "plus@client.example.org")
		if err != nil {
			t.Fatal(err)
		}
		id := e.receive(mbID, mkMail("plus@client.example.org", "Re: no number", "via token", map[string]string{"To": thread.PlusAddress("support@example.test", tok)}))
		if st, _, tid := e.msgStatus(id); st != "processed" || tid != tk {
			t.Fatalf("token: %s %s", st, tid)
		}
		// tampered token: flip one char → falls through to a new ticket, never another ticket
		bad := []byte(tok)
		bad[5] ^= 1
		if bad[5] < 'a' || bad[5] > 'z' {
			bad[5] = 'a'
			if tok[5] == 'a' {
				bad[5] = 'b'
			}
		}
		id = e.receive(mbID, mkMail("plus@client.example.org", "Re: tampered", "x", map[string]string{"To": thread.PlusAddress("support@example.test", string(bad))}))
		st, _, tid := e.msgStatus(id)
		if st != "processed" || tid == tk || tid == other {
			t.Fatalf("tampered token matched a ticket: %s %s", st, tid)
		}
		if c, _ := e.sink.counts(); c != 1 {
			t.Fatalf("tampered token should create a new ticket")
		}
	})
}

func TestThreadBySubjectRequiresKnownSender(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		cust := customer(t, db, "req@client.example.org", "")
		stranger := customer(t, db, "stranger@client.example.org", "")
		watcher := customer(t, db, "watch@client.example.org", "")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		exec(t, db, `INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, tk, watcher, store.Now())
		n := ticketNumber(t, db, tk)
		mbID, _ := e.webhookMailbox("", "")
		subj := "Re: [#" + itoa(n) + "] Printer"
		e.receive(mbID, mkMail("stranger@client.example.org", subj, "hijack", nil))
		if c, r := e.sink.counts(); c != 1 || r != 0 {
			t.Fatalf("stranger: creates=%d replies=%d", c, r)
		}
		e.receive(mbID, mkMail("req@client.example.org", subj, "mine", nil))
		e.receive(mbID, mkMail("watch@client.example.org", subj, "cc", nil))
		if c, r := e.sink.counts(); c != 1 || r != 2 {
			t.Fatalf("requester/watcher: creates=%d replies=%d", c, r)
		}
		_ = stranger
	})
}

func itoa(n int64) string   { return strconv.FormatInt(n, 10) }
func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

// TestReplyToClosedCreatesFollowup: mailin routes a reply to a closed ticket
// through ReplyFromEmail; the sink (tickets module) applies the follow-up
// rule and returns the follow-up ticket, which mailin records.
func TestReplyToClosedCreatesFollowup(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		cust := customer(t, db, "closed@client.example.org", "")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Status: domain.StatusClosed})
		fu := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		e.sink.followup[tk] = fu
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("closed@client.example.org", "Re: done", "still broken", map[string]string{"References": thread.TicketThreadID(tk, "help.example.test")}))
		if _, r := e.sink.counts(); r != 1 || e.sink.replies[0].TicketID != tk {
			t.Fatalf("ReplyFromEmail not called for the closed ticket")
		}
		if _, _, tid := e.msgStatus(id); tid != fu {
			t.Fatalf("recorded ticket %s, want follow-up %s", tid, fu)
		}
	})
}

func TestUnknownSenderSelfRegOff_Quarantined(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		setSelfReg(t, e.set, false)
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("new@unknown.example.org", "Hello", "x", nil))
		if st, reason, _ := e.msgStatus(id); st != "quarantined" || reason != "unknown_sender" {
			t.Fatalf("%s/%s", st, reason)
		}
		var n int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM users WHERE email = 'new@unknown.example.org'`).Scan(&n)
		if n != 0 {
			t.Fatal("user created with self-registration off")
		}
	})
}

func TestUnknownSenderSelfRegOn_PendingCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		setSelfReg(t, e.set, true)
		org := storetest.CreateOrg(t, db, "Acme")
		exec(t, db, `UPDATE organizations SET email_domains = ? WHERE id = ?`, `["acme.example.org"]`, org)
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("Carol <Carol@Acme.example.org>", "Hello", "x", nil))
		if st, reason, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("%s/%s", st, reason)
		}
		var status, gotOrg, kind, name string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT status, COALESCE(organization_id,''), kind, name FROM users WHERE email = 'carol@acme.example.org'`).Scan(&status, &gotOrg, &kind, &name); err != nil {
			t.Fatal(err)
		}
		if status != "pending_verification" || gotOrg != org || kind != "customer" || name != "Carol" {
			t.Fatalf("user %s %s %s %s", status, gotOrg, kind, name)
		}
	})
}

func TestStaffReplyNeedsAuthResults(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, map[string]string{"FD_INBOUND_TRUSTED_AUTHSERV_IDS": "mx.example.test"})
		e.m.trustedAuthserv = []string{"mx.example.test"}
		cust := customer(t, db, "c@client.example.org", "")
		agent := staff(t, db, "agent@acme.example.org")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mbID, _ := e.webhookMailbox("", "")
		ref := map[string]string{"References": thread.TicketThreadID(tk, "help.example.test")}
		with := func(ar string) map[string]string {
			h := map[string]string{"References": ref["References"]}
			if ar != "" {
				h["Authentication-Results"] = ar
			}
			return h
		}
		cases := []struct {
			name, ar, want string
		}{
			{"missing", "", "staff_spoof"},
			{"fail", "mx.example.test; dkim=fail header.d=acme.example.org; spf=fail smtp.mailfrom=agent@acme.example.org", "staff_spoof"},
			{"untrusted authserv", "evil.example.org; dkim=pass header.d=acme.example.org", "staff_spoof"},
			{"unaligned", "mx.example.test; dkim=pass header.d=other.example.org", "staff_spoof"},
			{"dkim pass", "mx.example.test; dkim=pass (good) header.d=acme.example.org header.s=s1", ""},
			{"spf pass", "mx.example.test 1; spf=pass smtp.mailfrom=agent@acme.example.org", ""},
		}
		for _, c := range cases {
			id := e.receive(mbID, mkMail("agent@acme.example.org", "Re: x", "answer "+c.name, with(c.ar)))
			st, reason, _ := e.msgStatus(id)
			if c.want != "" && (st != "quarantined" || reason != c.want) {
				t.Errorf("%s: %s/%s", c.name, st, reason)
			}
			if c.want == "" && st != "processed" {
				t.Errorf("%s: %s/%s", c.name, st, reason)
			}
		}
		if _, r := e.sink.counts(); r != 2 {
			t.Fatalf("replies = %d", r)
		}
		for _, rp := range e.sink.replies {
			if rp.AuthorID != agent || rp.Internal {
				t.Fatalf("bad reply %+v", rp)
			}
		}
		// staff never opens tickets
		id := e.receive(mbID, mkMail("agent@acme.example.org", "New thing", "x", map[string]string{"Authentication-Results": "mx.example.test; dkim=pass header.d=acme.example.org"}))
		if st, reason, _ := e.msgStatus(id); st != "quarantined" || reason != "staff_new_ticket" {
			t.Fatalf("staff new ticket: %s/%s", st, reason)
		}
	})
}

func TestStaffInternalPrefix(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.m.trustedAuthserv = []string{"mx.example.test"}
		cust := customer(t, db, "c2@client.example.org", "")
		staff(t, db, "agent2@acme.example.org")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mbID, _ := e.webhookMailbox("", "")
		e.receive(mbID, mkMail("agent2@acme.example.org", "Re: x", "#internal\nCANARY-INTERNAL secret note", map[string]string{
			"References": thread.TicketThreadID(tk, "help.example.test"), "Authentication-Results": "mx.example.test; dkim=pass header.d=acme.example.org"}))
		if _, r := e.sink.counts(); r != 1 {
			t.Fatal("no reply")
		}
		rp := e.sink.replies[0]
		if !rp.Internal || strings.Contains(rp.BodyMD, "#internal") || !strings.Contains(rp.BodyMD, "CANARY-INTERNAL") {
			t.Fatalf("internal note not detected: %+v", rp)
		}
	})
}

func TestBlocklistedSenderIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "spam@bad.example.org", "")
		customer(t, db, "ok@good.example.org", "")
		em, _ := e.set.Email(context.Background())
		em.InboundBlocklist = []string{"@bad.example.org", "someone@good.example.org"}
		if err := e.set.PutEmail(context.Background(), em.Email, ""); err != nil {
			t.Fatal(err)
		}
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("spam@bad.example.org", "Buy", "x", nil))
		if st, reason, _ := e.msgStatus(id); st != "ignored" || reason != "blocked" {
			t.Fatalf("%s/%s", st, reason)
		}
		id = e.receive(mbID, mkMail("ok@good.example.org", "Hi", "x", nil))
		if st, _, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("good sender %s", st)
		}
	})
}

func TestLoopHeaders(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "loop@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		cases := map[string][2]string{
			"auto_submitted":   {"Auto-Submitted", "auto-replied"},
			"precedence":       {"Precedence", "bulk"},
			"x_autoreply":      {"X-Autoreply", "yes"},
			"list_id":          {"List-Id", "<list.example.org>"},
			"null_return_path": {"Return-Path", "<>"},
		}
		for reason, h := range cases {
			id := e.receive(mbID, mkMail("loop@client.example.org", "Out of office", "away", map[string]string{h[0]: h[1]}))
			if st, got, _ := e.msgStatus(id); st != "ignored" || got != reason {
				t.Errorf("%s: %s/%s", reason, st, got)
			}
		}
		if c, r := e.sink.counts(); c+r != 0 {
			t.Fatalf("loop mail reached the sink: %d/%d", c, r)
		}
		// Auto-Submitted: no is fine
		id := e.receive(mbID, mkMail("loop@client.example.org", "Real", "x", map[string]string{"Auto-Submitted": "no"}))
		if st, _, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("Auto-Submitted: no → %s", st)
		}
	})
}

func TestOwnMessageIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		em, _ := e.set.Email(context.Background())
		em.CustomerFrom = settings.Identity{Name: "Support", Address: "support@acme.example.org"}
		_ = e.set.PutEmail(context.Background(), em.Email, "")
		customer(t, db, "support@acme.example.org", "")
		customer(t, db, "x@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		id := e.receive(mbID, mkMail("support@acme.example.org", "Ticket received", "x", nil))
		if st, reason, _ := e.msgStatus(id); st != "ignored" || reason != "own_message" {
			t.Fatalf("own From: %s/%s", st, reason)
		}
		id = e.receive(mbID, mkMail("x@client.example.org", "bounce", "x", map[string]string{"Message-ID": "<" + store.NewID() + ".abcdefabcdef@help.example.test>"}))
		if st, reason, _ := e.msgStatus(id); st != "ignored" || reason != "own_message" {
			t.Fatalf("own Message-ID: %s/%s", st, reason)
		}
	})
}

func TestSenderRateLimit(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "burst@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		var last string
		for i := range 21 {
			last = e.receive(mbID, mkMail("burst@client.example.org", "n"+fmtInt(int64(i)), "x", nil))
		}
		if st, reason, _ := e.msgStatus(last); st != "quarantined" || reason != "rate_limited" {
			t.Fatalf("21st: %s/%s", st, reason)
		}
		if c, _ := e.sink.counts(); c != 20 {
			t.Fatalf("created %d, want 20", c)
		}
		// an hour later it is allowed again
		e.m.now = func() time.Time { return time.Now().Add(61 * time.Minute) }
		id := e.receive(mbID, mkMail("burst@client.example.org", "later", "x", nil))
		if st, _, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("after window: %s", st)
		}
	})
}

func TestNoSinkFailsRetryable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "ns@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		e.m.SetTicketSink(nil)
		id := e.receive(mbID, mkMail("ns@client.example.org", "x", "x", nil))
		if st, reason, _ := e.msgStatus(id); st != "failed" || reason != "no_sink" {
			t.Fatalf("%s/%s", st, reason)
		}
		e.m.SetTicketSink(e.sink)
		e.sink.err = errors.New("db down")
		if err := e.m.Process(context.Background(), id, true); err == nil {
			t.Fatal("sink error must be retryable")
		}
		if st, reason, _ := e.msgStatus(id); st != "failed" || !strings.HasPrefix(reason, reasonRetrying) {
			t.Fatalf("%s/%s", st, reason)
		}
		e.sink.err = nil
		if err := e.m.Process(context.Background(), id, false); err != nil { // queue retry
			t.Fatal(err)
		}
		if st, _, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("after retry: %s", st)
		}
	})
}

// TestStaffAuthCheckPluggable: modules.go plugs authn.ResolveEmailSender in
// through SetStaffAuthCheck; its verdict replaces the built-in check.
func TestStaffAuthCheckPluggable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		e.m.trustedAuthserv = []string{"mx.example.test"}
		cust := customer(t, db, "c2@client.example.org", "")
		staff(t, db, "agent2@acme.example.org")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mbID, _ := e.webhookMailbox("", "")
		var gotFrom, gotAR, gotID string
		allow := false
		e.m.SetStaffAuthCheck(func(_ context.Context, from, ar, id string) (bool, error) {
			gotFrom, gotAR, gotID = from, ar, id
			return allow, nil
		})
		h := map[string]string{"References": thread.TicketThreadID(tk, "help.example.test"),
			"Authentication-Results": "mx.example.test; dkim=pass header.d=acme.example.org"}
		id := e.receive(mbID, mkMail("agent2@acme.example.org", "Re: x", "first", h))
		if st, reason, _ := e.msgStatus(id); st != "quarantined" || reason != "staff_spoof" {
			t.Fatalf("plugged check said no: got %s/%s", st, reason)
		}
		if gotFrom != "agent2@acme.example.org" || gotID != "mx.example.test" || !strings.Contains(gotAR, "dkim=pass") {
			t.Fatalf("check args: %q %q %q", gotFrom, gotAR, gotID)
		}
		allow = true
		id = e.receive(mbID, mkMail("agent2@acme.example.org", "Re: x", "second", h))
		if st, reason, _ := e.msgStatus(id); st != "processed" {
			t.Fatalf("plugged check said yes: got %s/%s", st, reason)
		}
	})
}
