// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// Deliberate CI proof (DEP-008 / DEP-030): strftime exists only in SQLite, so the
// Postgres leg of this test fails and turns the go job red. Never merged.
func TestProof_SQLiteOnlyQuery(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		var year string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT strftime('%Y', 'now')`).Scan(&year); err != nil {
			t.Fatal(err)
		}
	})
}
