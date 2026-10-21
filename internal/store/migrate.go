// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Migrations live in migrations/<engine>/NNNN_name.sql, one transaction per
// file. Number ranges per area are fixed by D-18 so parallel work never collides.
//
//go:embed migrations
var migrationsFS embed.FS

type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns the embedded migrations for the dialect, sorted by version.
func Migrations(d Dialect) ([]Migration, error) {
	dir := "migrations/" + d.String()
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: want NNNN_name.sql", e.Name())
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		b, err := fs.ReadFile(migrationsFS, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: e.Name(), SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].Version)
		}
	}
	return out, nil
}

// Migrate applies every pending migration. It refuses to run when the database
// already has a version newer than this binary knows (downgrade protection).
func (db *DB) Migrate(ctx context.Context) error {
	ts := "TIMESTAMP"
	if db.Dialect == Postgres {
		ts = "TIMESTAMPTZ"
	}
	if _, err := db.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at `+ts+` NOT NULL)`); err != nil {
		return err
	}
	ms, err := Migrations(db.Dialect)
	if err != nil {
		return err
	}
	applied := map[int]bool{}
	maxApplied := 0
	rows, err := db.W.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
		maxApplied = max(maxApplied, v)
	}
	rows.Close()
	if len(ms) > 0 && maxApplied > ms[len(ms)-1].Version {
		return fmt.Errorf("store: database schema version %d is newer than this binary (%d); refusing to start", maxApplied, ms[len(ms)-1].Version)
	}
	// A migration added later in a lower number range (D-18) does not raise
	// MAX(version): refuse any applied version this binary does not know too.
	known := make(map[int]bool, len(ms))
	for _, m := range ms {
		known[m.Version] = true
	}
	for v := range applied {
		if !known[v] && len(ms) > 0 {
			return fmt.Errorf("store: database schema version %d is newer than this binary (%d); refusing to start", v, ms[len(ms)-1].Version)
		}
	}
	for _, m := range ms {
		if applied[m.Version] {
			continue
		}
		err := db.Tx(ctx, func(tx *Tx) error {
			for _, stmt := range SplitStatements(m.SQL) {
				if _, err := tx.tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("%s: %w\n%s", m.Name, err, stmt)
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`, m.Version, m.Name, Now())
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration version (0 if none).
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := db.R.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// SplitStatements splits a migration file on `;` at line ends, keeping
// `-- +StatementBegin` … `-- +StatementEnd` blocks (triggers, functions) whole.
func SplitStatements(src string) []string {
	var out []string
	var cur strings.Builder
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "-- +StatementBegin":
			inBlock = true
			continue
		case t == "-- +StatementEnd":
			inBlock = false
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			continue
		case strings.HasPrefix(t, "--") && !inBlock:
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		if !inBlock && strings.HasSuffix(t, ";") {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}
