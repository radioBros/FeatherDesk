// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestBackupRestoreSQLite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "featherdesk.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	db, err := store.Open(ctx, "sqlite://"+dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cust := storetest.CreateCustomer(t, db, "")
	storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Subject: "Backed up"})
	blobRoot := filepath.Join(dir, "data")
	if err := os.MkdirAll(filepath.Join(blobRoot, "attachments/2026/10"), 0o700); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobRoot, "attachments/2026/10/a1"), []byte("attachment bytes"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	res, err := store.Backup(ctx, db, store.BackupOptions{OutDir: filepath.Join(dir, "backups"), BlobRoot: blobRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	if len(res.Manifest.Files) != 2 || res.Manifest.Engine != "sqlite" || res.Manifest.SchemaVersion == 0 {
		t.Fatalf("manifest %+v", res.Manifest)
	}

	// Restore on a "fresh host".
	fresh := filepath.Join(dir, "fresh")
	newDB := filepath.Join(fresh, "featherdesk.db")
	if _, err := store.Restore(ctx, store.RestoreOptions{BackupDir: res.Dir, DBPath: newDB, BlobRoot: fresh}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(fresh, "attachments/2026/10/a1"))
	if err != nil || string(b) != "attachment bytes" {
		t.Fatalf("blob not restored: %q %v", b, err)
	}
	rdb, err := store.Open(ctx, "sqlite://"+newDB, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			t.Errorf("rdb.Close: %v", err)
		}
	}()
	if err := store.IntegrityCheck(ctx, rdb); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var subj string
	if err := rdb.Read().QueryRowContext(ctx, `SELECT subject FROM tickets`).Scan(&subj); err != nil || subj != "Backed up" {
		t.Fatalf("restored data: %q %v", subj, err)
	}

	// Refuses to overwrite without force; detects tampering.
	if _, err := store.Restore(ctx, store.RestoreOptions{BackupDir: res.Dir, DBPath: newDB}); err == nil {
		t.Fatal("overwrote an existing DB without force")
	}
	if err := os.WriteFile(filepath.Join(res.Dir, "featherdesk.db"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	if _, err := store.Restore(ctx, store.RestoreOptions{BackupDir: res.Dir, DBPath: filepath.Join(dir, "x.db")}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampering not detected: %v", err)
	}
}

// TestRestoreSimpleTicketsBackup: a backup written by SimpleTickets 1.0.0
// (database file simpletickets.db) restores with FeatherDesk.
func TestRestoreSimpleTicketsBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(ctx, "sqlite://"+filepath.Join(dir, "src.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	storetest.CreateTicket(t, db, storetest.Ticket{Requester: storetest.CreateCustomer(t, db, ""), Subject: "From SimpleTickets"})
	res, err := store.Backup(ctx, db, store.BackupOptions{OutDir: filepath.Join(dir, "backups")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Rewrite it into the SimpleTickets layout: same bytes, old file name.
	if err := os.Rename(filepath.Join(res.Dir, "featherdesk.db"), filepath.Join(res.Dir, "simpletickets.db")); err != nil {
		t.Fatal(err)
	}
	mp := filepath.Join(res.Dir, "manifest.json")
	raw, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	files := m["files"].(map[string]any)
	files["simpletickets.db"] = files["featherdesk.db"]
	delete(files, "featherdesk.db")
	if raw, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "fresh", "featherdesk.db")
	if _, err := store.Restore(ctx, store.RestoreOptions{BackupDir: res.Dir, DBPath: target}); err != nil {
		t.Fatalf("restore a SimpleTickets backup: %v", err)
	}
	rdb, err := store.Open(ctx, "sqlite://"+target, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rdb.Close() }()
	var subj string
	if err := rdb.Read().QueryRowContext(ctx, `SELECT subject FROM tickets`).Scan(&subj); err != nil || subj != "From SimpleTickets" {
		t.Fatalf("restored data: %q %v", subj, err)
	}
}

func TestBackupPostgresHint(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs postgres")
	}
	db := storetest.Open(t, store.Postgres)
	res, err := store.Backup(ctx, db, store.BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Manifest.PGDumpHint, "pg_dump") || strings.Contains(res.Manifest.PGDumpHint, ":st@") || !res.Manifest.BlobsSkipped {
		t.Fatalf("manifest %+v", res.Manifest)
	}
}

func TestIntegrityCheck(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		if err := store.IntegrityCheck(ctx, db); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPrune(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		now := time.Now().UTC()
		old, fresh := now.Add(-40*24*time.Hour), now.Add(time.Hour)
		user := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
		for i := 0; i < 25; i++ {
			mustExec(t, db, `INSERT INTO sessions (id_hash, user_id, audience, csrf_token, expires_at) VALUES (?, ?, 'staff', 'x', ?)`, store.NewID(), user, old)
		}
		mustExec(t, db, `INSERT INTO sessions (id_hash, user_id, audience, csrf_token, expires_at) VALUES (?, ?, 'staff', 'x', ?)`, "live", user, fresh)
		mustExec(t, db, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, revoked_at) VALUES ('r1', ?, 'n', 'p', 'h1', ?)`, user, old)
		mustExec(t, db, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, revoked_at) VALUES ('r2', ?, 'n', 'p', 'h2', ?)`, user, now)
		mustExec(t, db, `INSERT INTO auth_tokens (id, user_id, purpose, token_hash, expires_at, used_at) VALUES ('t1', ?, 'reset_password', 'th1', ?, ?)`, user, fresh, old)
		mustExec(t, db, `INSERT INTO deferred_jobs (id, kind, next_run_at, status, updated_at) VALUES ('j1', 'mail', ?, 'dead', ?)`, old, old)
		mustExec(t, db, `INSERT INTO deferred_jobs (id, kind, next_run_at, status, updated_at) VALUES ('j2', 'mail', ?, 'retrying', ?)`, old, old)
		mustExec(t, db, `INSERT INTO idempotency_keys (principal_id, key, request_hash, created_at) VALUES ('p', 'k', 'h', ?)`, old)
		mustExec(t, db, `INSERT INTO attachments (id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at) VALUES ('d1', ?, 'f', 'text/plain', 1, 's', 'attachments/x/d1', ?)`, user, old)
		mustExec(t, db, `INSERT INTO attachments (id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at) VALUES ('d2', ?, 'f', 'text/plain', 1, 's', 'attachments/x/d2', ?)`, user, now)
		mustExec(t, db, `INSERT INTO audit_log (id, action, created_at) VALUES ('al1', 'x', ?)`, now.Add(-400*24*time.Hour))
		mustExec(t, db, `INSERT INTO audit_log (id, action, created_at) VALUES ('al2', 'x', ?)`, now)

		store.PruneBatch = 10
		defer func() { store.PruneBatch = 1000 }()
		var maxTx int64
		store.SetPruneTxHook(func(_ string, n int64) { maxTx = max(maxTx, n) })
		defer store.SetPruneTxHook(nil)

		check := func(name string, n int64, err error, want int64) {
			t.Helper()
			if err != nil || n != want {
				t.Errorf("%s: deleted %d (%v), want %d", name, n, err, want)
			}
		}
		n, err := store.PruneSessions(ctx, db, now)
		check("sessions", n, err, 25)
		if maxTx > 10 {
			t.Errorf("a prune transaction deleted %d rows > batch 10", maxTx)
		}
		n, err = store.PruneAPITokens(ctx, db, now)
		check("api_tokens", n, err, 1)
		n, err = store.PruneAuthTokens(ctx, db, now)
		check("auth_tokens", n, err, 1)
		n, err = store.PruneDeadJobs(ctx, db, now)
		check("dead jobs", n, err, 1)
		n, err = store.PruneIdempotencyKeys(ctx, db, now)
		check("idempotency", n, err, 1)
		n, keys, err := store.PruneOrphanAttachments(ctx, db, now)
		check("orphans", n, err, 1)
		if len(keys) != 1 || keys[0] != "attachments/x/d1" {
			t.Errorf("orphan keys %v", keys)
		}
		n, err = store.PruneAuditLog(ctx, db, now, 0)
		check("audit", n, err, 1)
		var left int
		if err := db.Read().QueryRowContext(context.Background(), `SELECT count(*) FROM sessions`).Scan(&left); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if left != 1 {
			t.Errorf("sessions left %d", left)
		}
		// Prunes are idempotent.
		if n, _ := store.PruneSessions(ctx, db, now); n != 0 {
			t.Errorf("second prune deleted %d", n)
		}
	})
}
