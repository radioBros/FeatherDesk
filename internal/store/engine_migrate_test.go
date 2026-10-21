// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func seedForCopy(t *testing.T, db *store.DB) {
	org := storetest.CreateOrg(t, db, "Acme")
	team := storetest.CreateTeam(t, db, "Front desk")
	agent := storetest.CreateStaff(t, db, domain.RoleAgent, 2, team)
	cust := storetest.CreateCustomer(t, db, org)
	a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, OrgID: org, TeamID: team, AssigneeID: agent, Subject: "Original"})
	b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, OrgID: org, Subject: "Follow-up"})
	mustExec(t, db, `UPDATE tickets SET followup_of_id = ? WHERE id = ?`, a, b)
	mustExec(t, db, `INSERT INTO ticket_events (id, ticket_id, actor_id, type, data, snapshot, created_at) VALUES (?, ?, ?, 'ticket.created', '{"b":2,"a":[1,2]}', '{"tier":1}', ?)`, store.NewID(), a, agent, store.Now())
	mustExec(t, db, `INSERT INTO users (id, kind, email, role, tier, break_glass, last_login_at) VALUES ('adm', 'staff', 'adm@x', 'admin', 3, TRUE, ?)`, store.Now())
	parent := store.NewID()
	mustExec(t, db, `INSERT INTO kb_categories (id, slug, title, visibility) VALUES (?, 'p', '{"en":"P"}', 'public')`, parent)
	mustExec(t, db, `INSERT INTO kb_categories (id, parent_id, slug, visibility) VALUES ('child', ?, 'c', 'customers')`, parent)
	mustExec(t, db, `INSERT INTO custom_field_defs (id, key, type, required) VALUES ('f1', 'contract', 'number', TRUE)`)
	mustExec(t, db, `INSERT INTO ticket_field_values (ticket_id, field_id, value_num, value_date) VALUES (?, 'f1', 42.5, ?)`, a, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	mustExec(t, db, `UPDATE settings SET value = '{"product_name":"Migrated"}' WHERE key = 'branding'`)
}

func TestMigrateEngineBothDirections(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs ST_TEST_POSTGRES_URL")
	}
	lite := storetest.Open(t, store.SQLite)
	pg := storetest.Open(t, store.Postgres)
	seedForCopy(t, lite)
	rep, err := store.MigrateEngine(ctx, lite, pg)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NeedsReindex || len(rep.Tables) < 30 {
		t.Fatalf("report %+v", rep)
	}
	var name string
	var bg bool
	if err := pg.Read().QueryRowContext(ctx, `SELECT value->>'product_name' FROM settings WHERE key = 'branding'`).Scan(&name); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if err := pg.Read().QueryRowContext(ctx, `SELECT break_glass FROM users WHERE id = 'adm'`).Scan(&bg); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if name != "Migrated" || !bg {
		t.Fatalf("values not carried: %q %v", name, bg)
	}
	var n int64
	if err := pg.Read().QueryRowContext(ctx, `SELECT int_value FROM counters WHERE name = 'ticket_number'`).Scan(&n); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if n != 1002 {
		t.Fatalf("counter %d", n)
	}
	// And back again into a fresh SQLite.
	lite2 := storetest.Open(t, store.SQLite)
	if _, err := store.MigrateEngine(ctx, pg, lite2); err != nil {
		t.Fatal(err)
	}
	// Refuses a non-empty target.
	if _, err := store.MigrateEngine(ctx, lite, lite2); err == nil {
		t.Fatal("copied into a non-empty target")
	}
}
