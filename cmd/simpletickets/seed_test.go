// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/store"
)

func seedEnvVars(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ST_SECRET_KEY", strings.Repeat("c", 32))
	t.Setenv("ST_DATA_DIR", dir)
	t.Setenv("ST_DATABASE_URL", "sqlite://"+filepath.Join(dir, "cli.db"))
	t.Setenv("ST_PUBLIC_URL", "http://localhost:8080")
	return dir
}

// TestSeed_DemoFlagsNeedDemo: the demo-only flags are refused on the basic seed.
func TestSeed_DemoFlagsNeedDemo(t *testing.T) {
	seedEnvVars(t)
	for _, f := range []string{"--force", "--smtp-mailpit", "--password=x"} {
		if c := run(context.Background(), []string{"seed", f}); c != 1 {
			t.Errorf("seed %s: exit %d, want 1", f, c)
		}
	}
}

// TestSeed_DemoCommand runs the real command on a fresh SQLite file: it seeds,
// then refuses a second run, and --force seeds again.
func TestSeed_DemoCommand(t *testing.T) {
	authntest.FastArgon()
	seedEnvVars(t)
	ctx := context.Background()
	// 32 = the scripted stories plus the Mobile-only agent's ticket (D-20).
	if c := run(ctx, []string{"seed", "--demo", "--tickets", "32"}); c != 0 {
		t.Fatalf("seed --demo: exit %d", c)
	}
	if c := run(ctx, []string{"seed", "--demo"}); c != 1 {
		t.Errorf("second seed --demo must be refused: exit %d", c)
	}
	if c := run(ctx, []string{"seed"}); c != 1 {
		t.Errorf("basic seed on a seeded database must be refused: exit %d", c)
	}
	if c := run(ctx, []string{"seed", "--demo", "--force", "--tickets", "32"}); c != 0 {
		t.Errorf("seed --demo --force: exit %d", c)
	}
	db, err := store.Open(ctx, os.Getenv("ST_DATABASE_URL"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var tickets, users int
	if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets`).Scan(&tickets); err != nil {
		t.Fatal(err)
	}
	if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if tickets != 32 || users != 27 {
		t.Errorf("after --force: %d tickets, %d users; want 32 and 27", tickets, users)
	}
}
