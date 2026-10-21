// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestMigration_DefaultProjectBackfill (D-20, PRJ-002): a database populated
// at the pre-project schema (0131) migrates to one Default project that owns
// every ticket, category, mailbox and KB category; ticket refs equal the old
// numbers; every staff user is a member and every team serves it; the SLA
// rows survive as global defaults; new tickets continue the sequence.
func TestMigration_DefaultProjectBackfill(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, engine *store.DB) {
		db := storetest.OpenUnmigrated(t, engine.Dialect)
		ctx := context.Background()
		if err := db.MigrateUntil(ctx, 131); err != nil {
			t.Fatal(err)
		}
		exec := func(q string, args ...any) {
			t.Helper()
			if err := db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q, args...); return err }); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		now := store.Now()
		admin, agent, cust, team, cat, org := store.NewID(), store.NewID(), store.NewID(), store.NewID(), store.NewID(), store.NewID()
		exec(`UPDATE settings SET value = ? WHERE key = 'branding'`, `{"product_name":"Acme Help"}`)
		exec(`INSERT INTO users (id, kind, email, name, role, tier, status, created_at, updated_at) VALUES (?, 'staff', 'a@x.test', 'A', 'admin', 3, 'active', ?, ?)`, admin, now, now)
		exec(`INSERT INTO users (id, kind, email, name, role, tier, status, created_at, updated_at) VALUES (?, 'staff', 'g@x.test', 'G', 'agent', 1, 'active', ?, ?)`, agent, now, now)
		exec(`INSERT INTO organizations (id, name, created_at, updated_at) VALUES (?, 'Org', ?, ?)`, org, now, now)
		exec(`INSERT INTO users (id, kind, email, name, role, organization_id, status, created_at, updated_at) VALUES (?, 'customer', 'c@x.test', 'C', 'requester', ?, 'active', ?, ?)`, cust, org, now, now)
		exec(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, 'T', ?, ?)`, team, now, now)
		exec(`INSERT INTO team_members (team_id, user_id, created_at) VALUES (?, ?, ?)`, team, agent, now)
		exec(`INSERT INTO categories (id, name, default_team_id, created_at, updated_at) VALUES (?, 'Cat', ?, ?, ?)`, cat, team, now, now)
		exec(`INSERT INTO inbound_mailboxes (id, name, kind, address, created_at, updated_at) VALUES (?, 'Mail', 'webhook', 'm@x.test', ?, ?)`, store.NewID(), now, now)
		exec(`INSERT INTO kb_categories (id, slug, title, visibility, created_at, updated_at) VALUES (?, 'kb', '{"en":"KB"}', 'public', ?, ?)`, store.NewID(), now, now)
		exec(`UPDATE sla_policies SET first_response_minutes = 15, resolution_minutes = 60 WHERE priority = 'urgent'`)
		for _, n := range []int{1001, 1002, 1040} {
			exec(`INSERT INTO tickets (id, number, subject, requester_id, organization_id, team_id, category_id, channel, created_at, updated_at)
				VALUES (?, ?, 'Old', ?, ?, ?, ?, 'portal', ?, ?)`, store.NewID(), n, cust, org, team, cat, now, now)
		}
		exec(`UPDATE counters SET int_value = 1050 WHERE name = 'ticket_number'`)

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
		var key, name string
		if err := db.Read().QueryRowContext(ctx, `SELECT key, name FROM projects WHERE id = ?`, domain.DefaultProjectID).Scan(&key, &name); err != nil {
			t.Fatal(err)
		}
		if key != "HD" || name != "Acme Help" || count(`SELECT COUNT(*) FROM projects`) != 1 {
			t.Fatalf("default project %s %q", key, name)
		}
		for q, want := range map[string]int{
			`SELECT COUNT(*) FROM tickets WHERE project_id = ? AND project_seq = number`:     3,
			`SELECT COUNT(*) FROM categories WHERE project_id = ?`:                           1,
			`SELECT COUNT(*) FROM inbound_mailboxes WHERE project_id = ?`:                    1,
			`SELECT COUNT(*) FROM kb_categories WHERE project_id IS NULL OR project_id <> ?`: 1, // KB stays global
			`SELECT COUNT(*) FROM project_members WHERE project_id = ? AND role = 'member'`:  2,
			`SELECT COUNT(*) FROM project_teams WHERE project_id = ?`:                        1,
		} {
			if got := count(q, domain.DefaultProjectID); got != want {
				t.Errorf("%s = %d, want %d", q, got, want)
			}
		}
		var fr, res int
		if err := db.Read().QueryRowContext(ctx, `SELECT first_response_minutes, resolution_minutes FROM sla_policies WHERE priority = 'urgent' AND project_id IS NULL`).Scan(&fr, &res); err != nil || fr != 15 || res != 60 {
			t.Fatalf("SLA after migration: %d/%d %v", fr, res, err)
		}
		if n := count(`SELECT COUNT(*) FROM sla_policies WHERE project_id IS NULL`); n != 4 {
			t.Fatalf("global SLA rows %d", n)
		}
		// The sequence continues after the global counter: refs stay equal
		// to numbers while the Default project is the only one.
		var seq, num int64
		err := db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			if num, err = store.NextTicketNumber(ctx, tx); err != nil {
				return err
			}
			seq, err = store.NextProjectSeq(ctx, tx, domain.DefaultProjectID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, number, project_id, project_seq, subject, requester_id, channel, created_at, updated_at)
				VALUES (?, ?, ?, ?, 'New', ?, 'portal', ?, ?)`, store.NewID(), num, domain.DefaultProjectID, seq, cust, now, now)
			return err
		})
		if err != nil || num != 1051 || seq != 1051 {
			t.Fatalf("next ticket number %d seq %d: %v", num, seq, err)
		}
		// A raw insert without project_seq gets the next one from the trigger.
		id := store.NewID()
		exec(`INSERT INTO tickets (id, number, subject, requester_id, channel, created_at, updated_at) VALUES (?, 2000, 'Raw', ?, 'portal', ?, ?)`, id, cust, now, now)
		if got := count(`SELECT project_seq FROM tickets WHERE id = ?`, id); got != 1052 {
			t.Fatalf("trigger sequence %d, want 1052", got)
		}
		// A project referenced by tickets cannot be deleted.
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, domain.DefaultProjectID)
			return err
		}); err == nil {
			t.Fatal("deleting a project with tickets must fail")
		}
	})
}
