// SPDX-License-Identifier: AGPL-3.0-only

package store

import "context"

// SetPruneTxHook exposes the prune transaction observer to external tests.
func SetPruneTxHook(fn func(table string, n int64)) { pruneTxHook = fn }

// MigrateUntil applies the migrations up to version limit.
func (db *DB) MigrateUntil(ctx context.Context, limit int) error { return db.migrateUntil(ctx, limit) }
