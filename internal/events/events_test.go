// SPDX-License-Identifier: AGPL-3.0-only

package events_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func TestWriteReadAfter(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		tk := ticket(t, db)
		var ids []string
		for i := 0; i < 3; i++ {
			err := db.Tx(ctx, func(tx *store.Tx) error {
				e := &events.Event{TicketID: tk, Type: events.TicketStatusChanged, Snapshot: events.Snapshot{Number: 1000, Status: "open", Tier: 2, TeamID: "team"}}
				if err := events.Write(ctx, tx, e, events.StatusChange{From: "new", To: "open"}); err != nil {
					return err
				}
				ids = append(ids, e.ID)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := events.ReadAfter(ctx, db.Read(), "", 10)
		if err != nil || len(got) != 3 {
			t.Fatalf("read: %v %d", err, len(got))
		}
		if got[0].ID != ids[0] || got[0].Snapshot.Tier != 2 || got[0].Snapshot.TeamID != "team" {
			t.Fatalf("event mismatch: %+v", got[0])
		}
		var sc events.StatusChange
		if err := got[0].Decode(&sc); err != nil || sc.To != "open" {
			t.Fatalf("decode %v %+v", err, sc)
		}
		rest, _ := events.ReadAfter(ctx, db.Read(), ids[0], 10)
		if len(rest) != 2 || rest[0].ID != ids[1] {
			t.Fatalf("cursor read %d", len(rest))
		}
	})
}

// IDs must be strictly increasing in commit order even with concurrent writers.
func TestIDsMonotonicUnderConcurrency(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		tk := ticket(t, db)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 10; i++ {
					_ = db.Tx(ctx, func(tx *store.Tx) error {
						return events.Write(ctx, tx, &events.Event{TicketID: tk, Type: events.NoteCreated}, nil)
					})
				}
			}()
		}
		wg.Wait()
		got, _ := events.ReadAfter(ctx, db.Read(), "", 100)
		if len(got) != 40 {
			t.Fatalf("want 40 got %d", len(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i].ID <= got[i-1].ID || got[i].CreatedAt.Before(got[i-1].CreatedAt) {
				t.Fatalf("not monotonic at %d", i)
			}
		}
	})
}

// specEvents is events_spec §1 as data: every event type and its
// "Customer-visible?" column ("only customer-visible keys" counts as yes: the
// publisher filters the payload, I-9). The last four are realtime/audit
// types the code adds to the table: subject edits are customer-visible like
// the ticket itself; edits, watcher changes and deletes are staff-only.
var specEvents = map[string]bool{
	"ticket.created":            true,
	"ticket.status_changed":     true, // mapped labels
	"ticket.priority_changed":   false,
	"ticket.assigned":           false,
	"ticket.team_changed":       false,
	"ticket.escalated":          false,
	"ticket.deescalated":        false,
	"ticket.category_changed":   false,
	"ticket.tags_changed":       false,
	"message.created":           true,
	"note.created":              false, // never (I-7)
	"sla.warning":               false,
	"sla.breached":              false,
	"ticket.csat":               false,
	"ticket.followup_created":   true,
	"ticket.merged":             true, // "merged into #n"
	"ticket.split":              false,
	"ticket.linked":             false,
	"ticket.unlinked":           false,
	"ticket.children_resolved":  false,
	"ticket.fields_changed":     true,
	"ticket.subject_changed":    true,
	"message.edited":            false,
	"ticket.watchers_changed":   false,
	"ticket.deleted":            false,
	"ticket.automation_applied": false,
}

// TestEventsCustomerVisibility (CON-009, I-7): every event type has a
// constant, and its customer visibility (the WebSocket / email / webhook
// audience tag) equals the spec column; note.created is never customer-visible.
func TestEventsCustomerVisibility(t *testing.T) {
	if events.CustomerVisible(events.NoteCreated) {
		t.Fatal("note.created must never be customer-visible (I-7)")
	}
	all := map[string]bool{}
	for _, typ := range events.All {
		if all[typ] {
			t.Errorf("duplicate type %s in events.All", typ)
		}
		all[typ] = true
		want, ok := specEvents[typ]
		if !ok {
			t.Errorf("%s is not in events_spec §1 (add it to the spec and to specEvents)", typ)
			continue
		}
		if got := events.CustomerVisible(typ); got != want {
			t.Errorf("%s: CustomerVisible=%v, spec says %v", typ, got, want)
		}
	}
	for typ := range specEvents {
		if !all[typ] {
			t.Errorf("spec event %s has no constant in events.All", typ)
		}
	}
	if events.CustomerVisible(events.TicketAccessLost) || events.CustomerVisible("bogus.event") {
		t.Error("unknown and realtime-only types must not be customer-visible")
	}
}

// emittedData is one data document per type, with exactly the keys the
// emitting code writes (internal/tickets emit/add call sites).
var emittedData = map[string]string{
	"ticket.created":            `{"channel":"portal","priority":"normal","team":"t","category":"c","message_id":"m","inbound_message_id":"i"}`,
	"ticket.status_changed":     `{"from":"new","to":"open"}`,
	"ticket.priority_changed":   `{"from":"low","to":"high"}`,
	"ticket.assigned":           `{"from":"u1","to":"u2"}`,
	"ticket.team_changed":       `{"from":null,"to":"t"}`,
	"ticket.escalated":          `{"from_tier":1,"to_tier":2,"from_team":"t","to_team":null,"reason":"r","unassigned":"u"}`,
	"ticket.deescalated":        `{"from_tier":2,"to_tier":1,"from_team":null,"to_team":"t","reason":"r"}`,
	"ticket.category_changed":   `{"from":null,"to":"c"}`,
	"ticket.tags_changed":       `{"from":["a"],"to":["a","b"]}`,
	"ticket.fields_changed":     `{"keys":["k"]}`,
	"ticket.subject_changed":    `{"from":"a","to":"b"}`,
	"message.created":           `{"message_id":"m","kind":"public","channel":"email","inbound_message_id":"i"}`,
	"note.created":              `{"message_id":"m","kind":"internal","mentioned":["u"]}`,
	"message.edited":            `{"message_id":"m","kind":"internal"}`,
	"sla.warning":               `{"which":"first_response","due_at":"2026-10-21T10:00:00Z"}`,
	"sla.breached":              `{"which":"resolution","due_at":"2026-10-21T10:00:00Z"}`,
	"ticket.csat":               `{"score":5}`,
	"ticket.followup_created":   `{"new_ticket_id":"n","original_ticket_id":"o"}`,
	"ticket.merged":             `{"source_id":"a","target_id":"b"}`,
	"ticket.split":              `{"parent_id":"p","child_id":"c","message_ids":["m"]}`,
	"ticket.linked":             `{"to_ticket_id":"x","type":"related"}`,
	"ticket.unlinked":           `{"to_ticket_id":"x","type":"parent_of"}`,
	"ticket.children_resolved":  `{"parent_id":"p","child_id":"c"}`,
	"ticket.watchers_changed":   `{"added":["u1","u2"]}`,
	"ticket.deleted":            `{}`,
	"ticket.automation_applied": `{"rule_id":"r","rule_name":"n","trigger":"ticket.created","source_event_id":"e","actions":[{"type":"add_tag","outcome":"applied","detail":"d"}],"truncated":true}`,
}

// TestEventDataStructs (CON-009): every type has a data struct that decodes
// the document its emitter writes without dropping a key.
func TestEventDataStructs(t *testing.T) {
	for _, typ := range events.All {
		doc, ok := emittedData[typ]
		if !ok {
			t.Errorf("%s: no sample data document", typ)
			continue
		}
		v := events.NewData(typ)
		if v == nil {
			t.Errorf("%s: no data struct", typ)
			continue
		}
		dec := json.NewDecoder(strings.NewReader(doc))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
		if err := (events.Event{Type: typ, Data: json.RawMessage(doc)}).Decode(events.NewData(typ)); err != nil {
			t.Errorf("%s: Event.Decode: %v", typ, err)
		}
	}
	if events.NewData("bogus") != nil {
		t.Error("unknown type has a struct")
	}
}

func ticket(t *testing.T, db *store.DB) string {
	return storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, "")})
}
