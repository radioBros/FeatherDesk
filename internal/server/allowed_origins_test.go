// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/config"
)

// TestAllowedOriginsAcceptedForCSRF: ST_ALLOWED_ORIGINS adds origins that pass
// the cookie-auth Origin check; anything else is still refused.
func TestAllowedOriginsAcceptedForCSRF(t *testing.T) {
	cfg := testConfig(t, map[string]string{"ST_ALLOWED_ORIGINS": "http://localhost:3200, http://127.0.0.1:3200"})
	s := newTestServer(t, Options{Config: cfg})
	for origin, want := range map[string]int{
		"https://help.example.com": 200,
		"http://localhost:3200":    200,
		"http://127.0.0.1:3200":    200,
		"http://localhost:9999":    403,
		"https://evil.example":     403,
	} {
		got := do(s, "POST", "/api/v1/me/thing", func(r *http.Request) {
			cookie("customer:requester")(r)
			r.Header.Set("X-CSRF-Token", "good")
			r.Header.Set("Origin", origin)
		}).Code
		if got != want {
			t.Errorf("origin %s: got %d want %d", origin, got, want)
		}
	}
	csp := do(s, "GET", "/api/v1/ping", nil).Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "ws://localhost:3200") || !strings.Contains(csp, "wss://help.example.com") {
		t.Errorf("CSP connect-src misses allowed origins: %s", csp)
	}
}

func TestAllowedOriginsValidated(t *testing.T) {
	for _, bad := range []string{"localhost:3200", "ftp://x", "http://h/path"} {
		_, err := config.Load(func(k string) string {
			return map[string]string{"ST_SECRET_KEY": strings.Repeat("x", 32), "ST_PUBLIC_URL": "https://help.example.com", "ST_ALLOWED_ORIGINS": bad}[k]
		})
		if err == nil || !strings.Contains(err.Error(), "ST_ALLOWED_ORIGINS") {
			t.Errorf("%q: want ST_ALLOWED_ORIGINS error, got %v", bad, err)
		}
	}
}
