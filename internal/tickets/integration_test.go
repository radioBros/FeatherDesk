// SPDX-License-Identifier: AGPL-3.0-only

package tickets_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/config"
)

// TestAppRegistry_MountsWithoutCollisions builds the real application with
// every registered module (authn, tickets, kb, notify, webhooks, mailin, …):
// chi panics on a duplicate route, and every route must be documented.
func TestAppRegistry_MountsWithoutCollisions(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": dir, "ST_PUBLIC_URL": "http://localhost:8080",
		"ST_DATABASE_URL": "sqlite://" + filepath.Join(dir, "st.db")}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.Build(context.Background(), cfg, app.BuildOptions{SPA: fstest.MapFS{"index.html": {Data: []byte("x")}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	apispec.AssertRoutes(t, a.Server.Router())
	for _, prefix := range []string{"/api/v1/agent/tickets", "/api/v1/portal/tickets", "/api/v1/agent/customers", "/api/v1/admin/teams", "/api/v1/admin/branding"} {
		missing, err := apispec.Unimplemented(a.Server.Router(), prefix)
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) > 0 {
			t.Errorf("unimplemented under %s: %v", prefix, missing)
		}
	}
}
