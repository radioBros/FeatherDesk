// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import "github.com/radioBros/FeatherDesk/internal/store"

// Dialect-specific SQL fragments (docs/dev/internals.md: dialect SQL lives in
// a dialect.go next to the query).

// jsonArrayContains: the JSON array in col contains the bound string.
func jsonArrayContains(d store.Dialect, col string) string {
	if d == store.Postgres {
		return "jsonb_exists(" + col + ", ?)"
	}
	return "EXISTS (SELECT 1 FROM json_each(" + col + ") WHERE json_each.value = ?)"
}

// jsonBoolEquals: the JSON boolean in col equals the bound "true"/"false".
func jsonBoolEquals(d store.Dialect, col string) string {
	if d == store.Postgres {
		return col + " = CAST(? AS jsonb)"
	}
	return col + " = ?"
}
