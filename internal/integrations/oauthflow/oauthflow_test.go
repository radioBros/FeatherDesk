// SPDX-License-Identifier: AGPL-3.0-only

package oauthflow

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestStatesSingleUseAndExpiry(t *testing.T) {
	s := NewStates()
	a, _ := s.Put("u1")
	b, _ := s.Put("u2")
	if a == b || len(a) < 32 {
		t.Fatalf("states %q %q", a, b)
	}
	if u, ok := s.Take(a); !ok || u != "u1" {
		t.Fatalf("take a = %q %v", u, ok)
	}
	if _, ok := s.Take(a); ok {
		t.Fatal("state reused")
	}
	if _, ok := s.Take("forged"); ok {
		t.Fatal("unknown state accepted")
	}
	s.Now = func() time.Time { return time.Now().Add(TTL + time.Second) }
	if _, ok := s.Take(b); ok {
		t.Fatal("expired state accepted")
	}
	s.Now = time.Now
	for range maxPending + 10 {
		s.Put("x")
	}
	if len(s.m) > maxPending {
		t.Fatalf("store grew to %d", len(s.m))
	}
}

func TestFinish(t *testing.T) {
	for _, c := range []struct{ reason, result, code string }{{"", "ok", ""}, {"access_denied", "error", "access_denied"}, {"<script>", "error", "error"}} {
		rec := httptest.NewRecorder()
		Finish(rec, httptest.NewRequest("GET", "/cb", nil), "https://h.test/agent", "slack", "st", c.reason)
		if rec.Code != http.StatusFound || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("%q: %d %v", c.reason, rec.Code, rec.Header())
		}
		loc, _ := url.Parse(rec.Header().Get("Location"))
		q := loc.Query()
		if loc.Path != "/agent/oauth/complete" || q.Get("provider") != "slack" || q.Get("state") != "st" || q.Get("result") != c.result || q.Get("reason") != c.code {
			t.Fatalf("%q: location %s", c.reason, loc)
		}
	}
}
