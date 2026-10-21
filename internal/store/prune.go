// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"time"
)

// Prune functions are idempotent and batched (≤ PruneBatch rows per transaction)
// so SQLite's single writer is never held for long (DB-018, DB-019). The
// platform scheduler calls them; blob keys returned must be deleted after commit.

// PruneBatch is the maximum number of rows deleted per transaction.
var PruneBatch = 1000

// pruneTxHook observes rows deleted per transaction (tests).
var pruneTxHook func(table string, n int64)

func pruneBatched(ctx context.Context, db *DB, table, keyCols, where string, args ...any) (int64, error) {
	var total int64
	for {
		var n int64
		err := db.Tx(ctx, func(tx *Tx) error {
			q := `DELETE FROM ` + table + ` WHERE ` + keyCols + ` IN (SELECT ` + keyCols + ` FROM ` + table + ` WHERE ` + where + ` LIMIT ?)`
			if keyCols[0] == '(' { // composite key: row-value IN works on both engines
				q = `DELETE FROM ` + table + ` WHERE ` + keyCols + ` IN (SELECT ` + keyCols[1:len(keyCols)-1] + ` FROM ` + table + ` WHERE ` + where + ` LIMIT ?)`
			}
			res, err := tx.ExecContext(ctx, q, append(args, PruneBatch)...)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return total, err
		}
		if pruneTxHook != nil {
			pruneTxHook(table, n)
		}
		total += n
		if n < int64(PruneBatch) {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// PruneSessions deletes expired sessions.
func PruneSessions(ctx context.Context, db *DB, now time.Time) (int64, error) {
	return pruneBatched(ctx, db, "sessions", "id_hash", "expires_at < ?", now.UTC())
}

// PruneAuthTokens deletes expired tokens and tokens used more than a day ago.
func PruneAuthTokens(ctx context.Context, db *DB, now time.Time) (int64, error) {
	return pruneBatched(ctx, db, "auth_tokens", "id", "expires_at < ? OR (used_at IS NOT NULL AND used_at < ?)", now.UTC(), now.UTC().Add(-24*time.Hour))
}

// PruneAPITokens deletes PATs 30 days after they expired or were revoked.
func PruneAPITokens(ctx context.Context, db *DB, now time.Time) (int64, error) {
	cut := now.UTC().Add(-30 * 24 * time.Hour)
	return pruneBatched(ctx, db, "api_tokens", "id", "expires_at < ? OR revoked_at < ?", cut, cut)
}

// PruneDeadJobs deletes dead deferred jobs untouched for 30 days.
func PruneDeadJobs(ctx context.Context, db *DB, now time.Time) (int64, error) {
	return pruneBatched(ctx, db, "deferred_jobs", "id", "status = 'dead' AND updated_at < ?", now.UTC().Add(-30*24*time.Hour))
}

// PruneWebhookDeliveries deletes delivery log rows older than 30 days.
func PruneWebhookDeliveries(ctx context.Context, db *DB, now time.Time) (int64, error) {
	return pruneBatched(ctx, db, "webhook_deliveries", "id", "created_at < ?", now.UTC().Add(-30*24*time.Hour))
}

// PruneIdempotencyKeys deletes keys older than 24 hours.
func PruneIdempotencyKeys(ctx context.Context, db *DB, now time.Time) (int64, error) {
	return pruneBatched(ctx, db, "idempotency_keys", "(principal_id, key)", "created_at < ?", now.UTC().Add(-24*time.Hour))
}

// PruneInboundMessages deletes processed/ignored/failed inbound log rows older
// than 90 days (quarantined rows wait for an admin) and returns their raw blob keys.
func PruneInboundMessages(ctx context.Context, db *DB, now time.Time) (int64, []string, error) {
	return pruneWithBlobs(ctx, db, "inbound_messages", "raw_blob_key", "received_at < ? AND status <> 'quarantined'", now.UTC().Add(-90*24*time.Hour))
}

// PruneOrphanAttachments deletes draft uploads never attached to a message within
// 24 hours and returns their blob keys.
func PruneOrphanAttachments(ctx context.Context, db *DB, now time.Time) (int64, []string, error) {
	return pruneWithBlobs(ctx, db, "attachments", "storage_key", "message_id IS NULL AND created_at < ?", now.UTC().Add(-24*time.Hour))
}

// PruneAuditLog deletes audit rows older than the retention (default 365 days)
// through the purge_allow escape hatch of the append-only trigger.
func PruneAuditLog(ctx context.Context, db *DB, now time.Time, retention time.Duration) (int64, error) {
	if retention <= 0 {
		retention = 365 * 24 * time.Hour
	}
	var total int64
	err := db.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO purge_allow (tbl, key) VALUES ('audit_log', '*') ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE created_at < ?`, now.UTC().Add(-retention))
		if err != nil {
			return err
		}
		total, _ = res.RowsAffected()
		_, err = tx.ExecContext(ctx, `DELETE FROM purge_allow WHERE tbl = 'audit_log' AND key = '*'`)
		return err
	})
	return total, err
}

func pruneWithBlobs(ctx context.Context, db *DB, table, blobCol, where string, args ...any) (int64, []string, error) {
	var total int64
	var keys []string
	for {
		var ids []string
		err := db.Tx(ctx, func(tx *Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id, `+blobCol+` FROM `+table+` WHERE `+where+` LIMIT ?`, append(args, PruneBatch)...)
			if err != nil {
				return err
			}
			var batchKeys []string
			for rows.Next() {
				var id, key string
				if err := rows.Scan(&id, &key); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
				if key != "" {
					batchKeys = append(batchKeys, key)
				}
			}
			rows.Close()
			for _, id := range ids {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id); err != nil {
					return err
				}
			}
			keys = append(keys, batchKeys...)
			return nil
		})
		if err != nil {
			return total, keys, err
		}
		if pruneTxHook != nil {
			pruneTxHook(table, int64(len(ids)))
		}
		total += int64(len(ids))
		if len(ids) < PruneBatch {
			return total, keys, nil
		}
	}
}
