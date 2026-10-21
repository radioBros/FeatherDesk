// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"container/list"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
)

// ---- readiness (PLT-013) ----

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.ready != nil {
		if err := s.ready(ctx); err != nil {
			Logger(ctx).Warn("readyz failed", "err", err)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "not ready\n")
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain")
	engine := ""
	if s.db != nil {
		engine = " " + s.db.Dialect.String()
	}
	_, _ = io.WriteString(w, "ready"+engine+"\n")
}

// ---- metrics (PLT-021): hand-written Prometheus text format ----

var latencyBuckets = []float64{0.005, 0.025, 0.1, 0.25, 1, 5}

// Metrics is a tiny registry: request counters, a latency histogram and
// gauges supplied by modules (queue depth, WebSocket connections, …).
type Metrics struct {
	mu       sync.Mutex
	requests map[string]int64 // "method|route|class"
	hist     []int64
	sum      float64
	count    int64
	gauges   map[string]gauge
}

type gauge struct {
	help    string
	fn      func() float64
	counter bool
}

func NewMetrics() *Metrics {
	return &Metrics{requests: map[string]int64{}, hist: make([]int64, len(latencyBuckets)), gauges: map[string]gauge{}}
}

// Gauge registers a gauge evaluated on scrape. name must be a valid metric name.
func (m *Metrics) Gauge(name, help string, fn func() float64) {
	m.mu.Lock()
	m.gauges[name] = gauge{help: help, fn: fn}
	m.mu.Unlock()
}

// Counter registers a monotonically increasing value read on scrape; by
// Prometheus convention its name ends in "_total" (promtool check metrics).
func (m *Metrics) Counter(name, help string, fn func() float64) {
	m.mu.Lock()
	m.gauges[name] = gauge{help: help, fn: fn, counter: true}
	m.mu.Unlock()
}

func (m *Metrics) observe(method, route string, status int, d time.Duration) {
	class := fmt.Sprintf("%dxx", status/100)
	m.mu.Lock()
	m.requests[method+"|"+route+"|"+class]++
	sec := d.Seconds()
	for i, b := range latencyBuckets {
		if sec <= b {
			m.hist[i]++
		}
	}
	m.sum += sec
	m.count++
	m.mu.Unlock()
}

// Write renders the exposition text.
func (m *Metrics) Write(w io.Writer) {
	m.mu.Lock()
	keys := make([]string, 0, len(m.requests))
	for k := range m.requests {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintln(w, "# HELP st_http_requests_total HTTP requests by method, route and status class.")
	fmt.Fprintln(w, "# TYPE st_http_requests_total counter")
	for _, k := range keys {
		p := strings.SplitN(k, "|", 3)
		fmt.Fprintf(w, "st_http_requests_total{method=%q,route=%q,code=%q} %d\n", p[0], p[1], p[2], m.requests[k])
	}
	fmt.Fprintln(w, "# HELP st_http_request_duration_seconds Request latency.")
	fmt.Fprintln(w, "# TYPE st_http_request_duration_seconds histogram")
	for i, b := range latencyBuckets {
		fmt.Fprintf(w, "st_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", b, m.hist[i])
	}
	fmt.Fprintf(w, "st_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.count)
	fmt.Fprintf(w, "st_http_request_duration_seconds_sum %g\nst_http_request_duration_seconds_count %d\n", m.sum, m.count)
	names := make([]string, 0, len(m.gauges))
	for n := range m.gauges {
		names = append(names, n)
	}
	sort.Strings(names)
	gs := make([]gauge, len(names))
	for i, n := range names {
		gs[i] = m.gauges[n]
	}
	m.mu.Unlock()
	for i, n := range names {
		typ := "gauge"
		if gs[i].counter {
			typ = "counter"
		}
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %g\n", n, gs[i].help, n, typ, n, gs[i].fn())
	}
	// Go runtime: GC CPU share is a load-test gate (DEP-024).
	samples := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/memory/classes/total:bytes"}, {Name: "/sched/goroutines:goroutines"}}
	metrics.Read(samples)
	val := func(s metrics.Sample) float64 {
		switch s.Value.Kind() {
		case metrics.KindFloat64:
			return s.Value.Float64()
		case metrics.KindUint64:
			return float64(s.Value.Uint64())
		}
		return 0
	}
	fmt.Fprintf(w, "# HELP st_go_gc_cpu_seconds_total Estimated CPU time spent in the garbage collector.\n# TYPE st_go_gc_cpu_seconds_total counter\nst_go_gc_cpu_seconds_total %g\n", val(samples[0]))
	fmt.Fprintf(w, "# HELP st_go_cpu_seconds_total Estimated CPU time used by the Go runtime and program.\n# TYPE st_go_cpu_seconds_total counter\nst_go_cpu_seconds_total %g\n", val(samples[1]))
	fmt.Fprintf(w, "# HELP st_go_memory_bytes Memory mapped by the Go runtime.\n# TYPE st_go_memory_bytes gauge\nst_go_memory_bytes %g\n", val(samples[2]))
	fmt.Fprintf(w, "# HELP st_go_goroutines Live goroutines.\n# TYPE st_go_goroutines gauge\nst_go_goroutines %g\n", val(samples[3]))
}

// serveMetrics is enabled only when ST_METRICS_TOKEN is set (404 otherwise).
func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	token := ""
	if s.cfg != nil {
		token = s.cfg.MetricsToken
	}
	if token == "" {
		problems.Write(w, r, problems.NotFound())
		return
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		problems.Write(w, r, problems.Unauthenticated())
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.metrics.Write(w)
}

// ---- branding assets (PLT-024) ----

var brandFile = regexp.MustCompile(`^([0-9a-f]{64})\.([a-z0-9]{2,5})$`)

type brandEntry struct {
	sha, key, ct string
}

// brandCache is a tiny LRU of sha256 → blob key (≤ 32 entries).
type brandCache struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[string]*list.Element
}

func newBrandCache(n int) *brandCache {
	return &brandCache{max: n, ll: list.New(), m: map[string]*list.Element{}}
}

func (c *brandCache) get(sha string) (brandEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[sha]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(brandEntry), true
	}
	return brandEntry{}, false
}

func (c *brandCache) put(e brandEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[e.sha]; ok {
		return
	}
	c.m[e.sha] = c.ll.PushFront(e)
	for c.ll.Len() > c.max {
		old := c.ll.Back()
		c.ll.Remove(old)
		delete(c.m, old.Value.(brandEntry).sha)
	}
}

func (s *Server) serveBranding(w http.ResponseWriter, r *http.Request) {
	m := brandFile.FindStringSubmatch(chi.URLParam(r, "file"))
	if m == nil {
		problems.Write(w, r, problems.NotFound())
		return
	}
	sha := m[1]
	e, ok := s.brand.get(sha)
	if !ok {
		err := s.db.Read().QueryRowContext(r.Context(), `SELECT blob_key, content_type FROM branding_assets WHERE sha256 = ? LIMIT 1`, sha).Scan(&e.key, &e.ct)
		if err != nil {
			problems.Write(w, r, problems.NotFound())
			return
		}
		e.sha = sha
		s.brand.put(e)
	}
	rc, _, err := s.blob.Open(r.Context(), e.key)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Type", e.ct)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.Contains(e.ct, "svg") {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, path.Base(e.key), time.Time{}, rc)
}

// serveFavicon redirects /favicon.ico to the uploaded favicon (immutable URL)
// so browsers that ignore <link rel=icon> still get the brand; without an
// upload the SPA's bundled favicon is served.
func (s *Server) withFavicon(spa http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/favicon.ico" {
			if spa != nil {
				spa.ServeHTTP(w, r)
			} else {
				problems.Write(w, r, problems.NotFound())
			}
			return
		}
		var sha, ct string
		err := s.db.Read().QueryRowContext(r.Context(), `SELECT sha256, content_type FROM branding_assets WHERE slot = 'favicon' ORDER BY created_at DESC LIMIT 1`).Scan(&sha, &ct)
		if err == nil && sha != "" {
			ext := "png"
			switch {
			case strings.Contains(ct, "svg"):
				ext = "svg"
			case strings.Contains(ct, "icon"):
				ext = "ico"
			case strings.Contains(ct, "webp"):
				ext = "webp"
			case strings.Contains(ct, "jpeg"):
				ext = "jpg"
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.Redirect(w, r, "/branding/"+sha+"."+ext, http.StatusFound)
			return
		}
		if spa != nil {
			spa.ServeHTTP(w, r)
			return
		}
		problems.Write(w, r, problems.NotFound())
	})
}
