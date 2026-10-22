// SPDX-License-Identifier: AGPL-3.0-only

package problems

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/store"
)

func TestWriteValidation(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/x", nil)
	Write(rec, req, Validation(Field("email", "email", "invalid email")))
	if rec.Code != 422 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type %q", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != BaseURI+"validation" || m["instance"] != "/api/v1/x" {
		t.Fatalf("body %v", m)
	}
	errs := m["errors"].([]any)
	if errs[0].(map[string]any)["code"] != "email" {
		t.Fatalf("errors %v", errs)
	}
}

func TestNotFoundMapping(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, httptest.NewRequest("GET", "/x", nil), fmt.Errorf("wrap: %w", store.ErrNotFound))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestInternalHidesDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, httptest.NewRequest("GET", "/x", nil), fmt.Errorf("secret db password leaked"))
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Body.String(); contains(got, "secret") {
		t.Fatalf("internal detail leaked: %s", got)
	}
}

func TestExtraAndRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, nil, InvalidTransition("closed", []string{"open"}))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 409 || m["allowed"] == nil {
		t.Fatalf("got %d %v", rec.Code, m)
	}
	rec = httptest.NewRecorder()
	Write(rec, nil, RateLimited(1500*time.Millisecond))
	if rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("retry-after %q", rec.Header().Get("Retry-After"))
	}
}

func TestRegistryComplete(t *testing.T) {
	for slug, r := range Registry {
		if r.Status < 400 || r.Title == "" {
			t.Errorf("bad registry entry %s", slug)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
