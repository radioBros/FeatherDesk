// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"testing"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestThreadBySubject_BothFormats (D-20, PRJ-005): replies thread by the
// project reference "[KEY-123]" (any letter case) and by the legacy
// "[#number]"; a reference of another ticket's project never matches.
func TestThreadBySubject_BothFormats(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		cust := customer(t, db, "req@client.example.org", "")
		mob := storetest.CreateProject(t, db, "MOB")
		hd := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mt := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, ProjectID: mob})
		mbID, _ := e.webhookMailbox("", "")
		n := ticketNumber(t, db, hd)
		for i, c := range []struct{ subject, want string }{
			{"Re: [MOB-1] Printer", mt},
			{"RE: [mob-1] Printer", mt},
			{"Re: [HD-" + itoa(n) + "] Printer", hd},
			{"Re: [#" + itoa(n) + "] Printer", hd},
		} {
			e.receive(mbID, mkMail("req@client.example.org", c.subject, "reply", nil))
			if _, r := e.sink.counts(); r != i+1 || e.sink.replies[i].TicketID != c.want {
				t.Fatalf("%q: replies=%d", c.subject, r)
			}
		}
		// An unknown reference opens a new ticket instead.
		e.receive(mbID, mkMail("req@client.example.org", "Re: [MOB-9] Printer", "x", nil))
		if c, _ := e.sink.counts(); c != 1 {
			t.Fatalf("unknown ref must create a ticket, creates=%d", c)
		}
	})
}

// TestNewTicketGetsMailboxProject: the project of a mailbox reaches the sink.
func TestNewTicketGetsMailboxProject(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "new@client.example.org", "")
		mob := storetest.CreateProject(t, db, "MOB")
		mbID, _ := e.webhookMailbox("", "")
		exec(t, db, `UPDATE inbound_mailboxes SET project_id = ? WHERE id = ?`, mob, mbID)
		e.receive(mbID, mkMail("new@client.example.org", "Help", "x", nil))
		if c, _ := e.sink.counts(); c != 1 || e.sink.creates[0].ProjectID != mob {
			t.Fatalf("mailbox project not passed: %+v", e.sink.creates)
		}
	})
}
