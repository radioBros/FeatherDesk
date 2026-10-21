// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"slices"
)

// WipeData removes every row of every data table (and the search indexes) in
// one write transaction, resets the ticket-number and dispatcher counters and
// keeps the schema, the migration history and the SLA policy rows. The
// append-only triggers are lifted through purge_allow for the duration of the
// call. It returns the blob keys the deleted rows referenced; the caller
// deletes those blobs after the commit. Used by `seed --demo --force`.
func WipeData(ctx context.Context, db *DB) (blobKeys []string, err error) {
	err = db.Tx(ctx, func(tx *Tx) error {
		for _, q := range []string{
			`SELECT storage_key FROM attachments`,
			`SELECT blob_key FROM branding_assets`,
			`SELECT blob_key FROM kb_assets`,
			`SELECT raw_blob_key FROM inbound_messages WHERE raw_blob_key <> ''`,
			`SELECT file_key FROM privacy_requests WHERE file_key <> ''`, // export ZIPs (D-19 §C)
		} {
			rows, err := tx.QueryContext(ctx, q)
			if err != nil {
				return err
			}
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err != nil {
					rows.Close()
					return err
				}
				blobKeys = append(blobKeys, k)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		for _, t := range []string{"audit_log", "ticket_events"} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO purge_allow (tbl, key) VALUES (?, '*') ON CONFLICT DO NOTHING`, t); err != nil {
				return err
			}
		}
		for tbl, col := range selfRefs {
			if _, err := tx.ExecContext(ctx, `UPDATE `+tbl+` SET `+col+` = NULL`); err != nil {
				return err
			}
		}
		tables, err := Tables(ctx, db)
		if err != nil {
			return err
		}
		for _, t := range []string{"tickets_fts", "kb_fts", "ticket_search", "kb_search"} {
			if slices.Contains(tables, t) {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
					return err
				}
			}
		}
		for _, t := range slices.Backward(copyOrder) {
			if t == "counters" || t == "sla_policies" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE counters SET text_value = ''`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE counters SET int_value = 1000 WHERE name = 'ticket_number'`); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM purge_allow WHERE tbl IN ('audit_log', 'ticket_events') AND key = '*'`)
		return err
	})
	return blobKeys, err
}
