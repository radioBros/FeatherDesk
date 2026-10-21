// SPDX-License-Identifier: AGPL-3.0-only

// Package storetest opens migrated databases on both engines for tests.
//
//	storetest.Engines(t, func(t *testing.T, db *store.DB) { ... })
//
// SQLite uses a temp file; Postgres uses an isolated schema on
// ST_TEST_POSTGRES_URL (dropped on cleanup). When the URL is unset, Postgres
// subtests are skipped locally and fail under CI=true.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// PostgresURL returns the test Postgres base URL ("" when not configured).
func PostgresURL() string { return os.Getenv("ST_TEST_POSTGRES_URL") }

// Engines runs fn once per engine as subtests "sqlite" and "postgres".
func Engines(t *testing.T, fn func(t *testing.T, db *store.DB)) {
	t.Helper()
	for _, d := range []store.Dialect{store.SQLite, store.Postgres} {
		t.Run(d.String(), func(t *testing.T) {
			fn(t, Open(t, d))
		})
	}
}

// Open returns a freshly migrated database of the given dialect, closed on cleanup.
func Open(t testing.TB, d store.Dialect) *store.DB {
	t.Helper()
	db := OpenUnmigrated(t, d)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate %s: %v", d, err)
	}
	return db
}

// OpenUnmigrated returns an empty database (no migrations applied).
func OpenUnmigrated(t testing.TB, d store.Dialect) *store.DB {
	t.Helper()
	ctx := context.Background()
	if d == store.SQLite {
		db, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "test.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("db.Close: %v", err)
			}
		})
		return db
	}
	u, schema := isolatedSchema(t)
	db, err := store.Open(ctx, u, store.Options{PoolSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
		dropSchema(t, schema)
	})
	return db
}

// SchemaURL returns a Postgres URL bound to a new empty schema (dropped on cleanup);
// useful for tests that need two databases (engine migration).
func SchemaURL(t testing.TB) string {
	t.Helper()
	u, schema := isolatedSchema(t)
	t.Cleanup(func() { dropSchema(t, schema) })
	return u
}

func isolatedSchema(t testing.TB) (string, string) {
	t.Helper()
	base := PostgresURL()
	if base == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("ST_TEST_POSTGRES_URL is required in CI (both engines must be tested)")
		}
		t.Skip("ST_TEST_POSTGRES_URL not set; skipping postgres")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "t_" + hex.EncodeToString(b)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := admin.Close(); err != nil {
			t.Errorf("admin.Close: %v", err)
		}
	}()
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	pu, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := pu.Query()
	q.Set("search_path", schema)
	pu.RawQuery = q.Encode()
	return pu.String(), schema
}

func dropSchema(t testing.TB, schema string) {
	if !strings.HasPrefix(schema, "t_") {
		return
	}
	admin, err := sql.Open("pgx", PostgresURL())
	if err != nil {
		return
	}
	defer func() {
		if err := admin.Close(); err != nil {
			t.Errorf("admin.Close: %v", err)
		}
	}()
	if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
		t.Errorf("drop schema %s: %v", schema, err)
	}
}
