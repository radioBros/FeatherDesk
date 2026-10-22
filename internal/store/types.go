// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"time"
)

// NullTime scans nullable timestamps on both engines.
type NullTime = sql.NullTime

// NullString scans nullable text on both engines.
type NullString = sql.NullString

// Nullable returns nil for the zero value so optional FKs bind as NULL.
func Nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// NullableTime returns nil for the zero time.
func NullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
