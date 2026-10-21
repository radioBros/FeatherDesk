// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON stores T as JSON text in a JSONTEXT (SQLite) or JSONB (Postgres) column.
// Bind it as a parameter and scan into it; NULL scans to the zero value.
//
//	var v store.JSON[map[string]any]
//	row.Scan(&v)
//	tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`, store.JSON[Cfg]{V: cfg}, "smtp")
type JSON[T any] struct {
	V     T
	Valid bool // false when the column was NULL
}

func NewJSON[T any](v T) JSON[T] { return JSON[T]{V: v, Valid: true} }

func (j *JSON[T]) Scan(src any) error {
	var zero T
	j.V, j.Valid = zero, false
	var b []byte
	switch s := src.(type) {
	case nil:
		return nil
	case []byte:
		b = s
	case string:
		b = []byte(s)
	default:
		return fmt.Errorf("store.JSON: cannot scan %T", src)
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, &j.V); err != nil {
		return fmt.Errorf("store.JSON: %w", err)
	}
	j.Valid = true
	return nil
}

// Value always encodes as a string: pgx sends it as text, which Postgres casts to
// jsonb, and SQLite stores it as TEXT.
func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// MustJSON marshals v to a JSON string for use as a query argument.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// RawJSON scans a JSON column into raw bytes regardless of engine.
type RawJSON []byte

func (r *RawJSON) Scan(src any) error {
	switch s := src.(type) {
	case nil:
		*r = nil
	case []byte:
		*r = append((*r)[:0], s...)
	case string:
		*r = []byte(s)
	default:
		return fmt.Errorf("store.RawJSON: cannot scan %T", src)
	}
	return nil
}

func (r RawJSON) Value() (driver.Value, error) {
	if r == nil {
		return nil, nil
	}
	return string(r), nil
}
