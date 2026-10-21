// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PendingMigrations returns the embedded migrations that are not applied yet.
// It compares the applied set, not MAX(version): a migration added later in a
// lower number range (D-18) is pending even when a higher one is applied.
func (db *DB) PendingMigrations(ctx context.Context) ([]Migration, error) {
	ms, err := Migrations(db.Dialect)
	if err != nil {
		return nil, err
	}
	applied := map[int]bool{}
	rows, err := db.R.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		if missingTable(err) {
			return ms, nil // fresh database
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Migration
	for _, m := range ms {
		if !applied[m.Version] {
			out = append(out, m)
		}
	}
	return out, nil
}

func missingTable(err error) bool {
	s := err.Error()
	return strings.Contains(s, "no such table") || strings.Contains(s, "SQLSTATE 42P01") || strings.Contains(s, "does not exist")
}

// BootReport is the result of the boot-time database check (DB-017), shown
// under `db.integrity` in GET /admin/system.
type BootReport struct {
	OK        bool      `json:"ok"`
	Check     string    `json:"check"` // "quick_check" (SQLite) or "select_1" (Postgres)
	CheckedAt time.Time `json:"checked_at"`
	Millis    int64     `json:"duration_ms"`
	Error     string    `json:"error,omitempty"`
}

// BootCheck verifies the database before `serve` touches it (DB-017):
// SQLite runs PRAGMA quick_check and fails fast on corruption; Postgres
// answers SELECT 1 on the pool (the version floor is enforced by Open).
func BootCheck(ctx context.Context, db *DB) (BootReport, error) {
	start := time.Now()
	rep := BootReport{CheckedAt: Now(), Check: "quick_check"}
	var err error
	if db.Dialect == Postgres {
		rep.Check = "select_1"
		var one int
		err = db.R.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	} else {
		err = IntegrityCheck(ctx, db)
	}
	rep.Millis = time.Since(start).Milliseconds()
	if err != nil {
		rep.Error = err.Error()
		return rep, fmt.Errorf("store: database check failed (%s): %w; stop here and restore the last backup (docs/DOCUMENTATION.md §13.1)", rep.Check, err)
	}
	rep.OK = true
	return rep, nil
}

// PreMigrateBackupPrefix names the automatic backups taken before migrations.
const PreMigrateBackupPrefix = "pre-migrate-"

// PreMigrateBackup writes `<dir>/pre-migrate-<version>-<UTC time>.db` with
// VACUUM INTO when a SQLite database that already has a schema is about to be
// migrated, then deletes all but the newest `keep` such files (DB-015). It
// returns "" without writing when there is nothing to protect: Postgres (no
// pg_dump in the image, DB-030), a fresh database, or no pending migration.
func PreMigrateBackup(ctx context.Context, db *DB, dir string, keep int, now time.Time) (string, error) {
	if db.Dialect != SQLite || sqlitePath(db.URL) == ":memory:" {
		return "", nil
	}
	pending, err := db.PendingMigrations(ctx)
	if err != nil {
		return "", err
	}
	ver, err := db.SchemaVersion(ctx)
	if err != nil {
		if missingTable(err) {
			return "", nil
		}
		return "", err
	}
	if len(pending) == 0 || ver == 0 {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s%04d-%s.db", PreMigrateBackupPrefix, ver, now.UTC().Format("20060102T150405.000000Z"))
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if _, err := db.W.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return "", fmt.Errorf("store: pre-migrate backup: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, prunePreMigrate(dir, keep)
}

func prunePreMigrate(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, PreMigrateBackupPrefix) && strings.HasSuffix(n, ".db") {
			names = append(names, n)
		}
	}
	// The UTC timestamp sorts lexically within one version; order by it, not
	// by the version, so the newest `keep` survive whatever their version.
	sort.Slice(names, func(i, j int) bool { return stamp(names[i]) < stamp(names[j]) })
	var errs []error
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return errors.Join(errs...)
}

func stamp(name string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(name, PreMigrateBackupPrefix), ".db")
	if _, t, ok := strings.Cut(s, "-"); ok {
		return t
	}
	return s
}

// LastBackup returns the modification time of the newest backup under dir:
// a `backup` directory (simpletickets-*) or a pre-migrate file. Zero when none.
func LastBackup(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var last time.Time
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "simpletickets-") && !strings.HasPrefix(n, PreMigrateBackupPrefix) {
			continue
		}
		if strings.HasSuffix(n, ".tmp") {
			continue
		}
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, n, backupManifest)); err != nil {
				continue // incomplete backup
			}
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last.UTC()
}

// SQLiteStats returns page_count and the WAL file size for /admin/system.
func SQLiteStats(ctx context.Context, db *DB) (pageCount int64, walBytes int64, err error) {
	if db.Dialect != SQLite {
		return 0, 0, errors.New("store: SQLiteStats on " + db.Dialect.String())
	}
	if err := db.R.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	if st, err := os.Stat(sqlitePath(db.URL) + "-wal"); err == nil {
		walBytes = st.Size()
	}
	return pageCount, walBytes, nil
}

// SQLiteFile returns the database file of a SQLite URL (the default when the
// URL is empty), for offline commands that work on the file itself.
func SQLiteFile(rawURL string) string { return sqlitePath(rawURL) }
