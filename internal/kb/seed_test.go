// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"errors"
	"testing"

	"path/filepath"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/cli"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// TestKBSeed_CommandRegistered runs `featherdesk seed-kb` against a fresh
// SQLite database and reads the result back anonymously.
func TestKBSeed_CommandRegistered(t *testing.T) {
	cmd, ok := cli.Lookup("seed-kb")
	if !ok {
		t.Fatal("seed-kb subcommand not registered")
	}
	dir := t.TempDir()
	url := "sqlite://" + filepath.Join(dir, "st.db")
	t.Setenv("FD_DATABASE_URL", url)
	t.Setenv("FD_SECRET_KEY", strings.Repeat("k", 32))
	t.Setenv("FD_DATA_DIR", dir)
	t.Setenv("FD_PUBLIC_URL", "https://help.example.com")
	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), url, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()
	e := newEnv(t, db)
	var page pageDTO
	r := e.do("GET", "/api/v1/kb/articles?limit=50", "", nil)
	e.want(r, 200)
	r.json(t, &page)
	if len(page.Items) != 15 {
		t.Fatalf("seed-kb: anonymous sees %d articles, want 15", len(page.Items))
	}
}

// ---- KB-015 ----

func TestKBSeed_FifteenPublicArticles(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		n, err := Seed(ctx, e.db)
		if err != nil || n != 15 {
			t.Fatalf("seed: %d, %v", n, err)
		}
		if _, err := Seed(ctx, e.db); !errors.Is(err, ErrAlreadySeeded) {
			t.Fatalf("second seed: %v", err)
		}
		var page pageDTO
		r := e.do("GET", "/api/v1/kb/articles?limit=50", "", nil)
		e.want(r, 200)
		r.json(t, &page)
		if len(page.Items) != 15 {
			t.Fatalf("anonymous sees %d seeded articles, want 15", len(page.Items))
		}
		var cats []categoryDTO
		e.do("GET", "/api/v1/kb/categories?locale=it", "", nil).json(t, &cats)
		if len(cats) != 3 || cats[0].Title["it"] == "" {
			t.Fatalf("seeded categories %+v", cats)
		}
		// Seeded content is indexed in both languages.
		hits, err := e.m.Search(ctx, nil, "password", "en", 10, false)
		if err != nil || len(hits) == 0 {
			t.Fatalf("search seeded: %v %v", hits, err)
		}
		hits, _ = e.m.Search(ctx, nil, "reimposta", "it", 10, false)
		if len(hits) == 0 || hits[0].Locale != "it" {
			t.Fatalf("italian seeded search: %+v", hits)
		}
	})
}
