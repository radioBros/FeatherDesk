// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// copyOrder lists every data table in foreign-key order for MigrateEngine.
// Search tables are rebuilt by the index worker afterwards (NeedsReindex).
var copyOrder = []string{
	"organizations", "users", "user_identities", "sessions", "api_tokens", "auth_tokens", "audit_log",
	"business_calendars", "teams", "team_members", "categories", "settings", "sla_policies", "canned_responses", "tags",
	"tickets", "ticket_messages", "attachments", "ticket_events", "ticket_links", "custom_field_defs", "ticket_field_values",
	"ticket_tags", "ticket_watchers", "saved_views", "counters",
	"deferred_jobs", "branding_assets", "idempotency_keys", "webhooks", "webhook_deliveries",
	"kb_categories", "kb_articles", "kb_article_translations", "kb_article_feedback", "kb_slug_history", "kb_assets",
	"inbound_mailboxes", "inbound_messages",
	"automation_rules", "automation_runs", "automation_time_runs", "macros", // 0100 (D-19 §A)
	"user_mfa", "mfa_recovery_codes", "mfa_challenges", // 0110 (D-19 §B)
	"privacy_requests", "user_consents", // 0120 (D-19 §C)
	"notifications", // 0130 (D-19 §D)
}

// selfRefs are columns referencing their own table; they are copied as NULL and
// patched afterwards so insertion order never matters.
var selfRefs = map[string]string{"tickets": "followup_of_id", "kb_categories": "parent_id"}

// seededTables are filled by migrations; the target's seed rows are replaced.
var seededTables = map[string]bool{"settings": true, "sla_policies": true, "counters": true}

type TableReport struct {
	Table    string
	Rows     int64
	Checksum string
}

type EngineReport struct {
	Tables       []TableReport
	NeedsReindex bool // search indexes must be rebuilt (`simpletickets reindex`)
}

// MigrateEngine copies all data from one database to another, e.g. SQLite →
// Postgres (DB-031). Both must be migrated to the same schema version and the
// target must hold no users or tickets. Every table is verified by row count
// and an engine-neutral checksum of its rows in primary-key order.
func MigrateEngine(ctx context.Context, from, to *DB) (EngineReport, error) {
	var rep EngineReport
	vf, err := from.SchemaVersion(ctx)
	if err != nil {
		return rep, err
	}
	vt, err := to.SchemaVersion(ctx)
	if err != nil {
		return rep, err
	}
	if vf != vt {
		return rep, fmt.Errorf("migrate-engine: schema versions differ (source %d, target %d); run migrate on both first", vf, vt)
	}
	for _, tbl := range []string{"users", "tickets"} {
		var n int
		if err := to.R.QueryRowContext(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			return rep, err
		}
		if n > 0 {
			return rep, fmt.Errorf("migrate-engine: target is not empty (%s has %d rows)", tbl, n)
		}
	}
	tables, err := Tables(ctx, from)
	if err != nil {
		return rep, err
	}
	known := map[string]bool{"schema_migrations": true, "purge_allow": true}
	for _, t := range copyOrder {
		known[t] = true
	}
	for _, t := range tables {
		if !known[t] && !IsSearchTable(t) {
			return rep, fmt.Errorf("migrate-engine: table %s is not in the copy order", t)
		}
	}
	for _, tbl := range copyOrder {
		if err := copyTable(ctx, from, to, tbl); err != nil {
			return rep, fmt.Errorf("migrate-engine: %s: %w", tbl, err)
		}
		cf, sf, err := tableChecksum(ctx, from, tbl)
		if err != nil {
			return rep, err
		}
		ct, st, err := tableChecksum(ctx, to, tbl)
		if err != nil {
			return rep, err
		}
		if cf != ct || sf != st {
			return rep, fmt.Errorf("migrate-engine: %s verification failed (rows %d→%d, checksum %s→%s)", tbl, cf, ct, sf[:12], st[:12])
		}
		rep.Tables = append(rep.Tables, TableReport{Table: tbl, Rows: cf, Checksum: sf})
	}
	rep.NeedsReindex = true
	return rep, nil
}

func copyTable(ctx context.Context, from, to *DB, tbl string) error {
	srcCols, err := Columns(ctx, from, tbl)
	if err != nil {
		return err
	}
	dstCols, err := Columns(ctx, to, tbl)
	if err != nil {
		return err
	}
	dstType := map[string]string{}
	for _, c := range dstCols {
		dstType[c.Name] = c.Type
	}
	names := make([]string, len(srcCols))
	for i, c := range srcCols {
		if _, ok := dstType[c.Name]; !ok {
			return fmt.Errorf("column %s missing on target", c.Name)
		}
		names[i] = c.Name
	}
	selfCol := selfRefs[tbl]
	if seededTables[tbl] {
		if err := to.Tx(ctx, func(tx *Tx) error { _, err := tx.ExecContext(ctx, `DELETE FROM `+tbl); return err }); err != nil {
			return err
		}
	}
	rows, err := from.R.QueryContext(ctx, `SELECT `+strings.Join(names, ", ")+` FROM `+tbl)
	if err != nil {
		return err
	}
	defer rows.Close()
	ins := `INSERT INTO ` + tbl + ` (` + strings.Join(names, ", ") + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ") + `)`
	type patch struct{ id, ref any }
	var patches []patch
	var batch [][]any
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := to.Tx(ctx, func(tx *Tx) error {
			for _, vals := range batch {
				if _, err := tx.ExecContext(ctx, ins, vals...); err != nil {
					return err
				}
			}
			return nil
		})
		batch = batch[:0]
		return err
	}
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, n := range names {
			vals[i] = convertValue(vals[i], dstType[n])
			if n == selfCol && vals[i] != nil {
				patches = append(patches, patch{id: vals[0], ref: vals[i]})
				vals[i] = nil
			}
		}
		batch = append(batch, vals)
		if len(batch) >= 500 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if len(patches) > 0 {
		return to.Tx(ctx, func(tx *Tx) error {
			for _, p := range patches {
				if _, err := tx.ExecContext(ctx, `UPDATE `+tbl+` SET `+selfCol+` = ? WHERE id = ?`, p.ref, p.id); err != nil {
					return err
				}
			}
			return nil
		})
	}
	return nil
}

// convertValue adapts a scanned value to the target column type.
func convertValue(v any, dstType string) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		v = string(x)
	}
	if IsBoolColumn(dstType) {
		switch x := v.(type) {
		case int64:
			return x != 0
		case string:
			b, _ := strconv.ParseBool(x)
			return b
		}
	}
	return v
}

// tableChecksum hashes every row in primary-key order using an engine-neutral
// encoding (bools as t/f, times as UTC RFC 3339, JSON re-marshalled).
func tableChecksum(ctx context.Context, db *DB, tbl string) (int64, string, error) {
	cols, err := Columns(ctx, db, tbl)
	if err != nil {
		return 0, "", err
	}
	pk, err := primaryKey(ctx, db, tbl)
	if err != nil {
		return 0, "", err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	rows, err := db.R.QueryContext(ctx, `SELECT `+strings.Join(names, ", ")+` FROM `+tbl+` ORDER BY `+strings.Join(pk, ", "))
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	h := sha256.New()
	var n int64
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return 0, "", err
		}
		for i, c := range cols {
			h.Write([]byte(canonical(vals[i], c.Type)))
			h.Write([]byte{0x1f})
		}
		h.Write([]byte{0x1e})
		n++
	}
	return n, hex.EncodeToString(h.Sum(nil)), rows.Err()
}

func canonical(v any, typ string) string {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	if v == nil {
		return "\x00NULL"
	}
	if IsBoolColumn(typ) {
		switch x := v.(type) {
		case bool:
			if x {
				return "t"
			}
			return "f"
		case int64:
			if x != 0 {
				return "t"
			}
			return "f"
		}
	}
	if IsJSONColumn(typ) {
		if s, ok := v.(string); ok {
			var any any
			if json.Unmarshal([]byte(s), &any) == nil {
				b, _ := json.Marshal(any)
				return string(b)
			}
		}
	}
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func primaryKey(ctx context.Context, db *DB, tbl string) ([]string, error) {
	var out []string
	if db.Dialect == Postgres {
		rows, err := db.R.QueryContext(ctx, `SELECT a.attname FROM pg_index i
			JOIN pg_class t ON t.oid = i.indrelid JOIN pg_namespace n ON n.oid = t.relnamespace
			JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
			JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
			WHERE i.indisprimary AND t.relname = $1 AND n.nspname = current_schema() ORDER BY k.ord`, tbl)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	} else {
		type pkc struct {
			name string
			pos  int
		}
		var cols []pkc
		rows, err := db.R.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?) WHERE pk > 0`, tbl)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c pkc
			if err := rows.Scan(&c.name, &c.pos); err != nil {
				return nil, err
			}
			cols = append(cols, c)
		}
		sort.Slice(cols, func(i, j int) bool { return cols[i].pos < cols[j].pos })
		for _, c := range cols {
			out = append(out, c.name)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("table " + tbl + " has no primary key")
	}
	return out, nil
}
