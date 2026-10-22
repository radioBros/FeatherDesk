// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"strings"
	"testing"
)

// TestSecurityHeaders_FramingOnlyForTemplatePreview (PLT-007): every response
// forbids framing, except the admin email-template preview, which the admin
// email and branding pages frame from the same origin. That one is sandboxed
// and may never be framed cross-origin.
func TestSecurityHeaders_FramingOnlyForTemplatePreview(t *testing.T) {
	s := newTestServer(t, Options{SPA: spaFS()})
	for _, path := range []string{"/", "/agent/admin/branding", "/api/v1/ping", "/api/v1/admin/email/templates/x/previewx", "/api/v1/admin/email/templates"} {
		h := do(s, "GET", path, nil).Header()
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: framing not forbidden: %q / %q", path, csp, h.Get("X-Frame-Options"))
		}
	}
	h := do(s, "GET", "/api/v1/admin/email/templates/customer.reply/preview?locale=en", nil).Header()
	csp := h.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox;") || !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("preview CSP = %q", csp)
	}
	if h.Get("X-Frame-Options") != "SAMEORIGIN" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("preview headers = %v", h)
	}
	if c := do(s, "POST", "/api/v1/admin/email/templates/customer.reply/preview", nil).Header().Get("Content-Security-Policy"); !strings.Contains(c, "frame-ancestors 'none'") {
		t.Fatalf("non-GET preview CSP = %q", c)
	}
}
