// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// Column describes one column of a table as reported by the engine.
type Column struct {
	Name    string
	Type    string // declared (sqlite) or data_type (postgres), lower-cased
	NotNull bool
}

// searchTables are engine-specific by design (FTS5 vs tsvector) and are excluded
// from schema parity; everything else must match column-for-column.
var searchTables = map[string]bool{
	"tickets_fts": true, "kb_fts": true, // sqlite
	"ticket_search": true, "kb_search": true, // postgres
}

// IsSearchTable reports whether a table is an engine-specific search index.
func IsSearchTable(name string) bool {
	if searchTables[name] {
		return true
	}
	for _, p := range []string{"tickets_fts_", "kb_fts_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Tables lists user tables (sorted), excluding engine internals.
func Tables(ctx context.Context, db *DB) ([]string, error) {
	q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`
	if db.Dialect == Postgres {
		q = `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	rows, err := db.R.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, rows.Err()
}

// Columns lists the columns of a table in declaration order.
func Columns(ctx context.Context, db *DB, table string) ([]Column, error) {
	var out []Column
	if db.Dialect == Postgres {
		rows, err := db.R.QueryContext(ctx, `SELECT column_name, data_type, is_nullable FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c Column
			var nullable string
			if err := rows.Scan(&c.Name, &c.Type, &nullable); err != nil {
				return nil, err
			}
			c.Type = strings.ToLower(c.Type)
			c.NotNull = nullable == "NO"
			out = append(out, c)
		}
		return out, rows.Err()
	}
	rows, err := db.R.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		var notnull, pk int
		if err := rows.Scan(&c.Name, &c.Type, &notnull, &pk); err != nil {
			return nil, err
		}
		c.Type = strings.ToLower(c.Type)
		// SQLite does not imply NOT NULL for TEXT primary keys; every PK here is NOT NULL by intent.
		c.NotNull = notnull == 1 || pk > 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// IsBoolColumn / IsJSONColumn classify a column type on either engine.
func IsBoolColumn(typ string) bool { return typ == "boolean" }
func IsJSONColumn(typ string) bool { return typ == "jsontext" || typ == "jsonb" }

var (
	reSQLiteIn = regexp.MustCompile(`(\w+)\s+IN\s*\(([^()]*)\)`)
	rePGAny    = regexp.MustCompile(`\(?(\w+)\)?(?:::text)?\s*=\s*ANY\s*\(\(?ARRAY\[([^\]]*)\]`)
	reQuoted   = regexp.MustCompile(`'([^']*)'`)
)

// CheckEnums returns, per column, every IN-list that appears in the table's CHECK
// constraints (a column may appear in several lists, e.g. users.role per kind).
// Values in each list are sorted.
func CheckEnums(ctx context.Context, db *DB, table string) (map[string][][]string, error) {
	var defs []string
	if db.Dialect == Postgres {
		rows, err := db.R.QueryContext(ctx, `SELECT pg_get_constraintdef(c.oid) FROM pg_constraint c
			JOIN pg_class t ON t.oid = c.conrelid JOIN pg_namespace n ON n.oid = t.relnamespace
			WHERE c.contype = 'c' AND t.relname = $1 AND n.nspname = current_schema()`, table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				return nil, err
			}
			defs = append(defs, d)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		var sqlText string
		if err := db.R.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sqlText); err != nil {
			return nil, err
		}
		defs = []string{sqlText}
	}
	out := map[string][][]string{}
	re := reSQLiteIn
	if db.Dialect == Postgres {
		re = rePGAny
	}
	for _, d := range defs {
		for _, m := range re.FindAllStringSubmatch(d, -1) {
			var vals []string
			for _, q := range reQuoted.FindAllStringSubmatch(m[2], -1) {
				vals = append(vals, q[1])
			}
			if len(vals) == 0 {
				continue
			}
			sort.Strings(vals)
			out[m[1]] = append(out[m[1]], vals)
		}
	}
	return out, nil
}

// LintSchema returns violations of the column conventions (DB-022): every *_at
// column is TIMESTAMP (SQLite) / timestamptz (Postgres); no float, real, double
// or money columns except the custom-field numeric value.
func LintSchema(ctx context.Context, db *DB) ([]string, error) {
	tables, err := Tables(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, tbl := range tables {
		if IsSearchTable(tbl) {
			continue
		}
		cols, err := Columns(ctx, db, tbl)
		if err != nil {
			return nil, err
		}
		for _, c := range cols {
			if strings.HasSuffix(c.Name, "_at") {
				want := "timestamp"
				if db.Dialect == Postgres {
					want = "timestamp with time zone"
				}
				if c.Type != want {
					out = append(out, tbl+"."+c.Name+": timestamp column has type "+c.Type+", want "+want)
				}
			}
			if (strings.Contains(c.Type, "float") || strings.Contains(c.Type, "real") || strings.Contains(c.Type, "double") || c.Type == "money") &&
				(tbl != "ticket_field_values" || c.Name != "value_num") {
				out = append(out, tbl+"."+c.Name+": forbidden numeric type "+c.Type)
			}
		}
	}
	return out, nil
}
