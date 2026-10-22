// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestMigration_TicketFTSRowid (0161): an index written the old way (fresh
// rowids, a stale duplicate, an orphan) migrates to one entry per ticket keyed
// by its number, with the newest text; search keeps working and re-indexing
// replaces the entry instead of adding one.
func TestMigration_TicketFTSRowid(t *testing.T) {
	db := storetest.OpenUnmigrated(t, store.SQLite)
	ctx := context.Background()
	if err := db.MigrateUntil(ctx, 150); err != nil {
		t.Fatal(err)
	}
	cust := storetest.CreateCustomer(t, db, "")
	a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Printer jam"})
	b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Login loop"})
	exec := func(q string, args ...any) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q, args...); return err }); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	old := `INSERT INTO tickets_fts (ticket_id, public_text, internal_text, staff_fields_text) VALUES (?, ?, ?, ?)`
	exec(old, a, "printer stale", "", "")
	exec(old, b, "login loop", "secretword", "")
	exec(old, a, "printer jam", "", "") // newer entry of the same ticket wins
	exec(old, store.NewID(), "orphan", "", "")

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate populated database: %v", err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Read().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM tickets_fts`); n != 2 {
		t.Fatalf("entries after migration = %d, want 2 (duplicate and orphan dropped)", n)
	}
	if n := count(`SELECT COUNT(*) FROM tickets_fts f JOIN tickets t ON t.number = f.rowid AND t.id = f.ticket_id`); n != 2 {
		t.Fatalf("entries keyed by ticket number = %d, want 2", n)
	}
	search := func(q string, staff bool) []string {
		t.Helper()
		sub, args, ok := store.TicketMatch(store.SQLite, q, staff)
		if !ok {
			t.Fatalf("no terms in %q", q)
		}
		rows, err := db.Read().QueryContext(ctx, `SELECT id FROM tickets WHERE id IN (`+sub+`) ORDER BY id`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	if got := search("jam", false); len(got) != 1 || got[0] != a {
		t.Fatalf("customer search jam = %v, want [%s]", got, a)
	}
	if got := search("stale", true); len(got) != 0 {
		t.Fatalf("the stale duplicate's text is still searchable: %v", got)
	}
	if got := search("secretword", false); len(got) != 0 {
		t.Fatalf("internal text leaked into customer search: %v", got)
	}
	if got := search("secretword", true); len(got) != 1 || got[0] != b {
		t.Fatalf("staff search secretword = %v, want [%s]", got, b)
	}

	for _, text := range []string{"toner empty", "toner refilled"} {
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			return store.IndexTicket(ctx, tx, store.TicketDoc{TicketID: a, Public: text})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(`SELECT COUNT(*) FROM tickets_fts WHERE ticket_id = ?`, a); n != 1 {
		t.Fatalf("re-indexing left %d entries, want 1", n)
	}
	if got := search("refilled", false); len(got) != 1 || got[0] != a {
		t.Fatalf("search after re-index = %v", got)
	}
	if err := db.Tx(ctx, func(tx *store.Tx) error { return store.UnindexTicket(ctx, tx, b) }); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT COUNT(*) FROM tickets_fts WHERE ticket_id = ?`, b); n != 0 {
		t.Fatalf("unindex left %d entries", n)
	}
}

// TestMigration_LegacyFunctionNames (0160): a Postgres database created by
// SimpleTickets 1.0.0 has st_ functions; after the upgrade it has the fd_
// names of a fresh database and the KB visibility triggers still work.
func TestMigration_LegacyFunctionNames(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("FD_TEST_POSTGRES_URL not set")
	}
	db := storetest.OpenUnmigrated(t, store.Postgres)
	ctx := context.Background()
	if err := db.MigrateUntil(ctx, 150); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) error {
		return db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q, args...); return err })
	}
	must := func(q string, args ...any) {
		t.Helper()
		if err := exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Turn the fresh schema into what SimpleTickets 1.0.0 created.
	funcs := []string{"users_kind_immutable()", "audit_log_append_only()", "tickets_principals()", "ticket_events_append_only()",
		"team_members_staff_only()", "ticket_watchers_customer_org()", "kb_rank(text)", "kb_article_visibility()",
		"kb_category_visibility()", "project_members_staff_only()", "tickets_project_seq()"}
	for _, f := range funcs {
		must(`ALTER FUNCTION fd_` + f + ` RENAME TO st_` + strings.SplitN(f, "(", 2)[0])
	}
	must(`CREATE OR REPLACE FUNCTION st_kb_article_visibility() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF st_kb_rank(NEW.visibility) < (SELECT st_kb_rank(visibility) FROM kb_categories WHERE id = NEW.category_id) THEN
        RAISE EXCEPTION 'kb article visibility is wider than its category';
    END IF;
    RETURN NEW;
END
$$`)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate a SimpleTickets database: %v", err)
	}
	for _, f := range funcs {
		var legacy, current *string
		if err := db.Read().QueryRowContext(ctx, `SELECT to_regprocedure(?)::text, to_regprocedure(?)::text`, "st_"+f, "fd_"+f).Scan(&legacy, &current); err != nil {
			t.Fatal(err)
		}
		if legacy != nil || current == nil {
			t.Errorf("%s: st_ present=%v, fd_ present=%v; want only fd_", f, legacy != nil, current != nil)
		}
	}
	now := store.Now()
	cat := store.NewID()
	must(`INSERT INTO kb_categories (id, slug, title, visibility, created_at, updated_at) VALUES (?, 'kb', '{"en":"KB"}', 'customers', ?, ?)`, cat, now, now)
	err := exec(`INSERT INTO kb_articles (id, category_id, slug, visibility, created_at, updated_at) VALUES (?, ?, 'a', 'public', ?, ?)`, store.NewID(), cat, now, now)
	if err == nil || !strings.Contains(err.Error(), "wider than its category") {
		t.Fatalf("visibility trigger after upgrade: err = %v", err)
	}
}

// TestMigration_DefaultProductName (0160): the untouched SimpleTickets default
// product name becomes FeatherDesk; a name the admin chose is kept.
func TestMigration_DefaultProductName(t *testing.T) {
	for _, tc := range []struct{ stored, want string }{{"SimpleTickets", "FeatherDesk"}, {"Acme Help", "Acme Help"}} {
		storetest.Engines(t, func(t *testing.T, engine *store.DB) {
			db := storetest.OpenUnmigrated(t, engine.Dialect)
			ctx := context.Background()
			if err := db.MigrateUntil(ctx, 150); err != nil {
				t.Fatal(err)
			}
			if err := db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = 'branding'`, `{"product_name":"`+tc.stored+`","powered_by":true}`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			var v string
			if err := db.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'branding'`).Scan(&v); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(v, `"product_name": "`+tc.want+`"`) && !strings.Contains(v, `"product_name":"`+tc.want+`"`) {
				t.Fatalf("stored %q: branding after upgrade = %s, want product_name %q", tc.stored, v, tc.want)
			}
			if !strings.Contains(v, "powered_by") {
				t.Fatalf("other branding keys lost: %s", v)
			}
		})
	}
}
