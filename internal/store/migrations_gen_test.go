// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite generated migrations from migrations/_templates")

// TestMigrationsGenerated keeps migrations/{sqlite,postgres} identical to what the
// templates render, so the two engines cannot drift (D-18).
func TestMigrationsGenerated(t *testing.T) {
	tpls, err := filepath.Glob("migrations/_templates/*.sql")
	if err != nil || len(tpls) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, tp := range tpls {
		src, err := os.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []Dialect{SQLite, Postgres} {
			want := renderMigrationTemplate(string(src), d)
			out := filepath.Join("migrations", d.String(), filepath.Base(tp))
			if *update {
				if err := os.WriteFile(out, []byte(want), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(out)
			if err != nil || string(got) != want {
				t.Errorf("%s is stale; run: go test ./internal/store -run TestMigrationsGenerated -update", out)
			}
		}
	}
	// No hand-written stray migration files.
	for _, d := range []Dialect{SQLite, Postgres} {
		files, _ := filepath.Glob(filepath.Join("migrations", d.String(), "*.sql"))
		for _, f := range files {
			if _, err := os.Stat(filepath.Join("migrations/_templates", filepath.Base(f))); err != nil {
				t.Errorf("%s has no template", f)
			}
		}
	}
}

func TestRenderTemplateBlocks(t *testing.T) {
	tpl := "A {TS} {JSON}\n@@sqlite\nS\n@@end\n@@postgres\nP\n@@end\nZ\n"
	s := renderMigrationTemplate(tpl, SQLite)
	p := renderMigrationTemplate(tpl, Postgres)
	if !strings.Contains(s, "A TIMESTAMP JSONTEXT\nS\nZ") || strings.Contains(s, "\nP\n") {
		t.Fatalf("sqlite render: %q", s)
	}
	if !strings.Contains(p, "A TIMESTAMPTZ JSONB\nP\nZ") || strings.Contains(p, "\nS\n") {
		t.Fatalf("postgres render: %q", p)
	}
}
