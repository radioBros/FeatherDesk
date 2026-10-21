// SPDX-License-Identifier: AGPL-3.0-only

package tickets

// Validation tests named by the database plan (DB-011, DB-013, DB-032). They
// go through the real router and the real list builder on both engines.

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestSearchEventuallyConsistent (DB-013): a new message is searchable within
// 500 ms of its commit through the real asynchronous index consumer.
func TestSearchEventuallyConsistent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.startQueue()
		ctx := context.Background()
		// Warm-up: the first request builds the harness's OpenAPI validator
		// (seconds under -race), which is not indexing latency.
		search(t, l.e, l.sup, "warmup")
		for i, word := range []string{"heliotropium", "quokkafication", "zymurgyfault"} {
			kind := "public"
			if i == 1 {
				kind = "internal"
			}
			if _, err := l.e.svc.AddStaffMessage(ctx, l.sup, l.ticketID, MessageInput{Kind: kind, BodyMD: "the " + word + " again"}); err != nil {
				t.Fatal(err)
			}
			committed := time.Now()
			for {
				if ids := search(t, l.e, l.sup, word); slices.Equal(ids, []string{l.ticketID}) {
					break
				}
				if time.Since(committed) > 500*time.Millisecond {
					t.Fatalf("%q not searchable 500 ms after commit", word)
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Logf("%s message searchable %s after commit", kind, time.Since(committed).Round(time.Millisecond))
		}
	})
}

// TestCursorStable (DB-011): paging 1 000 pre-existing tickets while other
// tickets are inserted concurrently returns every pre-existing row exactly
// once (no duplicates, no gaps) for each keyset sort.
func TestCursorStable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		prios := []domain.Priority{"low", "normal", "high", "urgent"}
		base := store.Now()
		orig := make([]string, 0, 1000)
		for i := range 1000 {
			orig = append(orig, storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: prios[i%4],
				Status: domain.TicketStatuses[i%6], UpdatedAt: base.Add(-time.Duration(i%97) * time.Minute)}))
		}
		for _, sort := range []string{"attention", "-updated_at", "priority"} {
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { // concurrent writer
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID, TeamID: team, Priority: prios[i%4], Status: domain.TicketStatuses[i%6]})
					time.Sleep(2 * time.Millisecond)
				}
			}()
			seen := make(map[string]int, 1200)
			cursor, pages := "", 0
			for ; pages < 200; pages++ {
				path := "/api/v1/agent/tickets?limit=50&sort=" + url.QueryEscape(sort)
				if cursor != "" {
					path += "&cursor=" + url.QueryEscape(cursor)
				}
				ids, next := listIDs(t, e, sup, path)
				for _, id := range ids {
					seen[id]++
				}
				if next == nil {
					break
				}
				cursor = *next
			}
			close(stop)
			wg.Wait()
			for _, id := range orig {
				if seen[id] != 1 {
					t.Fatalf("sort %s: pre-existing ticket %s seen %d times", sort, id, seen[id])
				}
			}
			for id, n := range seen {
				if n != 1 {
					t.Fatalf("sort %s: ticket %s returned %d times", sort, id, n)
				}
			}
			t.Logf("sort %s: %d pages, %d rows (%d pre-existing, each exactly once)", sort, pages+1, len(seen), len(orig))
		}
	})
}

// TestListBuilderNoInterpolation (DB-011, DB-032): hostile filter values are
// bound parameters, never SQL. Every value either matches literally (proved by
// seeding rows whose team id, tag and custom-field value ARE the payload) or
// is rejected with 422; never a 500, and the tables survive.
func TestListBuilderNoInterpolation(t *testing.T) {
	payloads := []string{
		`'; DROP TABLE tickets; --`,
		`' OR '1'='1`,
		`") OR 1=1 --`,
		`x' UNION SELECT id FROM users --`,
		`$1`, `?`, `\'`, `%`, `_`, `*`,
		`1; DELETE FROM users`,
		`ʼ OR 1=1 --`,
	}
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		e := f.e
		users := e.count(`SELECT COUNT(*) FROM users`)
		for i, pl := range payloads {
			// A ticket whose team id, tag and order_id value are the payload.
			e.exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, pl, "team "+pl, store.Now(), store.Now())
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.cust.UserID, TeamID: pl})
			tag := store.NewID()
			e.exec(`INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)`, tag, strings.ToLower(pl)+" tag", store.Now())
			e.exec(`INSERT INTO ticket_tags (ticket_id, tag_id) VALUES (?, ?)`, id, tag)
			e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"custom_fields": map[string]any{"order_id": pl}}).expect(t, 200)

			for _, c := range []struct {
				param, value string
			}{
				{"team_id", pl},
				{"tag", pl + " tag"},
				{"cf.order_id", pl},
			} {
				ids, _ := listIDs(t, e, f.sup, "/api/v1/agent/tickets?"+c.param+"="+url.QueryEscape(c.value))
				if !slices.Equal(ids, []string{id}) {
					t.Fatalf("payload %d %s=%q: got %v, want the literal match %s", i, c.param, c.value, ids, id)
				}
			}
			for _, param := range []string{"team_id", "assignee", "category_id", "organization_id", "requester_id", "tag", "q",
				"status", "priority", "tier", "updated_since", "sort", "breached", "cf.order_id", "cf.region", "cf.qty", "cf.due", "cf.root_cause"} {
				r := e.do(f.sup, "GET", "/api/v1/agent/tickets?"+param+"="+url.QueryEscape(pl+"x"), nil)
				if r.StatusCode != 200 && r.StatusCode != 422 {
					t.Fatalf("payload %d %s: status %d %s", i, param, r.StatusCode, r.body)
				}
				var page struct {
					Items []struct{ ID string } `json:"items"`
				}
				if r.StatusCode == 200 {
					r.json(t, &page)
					if param != "q" && param != "assignee" && len(page.Items) != 0 {
						t.Fatalf("payload %d %s: non-literal value matched %d tickets", i, param, len(page.Items))
					}
				}
			}
			for _, param := range []string{"status", "q", "mine_only"} {
				r := e.do(f.cust, "GET", "/api/v1/portal/tickets?"+param+"="+url.QueryEscape(pl), nil)
				if r.StatusCode != 200 && r.StatusCode != 422 {
					t.Fatalf("portal payload %d %s: status %d", i, param, r.StatusCode)
				}
			}
		}
		if n := e.count(`SELECT COUNT(*) FROM users`); n != users {
			t.Fatalf("users %d → %d", users, n)
		}
		if n := e.count(`SELECT COUNT(*) FROM tickets`); n != len(payloads)+1 {
			t.Fatalf("tickets: %d rows, want %d", n, len(payloads)+1)
		}
	})
}

// TestCustomerQueryNeverSelectsStaffFields (DB-032, I-9): no customer path —
// portal detail, portal list, portal search, portal field edit, a cf filter —
// ever returns or matches a staff-only custom field. The values are filtered
// through authz.CanSeeField in Go after one ticket_field_values query; the
// test pins the observable guarantee on both engines.
func TestCustomerQueryNeverSelectsStaffFields(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": map[string]any{
			"root_cause": "STAFFONLYCAUSE", "region": "eu", "vip": true, "due": "2026-12-31", "qty": 7,
		}}).expect(t, 200)
		f.e.index(f.ticketID)
		staffOnly := []string{"root_cause", "STAFFONLYCAUSE", `"region"`, `"vip"`, `"due"`, "2026-12-31"}
		for _, p := range []string{
			"/api/v1/portal/tickets/" + f.ticketID,
			"/api/v1/portal/tickets",
			"/api/v1/portal/tickets?q=printer",
		} {
			body := string(f.e.do(f.cust, "GET", p, nil).expect(t, 200).body)
			for _, s := range staffOnly {
				if strings.Contains(body, s) {
					t.Fatalf("%s leaks %q (I-9): %s", p, s, body)
				}
			}
			if !strings.Contains(body, `"qty":7`) {
				t.Fatalf("%s lacks the customer-visible field: %s", p, body)
			}
		}
		if ids := search(t, f.e, f.cust, "STAFFONLYCAUSE"); len(ids) != 0 {
			t.Fatalf("customer search matched a staff-only value: %v", ids)
		}
		// Customers cannot filter on custom fields at all.
		f.e.do(f.cust, "GET", "/api/v1/portal/tickets?cf.root_cause=STAFFONLYCAUSE", nil).expect(t, 422)
		// A staff-only key looks unknown on the customer edit path.
		f.e.do(f.cust, "PATCH", "/api/v1/portal/tickets/"+f.ticketID+"/fields", map[string]any{"custom_fields": map[string]any{"root_cause": "x"}}).expect(t, 422)
	})
}
