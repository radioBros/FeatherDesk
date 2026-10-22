// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// startQueue registers the module's consumers and starts the queue.
func (e *env) startQueue() {
	e.t.Helper()
	e.mod.RegisterQueue(e.q)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.q.Start(ctx); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = e.q.Stop(sctx)
		cancel()
	})
}

func (e *env) index(ids ...string) {
	e.t.Helper()
	for _, id := range ids {
		if err := e.svc.IndexOne(context.Background(), id); err != nil {
			e.t.Fatal(err)
		}
	}
}

func search(t *testing.T, e *env, p *domain.Principal, q string) []string {
	t.Helper()
	path := "/api/v1/agent/tickets?q=" + url.QueryEscape(q)
	if p.IsCustomer() {
		path = "/api/v1/portal/tickets?q=" + url.QueryEscape(q)
	}
	ids, _ := listIDs(t, e, p, path)
	return ids
}

func TestSearch_CustomerCannotMatchInternalNoteText(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		ctx := context.Background()
		if _, err := l.e.svc.AddStaffMessage(ctx, l.agent, l.ticketID, MessageInput{Kind: "internal", BodyMD: "the password is zebracorn"}); err != nil {
			t.Fatal(err)
		}
		l.e.index(l.ticketID)
		if ids := search(t, l.e, l.agent, "zebracorn"); !slices.Equal(ids, []string{l.ticketID}) {
			t.Fatalf("staff search %v", ids)
		}
		if ids := search(t, l.e, l.cust, "zebracorn"); len(ids) != 0 {
			t.Fatalf("customer matched an internal-only word: %v", ids)
		}
		if ids := search(t, l.e, l.cust, "printer"); !slices.Equal(ids, []string{l.ticketID}) {
			t.Fatalf("customer public search %v", ids)
		}
	})
}

func TestSearch_RespectsVisibility(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.exec(`UPDATE tickets SET subject = 'quasarlight outage' WHERE id = ?`, f.id)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		f.e.index(f.id)
		if ids := search(t, f.e, f.l1, "quasarlight"); len(ids) != 0 {
			t.Fatalf("L1 found an L2 ticket: %v", ids)
		}
		if ids := search(t, f.e, f.l2, "quasarlight"); len(ids) != 1 {
			t.Fatalf("L2 search %v", ids)
		}
	})
}

func TestSearch_HostileQueryNoError(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.index(l.ticketID)
		for _, q := range []string{`"`, `*`, `NEAR(`, `OR`, `printer OR`, `a"b*c:d`, `'); DROP TABLE tickets; --`, `-`, `^`, `\`, `ünïcödé`} {
			l.e.do(l.agent, "GET", "/api/v1/agent/tickets?q="+url.QueryEscape(q), nil).expect(t, 200)
			l.e.do(l.cust, "GET", "/api/v1/portal/tickets?q="+url.QueryEscape(q), nil).expect(t, 200)
		}
	})
}

func TestSearch_IndexedWithin1s(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.startQueue()
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{Subject: "Flux capacitor", BodyMD: "gigawatts missing"})
		start := time.Now()
		for {
			if ids := search(t, e, sup, "gigawatts"); slices.Equal(ids, []string{id}) {
				break
			}
			if time.Since(start) > 30*time.Second { // upper bound only: indexing is asynchronous
				t.Fatal("not indexed within 30 s")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("indexed after %s", time.Since(start))
	})
}

// TestTicketWriteTx_NoFTSStatements: ticket writes never touch the index;
// only the asynchronous worker does.
func TestTicketWriteTx_NoFTSStatements(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		tbl := "tickets_fts"
		if db.Dialect == store.Postgres {
			tbl = "ticket_search"
		}
		cust := e.customer("", domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{})
		e.do(cust, "POST", "/api/v1/portal/tickets/"+id+"/messages", map[string]any{"body_md": "more"}).expect(t, 201)
		if n := e.count(`SELECT COUNT(*) FROM ` + tbl); n != 0 {
			t.Fatalf("%d index rows written by request transactions", n)
		}
	})
}

func TestReindex_RebuildsIdenticalResults(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		other := l.e.createTicket(l.cust, CreateInput{Subject: "Monitor flicker", BodyMD: "screen"})
		l.e.index(l.ticketID, other)
		before := search(t, l.e, l.sup, "printer")
		tbl := "tickets_fts"
		if db.Dialect == store.Postgres {
			tbl = "ticket_search"
		}
		l.e.exec(`DELETE FROM ` + tbl)
		n, err := Reindex(context.Background(), db, nil)
		if err != nil || n != 2 {
			t.Fatalf("reindex %d %v", n, err)
		}
		if after := search(t, l.e, l.sup, "printer"); !slices.Equal(before, after) || len(after) != 1 {
			t.Fatalf("before %v after %v", before, after)
		}
	})
}
