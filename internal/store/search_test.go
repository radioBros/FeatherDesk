// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"sort"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func matchIDs(t *testing.T, db *store.DB, query string, staff bool) []string {
	t.Helper()
	sub, args, ok := store.TicketMatch(db.Dialect, query, staff)
	if !ok {
		return nil
	}
	rows, err := db.Read().QueryContext(ctx, `SELECT id FROM tickets WHERE id IN (`+sub+`) ORDER BY id`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("rows.Scan: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// TestTicketSearch_PublicNeverMatchesInternal (DB-013, DB-029): a customer search
// never matches words that appear only in internal notes or staff-only fields.
func TestTicketSearch_PublicNeverMatchesInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Printer broken"})
		b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "VPN slow"})
		docs := []store.TicketDoc{
			{TicketID: a, Public: "Printer broken in the office", Internal: "zebra firmware bug", StaffFields: "contract gold"},
			{TicketID: b, Public: "VPN slow from home", Internal: "", StaffFields: ""},
		}
		for _, d := range docs {
			if err := db.Tx(ctx, func(tx *store.Tx) error { return store.IndexTicket(ctx, tx, d) }); err != nil {
				t.Fatal(err)
			}
		}
		if got := matchIDs(t, db, "zebra", false); len(got) != 0 {
			t.Fatalf("customer search matched internal-only word: %v", got)
		}
		if got := matchIDs(t, db, "gold", false); len(got) != 0 {
			t.Fatalf("customer search matched staff-only field: %v", got)
		}
		if got := matchIDs(t, db, "zebra", true); len(got) != 1 || got[0] != a {
			t.Fatalf("staff search for internal word: %v", got)
		}
		// Terms across columns match for staff (printer in public, zebra internal).
		if got := matchIDs(t, db, "print zebra", true); len(got) != 1 {
			t.Fatalf("staff cross-column prefix search: %v", got)
		}
		if got := matchIDs(t, db, "PRINT", false); len(got) != 1 || got[0] != a {
			t.Fatalf("prefix + case-insensitive public search: %v", got)
		}
		// Query syntax is never passed through.
		if got := matchIDs(t, db, `"vpn"* ( ) : ^ -- '`, false); len(got) != 1 || got[0] != b {
			t.Fatalf("sanitised query: %v", got)
		}
		// Re-index replaces.
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			return store.IndexTicket(ctx, tx, store.TicketDoc{TicketID: a, Public: "Scanner jammed"})
		}); err != nil {
			t.Fatal(err)
		}
		if got := matchIDs(t, db, "printer", false); len(got) != 0 {
			t.Fatalf("stale index: %v", got)
		}
		if err := db.Tx(ctx, func(tx *store.Tx) error { return store.UnindexTicket(ctx, tx, b) }); err != nil {
			t.Fatal(err)
		}
		if got := matchIDs(t, db, "vpn", false); len(got) != 0 {
			t.Fatalf("unindexed still matches: %v", got)
		}
	})
}

func TestKBSearch(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cat := store.NewID()
		mustExec(t, db, `INSERT INTO kb_categories (id, slug, visibility) VALUES (?, 'c', 'public')`, cat)
		for _, id := range []string{"a1", "a2"} {
			mustExec(t, db, `INSERT INTO kb_articles (id, category_id, slug, status) VALUES (?, ?, ?, 'published')`, id, cat, id)
		}
		docs := []store.KBDoc{
			{ArticleID: "a1", Locale: "en", Title: "Reset your password", Body: "Open settings and click reset."},
			{ArticleID: "a2", Locale: "en", Title: "VPN setup", Body: "If your password expired, reset it first."},
			{ArticleID: "a1", Locale: "it", Title: "Reimposta la password", Body: "Apri impostazioni."},
		}
		for _, d := range docs {
			if err := db.Tx(ctx, func(tx *store.Tx) error { return store.IndexKB(ctx, tx, d) }); err != nil {
				t.Fatal(err)
			}
		}
		sub, args, ok := store.KBMatch(db.Dialect, "password")
		if !ok {
			t.Fatal("no terms")
		}
		rows, err := db.Read().QueryContext(ctx, `SELECT article_id, locale FROM (`+sub+`) m WHERE locale = 'en' ORDER BY rank, article_id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var id, loc string
			if err := rows.Scan(&id, &loc); err != nil {
				t.Fatalf("rows.Scan: %v", err)
			}
			got = append(got, id)
		}
		rows.Close()
		if len(got) != 2 || got[0] != "a1" {
			t.Fatalf("title match must rank first: %v", got)
		}
		if err := db.Tx(ctx, func(tx *store.Tx) error { return store.UnindexKB(ctx, tx, "a1") }); err != nil {
			t.Fatal(err)
		}
		sub, args, _ = store.KBMatch(db.Dialect, "reimposta")
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM (`+sub+`) m`, args...).Scan(&n); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if n != 0 {
			t.Fatalf("unindexed translations still match: %d", n)
		}
	})
}

func TestSearchTerms(t *testing.T) {
	got := store.SearchTerms(`Héllo, "world"* OR (x) ` + "ünïcode 123")
	want := []string{"héllo", "world", "or", "x", "ünïcode", "123"}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
