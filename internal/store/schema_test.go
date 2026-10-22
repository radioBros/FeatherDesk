// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

var ctx = context.Background()

// TestSchemaParity: both engines expose the same tables and columns (D-14, DB-027).
// Search tables differ by design (FTS5 vs tsvector) and are compared separately.
func TestSchemaParity(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs FD_TEST_POSTGRES_URL")
	}
	lite := storetest.Open(t, store.SQLite)
	pg := storetest.Open(t, store.Postgres)
	lt, pt := userTables(t, lite), userTables(t, pg)
	if !reflect.DeepEqual(lt, pt) {
		t.Fatalf("table sets differ:\nsqlite:   %v\npostgres: %v", lt, pt)
	}
	for _, tbl := range lt {
		lc, err := store.Columns(ctx, lite, tbl)
		if err != nil {
			t.Fatal(err)
		}
		pc, err := store.Columns(ctx, pg, tbl)
		if err != nil {
			t.Fatal(err)
		}
		if len(lc) != len(pc) {
			t.Errorf("%s: column count sqlite=%d postgres=%d", tbl, len(lc), len(pc))
			continue
		}
		for i := range lc {
			if lc[i].Name != pc[i].Name {
				t.Errorf("%s col %d: sqlite=%s postgres=%s", tbl, i, lc[i].Name, pc[i].Name)
			}
			if lc[i].NotNull != pc[i].NotNull {
				t.Errorf("%s.%s: NOT NULL sqlite=%v postgres=%v", tbl, lc[i].Name, lc[i].NotNull, pc[i].NotNull)
			}
			if store.IsBoolColumn(lc[i].Type) != store.IsBoolColumn(pc[i].Type) || store.IsJSONColumn(lc[i].Type) != store.IsJSONColumn(pc[i].Type) {
				t.Errorf("%s.%s: type class sqlite=%s postgres=%s", tbl, lc[i].Name, lc[i].Type, pc[i].Type)
			}
		}
		le, err := store.CheckEnums(ctx, lite, tbl)
		if err != nil {
			t.Fatal(err)
		}
		pe, err := store.CheckEnums(ctx, pg, tbl)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(normEnums(le), normEnums(pe)) {
			t.Errorf("%s: CHECK enums differ\nsqlite:   %v\npostgres: %v", tbl, normEnums(le), normEnums(pe))
		}
	}
}

func userTables(t *testing.T, db *store.DB) []string {
	all, err := store.Tables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range all {
		if !store.IsSearchTable(n) {
			out = append(out, n)
		}
	}
	return out
}

func normEnums(m map[string][][]string) []string {
	var out []string
	for col, lists := range m {
		for _, l := range lists {
			out = append(out, col+"="+strings.Join(l, ","))
		}
	}
	sort.Strings(out)
	return out
}

func strs[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	sort.Strings(out)
	return out
}

// TestEnumsMatchSchema: the CHECK lists in the schema equal the Go enums (CON-005, schema half).
func TestEnumsMatchSchema(t *testing.T) {
	cases := []struct {
		table, col string
		want       []string
	}{
		{"users", "kind", []string{"customer", "staff"}},
		{"users", "status", []string{"active", "disabled", "pending_verification"}},
		{"users", "locale", append([]string(nil), domain.Locales...)},
		{"tickets", "status", strs(domain.TicketStatuses)},
		{"tickets", "priority", strs(domain.Priorities)},
		{"tickets", "channel", strs(domain.Channels)},
		{"ticket_messages", "kind", strs(domain.MessageKinds)},
		{"ticket_links", "type", strs(domain.LinkTypes)},
		{"custom_field_defs", "type", strs(domain.FieldTypes)},
		{"kb_articles", "visibility", strs([]domain.KBVisibility{domain.KBPublic, domain.KBCustomers, domain.KBInternal})},
		{"kb_articles", "status", strs([]domain.KBStatus{domain.KBDraft, domain.KBPublished, domain.KBArchived})},
		{"kb_article_translations", "locale", append([]string(nil), domain.Locales...)},
		{"sessions", "audience", []string{"customer", "staff"}},
		{"project_members", "role", strs(domain.ProjectRoles)},
	}
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		for _, c := range cases {
			got, err := store.CheckEnums(ctx, db, c.table)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			lists := got[c.col]
			if len(lists) != 1 || !reflect.DeepEqual(lists[0], want) {
				t.Errorf("%s.%s CHECK = %v, want %v", c.table, c.col, lists, want)
			}
		}
		// users.role is split per kind (M-2): both role lists must appear.
		roles := map[string]bool{}
		got, _ := store.CheckEnums(ctx, db, "users")
		for _, l := range got["role"] {
			roles[strings.Join(l, ",")] = true
		}
		if !roles[strings.Join(strs(domain.StaffRoles), ",")] || !roles[strings.Join(strs(domain.CustomerRoles), ",")] {
			t.Errorf("users.role CHECK lists = %v", got["role"])
		}
	})
}

func expectErr(t *testing.T, db *store.DB, wantSub string, q string, args ...any) {
	t.Helper()
	err := db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("want error containing %q, got %v\n%s", wantSub, err, q)
	}
}

func mustExec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func TestKindImmutable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		id := storetest.CreateCustomer(t, db, "")
		expectErr(t, db, "users.kind is immutable", `UPDATE users SET kind = 'staff', role = 'agent', tier = 1 WHERE id = ?`, id)
	})
}

// TestStaffCustomerColumns covers M-2: staff-only and customer-only columns never mix.
func TestStaffCustomerColumns(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		org := storetest.CreateOrg(t, db, "")
		bad := []string{
			`INSERT INTO users (id, kind, email, role, tier) VALUES ('a1', 'customer', 'a1@x', 'requester', 2)`,
			`INSERT INTO users (id, kind, email, role) VALUES ('a2', 'staff', 'a2@x', 'agent')`,
			`INSERT INTO users (id, kind, email, role, tier, organization_id) VALUES ('a3', 'staff', 'a3@x', 'agent', 1, '` + org + `')`,
			`INSERT INTO users (id, kind, email, role) VALUES ('a4', 'customer', 'a4@x', 'admin')`,
			`INSERT INTO users (id, kind, email, role, tier, break_glass) VALUES ('a5', 'staff', 'a5@x', 'agent', 1, TRUE)`,
		}
		for _, q := range bad {
			err := db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q); return err })
			if err == nil {
				t.Errorf("accepted: %s", q)
			}
		}
		mustExec(t, db, `INSERT INTO users (id, kind, email, role, tier, break_glass) VALUES ('ok1', 'staff', 'ok1@x', 'admin', 3, TRUE)`)
	})
}

func TestTicketEventsAppendOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		ev := store.NewID()
		mustExec(t, db, `INSERT INTO ticket_events (id, ticket_id, type, data, snapshot, created_at) VALUES (?, ?, 'ticket.created', '{}', '{}', ?)`, ev, tk, store.Now())
		expectErr(t, db, "append-only", `UPDATE ticket_events SET type = 'x' WHERE id = ?`, ev)
		expectErr(t, db, "append-only", `DELETE FROM ticket_events WHERE id = ?`, ev)
		// Hard delete path: allowed only through purge_allow.
		err := db.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO purge_allow (tbl, key) VALUES ('ticket_events', ?)`, tk); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_events WHERE ticket_id = ?`, tk); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM purge_allow WHERE tbl = 'ticket_events' AND key = ?`, tk)
			return err
		})
		if err != nil {
			t.Fatalf("purge path: %v", err)
		}
	})
}

func TestAuditLogAppendOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		id := store.NewID()
		mustExec(t, db, `INSERT INTO audit_log (id, action, created_at) VALUES (?, 'x', ?)`, id, store.Now())
		expectErr(t, db, "append-only", `UPDATE audit_log SET action = 'y' WHERE id = ?`, id)
		expectErr(t, db, "append-only", `DELETE FROM audit_log WHERE id = ?`, id)
	})
}

func TestTeamMembersStaffOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		team := storetest.CreateTeam(t, db, "")
		cust := storetest.CreateCustomer(t, db, "")
		expectErr(t, db, "staff users only", `INSERT INTO team_members (team_id, user_id) VALUES (?, ?)`, team, cust)
		storetest.CreateStaff(t, db, domain.RoleAgent, 1, team)
	})
}

func TestKBVisibilityNotWiderThanCategory(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cat := store.NewID()
		mustExec(t, db, `INSERT INTO kb_categories (id, slug, visibility) VALUES (?, 'c1', 'customers')`, cat)
		expectErr(t, db, "wider than its category", `INSERT INTO kb_articles (id, category_id, slug, visibility) VALUES ('a1', ?, 'a1', 'public')`, cat)
		mustExec(t, db, `INSERT INTO kb_articles (id, category_id, slug, visibility) VALUES ('a2', ?, 'a2', 'customers')`, cat)
		expectErr(t, db, "wider than its category", `UPDATE kb_articles SET visibility = 'public' WHERE id = 'a2'`)
		expectErr(t, db, "narrower than one of its articles", `UPDATE kb_categories SET visibility = 'internal' WHERE id = ?`, cat)
		mustExec(t, db, `UPDATE kb_articles SET visibility = 'internal' WHERE id = 'a2'`)
		mustExec(t, db, `UPDATE kb_categories SET visibility = 'internal' WHERE id = ?`, cat)
	})
}

func TestOneParentPerTicket(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		c := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mustExec(t, db, `INSERT INTO ticket_links (id, from_ticket_id, to_ticket_id, type) VALUES (?, ?, ?, 'parent_of')`, store.NewID(), a, c)
		expectErr(t, db, "", `INSERT INTO ticket_links (id, from_ticket_id, to_ticket_id, type) VALUES (?, ?, ?, 'parent_of')`, store.NewID(), b, c)
		expectErr(t, db, "", `INSERT INTO ticket_links (id, from_ticket_id, to_ticket_id, type) VALUES (?, ?, ?, 'related_to')`, store.NewID(), a, a)
	})
}

func TestMigrateIdempotent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		v1, err := db.SchemaVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		v2, _ := db.SchemaVersion(ctx)
		if v1 != v2 || v1 == 0 {
			t.Fatalf("versions %d -> %d", v1, v2)
		}
	})
}

func TestRefuseNewerSchema(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		mustExec(t, db, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, '9999_future.sql', ?)`, store.Now())
		err := db.Migrate(ctx)
		if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
			t.Fatalf("want refusal, got %v", err)
		}
	})
}

// TestSchemaLint (DB-022): the real schema is clean, and a deliberately bad
// table is caught on both engines.
func TestSchemaLint(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		v, err := store.LintSchema(ctx, db)
		if err != nil || len(v) != 0 {
			t.Fatalf("lint: %v %v", v, err)
		}
		bad := `CREATE TABLE bad_fixture (id TEXT PRIMARY KEY, seen_at TEXT, amount REAL)`
		mustExec(t, db, bad)
		v, _ = store.LintSchema(ctx, db)
		if len(v) != 2 {
			t.Fatalf("bad fixture must give 2 violations, got %v", v)
		}
	})
}

// TestNextTicketNumberConcurrent (M-6): concurrent allocations never collide.
func TestNextTicketNumberConcurrent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		const n = 40
		var mu sync.Mutex
		seen := map[int64]bool{}
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := db.Tx(ctx, func(tx *store.Tx) error {
					v, err := store.NextTicketNumber(ctx, tx)
					if err != nil {
						return err
					}
					mu.Lock()
					defer mu.Unlock()
					if seen[v] {
						return fmt.Errorf("duplicate number %d", v)
					}
					seen[v] = true
					return nil
				})
				if err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if len(seen) != n {
			t.Fatalf("got %d numbers", len(seen))
		}
	})
}

func TestSeededDefaults(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		rows, err := db.Read().QueryContext(ctx, `SELECT key FROM settings ORDER BY key`)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatalf("rows.Scan: %v", err)
			}
			keys = append(keys, k)
		}
		rows.Close()
		if strings.Join(keys, ",") != "branding,email,security,setup,smtp,support" { // security: 0110 (D-19 §B)
			t.Fatalf("settings keys %v", keys)
		}
		var v store.JSON[map[string]any]
		if err := db.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'branding'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v.V["product_name"] != "FeatherDesk" {
			t.Fatalf("branding %v", v.V)
		}
		var c string
		if c, err = store.Cursor(ctx, db.Read(), "dispatch_cursor"); err != nil || c != "" {
			t.Fatalf("cursor %q %v", c, err)
		}
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM sla_policies`).Scan(&n); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if n != 4 {
			t.Fatalf("sla_policies %d", n)
		}
	})
}

func TestBoolAndTimeRoundTrip(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		now := store.Now()
		id := store.NewID()
		mustExec(t, db, `INSERT INTO users (id, kind, email, role, tier, break_glass, last_login_at) VALUES (?, 'staff', ?, 'admin', 3, ?, ?)`, id, id+"@x", true, now)
		var bg bool
		var at store.NullTime
		if err := db.Read().QueryRowContext(ctx, `SELECT break_glass, last_login_at FROM users WHERE id = ?`, id).Scan(&bg, &at); err != nil {
			t.Fatal(err)
		}
		if !bg || !at.Valid || !at.Time.Equal(now) {
			t.Fatalf("got %v %v want true %v", bg, at, now)
		}
		var cnt int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM users WHERE break_glass AND id = ?`, id).Scan(&cnt); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if cnt != 1 {
			t.Fatal("boolean predicate without comparison must work on both engines")
		}
	})
}
