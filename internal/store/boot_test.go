// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// makeLastIdentityMigrationPending un-applies migration 0002 (users_email_ci),
// the way a database written by an older release looks to a newer binary.
func makeLastIdentityMigrationPending(t *testing.T, db *store.DB) {
	t.Helper()
	err := db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DROP INDEX users_email_lower_uq`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 2`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func preMigrateFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), store.PreMigrateBackupPrefix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TestPreMigrateBackupRetention (DB-015): before `serve` applies pending
// migrations to an existing SQLite database it writes a VACUUM INTO copy
// that passes the integrity check; only the newest 3 are kept. Nothing is
// written for a fresh database, an up-to-date one, or Postgres.
func TestPreMigrateBackupRetention(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		dir := filepath.Join(t.TempDir(), "backups")
		if p, err := store.PreMigrateBackup(ctx, db, dir, 3, time.Now()); err != nil || p != "" {
			t.Fatalf("up-to-date database: backup %q, %v", p, err)
		}
		storetest.CreateCustomer(t, db, "")
		base := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
		var written []string
		for i := 0; i < 5; i++ {
			makeLastIdentityMigrationPending(t, db)
			p, err := store.PreMigrateBackup(ctx, db, dir, 3, base.Add(time.Duration(i)*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if db.Dialect == store.Postgres {
				if p != "" {
					t.Fatalf("postgres: unexpected pre-migrate backup %s", p)
				}
			} else {
				if p == "" {
					t.Fatalf("round %d: no backup although migration 0002 is pending", i)
				}
				written = append(written, filepath.Base(p))
			}
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if pend, err := db.PendingMigrations(ctx); err != nil || len(pend) != 0 {
				t.Fatalf("pending after migrate: %v %v", pend, err)
			}
		}
		got := preMigrateFiles(t, dir)
		if db.Dialect == store.Postgres {
			if len(got) != 0 {
				t.Fatalf("postgres wrote %v", got)
			}
			return
		}
		if len(got) != 3 {
			t.Fatalf("kept %d pre-migrate backups (%v), want exactly 3", len(got), got)
		}
		want := append([]string(nil), written[2:]...)
		sort.Strings(want)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("kept %v, want the newest three %v", got, want)
			}
		}
		// The newest copy is a valid database at the pre-migration version.
		snap, err := store.Open(ctx, "sqlite://"+filepath.Join(dir, written[4]), store.Options{})
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
		if pend, err := snap.PendingMigrations(ctx); err != nil || len(pend) != 1 || pend[0].Version != 2 {
			t.Fatalf("snapshot pending migrations = %v, %v; want [0002]", pend, err)
		}
		if lb := store.LastBackup(dir); lb.Before(mtime(t, filepath.Join(dir, written[2]))) || time.Since(lb) > time.Minute {
			t.Errorf("LastBackup = %v", lb)
		}
	})
}

func mtime(t *testing.T, p string) time.Time {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UTC()
}

// TestPreMigrateBackupSkipsFreshDatabase: an empty database has nothing to protect.
func TestPreMigrateBackupSkipsFreshDatabase(t *testing.T) {
	db := storetest.OpenUnmigrated(t, store.SQLite)
	dir := t.TempDir()
	if p, err := store.PreMigrateBackup(ctx, db, dir, 3, time.Now()); err != nil || p != "" {
		t.Fatalf("fresh database: backup %q, %v", p, err)
	}
	if pend, err := db.PendingMigrations(ctx); err != nil || len(pend) == 0 {
		t.Fatalf("fresh database pending = %d, %v", len(pend), err)
	}
}

// TestBootCheck (DB-017): both engines pass on a healthy database and report
// which check ran.
func TestBootCheck(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		rep, err := store.BootCheck(ctx, db)
		if err != nil || !rep.OK || rep.CheckedAt.IsZero() {
			t.Fatalf("BootCheck = %+v, %v", rep, err)
		}
		want := map[store.Dialect]string{store.SQLite: "quick_check", store.Postgres: "select_1"}[db.Dialect]
		if rep.Check != want {
			t.Fatalf("check = %q, want %q", rep.Check, want)
		}
	})
}

// TestPostgresSessionJITOff (DB-023): JIT is off per session unless the URL sets it.
func TestPostgresSessionJITOff(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs postgres")
	}
	db := storetest.Open(t, store.Postgres)
	var jit string
	if err := db.Read().QueryRowContext(ctx, `SHOW jit`).Scan(&jit); err != nil {
		t.Fatal(err)
	}
	if jit != "off" {
		t.Fatalf("jit = %q, want off", jit)
	}
}

// TestRefuseUnknownLowerMigration: a database carrying a migration this binary
// does not know is refused even when MAX(version) is not above the binary's
// head (a newer release added it in a lower D-18 range). Downgrade protection.
func TestRefuseUnknownLowerMigration(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ms, err := store.Migrations(db.Dialect)
		if err != nil || len(ms) < 2 {
			t.Fatal(ms, err)
		}
		unknown := ms[0].Version + 1
		for _, m := range ms {
			if m.Version == unknown {
				unknown++
			}
		}
		if unknown >= ms[len(ms)-1].Version {
			t.Skip("no gap below head")
		}
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, 'future_lower.sql', ?)`, unknown, store.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
			t.Fatalf("want refusal, got %v", err)
		}
	})
}
