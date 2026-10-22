// SPDX-License-Identifier: AGPL-3.0-only

package problems

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestErrorMapping: every registered slug renders with its status and type (PLT-010).
func TestErrorMapping(t *testing.T) {
	for slug, reg := range Registry {
		rec := httptest.NewRecorder()
		Write(rec, httptest.NewRequest("GET", "/x", nil), New(slug, "d"))
		var p map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if rec.Code != reg.Status || p["type"] != BaseURI+slug || p["title"] != reg.Title {
			t.Errorf("%s: %d %v", slug, rec.Code, p)
		}
	}
}

func TestInternalErrorNoLeak(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, httptest.NewRequest("GET", "/x", nil), errors.New("pq: SELECT password FROM users\ngoroutine 1 [running]"))
	if b := rec.Body.String(); strings.Contains(b, "SELECT") || strings.Contains(b, "goroutine") {
		t.Fatalf("leak: %s", b)
	}
}
