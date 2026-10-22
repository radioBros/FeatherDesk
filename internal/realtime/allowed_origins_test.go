// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import "testing"

// TestWS_AllowedOriginsAccepted: FD_ALLOWED_ORIGINS extends the cookie-auth
// Origin guard; other origins are still refused.
func TestWS_AllowedOriginsAccepted(t *testing.T) {
	_, srv := newHub(t, Options{AllowedOrigins: []string{"http://localhost:3200"}})
	c, _, err := dial(t, srv, cookieHdr("customer:requester", "http://localhost:3200"), true)
	if err != nil {
		t.Fatalf("allowed origin: %v", err)
	}
	_ = c.CloseNow() // safety net
	if _, resp, err := dial(t, srv, cookieHdr("customer:requester", "http://localhost:9999"), true); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("other origin must stay 403, got %v %v", resp, err)
	}
}
