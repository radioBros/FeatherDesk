// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestReplayAfterCrash (NOT-005): ticket mutations commit while the
// dispatcher never runs (process "crashes" after commit). The next start
// replays the event log from the persisted cursor and every recipient gets
// exactly one mail per Message-ID; a third start sends nothing more.
func TestReplayAfterCrash(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		agentA := storetest.CreateStaff(t, db, domain.RoleAgent, 1, team)
		agentB := storetest.CreateStaff(t, db, domain.RoleAgent, 1, team)
		custID := storetest.CreateCustomer(t, db, "")
		vars := e2eEnv(t, db, sink, t.TempDir())

		// Instance A: commits, never dispatches, then goes away.
		a := buildStack(t, sink, vars)
		cust := a.loginFixture(custID, domain.KindCustomer)
		agent := a.loginFixture(agentA, domain.KindStaff)
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Replay me", "body_md": "Lost on crash?", "category_id": category}, "Idempotency-Key", "replay-1")
		id := tk["id"].(string)
		agent.must(200, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"assignee_id": agentA})
		agent.must(201, "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "public", "body_md": "Replayed reply"}, "Idempotency-Key", "replay-2")
		a.close()
		if n := len(sink.Messages()); n != 0 {
			t.Fatalf("instance A sent %d mails without a running dispatcher", n)
		}

		emails := map[string]string{}
		for _, u := range []string{agentA, agentB, custID} {
			var e string
			if err := db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, u).Scan(&e); err != nil {
				t.Fatal(err)
			}
			emails[u] = e
		}

		// Instance B replays from the cursor.
		b := buildStack(t, sink, vars)
		stopB := b.run()
		b.waitMail(emails[custID], 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Text, "Replayed reply") })
		b.waitMail(emails[agentB], 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "Replay me") })
		time.Sleep(500 * time.Millisecond) // several dispatcher rounds: duplicates would show up
		stopB()

		// Instance C: the persisted cursor is past every event.
		c := buildStack(t, sink, vars)
		stopC := c.run()
		time.Sleep(500 * time.Millisecond)
		stopC()

		seen := map[string]int{}
		perRcpt := map[string]int{}
		for _, m := range sink.Messages() {
			d := decodeMail(t, m)
			for _, to := range d.To {
				seen[d.MessageID+" "+strings.ToLower(strings.Trim(to, "<>"))]++
				perRcpt[strings.ToLower(strings.Trim(to, "<>"))]++
			}
		}
		for k, n := range seen {
			if n != 1 {
				t.Errorf("%s delivered %d times", k, n)
			}
		}
		// customer: customer.reply; agentB: staff.ticket_new; agentA: staff.ticket_new
		// (the assignment and the reply are agentA's own actions: actor exclusion).
		want := map[string]int{emails[custID]: 1, emails[agentB]: 1, emails[agentA]: 1}
		var got []string
		for r, n := range perRcpt {
			got = append(got, r+"="+strconv.Itoa(n))
		}
		sort.Strings(got)
		for r, n := range want {
			if perRcpt[strings.ToLower(r)] != n {
				t.Errorf("recipient %s: want %d mails, got %d (all: %v)", r, n, perRcpt[strings.ToLower(r)], got)
			}
		}
	})
}

// tableCounts returns the row count of every table of the schema.
func tableCounts(t *testing.T, db *store.DB) map[string]int {
	t.Helper()
	ctx := context.Background()
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	if db.Dialect == store.Postgres {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	rows, err := db.Read().QueryContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM `+n).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", n, err)
		}
		out[n] = c
	}
	return out
}

// TestTicketTxHasNoNotifyWrites (NOT-005, D-11): creating a ticket writes
// only the ticket's own rows plus exactly one ticket_events row. Nothing
// notification-related (deferred_jobs, mails, webhook deliveries) happens in
// or because of the ticket transaction; fan-out happens after commit from
// the event log, which this test keeps stopped.
func TestTicketTxHasNoNotifyWrites(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Printers", team)
		storetest.CreateStaff(t, db, domain.RoleAgent, 1, team)
		custID := storetest.CreateCustomer(t, db, "")
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		defer s.close()
		cust := s.loginFixture(custID, domain.KindCustomer)
		before := tableCounts(t, s.db)
		cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Tx shape", "body_md": "Only domain rows.", "category_id": category}, "Idempotency-Key", "tx-1")
		after := tableCounts(t, s.db)
		allowed := map[string]bool{"tickets": true, "ticket_messages": true, "ticket_events": true, "counters": true,
			"idempotency_keys": true, "ticket_watchers": true, "ticket_field_values": true}
		var changed []string
		for tbl, n := range after {
			if n != before[tbl] {
				changed = append(changed, tbl)
				if !allowed[tbl] {
					t.Errorf("ticket create wrote to %s (%d → %d)", tbl, before[tbl], n)
				}
			}
		}
		sort.Strings(changed)
		t.Logf("tables written by the create: %v", changed)
		if d := after["ticket_events"] - before["ticket_events"]; d != 1 {
			t.Errorf("ticket_events grew by %d, want exactly 1", d)
		}
		for _, tbl := range []string{"deferred_jobs", "webhook_deliveries"} {
			if after[tbl] != before[tbl] {
				t.Errorf("%s written in the ticket path", tbl)
			}
		}
		if n := len(sink.Messages()); n != 0 {
			t.Errorf("%d mails sent from the request path", n)
		}
	})
}
