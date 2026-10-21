// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// cliEnv points the CLI at a fresh data directory (SQLite, local blobs).
func cliEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for k, v := range map[string]string{
		"ST_DEV": "true", "ST_DATA_DIR": dir, "ST_DATABASE_URL": "", "ST_BLOB_URL": "",
		"ST_LISTEN_ADDR": "127.0.0.1:0", "ST_LOG_FORMAT": "text", "ST_LOG_LEVEL": "warn",
		"ST_PUBLIC_URL": "http://localhost:8080", "ST_SECRET_KEY": "", "ST_BOOTSTRAP_ADMIN_EMAIL": "",
		"ST_BOOTSTRAP_ADMIN_PASSWORD": "",
	} {
		t.Setenv(k, v)
	}
	return dir
}

func closeT(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	if code := run(context.Background(), args); code != 0 {
		t.Fatalf("simpletickets %s: exit %d", strings.Join(args, " "), code)
	}
}

func sha(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func counts(t *testing.T, path string) map[string]int {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, "sqlite://"+path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeT(t, db)
	out := map[string]int{}
	for _, tbl := range []string{"users", "organizations", "teams", "tickets", "ticket_messages", "ticket_events"} {
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[tbl] = n
	}
	return out
}

func lockDB(t *testing.T, path string) (release func()) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, "sqlite://"+path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := db.LockInstance(ctx)
	if err != nil {
		closeT(t, db)
		t.Fatal(err)
	}
	return func() { unlock(); closeT(t, db) }
}

// TestRestoreRoundTrip (DB-016): seed → backup → wipe → restore gives the
// same row counts and blob sha256s; restore while the app holds the instance
// lock (store.LockInstance, as `serve` does) exits non-zero and changes nothing.
func TestRestoreRoundTrip(t *testing.T) {
	dir := cliEnv(t)
	dbPath := filepath.Join(dir, "simpletickets.db")
	mustRun(t, "seed", "--tickets", "12")
	blobPath := filepath.Join(dir, "attachments", "2026", "10", "roundtrip.bin")
	if err := os.MkdirAll(filepath.Dir(blobPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobPath, []byte(strings.Repeat("attachment bytes ", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	before, blobSum := counts(t, dbPath), sha(t, blobPath)
	if before["tickets"] != 12 {
		t.Fatalf("seeded %d tickets", before["tickets"])
	}
	out := filepath.Join(t.TempDir(), "bk")
	mustRun(t, "backup", "--out", out)
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Fatalf("backup dir entries: %v", entries)
	}
	bk := filepath.Join(out, entries[0].Name())

	// Wipe: database and files are gone.
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", filepath.Join(dir, "attachments")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(t, "restore", "--from", bk)
	if after := counts(t, dbPath); !mapsEqual(after, before) {
		t.Fatalf("row counts after restore %v, want %v", after, before)
	}
	if got := sha(t, blobPath); got != blobSum {
		t.Fatalf("blob sha256 after restore %s, want %s", got, blobSum)
	}

	// The app is running: restore must refuse, even with --force.
	dbSum := sha(t, dbPath)
	release := lockDB(t, dbPath)
	if code := run(context.Background(), []string{"restore", "--from", bk, "--force"}); code == 0 {
		t.Fatal("restore succeeded while the app holds the instance lock")
	}
	release()
	if sha(t, dbPath) != dbSum {
		t.Fatal("refused restore modified the database")
	}
	if _, err := os.Stat(dbPath + ".pre-restore"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused restore moved the database aside: %v", err)
	}
	// Stopped: --force restores and keeps the replaced file as .pre-restore.
	mustRun(t, "restore", "--from", bk, "--force")
	if _, err := os.Stat(dbPath + ".pre-restore"); err != nil {
		t.Fatalf("previous database not kept: %v", err)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestMigrateEngineRefusesWhileRunning (DB-031): migrate-engine is offline;
// it refuses while the source or the target is locked by a running app.
func TestMigrateEngineRefusesWhileRunning(t *testing.T) {
	cliEnv(t)
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "src.db"), filepath.Join(tmp, "dst.db")
	t.Setenv("ST_DATABASE_URL", "sqlite://"+src)
	mustRun(t, "seed", "--tickets", "5")
	args := []string{"migrate-engine", "--from", "sqlite://" + src, "--to", "sqlite://" + dst}
	for _, locked := range []string{src, dst} {
		release := lockDB(t, locked)
		if code := run(context.Background(), args); code == 0 {
			release()
			t.Fatalf("migrate-engine ran while %s was locked", filepath.Base(locked))
		}
		release()
	}
	// lockDB opened (created) dst; a refused run must not have migrated or filled it.
	ctx := context.Background()
	ddb, err := store.Open(ctx, "sqlite://"+dst, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if pend, err := ddb.PendingMigrations(ctx); err != nil || len(pend) == 0 {
		closeT(t, ddb)
		t.Fatalf("refused migrate-engine touched the target schema: pending=%d err=%v", len(pend), err)
	}
	closeT(t, ddb)
	t.Setenv("ST_DATABASE_URL", "sqlite://"+dst) // the documented flow: migrate the target, then copy
	mustRun(t, "migrate")
	mustRun(t, args...)
	if c := counts(t, dst); c["tickets"] != 5 {
		t.Fatalf("migrate-engine copied %d tickets, want 5", c["tickets"])
	}
}

// TestServeRefusesWhileLocked: `serve` takes the single-instance lock before
// it touches the database (DB-024 wiring).
func TestServeRefusesWhileLocked(t *testing.T) {
	dir := cliEnv(t)
	mustRun(t, "migrate")
	release := lockDB(t, filepath.Join(dir, "simpletickets.db"))
	defer release()
	if code := run(context.Background(), []string{"serve"}); code == 0 {
		t.Fatal("serve started while another instance holds the lock")
	}
}

// TestServeBootWiring: serve writes the pre-migrate backup when a migration is
// pending, migrates, serves, and releases the instance lock on shutdown.
func TestServeBootWiring(t *testing.T) {
	dir := cliEnv(t)
	dbPath := filepath.Join(dir, "simpletickets.db")
	mustRun(t, "seed", "--tickets", "3")
	ctx := context.Background()
	db, err := store.Open(ctx, "sqlite://"+dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DROP INDEX users_email_lower_uq`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 2`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	closeT(t, db)

	sctx, cancel := context.WithCancel(ctx)
	done := make(chan int, 1)
	go func() { done <- run(sctx, []string{"serve"}) }()
	backups := filepath.Join(dir, "backups")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if ents, _ := os.ReadDir(backups); len(ents) == 1 && strings.HasPrefix(ents[0].Name(), store.PreMigrateBackupPrefix) && !strings.HasSuffix(ents[0].Name(), ".tmp") {
			break
		}
		select {
		case code := <-done:
			cancel()
			t.Fatalf("serve exited early with %d", code)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no pre-migrate backup written")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Serving: the instance lock is held.
	time.Sleep(200 * time.Millisecond)
	if _, err := store.LockSQLiteFile(dbPath); !errors.Is(err, store.ErrInstanceRunning) {
		cancel()
		t.Fatalf("lock while serving: %v", err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("serve exit %d after SIGTERM-equivalent", code)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("serve did not stop")
	}
	r, err := store.LockSQLiteFile(dbPath)
	if err != nil {
		t.Fatalf("lock not released on shutdown: %v", err)
	}
	r()
	db, err = store.Open(ctx, "sqlite://"+dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeT(t, db)
	if pend, err := db.PendingMigrations(ctx); err != nil || len(pend) != 0 {
		t.Fatalf("pending after serve: %v %v", pend, err)
	}
}

// TestBootRefusesCorruptDB (DB-017): serve runs quick_check before migrating
// and exits non-zero with a clear message on a corrupted SQLite file, without
// writing to it.
func TestBootRefusesCorruptDB(t *testing.T) {
	dir := cliEnv(t)
	dbPath := filepath.Join(dir, "simpletickets.db")
	mustRun(t, "seed", "--tickets", "60")
	b, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 64<<10 {
		t.Fatalf("database too small to corrupt meaningfully: %d bytes", len(b))
	}
	// Flip bytes inside b-tree pages past the schema page.
	for i := 32 << 10; i < 48<<10; i++ {
		b[i] ^= 0xFF
	}
	if err := os.WriteFile(dbPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha(t, dbPath)
	if code := run(context.Background(), []string{"serve"}); code != 1 {
		t.Fatalf("serve on a corrupt database: exit %d, want 1", code)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Build(context.Background(), cfg, app.BuildOptions{LockInstance: true})
	if err == nil {
		t.Fatal("app.Build accepted a corrupt database")
	}
	msg := err.Error()
	if !strings.Contains(msg, "database check failed (quick_check)") && !strings.Contains(msg, "malformed") && !strings.Contains(msg, "not a database") {
		t.Fatalf("unclear refusal: %v", err)
	}
	if !strings.Contains(msg, "restore") && !strings.Contains(msg, "malformed") && !strings.Contains(msg, "not a database") {
		t.Fatalf("refusal does not point at a restore: %v", err)
	}
	if sha(t, dbPath) != sum {
		t.Fatal("refused boot wrote to the corrupt database")
	}
}

// TestBootRefusesPostgres15 (DB-017): serve refuses a Postgres older than 16.
// Needs ST_TEST_POSTGRES15_URL (a postgres:15 container); required in CI.
func TestBootRefusesPostgres15(t *testing.T) {
	u := os.Getenv("ST_TEST_POSTGRES15_URL")
	if u == "" {
		if os.Getenv("ST_REQUIRE_POSTGRES15") != "" {
			t.Fatal("ST_TEST_POSTGRES15_URL is required")
		}
		t.Skip("ST_TEST_POSTGRES15_URL not set")
	}
	cliEnv(t)
	t.Setenv("ST_DATABASE_URL", u)
	if code := run(context.Background(), []string{"serve"}); code != 1 {
		t.Fatalf("serve on Postgres 15: exit %d, want 1", code)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Build(context.Background(), cfg, app.BuildOptions{LockInstance: true})
	if err == nil || !strings.Contains(err.Error(), "Postgres 15.") || !strings.Contains(err.Error(), "needs Postgres 16 or newer") {
		t.Fatalf("refusal = %v", err)
	}
}

// TestRotateSecretsCLI (PLT-019): rotate-secrets needs the old key as
// ST_SECRET_KEY_PREVIOUS and succeeds on a migrated database with it.
func TestRotateSecretsCLI(t *testing.T) {
	cliEnv(t)
	t.Setenv("ST_SECRET_KEY", strings.Repeat("n", 32))
	mustRun(t, "migrate")
	if code := run(context.Background(), []string{"rotate-secrets"}); code == 0 {
		t.Fatal("rotate-secrets ran without ST_SECRET_KEY_PREVIOUS")
	}
	t.Setenv("ST_SECRET_KEY_PREVIOUS", strings.Repeat("o", 32))
	mustRun(t, "rotate-secrets")
}

// TestMigrateEngineSearchSameHits (DB-031): after `migrate-engine` SQLite →
// Postgres and `reindex` on the target, search returns the same tickets for
// the same queries (staff and customer audiences).
func TestMigrateEngineSearchSameHits(t *testing.T) {
	if storetest.PostgresURL() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("ST_TEST_POSTGRES_URL is required in CI")
		}
		t.Skip("needs postgres")
	}
	ctx := context.Background()
	cliEnv(t)
	src := "sqlite://" + filepath.Join(t.TempDir(), "src.db")
	pg := storetest.SchemaURL(t)
	t.Setenv("ST_DATABASE_URL", src)
	mustRun(t, "seed", "--tickets", "40")
	mustRun(t, "reindex")
	t.Setenv("ST_DATABASE_URL", pg)
	mustRun(t, "migrate")
	mustRun(t, "migrate-engine", "--from", src, "--to", pg)
	mustRun(t, "reindex")

	sdb, err := store.Open(ctx, src, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeT(t, sdb)
	pdb, err := store.Open(ctx, pg, store.Options{PoolSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer closeT(t, pdb)

	// Query terms: words of the seeded subjects (plus a prefix and a miss).
	rows, err := sdb.Read().QueryContext(ctx, `SELECT subject FROM tickets ORDER BY number LIMIT 12`)
	if err != nil {
		t.Fatal(err)
	}
	terms := map[string]bool{"zzznomatch": true}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		for _, w := range strings.Fields(s) {
			if w = strings.Trim(strings.ToLower(w), ".,:;!?()\"'"); len(w) >= 4 {
				terms[w] = true
				terms[w[:3]] = true
			}
		}
	}
	rows.Close()
	hits := func(db *store.DB, q string, staff bool) []string {
		sub, args, ok := store.TicketMatch(db.Dialect, q, staff)
		if !ok {
			return nil
		}
		r, err := db.Read().QueryContext(ctx, `SELECT id FROM tickets WHERE id IN (`+sub+`) ORDER BY id`, args...)
		if err != nil {
			t.Fatalf("%s %q: %v", db.Dialect, q, err)
		}
		defer r.Close()
		var ids []string
		for r.Next() {
			var id string
			if err := r.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	matched := 0
	for q := range terms {
		for _, staff := range []bool{true, false} {
			a, b := hits(sdb, q, staff), hits(pdb, q, staff)
			if strings.Join(a, ",") != strings.Join(b, ",") {
				t.Errorf("query %q (staff=%t): sqlite %d hits, postgres %d hits", q, staff, len(a), len(b))
			}
			if len(a) > 0 {
				matched++
			}
		}
	}
	if matched < 5 {
		t.Fatalf("only %d queries matched anything; fixture too thin", matched)
	}
}
