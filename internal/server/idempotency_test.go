// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestIdempotencyKeyPerPrincipal (DB-008): the idempotency_keys primary key is
// (principal_id, key), so the same Idempotency-Key sent by two principals is
// stored as two rows and runs the handler twice, while a replay by the same
// principal adds no row. Both engines.
func TestIdempotencyKeyPerPrincipal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		s := newTestServer(t, Options{DB: db})
		calls := 0
		s.Groups().Authed.With(s.Idempotent()).Post("/things", func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
		post := func(who string) int {
			req := httptest.NewRequest("POST", "/api/v1/things", strings.NewReader(`{"a":1}`))
			req.Header.Set("Authorization", "Bearer "+who)
			req.Header.Set("Idempotency-Key", "same-key-123")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			return rec.Code
		}
		count := func() (rows, principals int) {
			t.Helper()
			err := db.Read().QueryRowContext(context.Background(),
				`SELECT COUNT(*), COUNT(DISTINCT principal_id) FROM idempotency_keys WHERE key = ?`, "same-key-123").Scan(&rows, &principals)
			if err != nil {
				t.Fatal(err)
			}
			return rows, principals
		}
		for _, who := range []string{"customer:requester", "staff:agent", "customer:requester"} {
			if c := post(who); c != http.StatusCreated {
				t.Fatalf("%s: %d", who, c)
			}
		}
		if rows, principals := count(); rows != 2 || principals != 2 {
			t.Fatalf("same key from two principals: %d rows for %d principals, want 2/2", rows, principals)
		}
		if calls != 2 {
			t.Fatalf("handler ran %d times, want 2 (one per principal, replay served from the row)", calls)
		}
	})
}
