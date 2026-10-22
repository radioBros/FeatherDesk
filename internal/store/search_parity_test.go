// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func indexDocs(t *testing.T, db *store.DB, docs ...store.TicketDoc) {
	t.Helper()
	for _, d := range docs {
		if err := db.Tx(ctx, func(tx *store.Tx) error { return store.IndexTicket(ctx, tx, d) }); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSearchDiacritics (DB-029): accents never matter, on either side, on both
// engines (FTS5 remove_diacritics 2 on SQLite, Go folding on Postgres).
func TestSearchDiacritics(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "a"})
		b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "b"})
		c := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "c"})
		indexDocs(t, db,
			store.TicketDoc{TicketID: a, Public: "Perché la stampante non funziona?"},
			store.TicketDoc{TicketID: b, Public: "perche senza accento, café crème"},
			store.TicketDoc{TicketID: c, Public: "Mañana Ärger übermäßig", Internal: "Ĺódź ŠKODA"},
		)
		for _, tc := range []struct {
			q     string
			staff bool
			want  []string
		}{
			{"perche", false, []string{a, b}},
			{"perché", false, []string{a, b}},
			{"PERCHÉ", false, []string{a, b}},
			{"cafe creme", false, []string{b}},
			{"manana", false, []string{c}},
			{"arger", false, []string{c}},
			{"Ärger", false, []string{c}},
			{"skoda", false, nil}, // internal only: customer never matches
			{"skoda", true, []string{c}},
			{"lodz", true, []string{c}},
		} {
			want := slices.Clone(tc.want)
			slices.Sort(want)
			if got := matchIDs(t, db, tc.q, tc.staff); !slices.Equal(got, want) {
				t.Errorf("%s: q=%q staff=%v got %v want %v", db.Dialect, tc.q, tc.staff, got, want)
			}
		}
	})
}

// TestSearchQueryInjection (DB-029): operator and quote payloads never reach
// the FTS5 / to_tsquery syntax: no error and no spurious hit, both engines.
func TestSearchQueryInjection(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "a"})
		indexDocs(t, db, store.TicketDoc{TicketID: a, Public: "printer jammed", Internal: "secretword"})
		for _, q := range []string{
			`'); DROP TABLE tickets; --`, `" OR 1=1 --`, `*`, `NEAR(printer`, `printer | secretword`,
			`!printer`, `printer & !x`, `public_text : secretword`, `internal_text:secretword`, `{internal_text}: secretword`,
			`secretword:*`, `$1`, `?`, `\`, `^printer`, `"printer"*`, `-printer`, `<->`, `printer <-> jammed`,
		} {
			for _, staff := range []bool{false, true} {
				words := []string{"printer", "jammed"}
				if staff {
					words = append(words, "secretword")
				}
				hit := slices.Contains(matchIDs(t, db, q, staff), a)
				if want := legit(q, words); hit != want {
					t.Errorf("q=%q staff=%v: hit=%v want %v (terms %v)", q, staff, hit, want, store.SearchTerms(q))
				}
			}
		}
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tickets`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("tickets table damaged: %d %v", n, err)
		}
	})
}

// legit: every term of q is a prefix of one of the indexed words (the only
// way a sanitised AND-of-prefixes query may hit).
func legit(q string, words []string) bool {
	terms := store.SearchTerms(q)
	if len(terms) == 0 {
		return false
	}
	for _, t := range terms {
		if !slices.ContainsFunc(words, func(w string) bool { return strings.HasPrefix(w, t) }) {
			return false
		}
	}
	return true
}

// TestSearchRankParity (DB-029): for one fixed fixture both engines return
// the same hit set for tickets and the same top-3 ranking for the ranked KB
// search (bm25 on SQLite, ts_rank on Postgres).
func TestSearchRankParity(t *testing.T) {
	type result struct{ tickets, kb []string }
	got := map[store.Dialect]result{}
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		var ids []string
		for i, text := range []string{
			"vpn disconnects every hour",
			"vpn client crash on start",
			"printer offline",
			"vpn vpn vpn tunnel error vpn",
			"email bounce",
		} {
			id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: string(rune('a' + i))})
			ids = append(ids, id)
			indexDocs(t, db, store.TicketDoc{TicketID: id, Public: text})
		}
		var r result
		for _, id := range matchIDs(t, db, "vpn", false) {
			r.tickets = append(r.tickets, string(rune('a'+slices.Index(ids, id))))
		}

		cat := store.NewID()
		mustExec(t, db, `INSERT INTO kb_categories (id, slug, visibility) VALUES (?, 'c', 'public')`, cat)
		docs := []store.KBDoc{
			{ArticleID: "k1", Locale: "en", Title: "VPN setup guide", Body: "Install the client and connect."},
			{ArticleID: "k2", Locale: "en", Title: "Printer drivers", Body: "If the vpn is down, printing over vpn fails."},
			{ArticleID: "k3", Locale: "en", Title: "Email rules", Body: "Nothing about networks here."},
			{ArticleID: "k4", Locale: "en", Title: "Remote work", Body: "Use the vpn when working remotely."},
		}
		for _, d := range docs {
			mustExec(t, db, `INSERT INTO kb_articles (id, category_id, slug, status) VALUES (?, ?, ?, 'published')`, d.ArticleID, cat, d.ArticleID)
			if err := db.Tx(ctx, func(tx *store.Tx) error { return store.IndexKB(ctx, tx, d) }); err != nil {
				t.Fatal(err)
			}
		}
		sub, args, _ := store.KBMatch(db.Dialect, "vpn")
		rows, err := db.Read().QueryContext(ctx, `SELECT article_id FROM (`+sub+`) m ORDER BY rank, article_id LIMIT 3`, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			r.kb = append(r.kb, id)
		}
		rows.Close()
		if len(r.kb) != 3 || r.kb[0] != "k1" {
			t.Fatalf("title match must rank first, got %v", r.kb)
		}
		slices.Sort(r.tickets)
		if !slices.Equal(r.tickets, []string{"a", "b", "d"}) {
			t.Fatalf("ticket hits %v", r.tickets)
		}
		got[db.Dialect] = r
	})
	s, okS := got[store.SQLite]
	p, okP := got[store.Postgres]
	if !okS || !okP {
		if os.Getenv("CI") == "true" {
			t.Fatal("both engines must run in CI")
		}
		t.Skip("parity needs both engines")
	}
	if !slices.Equal(s.tickets, p.tickets) || !slices.Equal(s.kb, p.kb) {
		t.Fatalf("engines disagree: sqlite %+v postgres %+v", s, p)
	}
	t.Logf("top-3 KB: %v, ticket hits: %v (identical on both engines)", s.kb, s.tickets)
}
