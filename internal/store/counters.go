// SPDX-License-Identifier: AGPL-3.0-only

package store

import "context"

// NextTicketNumber allocates the next human ticket number inside the caller's
// write transaction (M-6): numbers are unique and never reused; a rolled-back
// transaction simply leaves no gap behind because the increment rolls back too.
func NextTicketNumber(ctx context.Context, tx *Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `UPDATE counters SET int_value = int_value + 1 WHERE name = 'ticket_number' RETURNING int_value`).Scan(&n)
	return n, err
}

// Cursor reads a text counter (e.g. "dispatch_cursor", the last dispatched ticket_events.id).
func Cursor(ctx context.Context, q Querier, name string) (string, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT text_value FROM counters WHERE name = ?`, name).Scan(&v)
	return v, NotFound(err)
}

// SetCursor upserts a text counter inside a write transaction.
func SetCursor(ctx context.Context, tx *Tx, name, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO counters (name, int_value, text_value) VALUES (?, 0, ?)
		ON CONFLICT (name) DO UPDATE SET text_value = excluded.text_value`, name, value)
	return err
}
