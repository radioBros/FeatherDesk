// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestPerf100k (DB-023): 100 000 tickets / 500 000 messages with a built
// search index, measured through the real service layer:
//
//   - agent "attention" list, first page of 50, L1 visibility predicate
//   - portal list for an org_manager
//   - full-text search (staff, through the list builder) and portal search
//   - ticket detail load (ticket + messages + refs + allowed actions)
//
// Budgets: agent p95 < 50 ms, portal p95 < 30 ms, search p95 < 100 ms; the
// Postgres leg must be within 2x of the SQLite figure. Heavy (minutes), so it
// only runs with FD_PERF=1 and never under -race. The Postgres leg runs when
// FD_TEST_POSTGRES_URL is set (point it at a tuned small-host instance, see
// development/database/database_log.md). FD_PERF_TICKETS overrides the size.
func TestPerf100k(t *testing.T) {
	if os.Getenv("FD_PERF") != "1" {
		t.Skip("set FD_PERF=1 to run the 100k benchmark")
	}
	if raceEnabled {
		t.Skip("latency budgets are not meaningful under -race")
	}
	n := 100000
	if v := os.Getenv("FD_PERF_TICKETS"); v != "" {
		_, _ = fmt.Sscan(v, &n)
	}
	results := map[string]map[string]perfStat{}
	for _, d := range []store.Dialect{store.SQLite, store.Postgres} {
		t.Run(d.String(), func(t *testing.T) {
			if d == store.Postgres && storetest.PostgresURL() == "" {
				t.Skip("FD_TEST_POSTGRES_URL not set")
			}
			results[d.String()] = runPerf100k(t, storetest.Open(t, d), n)
		})
	}
	sq, pg := results["sqlite"], results["postgres"]
	if sq != nil && pg != nil {
		for _, k := range perfOrder {
			ratio := float64(pg[k].p95) / float64(sq[k].p95)
			t.Logf("RATIO %-24s postgres/sqlite p95 = %.2fx", k, ratio)
			// Sub-millisecond SQLite figures make the ratio noise; the 2x rule
			// applies to the budgeted queries above 1 ms.
			if ratio > 2 && pg[k].p95 > time.Millisecond && sq[k].p95 > time.Millisecond {
				t.Errorf("%s: postgres p95 %v is more than 2x sqlite %v", k, pg[k].p95, sq[k].p95)
			}
		}
	}
}

type perfStat struct{ p50, p95 time.Duration }

var perfOrder = []string{"agent_list", "portal_list", "detail"}

var perfBudget = map[string]time.Duration{
	"agent_list": 50 * time.Millisecond, "portal_list": 30 * time.Millisecond,
}

func measure(n int, fn func(i int)) perfStat {
	d := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		s := time.Now()
		fn(i)
		d[i] = time.Since(s)
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return perfStat{p50: d[n/2], p95: d[n*95/100]}
}

func runPerf100k(t *testing.T, db *store.DB, total int) map[string]perfStat {
	ctx := context.Background()
	e := newEnv(t, db)
	rng := rand.New(rand.NewPCG(1, 2))
	start := time.Now()

	const nTeams, nOrgs, custPerOrg = 6, 50, 10
	teams := make([]string, nTeams)
	for i := range teams {
		teams[i] = storetest.CreateTeam(t, db, "")
	}
	agent := e.staff(domain.RoleAgent, 1, teams[0], teams[1]) // L1 in two of six teams
	staffAuthor := storetest.CreateStaff(t, db, domain.RoleAgent, 3, teams...)
	orgs := make([]string, nOrgs)
	custs := make([][]string, nOrgs)
	for o := range orgs {
		orgs[o] = storetest.CreateOrg(t, db, "")
		for c := 0; c < custPerOrg; c++ {
			custs[o] = append(custs[o], storetest.CreateCustomerRole(t, db, orgs[o], domain.RoleRequester))
		}
	}
	manager := e.customer(orgs[0], domain.RoleOrgManager)

	// Vocabulary: 3 000 synthetic words with a Zipf-like draw, plus a rare
	// marker word ("zephyr") in ~0.5 % of tickets used as the search probe.
	vocab := make([]string, 3000)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("w%04dx", i)
	}
	zipf := rand.NewZipf(rng, 1.1, 1, uint64(len(vocab)-1))
	sentence := func(words int) string {
		var b strings.Builder
		for i := 0; i < words; i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(vocab[zipf.Uint64()])
		}
		return b.String()
	}
	statuses := []string{"new", "open", "pending", "on_hold", "resolved", "closed", "closed", "closed"}
	prios := []string{"low", "normal", "normal", "high", "urgent"}
	base := time.Now().UTC().Add(-2 * 365 * 24 * time.Hour)

	const chunk = 2000
	ids := make([]string, 0, total)
	for c := 0; c < total; c += chunk {
		end := min(c+chunk, total)
		type tk struct {
			id, subj, req, org string
			at                 time.Time
		}
		batch := make([]tk, 0, end-c)
		for i := c; i < end; i++ {
			o := rng.IntN(nOrgs)
			subj := sentence(4)
			if rng.IntN(200) == 0 {
				subj += " zephyr"
			}
			batch = append(batch, tk{store.NewID(), subj, custs[o][rng.IntN(custPerOrg)], orgs[o], base.Add(time.Duration(i) * 10 * time.Minute)})
		}
		err := db.Tx(ctx, func(tx *store.Tx) error {
			// tickets: 100 rows per statement
			for s := 0; s < len(batch); s += 100 {
				part := batch[s:min(s+100, len(batch))]
				var sb strings.Builder
				args := make([]any, 0, len(part)*14)
				sb.WriteString(`INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, requester_id, organization_id, channel,
					first_response_due_at, resolution_due_at, last_customer_activity_at, last_staff_activity_at, created_at, updated_at) VALUES `)
				for j, x := range part {
					if j > 0 {
						sb.WriteByte(',')
					}
					sb.WriteString(`(?, ?, ?, ?, ?, ?, ?, ?, ?, 'portal', ?, ?, ?, ?, ?, ?)`)
					i := c + s + j
					var staffAt any
					if i%3 != 0 {
						staffAt = x.at.Add(2 * time.Hour)
					}
					args = append(args, x.id, 2000+i, x.subj, statuses[i%len(statuses)], prios[i%len(prios)], 1+i%3, teams[i%nTeams], x.req, x.org,
						x.at.Add(8*time.Hour), x.at.Add(48*time.Hour), x.at.Add(time.Hour), staffAt, x.at, x.at.Add(3*time.Hour))
				}
				if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
					return err
				}
			}
			// messages: 5 per ticket (4 public, 1 internal), 200 rows per statement
			type msg struct {
				id, tid, author, kind, body string
				at                          time.Time
			}
			msgs := make([]msg, 0, len(batch)*5)
			for _, x := range batch {
				for m := 0; m < 5; m++ {
					kind, author := "public", x.req
					if m == 3 {
						kind, author = "internal", staffAuthor
					} else if m%2 == 1 {
						author = staffAuthor
					}
					msgs = append(msgs, msg{store.NewID(), x.id, author, kind, sentence(25 + rng.IntN(40)), x.at.Add(time.Duration(m) * time.Hour)})
				}
			}
			for s := 0; s < len(msgs); s += 200 {
				part := msgs[s:min(s+200, len(msgs))]
				var sb strings.Builder
				args := make([]any, 0, len(part)*6)
				sb.WriteString(`INSERT INTO ticket_messages (id, ticket_id, author_id, kind, body_md, created_at) VALUES `)
				for j, m := range part {
					if j > 0 {
						sb.WriteByte(',')
					}
					sb.WriteString(`(?, ?, ?, ?, ?, ?)`)
					args = append(args, m.id, m.tid, m.author, m.kind, m.body, m.at)
				}
				if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
					return err
				}
			}
			// search index, built from the same text the index worker would read
			byT := map[string][]msg{}
			for _, m := range msgs {
				byT[m.tid] = append(byT[m.tid], m)
			}
			for _, x := range batch {
				pub, in := []string{x.subj}, []string{}
				for _, m := range byT[x.id] {
					if m.kind == "public" {
						pub = append(pub, m.body)
					} else {
						in = append(in, m.body)
					}
				}
				doc := store.TicketDoc{TicketID: x.id, Public: strings.Join(pub, "\n"), Internal: strings.Join(in, "\n")}
				if db.Dialect == store.SQLite {
					// Fresh rows: a plain INSERT keyed by the ticket number, as
					// store.IndexTicket writes it (its replace path is measured
					// separately below).
					if _, err := tx.ExecContext(ctx, `INSERT INTO tickets_fts (rowid, ticket_id, public_text, internal_text, staff_fields_text) SELECT number, id, ?, ?, '' FROM tickets WHERE id = ?`,
						doc.Public, doc.Internal, doc.TicketID); err != nil {
						return err
					}
				} else if err := store.IndexTicket(ctx, tx, doc); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range batch {
			ids = append(ids, x.id)
		}
	}
	// Table by table (on Postgres a bare ANALYZE would visit every schema of
	// the shared test database); logged so a slow or failing one is visible.
	// Postgres gets VACUUM (ANALYZE), the steady state of a database that has
	// been running a while: right after a 600k-row load, autovacuum otherwise
	// fires on these tables (and the search TOAST table) in the middle of the
	// measurements, on the same single vCPU, and the figures measure it
	// instead of the queries (DB-023). It runs at autovacuum's pace
	// (vacuum_cost_delay 2ms): an unthrottled VACUUM dirties pages faster than
	// a 128 MB container writes them back and can get Postgres OOM-killed.
	analyze := map[store.Dialect]string{store.SQLite: "ANALYZE ", store.Postgres: "VACUUM (ANALYZE) "}[db.Dialect]
	conn, err := db.W.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if db.Dialect == store.Postgres {
		if _, err := conn.ExecContext(ctx, `SET vacuum_cost_delay = '2ms'`); err != nil {
			t.Fatal(err)
		}
	}
	for _, tbl := range []string{"users", "team_members", "tickets", "ticket_messages", map[store.Dialect]string{store.SQLite: "tickets_fts", store.Postgres: "ticket_search"}[db.Dialect]} {
		if db.Dialect == store.SQLite && tbl == "tickets_fts" {
			continue // virtual table: nothing to analyze
		}
		as := time.Now()
		if _, err := conn.ExecContext(ctx, analyze+tbl); err != nil {
			t.Fatalf("%s%s: %v", analyze, tbl, err)
		}
		t.Logf("%s%s took %v", analyze, tbl, time.Since(as).Round(time.Millisecond))
	}
	if db.Dialect == store.Postgres {
		if _, err := conn.ExecContext(ctx, `RESET vacuum_cost_delay`); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var nt, nm int
	_ = db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tickets`).Scan(&nt)
	_ = db.Read().QueryRowContext(ctx, `SELECT count(*) FROM ticket_messages`).Scan(&nm)
	t.Logf("seeded %d tickets / %d messages (+ search index) in %v", nt, nm, time.Since(start).Round(time.Second))
	if db.Dialect == store.Postgres {
		// The search heap must stay small enough to cache (DB-023, migration 0131).
		var heap, total int64
		_ = db.Read().QueryRowContext(ctx, `SELECT pg_relation_size('ticket_search'), pg_total_relation_size('ticket_search')`).Scan(&heap, &total)
		t.Logf("ticket_search heap %d MiB, with TOAST and indexes %d MiB", heap>>20, total>>20)
	}

	if os.Getenv("FD_PERF_EXPLAIN") == "1" {
		for _, qv := range []url.Values{{"sort": {"attention"}}, {"q": {"w0500x"}, "sort": {"-updated_at"}}} {
			lq, _ := ParseAgentListQuery(qv)
			where, args, err := e.svc.filterSQL(ctx, agent, lq)
			if err != nil {
				t.Fatal(err)
			}
			sqlq := `SELECT ` + ticketCols + ` FROM tickets t WHERE ` + where + ` ORDER BY ` + orderBy(sorts[lq.Sort]) + ` LIMIT 51`
			ex := `EXPLAIN QUERY PLAN `
			if db.Dialect == store.Postgres {
				ex = `EXPLAIN (ANALYZE, BUFFERS) `
			}
			rows, err := db.Read().QueryContext(ctx, ex+sqlq, args...)
			if err != nil {
				t.Fatal(err)
			}
			cols, _ := rows.Columns()
			var plan []string
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				_ = rows.Scan(ptrs...)
				plan = append(plan, fmt.Sprint(vals[len(vals)-1]))
			}
			rows.Close()
			t.Logf("EXPLAIN %v\n%s\n%s", qv, sqlq, strings.Join(plan, "\n"))
		}
	}
	const iters = 60
	res := map[string]perfStat{}
	// warm-up so the first iteration does not pay for cold caches only
	agentQ, _ := ParseAgentListQuery(url.Values{"sort": {"attention"}})
	portalQ, _ := ParsePortalListQuery(url.Values{})
	_, _ = e.svc.ListStaff(ctx, agent, agentQ)
	_, _ = e.svc.ListCustomer(ctx, manager, portalQ)

	res["agent_list"] = measure(iters, func(int) {
		page, err := e.svc.ListStaff(ctx, agent, agentQ)
		if err != nil || len(page.Items) != 50 {
			t.Fatalf("agent list: %v (%d rows)", err, len(page.Items))
		}
	})
	res["portal_list"] = measure(iters, func(int) {
		page, err := e.svc.ListCustomer(ctx, manager, portalQ)
		if err != nil || len(page.Items) != 50 {
			t.Fatalf("portal list: %v", err)
		}
	})
	// Search through the list builder (visibility predicate + match subquery),
	// by query class. Document frequency (share of tickets matching) is logged.
	// Budgeted: a rare term (~0.5 %) and a typical term (~1-3 %); the
	// high-frequency AND query and a short prefix are reported as stress cases.
	searches := []struct {
		name, q  string
		budgeted bool
	}{
		{"rare", "zephyr", true},
		{"typical", vocab[500], true},
		{"typical2", vocab[60] + " " + vocab[400], true},
		{"prefix", vocab[40][:4], false},
		{"common", vocab[3] + " " + vocab[10], false},
	}
	for _, sq := range searches {
		for _, staff := range []bool{true, false} {
			sub, args, _ := store.TicketMatch(db.Dialect, sq.q, staff)
			var df int
			_ = db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tickets t WHERE t.id IN (`+sub+`)`, args...).Scan(&df)
			key := "search_portal_" + sq.name
			if staff {
				key = "search_staff_" + sq.name
			}
			res[key] = measure(iters, func(int) {
				if staff {
					q, _ := ParseAgentListQuery(url.Values{"q": {sq.q}, "sort": {"-updated_at"}})
					if _, err := e.svc.ListStaff(ctx, agent, q); err != nil {
						t.Fatalf("search: %v", err)
					}
					return
				}
				q, _ := ParsePortalListQuery(url.Values{"q": {sq.q}})
				if _, err := e.svc.ListCustomer(ctx, manager, q); err != nil {
					t.Fatalf("portal search: %v", err)
				}
			})
			t.Logf("SEARCH engine=%s %-26s q=%-14q matches %6d tickets (%.2f%%)", db.Dialect, key, sq.q, df, 100*float64(df)/float64(nt))
			if sq.budgeted {
				perfBudget[key] = 100 * time.Millisecond
			}
			if !slices.Contains(perfOrder, key) {
				perfOrder = append(perfOrder, key)
			}
		}
	}
	// detail: tickets the L1 agent can see (team 0, tier 1)
	var visible []string
	for i, id := range ids {
		if i%nTeams == 0 && i%3 == 0 {
			visible = append(visible, id)
		}
	}
	res["detail"] = measure(iters, func(i int) {
		d, err := e.svc.StaffTicket(ctx, agent, visible[(i*7919)%len(visible)])
		if err != nil || len(d.Messages) != 5 {
			t.Fatalf("detail: %v", err)
		}
	})
	// One index-worker write (store.IndexTicket for an existing ticket):
	// reported, not budgeted; it runs off the request path (D-11).
	res["index_one"] = measure(20, func(i int) {
		id := ids[(i*104729)%len(ids)]
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			return store.IndexTicket(ctx, tx, store.TicketDoc{TicketID: id, Public: "reindexed " + vocab[i], Internal: ""})
		}); err != nil {
			t.Fatalf("index: %v", err)
		}
	})
	for _, k := range append(perfOrder, "index_one") {
		r := res[k]
		t.Logf("RESULT engine=%s %-24s p50=%-10v p95=%v", db.Dialect, k, r.p50.Round(10*time.Microsecond), r.p95.Round(10*time.Microsecond))
		if b, ok := perfBudget[k]; ok && r.p95 > b {
			t.Errorf("%s/%s p95 %v > budget %v", db.Dialect, k, r.p95, b)
		}
	}
	return res
}
