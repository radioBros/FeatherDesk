// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// lockSetup serialises first-run setup across connections. SQLite write
// transactions are already BEGIN IMMEDIATE (one writer); Postgres runs READ
// COMMITTED, so the seeded settings 'setup' row is locked FOR UPDATE.
func lockSetup(ctx context.Context, tx *store.Tx) error {
	if tx.Dialect() != store.Postgres {
		return nil
	}
	var key string
	err := tx.QueryRowContext(ctx, `SELECT key FROM settings WHERE key = 'setup' FOR UPDATE`).Scan(&key)
	return store.NotFound(err)
}
