// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"runtime"
	"testing"
	"time"
)

// TestWSNoGoroutineLeak: disconnecting clients release their goroutines.
func TestWSNoGoroutineLeak(t *testing.T) {
	h, srv := newHub(t, Options{MaxConnsPerUser: 100})
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		c, _, err := dial(t, srv, bearer("staff:agent"), true)
		if err != nil {
			t.Fatal(err)
		}
		subscribe(t, c, "tickets")
		_ = c.CloseNow() // safety net: the server usually closed it already
	}
	deadline := time.Now().Add(5 * time.Second)
	for (h.Conns() > 0 || runtime.NumGoroutine() > before+5) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.Conns() != 0 || runtime.NumGoroutine() > before+5 {
		t.Fatalf("leak: conns=%d goroutines %d → %d", h.Conns(), before, runtime.NumGoroutine())
	}
}
