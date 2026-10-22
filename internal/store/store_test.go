// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRebind(t *testing.T) {
	got := Rebind(Postgres, "SELECT * FROM t WHERE a = ? AND b = '?' AND c = ?")
	want := "SELECT * FROM t WHERE a = $1 AND b = '?' AND c = $2"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if Rebind(SQLite, "a = ?") != "a = ?" {
		t.Fatal("sqlite must not rebind")
	}
}

func TestSplitStatements(t *testing.T) {
	src := "-- c\nCREATE TABLE a(x int);\n-- +StatementBegin\nCREATE TRIGGER t BEGIN SELECT 1; END;\n-- +StatementEnd\nCREATE TABLE b(y int);"
	got := SplitStatements(src)
	if len(got) != 3 {
		t.Fatalf("want 3 statements, got %d: %q", len(got), got)
	}
}

func TestOpenSQLiteAndMigrate(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "t.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal("second migrate must be a no-op:", err)
	}
}
