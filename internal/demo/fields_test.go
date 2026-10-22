// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// specChecked validates every response of h against the OpenAPI spec.
type specChecked struct {
	t *testing.T
	h http.Handler
}

func (s specChecked) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	vreq := r.Clone(context.Background())
	vreq.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	res := rec.Result()
	apispec.ValidateResponse(s.t, vreq, res)
	maps.Copy(w.Header(), res.Header)
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(rec.Body.Bytes())
}

// TestSeed_OrderNumberOnlyForBilling: the demo `order_number` field (text,
// required, customer-editable, scoped to Billing) neither blocks a ticket
// without a category or in another category nor is it skipped in Billing.
func TestSeed_OrderNumberOnlyForBilling(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		if _, err := run(t, db, Options{Tickets: 5, Now: fixedNow}); err != nil {
			t.Fatal(err)
		}
		h, _ := stackFor(t, db)
		c := newClient(specChecked{t, h}, "http://localhost:8080", "localhost:8080", "10.98.0.1")
		if err := c.login("customer", "uma.user@acme.test", DefaultPassword); err != nil {
			t.Fatal(err)
		}
		var raw []map[string]any
		if err := c.get("/portal/categories", &raw); err != nil {
			t.Fatal(err)
		}
		var billing, general, project string
		for _, x := range raw {
			switch x["name"] {
			case "Billing & invoices":
				billing, project = x["id"].(string), x["project_id"].(string)
			case "General question":
				general = x["id"].(string)
			}
		}
		if billing == "" || general == "" {
			t.Fatalf("categories %v", raw)
		}
		create := func(cat any, cf map[string]any) error {
			body := map[string]any{"subject": "Question", "body_md": "Hello", "project_id": project, "category_id": cat}
			if cf != nil {
				body["custom_fields"] = cf
			}
			return c.post("/portal/tickets", body, nil)
		}
		for _, tc := range []struct {
			cat any
			cf  map[string]any
		}{
			{nil, map[string]any{"order_number": ""}},
			{nil, nil},
			{general, map[string]any{"order_number": ""}},
			{billing, map[string]any{"order_number": "INV-2026-001"}},
		} {
			if err := create(tc.cat, tc.cf); err != nil {
				t.Errorf("category %v, fields %v: %v", tc.cat, tc.cf, err)
			}
		}
		var apiErr *APIError
		if err := create(billing, nil); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity || !bytes.Contains([]byte(apiErr.Body), []byte(`"required"`)) {
			t.Fatalf("billing without order_number: %v", err)
		}
	})
}
