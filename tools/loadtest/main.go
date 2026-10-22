// SPDX-License-Identifier: AGPL-3.0-only

// Command loadtest drives the DEP-024 load profile against a running instance
// and gates memory and GC (CONVENTIONS §10): 50 agents, 200 WebSocket
// connections and 10 req/s of mixed REST traffic for a configurable duration.
// It is a separate main package (stdlib + coder/websocket, both already in
// go.mod) and is never linked into the featherdesk binary.
//
// Target requirements (scripts/loadtest.sh sets them up):
//   - FD_BOOTSTRAP_ADMIN_EMAIL / _PASSWORD for the admin used to create agents;
//   - FD_AUTH_STAFF_LOCAL=true so the created agents can log in locally;
//   - FD_METRICS_TOKEN (GC CPU share comes from /metrics);
//   - FD_TRUSTED_PROXIES covering this tool's address: each agent logs in with
//     its own X-Forwarded-For so the per-IP login throttle (10/min) is not hit;
//   - FD_INSECURE_COOKIES=true when the target is plain http.
//
// WebSocket delivery lag is measured on this tool's clock only: every internal
// note carries a unique marker; the lag is the time from just before the REST
// POST is sent to the arrival of the WebSocket frame that contains the marker,
// on every connection that receives it (one sample per delivery). Server and
// container clocks are never compared.
//
// Exit status 1 when peak RSS > -max-rss-mib, GC CPU share > -max-gc, any
// WebSocket closed with a code other than 1001/normal during the run, the WS
// delivery lag p95 exceeds -max-ws-lag, or no marked note was delivered at all.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

type options struct {
	base         string
	adminEmail   string
	adminPass    string
	metricsToken string
	container    string
	agents       int
	wsConns      int
	rps          float64
	duration     time.Duration
	maxRSSMiB    float64
	maxGC        float64
	maxWSLag     time.Duration
	password     string
	probe        bool
	insecureTLS  bool
}

func main() {
	var o options
	flag.StringVar(&o.base, "url", "http://127.0.0.1:8080", "instance base URL (= its FD_PUBLIC_URL)")
	flag.StringVar(&o.adminEmail, "admin-email", "admin@load.test", "bootstrap admin email")
	flag.StringVar(&o.adminPass, "admin-password", "load-admin-password-123", "bootstrap admin password")
	flag.StringVar(&o.metricsToken, "metrics-token", "", "FD_METRICS_TOKEN of the target (GC share)")
	flag.StringVar(&o.container, "container", "", "docker container to sample RSS from (docker stats); empty = /metrics Go memory only")
	flag.IntVar(&o.agents, "agents", 50, "agents to create and log in")
	flag.IntVar(&o.wsConns, "ws", 200, "WebSocket connections, spread over the agents (≤ 5 per user)")
	flag.Float64Var(&o.rps, "rps", 10, "REST requests per second (mixed)")
	flag.DurationVar(&o.duration, "duration", 10*time.Minute, "load duration")
	flag.Float64Var(&o.maxRSSMiB, "max-rss-mib", 120, "hard limit for peak container RSS")
	flag.Float64Var(&o.maxGC, "max-gc", 0.10, "hard limit for GC CPU share over the run")
	flag.DurationVar(&o.maxWSLag, "max-ws-lag", 2*time.Second, "hard limit for the WebSocket delivery lag p95 (500 ms target is reported)")
	flag.StringVar(&o.password, "agent-password", "load-agent-password-123", "password given to created agents")
	flag.BoolVar(&o.probe, "probe", false, "only check a WebSocket ping/pong through -url (e.g. a reverse proxy), then exit")
	flag.BoolVar(&o.insecureTLS, "k", false, "accept a self-signed TLS certificate (probe through a local proxy)")
	flag.Parse()
	if o.insecureTLS {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local proxy tests only
	}
	o.base = strings.TrimRight(o.base, "/")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	runFn := run
	if o.probe {
		runFn = probe
	}
	if err := runFn(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- client --

type client struct {
	base    string
	http    *http.Client
	cookie  string
	csrf    string
	bearer  string
	fwdFor  string
	latency *latencies
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

func (c *client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	} else if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
		req.Header.Set("Origin", c.base)
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	if c.fwdFor != "" {
		req.Header.Set("X-Forwarded-For", c.fwdFor)
	}
	t0 := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if c.latency != nil {
		c.latency.add(time.Since(t0))
	}
	for _, sc := range resp.Cookies() {
		if sc.Name == "fd_session" && sc.Value != "" {
			c.cookie = sc.Name + "=" + sc.Value
		}
	}
	if resp.StatusCode >= 300 {
		return &apiError{resp.StatusCode, strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// login logs in with a local password and returns a PAT with ticket scopes.
func login(ctx context.Context, base, email, pass, fwd string) (*client, error) {
	c := &client{base: base, http: &http.Client{Timeout: 15 * time.Second, Transport: http.DefaultTransport}, fwdFor: fwd}
	var me struct {
		ID        string `json:"id"`
		CSRFToken string `json:"csrf_token"`
	}
	for attempt := 0; ; attempt++ {
		err := c.do(ctx, "POST", "/api/v1/auth/login", map[string]any{"audience": "staff", "email": email, "password": pass}, &me)
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusTooManyRequests && attempt < 20 {
			time.Sleep(3 * time.Second)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("login %s: %w", email, err)
		}
		break
	}
	c.csrf = me.CSRFToken
	return c, nil
}

func (c *client) pat(ctx context.Context, name string, scopes ...string) (string, error) {
	var tok struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, "POST", "/api/v1/me/tokens", map[string]any{"name": name, "scopes": scopes}, &tok); err != nil {
		return "", fmt.Errorf("create token: %w", err)
	}
	return tok.Token, nil
}

// ------------------------------------------------------------- latencies --

type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d time.Duration) { l.mu.Lock(); l.d = append(l.d, d); l.mu.Unlock() }
func (l *latencies) pct(p float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), l.d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[min(len(s)-1, int(float64(len(s))*p))]
}

// -------------------------------------------------------------------- run --

type stats struct {
	reqOK, reqErr, req5xx atomic.Int64
	wsOpen, wsFrames      atomic.Int64
	wsBadClose            atomic.Int64
	mu                    sync.Mutex
	badCloses             []string

	// WS delivery lag (client clock): marker → send time of the marked note.
	marks     sync.Map // string → time.Time
	marksSent atomic.Int64
	marksSeen sync.Map // string → struct{} (marker delivered at least once)
	wsLag     latencies
}

// markPrefix tags the internal notes whose WebSocket delivery is timed.
const markPrefix = "lagmark-"

// observeFrame records a delivery-lag sample when frame carries a marker.
func (st *stats) observeFrame(frame []byte, at time.Time) {
	i := bytes.Index(frame, []byte(markPrefix))
	if i < 0 {
		return
	}
	j := i + len(markPrefix)
	for j < len(frame) && frame[j] >= '0' && frame[j] <= '9' {
		j++
	}
	mark := string(frame[i:j])
	if v, ok := st.marks.Load(mark); ok {
		st.wsLag.add(at.Sub(v.(time.Time)))
		st.marksSeen.LoadOrStore(mark, struct{}{})
	}
}

func run(ctx context.Context, o options) error {
	lat := &latencies{}
	st := &stats{}

	// 1. admin session, teams, agents
	fmt.Printf("== setup: admin login, %d agents\n", o.agents)
	admin, err := login(ctx, o.base, o.adminEmail, o.adminPass, "10.250.0.1")
	if err != nil {
		return err
	}
	var teams []struct {
		ID string `json:"id"`
	}
	var teamPage struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := admin.do(ctx, "GET", "/api/v1/admin/teams", nil, &teamPage); err != nil {
		if err2 := admin.do(ctx, "GET", "/api/v1/admin/teams", nil, &teams); err2 != nil {
			return fmt.Errorf("list teams: %w", err)
		}
	} else {
		teams = teamPage.Items
	}
	teamIDs := make([]string, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}
	runID := strconv.FormatInt(time.Now().Unix(), 36)
	agents := make([]*client, 0, o.agents)
	for i := 0; i < o.agents; i++ {
		email := fmt.Sprintf("load-%s-%02d@load.test", runID, i)
		body := map[string]any{"email": email, "name": fmt.Sprintf("Load Agent %02d", i), "role": "agent", "tier": 1 + i%3, "team_ids": teamIDs, "password": o.password}
		if err := admin.do(ctx, "POST", "/api/v1/admin/staff", body, nil); err != nil {
			return fmt.Errorf("create agent %d: %w", i, err)
		}
		c, err := login(ctx, o.base, email, o.password, fmt.Sprintf("10.250.%d.%d", 1+i/250, 1+i%250))
		if err != nil {
			return err
		}
		tok, err := c.pat(ctx, "loadtest", "tickets:read", "tickets:write")
		if err != nil {
			return err
		}
		agents = append(agents, &client{base: o.base, http: &http.Client{Timeout: 15 * time.Second}, bearer: tok, latency: lat})
	}

	// ticket ids to work on
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := agents[0].do(ctx, "GET", "/api/v1/agent/tickets?limit=100", nil, &page); err != nil {
		return fmt.Errorf("list tickets: %w", err)
	}
	if len(page.Items) == 0 {
		return errors.New("no tickets visible: seed the instance first (featherdesk seed --tickets 1000)")
	}
	ids := make([]string, len(page.Items))
	for i, t := range page.Items {
		ids[i] = t.ID
	}
	lat.mu.Lock()
	lat.d = nil // only load-phase latencies count
	lat.mu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, o.duration)
	defer cancel()
	var wg sync.WaitGroup

	// 2. WebSockets: spread over agents, ≤ 4 per user (per-user cap is 5)
	fmt.Printf("== %d WebSocket connections\n", o.wsConns)
	wsURL := "ws" + strings.TrimPrefix(o.base, "http") + "/api/v1/ws"
	for i := 0; i < o.wsConns; i++ {
		a := agents[i%len(agents)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			wsClient(runCtx, wsURL, a.bearer, st)
		}()
		time.Sleep(10 * time.Millisecond)
	}

	// 3. samplers
	var peakRSS, lastRSS atomic.Uint64 // MiB × 100
	var rssSamples atomic.Int64
	sample := func() {
		if v, ok := containerRSS(o.container); ok {
			lastRSS.Store(uint64(v * 100))
			rssSamples.Add(1)
			for {
				p := peakRSS.Load()
				if uint64(v*100) <= p || peakRSS.CompareAndSwap(p, uint64(v*100)) {
					break
				}
			}
		}
	}
	gc0, cpu0, gcErr := gcCPU(ctx, o.base, o.metricsToken)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		sample()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				sample()
				fmt.Printf("  t+%3.0fs rss=%.1f MiB req ok=%d err=%d ws open=%d frames=%d\n",
					time.Since(start).Seconds(), float64(lastRSS.Load())/100, st.reqOK.Load(), st.reqErr.Load(), st.wsOpen.Load(), st.wsFrames.Load())
			}
		}
	}()

	// 4. REST mix at o.rps
	fmt.Printf("== %.0f req/s for %s\n", o.rps, o.duration)
	interval := time.Duration(float64(time.Second) / o.rps)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	lastEscalate := time.Now()
	n := 0
loop:
	for {
		select {
		case <-runCtx.Done():
			break loop
		case <-tick.C:
		}
		n++
		a := agents[rand.IntN(len(agents))]
		id := ids[rand.IntN(len(ids))]
		var method, path, mark string
		var body any
		switch r := rand.IntN(100); {
		case time.Since(lastEscalate) >= time.Minute:
			lastEscalate = time.Now()
			method, path, body = "POST", "/api/v1/agent/tickets/"+id+"/escalate", map[string]any{"reason": "load test escalation"}
		case r < 40:
			method, path = "GET", "/api/v1/agent/tickets?limit=25"
		case r < 70:
			method, path = "GET", "/api/v1/agent/tickets/"+id
		case r < 85:
			mark = fmt.Sprintf("%s%d", markPrefix, n)
			method, path, body = "POST", "/api/v1/agent/tickets/"+id+"/messages", map[string]any{"kind": "internal", "body_md": "load note " + mark}
		case r < 95:
			status := []string{"open", "pending", "on_hold"}[rand.IntN(3)]
			method, path, body = "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"status": status}
		default:
			method, path = "GET", "/api/v1/agent/tickets/counts"
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if mark != "" {
				st.marks.Store(mark, time.Now())
				st.marksSent.Add(1)
			}
			err := a.do(runCtx, method, path, body, nil)
			var ae *apiError
			switch {
			case err == nil:
				st.reqOK.Add(1)
			case errors.As(err, &ae) && ae.status >= 500:
				st.req5xx.Add(1)
				st.reqErr.Add(1)
			case errors.As(err, &ae): // 4xx: forbidden transitions, tier rules … are expected
				st.reqOK.Add(1)
			case runCtx.Err() != nil:
			default:
				st.reqErr.Add(1)
			}
		}()
	}
	// Let in-flight deliveries of the last notes arrive before closing the sockets.
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	cancel()
	wg.Wait()
	sample()

	// 5. report
	gc1, cpu1, gcErr2 := gcCPU(ctx, o.base, o.metricsToken)
	gcShare := -1.0
	if gcErr == nil && gcErr2 == nil && cpu1 > cpu0 {
		gcShare = (gc1 - gc0) / (cpu1 - cpu0)
	}
	peak := float64(peakRSS.Load()) / 100
	fail := false
	verdict := func(ok bool) string {
		if !ok {
			fail = true
			return "FAIL"
		}
		return "ok"
	}
	fmt.Println()
	fmt.Println("| Metric | Measured | Target | Hard limit | Result |")
	fmt.Println("|---|---|---|---|---|")
	if rssSamples.Load() > 0 {
		fmt.Printf("| Peak RSS (docker stats, %d samples) | %.1f MiB | ≤ 80 MiB | ≤ %.0f MiB | %s |\n", rssSamples.Load(), peak, o.maxRSSMiB, verdict(peak <= o.maxRSSMiB))
	} else {
		fmt.Println("| Peak RSS | not sampled (-container unset) | ≤ 80 MiB | - | - |")
	}
	if gcShare >= 0 {
		fmt.Printf("| GC CPU share (of available CPU) | %.2f%% | - | ≤ %.0f%% | %s |\n", gcShare*100, o.maxGC*100, verdict(gcShare <= o.maxGC))
	} else {
		fmt.Printf("| GC CPU share | unavailable (%v) | - | ≤ %.0f%% | - |\n", errors.Join(gcErr, gcErr2), o.maxGC*100)
	}
	fmt.Printf("| REST p50 / p95 | %s / %s | p95 < 200 ms | - | - |\n", lat.pct(0.50).Round(time.Millisecond), lat.pct(0.95).Round(time.Millisecond))
	fmt.Printf("| REST requests ok / errors / 5xx | %d / %d / %d | - | 0 5xx | %s |\n", st.reqOK.Load(), st.reqErr.Load(), st.req5xx.Load(), verdict(st.req5xx.Load() == 0))
	fmt.Printf("| WebSockets open at end / frames received | %d / %d | %d | - | - |\n", st.wsOpen.Load(), st.wsFrames.Load(), o.wsConns)
	delivered := 0
	st.marksSeen.Range(func(_, _ any) bool { delivered++; return true })
	st.wsLag.mu.Lock()
	lagN := len(st.wsLag.d)
	st.wsLag.mu.Unlock()
	if lagN > 0 {
		p95 := st.wsLag.pct(0.95)
		fmt.Printf("| WS delivery lag p50 / p95 / max (%d deliveries of %d/%d marked notes) | %s / %s / %s | p95 < 500 ms | p95 ≤ %s | %s |\n",
			lagN, delivered, st.marksSent.Load(), st.wsLag.pct(0.50).Round(time.Millisecond), p95.Round(time.Millisecond),
			st.wsLag.pct(1).Round(time.Millisecond), o.maxWSLag, verdict(p95 <= o.maxWSLag))
	} else {
		fmt.Printf("| WS delivery lag | no marked note delivered (%d sent) | p95 < 500 ms | p95 ≤ %s | %s |\n", st.marksSent.Load(), o.maxWSLag, verdict(st.marksSent.Load() == 0))
	}
	fmt.Printf("| WebSocket abnormal closes | %d | 0 | 0 | %s |\n", st.wsBadClose.Load(), verdict(st.wsBadClose.Load() == 0))
	for _, c := range st.badCloses {
		fmt.Println("  close:", c)
	}
	if fail {
		return errors.New("a hard limit was exceeded")
	}
	return nil
}

var start = time.Now()

func wsClient(ctx context.Context, url, token string, st *stats) {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"featherdesk.v1"}})
	if err != nil {
		if ctx.Err() == nil {
			st.wsBadClose.Add(1)
			st.mu.Lock()
			st.badCloses = append(st.badCloses, "dial: "+err.Error())
			st.mu.Unlock()
		}
		return
	}
	st.wsOpen.Add(1)
	defer st.wsOpen.Add(-1)
	conn.SetReadLimit(1 << 20)
	sub, _ := json.Marshal(map[string]any{"op": "subscribe", "id": "1", "topics": []string{"tickets", "counts"}})
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		return
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			code := websocket.CloseStatus(err)
			if ctx.Err() == nil && code != websocket.StatusGoingAway && code != websocket.StatusNormalClosure {
				st.wsBadClose.Add(1)
				st.mu.Lock()
				st.badCloses = append(st.badCloses, fmt.Sprintf("code=%d %v", code, err))
				st.mu.Unlock()
			}
			_ = conn.CloseNow()
			return
		}
		st.wsFrames.Add(1)
		st.observeFrame(data, time.Now())
	}
}

// containerRSS reads the container memory as docker stats reports it (MiB).
func containerRSS(container string) (float64, bool) {
	if container == "" {
		return 0, false
	}
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", container).Output()
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return 0, false
	}
	v := f[0]
	mult := 1.0
	for _, u := range []struct {
		s string
		m float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"GB", 1000 * 1000 * 1000 / 1048576.0}, {"MB", 1000 * 1000 / 1048576.0}, {"kB", 1000 / 1048576.0}, {"B", 1 / 1048576.0}} {
		if strings.HasSuffix(v, u.s) {
			v, mult = strings.TrimSuffix(v, u.s), u.m
			break
		}
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

// gcCPU returns the cumulative GC and total available CPU seconds from /metrics.
func gcCPU(ctx context.Context, base, token string) (gc, total float64, err error) {
	if token == "" {
		return 0, 0, errors.New("no -metrics-token")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("/metrics: %s", resp.Status)
	}
	b, _ := io.ReadAll(resp.Body)
	var okGC, okTotal bool
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		switch f[0] {
		case "fd_go_gc_cpu_seconds_total":
			gc, _ = strconv.ParseFloat(f[1], 64)
			okGC = true
		case "fd_go_cpu_seconds_total":
			total, _ = strconv.ParseFloat(f[1], 64)
			okTotal = true
		}
	}
	if !okGC || !okTotal {
		return 0, 0, errors.New("/metrics: fd_go_gc_cpu_seconds_total or fd_go_cpu_seconds_total missing")
	}
	return gc, total, nil
}

// probe logs in as the admin, creates a PAT and checks one WebSocket
// round trip (subscribe + ping → pong) through -url. It is how the reverse
// proxy examples in deploy/examples are verified (DEP-015).
func probe(ctx context.Context, o options) error {
	admin, err := login(ctx, o.base, o.adminEmail, o.adminPass, "")
	if err != nil {
		return err
	}
	tok, err := admin.pat(ctx, "ws-probe", "tickets:read")
	if err != nil {
		return err
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok)
	wsURL := "ws" + strings.TrimPrefix(o.base, "http") + "/api/v1/ws"
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"featherdesk.v1"}, HTTPClient: &http.Client{Transport: http.DefaultTransport}})
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL, err)
	}
	defer func() { _ = conn.CloseNow() }()
	if conn.Subprotocol() != "featherdesk.v1" {
		return fmt.Errorf("subprotocol %q (status %d)", conn.Subprotocol(), resp.StatusCode)
	}
	ping, _ := json.Marshal(map[string]any{"op": "ping", "id": "probe-1"})
	if err := conn.Write(dctx, websocket.MessageText, ping); err != nil {
		return err
	}
	for {
		_, data, err := conn.Read(dctx)
		if err != nil {
			return fmt.Errorf("waiting for pong: %w", err)
		}
		if strings.Contains(string(data), "pong") {
			fmt.Printf("ok: %s upgraded (subprotocol %s), ping → %s\n", wsURL, conn.Subprotocol(), data)
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return nil
		}
	}
}
