// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestMetricsExpositionLint (PLT-021) applies the `promtool check metrics`
// lint rules offline: every family has HELP and TYPE, counters end in
// "_total" and nothing else does. (promtool itself is run against a live
// container in the PLT-021 evidence; it is not a Go dependency.)
func TestMetricsExpositionLint(t *testing.T) {
	m := NewMetrics()
	m.Gauge("fd_ws_connections", "Open WebSocket connections.", func() float64 { return 3 })
	m.Counter("fd_queue_dispatched_total", "Events dispatched by the queue.", func() float64 { return 7 })
	m.observe("GET", "/api/v1/ping", 200, 3*time.Millisecond)
	var buf bytes.Buffer
	m.Write(&buf)
	help, typ := map[string]bool{}, map[string]string{}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) >= 3 && f[0] == "#" && f[1] == "HELP":
			help[f[2]] = len(f) > 3
		case len(f) == 4 && f[0] == "#" && f[1] == "TYPE":
			typ[f[2]] = f[3]
		}
	}
	if len(typ) < 7 {
		t.Fatalf("only %d metric families: %s", len(typ), buf.String())
	}
	for name, kind := range typ {
		if !help[name] {
			t.Errorf("%s has no help text", name)
		}
		if kind == "counter" != strings.HasSuffix(name, "_total") {
			t.Errorf("%s: type %s, but counters (and only counters) must end in _total", name, kind)
		}
	}
}
