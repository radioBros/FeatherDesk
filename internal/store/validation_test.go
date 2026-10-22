// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestPragmasPerConnection (DB-001): every pooled connection carries the pragmas.
func TestPragmasPerConnection(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	check := func(name string, q store.Querier) {
		t.Helper()
		want := map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "cache_size": "-2000", "synchronous": "1"}
		for p, w := range want {
			var got string
			if err := q.QueryRowContext(ctx, `PRAGMA `+p).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != w {
				t.Errorf("%s: PRAGMA %s = %s, want %s", name, p, got, w)
			}
		}
	}
	for i := 0; i < 2; i++ {
		check("reader", db.Read())
	}
	if err := db.Tx(ctx, func(tx *store.Tx) error { check("writer", tx); return nil }); err != nil {
		t.Fatalf("db.Tx: %v", err)
	}
}

// TestReaderIsQueryOnly (DB-001): the reader pool cannot write.
func TestReaderIsQueryOnly(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	if _, err := db.Read().ExecContext(ctx, `INSERT INTO tags (id, name) VALUES ('x', 'x')`); err == nil {
		t.Fatal("reader pool accepted a write")
	}
}

// TestConcurrentWritersSerialise (DB-001): 50 concurrent writers, 0 SQLITE_BUSY.
func TestConcurrentWritersSerialise(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		var wg sync.WaitGroup
		var fails atomic.Int32
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				err := db.Tx(ctx, func(tx *store.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO tags (id, name) VALUES (?, ?)`, store.NewID(), "tag-"+store.NewID())
					return err
				})
				if err != nil {
					t.Log(err)
					fails.Add(1)
				}
			}(i)
		}
		wg.Wait()
		if fails.Load() != 0 {
			t.Fatalf("%d concurrent writes failed", fails.Load())
		}
	})
}

// TestTimeRoundTrip (DB-003): random UTC instants (µs precision, incl. pre-1970)
// read back Equal, and ORDER BY on the column is chronological, on both engines.
func TestTimeRoundTrip(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		r := rand.New(rand.NewSource(1))
		want := map[string]time.Time{}
		err := db.Tx(ctx, func(tx *store.Tx) error {
			for i := 0; i < 1000; i++ {
				sec := r.Int63n(4102444800+2208988800) - 2208988800 // 1900 … 2100
				ts := time.Unix(sec, r.Int63n(1e9)).UTC().Truncate(time.Microsecond)
				id := store.NewID()
				want[id] = ts
				if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, action, created_at) VALUES (?, 't', ?)`, id, ts); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Read().QueryContext(ctx, `SELECT id, created_at FROM audit_log ORDER BY created_at`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var prev time.Time
		n := 0
		for rows.Next() {
			var id string
			var got time.Time
			if err := rows.Scan(&id, &got); err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want[id]) {
				t.Fatalf("%s: got %v want %v", id, got, want[id])
			}
			if got.Before(prev) {
				t.Fatalf("ORDER BY not chronological: %v after %v", got, prev)
			}
			prev = got
			n++
		}
		if n != 1000 {
			t.Fatalf("read %d rows", n)
		}
	})
}

// TestFTS5Available (DB-004): FTS5 works in the pinned modernc build and folds diacritics.
func TestFTS5Available(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	cust := storetest.CreateCustomer(t, db, "")
	id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		return store.IndexTicket(ctx, tx, store.TicketDoc{TicketID: id, Public: "Non funziona, perché?"})
	}); err != nil {
		t.Fatal(err)
	}
	if got := matchIDs(t, db, "perche", false); len(got) != 1 {
		t.Fatalf("diacritic folding: %v", got)
	}
}

// TestInvariantM3M4: requester must be a customer, assignee must be staff.
func TestInvariantM3M4(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		staff := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
		cust := storetest.CreateCustomer(t, db, "")
		expectErr(t, db, "must be a customer", `INSERT INTO tickets (id, number, subject, requester_id, channel) VALUES ('t1', 1, 's', ?, 'portal')`, staff)
		expectErr(t, db, "must be staff", `INSERT INTO tickets (id, number, subject, requester_id, assignee_id, channel) VALUES ('t2', 2, 's', ?, ?, 'portal')`, cust, cust)
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, AssigneeID: staff})
		expectErr(t, db, "must be staff", `UPDATE tickets SET assignee_id = ? WHERE id = ?`, cust, id)
		expectErr(t, db, "must be a customer", `UPDATE tickets SET requester_id = ? WHERE id = ?`, staff, id)
	})
}

func explain(t *testing.T, db *store.DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.Read().QueryContext(ctx, `EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("rows.Scan: %v", err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, " | ")
}

// TestListShapesUseIndexes (DB-005, DB-007, DB-032): the main list shapes never full-scan.
func TestListShapesUseIndexes(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	shapes := map[string]string{
		"agent queue":   `SELECT id FROM tickets WHERE team_id = 'x' AND status IN ('new','open') ORDER BY updated_at DESC LIMIT 50`,
		"my tickets":    `SELECT id FROM tickets WHERE assignee_id = 'x' AND status = 'open' LIMIT 50`,
		"portal own":    `SELECT id FROM tickets WHERE requester_id = 'x' ORDER BY updated_at DESC LIMIT 50`,
		"portal org":    `SELECT id FROM tickets WHERE organization_id = 'x' ORDER BY updated_at DESC LIMIT 50`,
		"deferred jobs": `SELECT id FROM deferred_jobs WHERE status = 'retrying' AND next_run_at <= '2026-01-01' ORDER BY next_run_at LIMIT 100`,
		"cf filter":     `SELECT ticket_id FROM ticket_field_values WHERE field_id = 'f' AND value_text = 'gold'`,
		"timeline":      `SELECT id FROM ticket_events WHERE ticket_id = 'x' ORDER BY id`,
		"dispatcher":    `SELECT id FROM ticket_events WHERE id > 'x' ORDER BY id LIMIT 200`,
	}
	for name, q := range shapes {
		plan := explain(t, db, q)
		for _, step := range strings.Split(plan, " | ") {
			if strings.HasPrefix(step, "SCAN ") && !strings.Contains(step, "USING") {
				t.Errorf("%s does a full scan: %s", name, plan)
			}
		}
	}
	if !strings.Contains(explain(t, db, shapes["deferred jobs"]), "deferred_jobs_status_next_run_idx") {
		t.Error("deferred job pickup does not use deferred_jobs_status_next_run_idx")
	}
}

// TestTeamNotifyModeCheck, TestCalendarModeCheck, TestDeleteCalendarFallsBackTo24x7 (DB-006, DB-033).
func TestSupportConfigChecks(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		expectErr(t, db, "", `INSERT INTO teams (id, name, notify_mode) VALUES ('t', 'T', 'pigeon')`)
		expectErr(t, db, "", `UPDATE sla_policies SET calendar_mode = 'moon' WHERE priority = 'low'`)
		mustExec(t, db, `INSERT INTO business_calendars (id, name, timezone) VALUES ('cal', 'Office', 'Europe/Rome')`)
		mustExec(t, db, `INSERT INTO teams (id, name, calendar_id) VALUES ('t2', 'T2', 'cal')`)
		mustExec(t, db, `DELETE FROM business_calendars WHERE id = 'cal'`)
		var cal store.NullString
		if err := db.Read().QueryRowContext(ctx, `SELECT calendar_id FROM teams WHERE id = 't2'`).Scan(&cal); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if cal.Valid {
			t.Fatalf("team still points at a deleted calendar: %v", cal)
		}
	})
}

// TestNoJobsTable (DB-007): D-11 removed the DB-polled queue.
func TestNoJobsTable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		tables, _ := store.Tables(ctx, db)
		for _, n := range tables {
			if n == "jobs" {
				t.Fatal("jobs table exists")
			}
		}
	})
}

// TestWatcherCustomerSameOrg (DB-008).
func TestWatcherCustomerSameOrg(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		orgA, orgB := storetest.CreateOrg(t, db, ""), storetest.CreateOrg(t, db, "")
		req := storetest.CreateCustomer(t, db, orgA)
		colleague := storetest.CreateCustomer(t, db, orgA)
		stranger := storetest.CreateCustomer(t, db, orgB)
		agent := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
		tk := storetest.CreateTicket(t, db, storetest.Ticket{Requester: req, OrgID: orgA})
		mustExec(t, db, `INSERT INTO ticket_watchers (ticket_id, user_id) VALUES (?, ?)`, tk, colleague)
		mustExec(t, db, `INSERT INTO ticket_watchers (ticket_id, user_id) VALUES (?, ?)`, tk, agent)
		expectErr(t, db, "ticket organization", `INSERT INTO ticket_watchers (ticket_id, user_id) VALUES (?, ?)`, tk, stranger)
	})
}

// TestTicketNumbersUniqueUnderConcurrency (DB-012): 200 parallel creates → 200
// distinct, gap-free numbers on both engines.
func TestTicketNumbersUniqueUnderConcurrency(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		cust := storetest.CreateCustomer(t, db, "")
		var wg sync.WaitGroup
		errs := make(chan error, 200)
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- db.Tx(ctx, func(tx *store.Tx) error {
					n, err := store.NextTicketNumber(ctx, tx)
					if err != nil {
						return err
					}
					_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, number, subject, requester_id, channel) VALUES (?, ?, 's', ?, 'api')`, store.NewID(), n, cust)
					return err
				})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var cnt, lo, hi int64
		if err := db.Read().QueryRowContext(ctx, `SELECT count(DISTINCT number), min(number), max(number) FROM tickets`).Scan(&cnt, &lo, &hi); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if cnt != 200 || hi-lo != 199 {
			t.Fatalf("distinct=%d range=%d..%d", cnt, lo, hi)
		}
	})
}

// TestBackupWhileWriting (DB-015): a backup taken during concurrent writes is
// a consistent, healthy snapshot.
func TestBackupWhileWriting(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(ctx, "sqlite://"+filepath.Join(dir, "live.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	var before int
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO tags (id, name) VALUES (?, ?)`, store.NewID(), store.NewID())
				return err
			}); err != nil {
				t.Errorf("db.Tx: %v", err)
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tags`).Scan(&before); err != nil {
		t.Fatalf("db.Read: %v", err)
	}
	res, err := store.Backup(ctx, db, store.BackupOptions{OutDir: filepath.Join(dir, "bk")})
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := store.Open(ctx, "sqlite://"+filepath.Join(res.Dir, "featherdesk.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := snap.Close(); err != nil {
			t.Errorf("snap.Close: %v", err)
		}
	}()
	if err := store.IntegrityCheck(ctx, snap); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := snap.Read().QueryRowContext(ctx, `SELECT count(*) FROM tags`).Scan(&after); err != nil {
		t.Fatalf("snap.Read: %v", err)
	}
	if after < before {
		t.Fatalf("snapshot has %d rows, fewer than the %d present before the backup", after, before)
	}
}

// TestCorruptDBDetected (DB-017): flipped bytes fail the integrity check (or the open).
func TestCorruptDBDetected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.db")
	db, _ := store.Open(ctx, "sqlite://"+p, store.Options{})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	cust := storetest.CreateCustomer(t, db, "")
	for i := 0; i < 50; i++ {
		storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: strings.Repeat("x", 150)})
	}
	if _, err := db.W.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("db.W.ExecContext: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	b, _ := os.ReadFile(p)
	for i := 8192; i < len(b) && i < 8192+4096; i++ {
		b[i] ^= 0xFF
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	db2, err := store.Open(ctx, "sqlite://"+p, store.Options{})
	if err != nil {
		return // refused at open: also a clear failure
	}
	defer func() {
		if err := db2.Close(); err != nil {
			t.Errorf("db2.Close: %v", err)
		}
	}()
	if err := store.IntegrityCheck(ctx, db2); err == nil {
		t.Fatal("corruption not detected")
	}
}

// TestPruneV1Tables (DB-019): inbound log keeps quarantined rows; old deliveries go.
func TestPruneV1Tables(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		now := time.Now().UTC()
		old := now.Add(-100 * 24 * time.Hour)
		mustExec(t, db, `INSERT INTO inbound_mailboxes (id, name, kind) VALUES ('mb', 'Support', 'imap')`)
		mustExec(t, db, `INSERT INTO inbound_messages (id, mailbox_id, message_id, status, raw_blob_key, received_at) VALUES ('m1', 'mb', '<1@x>', 'processed', 'inbound/a', ?)`, old)
		mustExec(t, db, `INSERT INTO inbound_messages (id, mailbox_id, message_id, status, raw_blob_key, received_at) VALUES ('m2', 'mb', '<2@x>', 'quarantined', 'inbound/b', ?)`, old)
		mustExec(t, db, `INSERT INTO webhooks (id, url, secret_enc) VALUES ('w', 'https://x', 'enc')`)
		mustExec(t, db, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, created_at) VALUES ('d1', 'w', 'e', 't', ?)`, old)
		mustExec(t, db, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, created_at) VALUES ('d2', 'w', 'e', 't', ?)`, now)
		n, keys, err := store.PruneInboundMessages(ctx, db, now)
		if err != nil || n != 1 || len(keys) != 1 || keys[0] != "inbound/a" {
			t.Fatalf("inbound prune %d %v %v", n, keys, err)
		}
		if n, err := store.PruneWebhookDeliveries(ctx, db, now); err != nil || n != 1 {
			t.Fatalf("deliveries prune %d %v", n, err)
		}
	})
}

// TestInboundMessageProcessedOnce (DB-034): the same Message-ID wins once.
func TestInboundMessageProcessedOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		mustExec(t, db, `INSERT INTO inbound_mailboxes (id, name, kind) VALUES ('mb', 'Support', 'imap')`)
		var wg sync.WaitGroup
		var ok, dup atomic.Int32
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := db.Tx(ctx, func(tx *store.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO inbound_messages (id, mailbox_id, message_id, status) VALUES (?, 'mb', '<same@x>', 'processed')`, store.NewID())
					return err
				})
				if err == nil {
					ok.Add(1)
				} else if store.IsUniqueViolation(err) {
					dup.Add(1)
				} else {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if ok.Load() != 1 || dup.Load() != 1 {
			t.Fatalf("ok=%d dup=%d", ok.Load(), dup.Load())
		}
	})
}

// TestEngineSelectedFromURL (DB-024).
func TestEngineSelectedFromURL(t *testing.T) {
	db, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "x.db"), store.Options{})
	if err != nil || db.Dialect != store.SQLite {
		t.Fatalf("%v %v", db, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	for _, bad := range []string{"mysql://u:secret@h/db", "redis://x"} {
		_, err := store.Open(ctx, bad, store.Options{})
		if err == nil || !strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	if storetest.PostgresURL() != "" {
		pg := storetest.Open(t, store.Postgres)
		if pg.Dialect != store.Postgres {
			t.Fatal("postgres URL did not select postgres")
		}
	}
}

// TestPostCommitHookNotRunOnRollback (DB-024).
func TestPostCommitHookNotRunOnRollback(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		var commits, after atomic.Int32
		db.OnCommit(func() { commits.Add(1) })
		boom := errors.New("boom")
		err := db.Tx(ctx, func(tx *store.Tx) error {
			tx.AfterCommit(func() { after.Add(1) })
			return boom
		})
		if !errors.Is(err, boom) || commits.Load() != 0 || after.Load() != 0 {
			t.Fatalf("rollback ran hooks: commits=%d after=%d", commits.Load(), after.Load())
		}
		if err := db.Tx(ctx, func(tx *store.Tx) error { tx.AfterCommit(func() { after.Add(1) }); return nil }); err != nil {
			t.Fatalf("db.Tx: %v", err)
		}
		if commits.Load() != 1 || after.Load() != 1 {
			t.Fatalf("commit hooks: %d %d", commits.Load(), after.Load())
		}
	})
}

// TestSecondInstanceRefused (DB-024): one app process per database.
func TestSecondInstanceRefused(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		release, err := db.LockInstance(ctx)
		if err != nil {
			t.Fatal(err)
		}
		other := db
		if db.Dialect == store.SQLite {
			var err error
			other, err = store.Open(ctx, db.URL, store.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := other.Close(); err != nil {
					t.Errorf("other.Close: %v", err)
				}
			}()
		}
		if _, err := other.LockInstance(ctx); !errors.Is(err, store.ErrInstanceRunning) {
			t.Fatalf("second lock: %v", err)
		}
		release()
		r2, err := other.LockInstance(context.Background())
		if err != nil {
			t.Fatalf("lock after release: %v", err)
		}
		r2()
	})
}

// TestInstanceLockCoversSimpleTickets: on Postgres, a SimpleTickets 1.0.0
// process still holding its own lock key keeps FeatherDesk from starting.
func TestInstanceLockCoversSimpleTickets(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs postgres")
	}
	db := storetest.Open(t, store.Postgres)
	conn, err := db.W.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var schema string
	if err := conn.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	h := fnv.New64a()
	h.Write([]byte("simpletickets-instance" + schema))
	key := int64(h.Sum64() >> 1)
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LockInstance(ctx); !errors.Is(err, store.ErrInstanceRunning) {
		t.Fatalf("lock while SimpleTickets runs: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
	release, err := db.LockInstance(ctx)
	if err != nil {
		t.Fatalf("lock after SimpleTickets stopped: %v", err)
	}
	release()
}

// TestMigrateEngineRoundTrip (DB-031): demo seed SQLite → Postgres → SQLite.
func TestMigrateEngineRoundTrip(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs postgres")
	}
	src := storetest.Open(t, store.SQLite)
	if _, err := store.SeedDemo(ctx, src, store.SeedOptions{Tickets: 40}); err != nil {
		t.Fatal(err)
	}
	pg := storetest.Open(t, store.Postgres)
	r1, err := store.MigrateEngine(ctx, src, pg)
	if err != nil {
		t.Fatal(err)
	}
	back := storetest.Open(t, store.SQLite)
	r2, err := store.MigrateEngine(ctx, pg, back)
	if err != nil {
		t.Fatal(err)
	}
	for i := range r1.Tables {
		if r1.Tables[i] != r2.Tables[i] {
			t.Fatalf("%s differs after round trip: %+v vs %+v", r1.Tables[i].Table, r1.Tables[i], r2.Tables[i])
		}
	}
}
