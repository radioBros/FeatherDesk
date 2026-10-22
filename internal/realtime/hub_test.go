// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

const origin = "https://help.example.com"

// fakeAuth: "Authorization: Bearer <kind>:<role>[:scope]" → PAT principal;
// cookie "sess=<kind>:<role>" → session principal.
type fakeAuth struct{}

func (fakeAuth) Authenticate(r *http.Request) (*domain.Principal, error) {
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		parts := strings.SplitN(strings.TrimPrefix(v, "Bearer "), ":", 3)
		if parts[0] == "bad" {
			return nil, problems.Unauthenticated()
		}
		p := &domain.Principal{UserID: "u-" + parts[0] + parts[1], Kind: domain.UserKind(parts[0]), Role: domain.Role(parts[1]), Tier: 1, TokenID: "t1"}
		if len(parts) == 3 {
			p.Scopes = []string{parts[2]}
		}
		return p, nil
	}
	if c, err := r.Cookie("sess"); err == nil {
		kind, role, _ := strings.Cut(c.Value, ":")
		return &domain.Principal{UserID: "u-" + kind + role, Kind: domain.UserKind(kind), Role: domain.Role(role), Tier: 1, SessionID: "s-" + c.Value}, nil
	}
	return nil, nil
}

func newHub(t *testing.T, opt Options) (*Hub, *httptest.Server) {
	opt.PublicOrigin = origin
	h := NewHub(opt)
	h.SetAuthenticator(fakeAuth{})
	// "tickets": anyone; "counts": staff only; "ticket:<id>": allowed unless id starts with "secret".
	h.Authorize("tickets", TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool { return true }))
	h.Authorize("me", TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool { return true }))
	h.Authorize("counts", TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool { return p.IsStaff() }))
	h.Authorize("ticket:", TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		return !strings.HasPrefix(strings.TrimPrefix(topic, "ticket:"), "secret")
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return h, srv
}

func wsURL(srv *httptest.Server) string { return "ws" + strings.TrimPrefix(srv.URL, "http") }

func dial(t *testing.T, srv *httptest.Server, hdr http.Header, subproto bool) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	opts := &websocket.DialOptions{HTTPHeader: hdr}
	if subproto {
		opts.Subprotocols = []string{Subprotocol}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL(srv), opts)
}

func bearer(spec string) http.Header { return http.Header{"Authorization": {"Bearer " + spec}} }

func cookieHdr(spec, org string) http.Header {
	h := http.Header{"Cookie": {"sess=" + spec}}
	if org != "" {
		h.Set("Origin", org)
	}
	return h
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func noFrame(t *testing.T, c *websocket.Conn, within time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	if _, b, err := c.Read(ctx); err == nil {
		t.Fatalf("unexpected frame %s", b)
	}
}

func subscribe(t *testing.T, c *websocket.Conn, topics ...string) map[string]any {
	send(t, c, map[string]any{"op": "subscribe", "id": "s1", "topics": topics})
	return recv(t, c)
}

func TestSubprotocolRequired(t *testing.T) {
	_, srv := newHub(t, Options{})
	c, _, err := dial(t, srv, bearer("staff:agent"), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusProtocolError {
		t.Fatalf("want 1002, got %v", err)
	}
}

// TestLegacySubprotocolAccepted: clients written for SimpleTickets 1.0.0 still
// connect with simpletickets.v1.
func TestLegacySubprotocolAccepted(t *testing.T) {
	_, srv := newHub(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{Subprotocols: []string{LegacySubprotocol}, HTTPHeader: bearer("staff:agent")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	if c.Subprotocol() != LegacySubprotocol {
		t.Fatalf("negotiated %q", c.Subprotocol())
	}
	if f := subscribe(t, c, "tickets"); f["op"] == "error" {
		t.Fatalf("subscribe over the legacy subprotocol: %v", f)
	}
}

// TestWS_CookieOriginEnforced: cross-site WebSocket hijacking is refused (D-10).
func TestWS_CookieOriginEnforced(t *testing.T) {
	_, srv := newHub(t, Options{})
	if _, resp, err := dial(t, srv, cookieHdr("customer:requester", "https://evil.example"), true); err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("foreign origin must be 403, got %v %v", resp, err)
	}
	if _, resp, err := dial(t, srv, cookieHdr("customer:requester", ""), true); err == nil || resp.StatusCode != 403 {
		t.Fatalf("missing origin must be 403")
	}
	c, _, err := dial(t, srv, cookieHdr("customer:requester", origin), true)
	if err != nil {
		t.Fatalf("same origin: %v", err)
	}
	_ = c.CloseNow() // safety net: the server usually closed it already
}

func TestWS_AuthRequiredAndScopes(t *testing.T) {
	_, srv := newHub(t, Options{})
	if _, resp, err := dial(t, srv, nil, true); err == nil || resp.StatusCode != 401 {
		t.Fatal("anonymous must be 401")
	}
	if _, resp, err := dial(t, srv, bearer("bad:x"), true); err == nil || resp.StatusCode != 401 {
		t.Fatal("bad token must be 401")
	}
	if _, resp, err := dial(t, srv, bearer("staff:agent:directory:read"), true); err == nil || resp.StatusCode != 403 {
		t.Fatal("PAT without tickets:read must be 403")
	}
	c, _, err := dial(t, srv, bearer("staff:agent:tickets:read"), true)
	if err != nil {
		t.Fatal("PAT with tickets:read must connect:", err)
	}
	_ = c.CloseNow() // safety net: the server usually closed it already
}

func TestSubscribeAuthorizeAndPing(t *testing.T) {
	_, srv := newHub(t, Options{})
	c, _, err := dial(t, srv, bearer("customer:requester"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	ack := subscribe(t, c, "tickets", "ticket:a1", "ticket:secret9", "unknown")
	denied := ack["denied"].([]any)
	if ack["type"] != "ack" || ack["ok"] != true || len(denied) != 2 {
		t.Fatalf("ack %v", ack)
	}
	send(t, c, map[string]any{"op": "ping", "id": "p1"})
	if m := recv(t, c); m["type"] != "pong" || m["id"] != "p1" {
		t.Fatalf("pong %v", m)
	}
	send(t, c, map[string]any{"op": "bogus", "id": "x"})
	if m := recv(t, c); m["ok"] != false {
		t.Fatalf("unknown op must nack: %v", m)
	}
}

// TestWS_PublishFiltersPerSubscriber: the builder decides per principal, so a
// customer never receives a staff-only frame (I-7 shape test; the tickets
// module fuzzes every event type in TestWS_CustomerNeverReceivesInternal).
func TestWS_PublishFiltersPerSubscriber(t *testing.T) {
	h, srv := newHub(t, Options{})
	staff, _, _ := dial(t, srv, bearer("staff:agent"), true)
	cust, _, _ := dial(t, srv, bearer("customer:requester"), true)
	defer func() {
		_ = staff.CloseNow() // safety net: the server usually closed it already
	}()
	defer func() {
		_ = cust.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, staff, "ticket:t1")
	subscribe(t, cust, "ticket:t1")
	e := events.Event{ID: "e1", TicketID: "t1", Type: events.NoteCreated, CreatedAt: time.Now()}
	h.PublishFunc("ticket:t1", func(p *domain.Principal) (Frame, bool) {
		if !p.IsStaff() && !events.CustomerVisible(e.Type) {
			return Frame{}, false
		}
		return EventFrame("ticket:t1", e, map[string]string{"body": "internal"}), true
	})
	if m := recv(t, staff); m["event"] != events.NoteCreated || m["topic"] != "ticket:t1" {
		t.Fatalf("staff frame %v", m)
	}
	noFrame(t, cust, 200*time.Millisecond)
}

func TestHandleEventRoutersAndBroadcast(t *testing.T) {
	h, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, c, "tickets", "me")
	h.OnEvent(func(ctx context.Context, e events.Event) {
		h.Publish("tickets", EventFrame("tickets", e, nil), nil)
	})
	_ = h.HandleEvent(context.Background(), events.Event{ID: "e9", Type: events.TicketCreated, CreatedAt: time.Now()})
	if m := recv(t, c); m["event_id"] != "e9" {
		t.Fatalf("routed frame %v", m)
	}
	h.Broadcast(Frame{Type: "event", Event: "settings.branding_changed"})
	if m := recv(t, c); m["event"] != "settings.branding_changed" || m["topic"] != "me" {
		t.Fatalf("broadcast %v", m)
	}
	h.SendUser("u-staffagent", Frame{Type: "event", Event: "assigned_to_you"})
	if m := recv(t, c); m["event"] != "assigned_to_you" {
		t.Fatalf("send user %v", m)
	}
}

type fakeReplayer struct{}

func (fakeReplayer) Replay(ctx context.Context, p *domain.Principal, topics []string, last string) ([]Frame, bool) {
	if last == "too-old" {
		return nil, false
	}
	return []Frame{{Type: "event", Topic: "tickets", EventID: "e2", Event: events.MessageCreated}}, true
}

func TestResumeReplayAndResync(t *testing.T) {
	h, srv := newHub(t, Options{})
	h.SetReplayer(fakeReplayer{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, c, "tickets")
	send(t, c, map[string]any{"op": "resume", "id": "r1", "last_event_id": "e1"})
	if m := recv(t, c); m["event_id"] != "e2" {
		t.Fatalf("replayed %v", m)
	}
	if m := recv(t, c); m["type"] != "ack" {
		t.Fatalf("ack %v", m)
	}
	send(t, c, map[string]any{"op": "resume", "id": "r2", "last_event_id": "too-old"})
	if m := recv(t, c); m["type"] != "resync" || m["reason"] != "gap_too_large" {
		t.Fatalf("resync %v", m)
	}
}

func readClose(t *testing.T, c *websocket.Conn) websocket.StatusCode {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func TestSlowConsumerClosed4008(t *testing.T) {
	h, srv := newHub(t, Options{SendBuffer: 2})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, c, "tickets")
	// Never read: flood until the buffer overflows.
	for i := 0; i < 5000; i++ {
		h.Publish("tickets", Frame{Type: "event", Data: strings.Repeat("x", 512)}, nil)
	}
	if code := readClose(t, c); code != CloseSlowConsumer {
		t.Fatalf("want 4008, got %d", code)
	}
}

func TestRateLimitClose4029(t *testing.T) {
	_, srv := newHub(t, Options{RatePerSecond: 1, RateBurst: 3})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(map[string]any{"op": "ping"})
		_ = c.Write(context.Background(), websocket.MessageText, b)
	}
	if code := readClose(t, c); code != CloseRateLimited {
		t.Fatalf("want 4029, got %d", code)
	}
}

func TestMessageTooBig(t *testing.T) {
	_, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"op":"ping","id":"`+strings.Repeat("x", 5000)+`"}`))
	if code := readClose(t, c); code != websocket.StatusMessageTooBig {
		t.Fatalf("want 1009, got %d", code)
	}
}

func TestConnectionCaps(t *testing.T) {
	h, srv := newHub(t, Options{MaxConnsPerUser: 2, MaxConns: 3})
	var conns []*websocket.Conn
	for i := 0; i < 2; i++ {
		c, _, err := dial(t, srv, bearer("staff:agent"), true)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	if _, resp, err := dial(t, srv, bearer("staff:agent"), true); err == nil || resp.StatusCode != 429 {
		t.Fatal("per-user cap not enforced")
	}
	c3, _, err := dial(t, srv, bearer("staff:supervisor"), true)
	if err != nil {
		t.Fatal(err)
	}
	conns = append(conns, c3)
	if _, resp, err := dial(t, srv, bearer("staff:admin"), true); err == nil || resp.StatusCode != 429 {
		t.Fatal("global cap not enforced")
	}
	for _, c := range conns {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.Conns() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if h.Conns() != 0 {
		t.Fatalf("connections not released: %d", h.Conns())
	}
}

// TestConnectionCapsConcurrent: the caps are check-and-reserve atomic, so a
// burst of simultaneous upgrades admits exactly the cap and refuses the rest
// with a 429 (never a 101 followed by a close).
func TestConnectionCapsConcurrent(t *testing.T) {
	h, srv := newHub(t, Options{MaxConnsPerUser: 100, MaxConns: 3})
	var wg sync.WaitGroup
	var ok, refused atomic.Int64
	var mu sync.Mutex
	var conns []*websocket.Conn
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, resp, err := dial(t, srv, bearer("staff:agent"), true)
			if err == nil {
				ok.Add(1)
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			} else if resp != nil && resp.StatusCode == 429 {
				refused.Add(1)
			}
		}()
	}
	wg.Wait()
	defer func() {
		for _, c := range conns {
			_ = c.CloseNow()
		}
	}()
	if ok.Load() != 3 || refused.Load() != 9 {
		t.Fatalf("admitted %d, refused with 429 %d; want 3 and 9", ok.Load(), refused.Load())
	}
	waitConns(t, h, 3)
}

type fakeRecheck struct {
	revoke atomic.Bool
	tier   atomic.Int64
}

func (f *fakeRecheck) Recheck(ctx context.Context, p *domain.Principal) (*domain.Principal, error) {
	if f.revoke.Load() {
		return nil, errors.New("revoked")
	}
	np := *p
	if t := f.tier.Load(); t > 0 {
		np.Tier = int(t)
	}
	return &np, nil
}

// TestRecheckRevokedCloses4401 extends the I-5 test to sockets.
func TestRecheckRevokedCloses4401(t *testing.T) {
	h, srv := newHub(t, Options{RecheckInterval: 50 * time.Millisecond})
	rc := &fakeRecheck{}
	h.SetRechecker(rc)
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	rc.revoke.Store(true)
	if code := readClose(t, c); code != CloseUnauthenticated {
		t.Fatalf("want 4401, got %d", code)
	}
}

func TestRecheckPermissionsChangedResubscribes(t *testing.T) {
	h, srv := newHub(t, Options{RecheckInterval: 50 * time.Millisecond})
	rc := &fakeRecheck{}
	h.SetRechecker(rc)
	// Only tier-1 principals may hold "queue:x" (contrived, to observe a drop).
	h.Authorize("queue:", TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool { return p.Tier == 1 }))
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	if ack := subscribe(t, c, "queue:x"); ack["denied"] != nil {
		t.Fatalf("initial subscribe denied: %v", ack)
	}
	rc.tier.Store(2) // role/tier change → re-authorize → dropped
	if m := recv(t, c); m["type"] != "resync" || m["reason"] != "permissions_changed" {
		t.Fatalf("want resync permissions_changed, got %v", m)
	}
	h.Publish("queue:x", Frame{Type: "event"}, nil)
	noFrame(t, c, 150*time.Millisecond)
}

func TestRevokeSendsAccessLost(t *testing.T) {
	h, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, c, "ticket:t7")
	h.Revoke("ticket:t7", func(p *domain.Principal) bool { return p.Tier < 2 })
	if m := recv(t, c); m["event"] != events.TicketAccessLost {
		t.Fatalf("access lost %v", m)
	}
	h.Publish("ticket:t7", Frame{Type: "event"}, nil)
	noFrame(t, c, 150*time.Millisecond)
}

func TestPresenceStaffOnly(t *testing.T) {
	_, srv := newHub(t, Options{})
	a, _, _ := dial(t, srv, bearer("staff:agent"), true)
	cu, _, _ := dial(t, srv, bearer("customer:requester"), true)
	defer func() {
		_ = a.CloseNow() // safety net: the server usually closed it already
	}()
	defer func() {
		_ = cu.CloseNow() // safety net: the server usually closed it already
	}()
	subscribe(t, a, "ticket:t1")
	subscribe(t, cu, "ticket:t1")
	send(t, a, map[string]any{"op": "presence", "id": "p", "topic": "ticket:t1", "state": "typing"})
	m := recv(t, a)
	if m["type"] == "ack" {
		m = recv(t, a)
	}
	if m["type"] != "presence" {
		t.Fatalf("presence %v", m)
	}
	// The customer's first frame must be the nack to its own presence op,
	// proving the staff presence frame never reached it.
	send(t, cu, map[string]any{"op": "presence", "id": "p2", "topic": "ticket:t1", "state": "typing"})
	if m := recv(t, cu); m["type"] != "ack" || m["ok"] != false {
		t.Fatalf("customer got %v", m)
	}
}

func TestShutdownCloses1001(t *testing.T) {
	h, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	waitConns(t, h, 1)
	h.Shutdown()
	if code := readClose(t, c); code != websocket.StatusGoingAway {
		t.Fatalf("want 1001, got %d", code)
	}
	if _, resp, err := dial(t, srv, bearer("staff:agent"), true); err == nil || resp.StatusCode < 400 {
		t.Fatal("new connections must be refused after shutdown")
	}
}

func TestCloseUser(t *testing.T) {
	h, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}()
	waitConns(t, h, 1)
	h.CloseUser("u-staffagent", CloseUnauthenticated, "disabled")
	if code := readClose(t, c); code != CloseUnauthenticated {
		t.Fatalf("want 4401, got %d", code)
	}
}

// TestIdleMemoryPerConnection measures 1 000 idle sockets server-side only:
// the clients run in a child process, so this process's heap+stack delta is
// the hub's cost (budget ≤ 20 KB per connection, realtime_spec §6).
func TestIdleMemoryPerConnection(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("memory benchmark (meaningless under -race)")
	}
	h, srv := newHub(t, Options{MaxConns: 2000, MaxConnsPerUser: 2000})
	const n = 1000
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperIdleClients$")
	cmd.Env = append(os.Environ(), "FD_WS_HELPER_URL="+wsURL(srv), "FD_WS_HELPER_N="+strconv.Itoa(n))
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = cmd.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for h.Conns() < n && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.Conns() < n {
		t.Fatalf("only %d connections", h.Conns())
	}
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	per := float64((after.HeapInuse+after.StackInuse)-(before.HeapInuse+before.StackInuse)) / n
	t.Logf("idle sockets: %.1f KB per connection (server side, %d conns, %d goroutines)", per/1024, h.Conns(), runtime.NumGoroutine())
	if per > 20*1024 {
		t.Errorf("%.1f KB per connection exceeds the 20 KB budget", per/1024)
	}
}

// TestHelperIdleClients is the child process of TestIdleMemoryPerConnection.
func TestHelperIdleClients(t *testing.T) {
	u := os.Getenv("FD_WS_HELPER_URL")
	if u == "" {
		t.Skip("helper process only")
	}
	n, _ := strconv.Atoi(os.Getenv("FD_WS_HELPER_N"))
	conns := make([]*websocket.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{Subprotocols: []string{Subprotocol}, HTTPHeader: bearer("staff:agent")})
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	_, _ = io.Copy(io.Discard, os.Stdin) // hold until the parent closes stdin
	for _, c := range conns {
		_ = c.CloseNow() // safety net: the server usually closed it already
	}
}

// waitConns waits until the hub has registered n connections: a client's Dial
// returns when the 101 is written, slightly before the server registers it.
func waitConns(t *testing.T, h *Hub, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for h.Conns() < n {
		if time.Now().After(deadline) {
			t.Fatalf("hub registered %d connections, want %d", h.Conns(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCustomerStaffOnlyTopicCloses4403: a customer asking for a staff-only
// topic class (counts, queue:*, kb, admin:*) is closed with 4403
// (realtime_spec §7); staff get those topics through their authorizers.
func TestCustomerStaffOnlyTopicCloses4403(t *testing.T) {
	_, srv := newHub(t, Options{})
	for _, topic := range []string{"counts", "queue:t1", "kb", "admin:system"} {
		for _, hdr := range []http.Header{bearer("customer:requester:tickets:read"), cookieHdr("customer:org_manager", origin)} {
			c, _, err := dial(t, srv, hdr, true)
			if err != nil {
				t.Fatal(err)
			}
			send(t, c, map[string]any{"op": "subscribe", "id": "s", "topics": []string{"tickets", topic}})
			if code := readClose(t, c); code != CloseForbidden {
				t.Errorf("customer subscribing %s: close %d, want 4403", topic, code)
			}
			_ = c.CloseNow()
		}
	}
	st, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() { _ = st.CloseNow() }()
	if ack := subscribe(t, st, "counts"); ack["ok"] != true || ack["denied"] != nil {
		t.Fatalf("staff counts: %v", ack)
	}
}

// TestEventFramesCarryAt: every event frame has the `at` WsServerFrame
// requires, whoever published it (Publish, PublishFunc, SendUser, Broadcast,
// Revoke), and an empty presence frame still carries `users: []`.
func TestEventFramesCarryAt(t *testing.T) {
	h, srv := newHub(t, Options{})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() { _ = c.CloseNow() }()
	subscribe(t, c, "tickets", "me", "ticket:t1")
	h.Publish("tickets", Frame{Type: "event", Event: "x.publish"}, nil)
	h.PublishFunc("tickets", func(*domain.Principal) (Frame, bool) { return Frame{Type: "event", Event: "x.func"}, true })
	h.SendUser("u-staffagent", Frame{Type: "event", Event: "x.user"})
	h.Broadcast(Frame{Type: "event", Event: "x.broadcast"})
	h.Revoke("ticket:t1", func(*domain.Principal) bool { return true })
	for _, want := range []string{"x.publish", "x.func", "x.user", "x.broadcast", events.TicketAccessLost} {
		m := recv(t, c)
		if m["event"] != want || m["at"] == nil || m["topic"] == nil {
			t.Fatalf("want %s with at and topic, got %v", want, m)
		}
	}
	h.broadcastPresence("tickets")
	if m := recv(t, c); m["type"] != "presence" || m["users"] == nil {
		t.Fatalf("empty presence must carry users: %v", m)
	}
}

// bigReplayer returns n frames, far more than the send buffer.
type bigReplayer struct{ n int }

func (r bigReplayer) Replay(ctx context.Context, p *domain.Principal, topics []string, last string) ([]Frame, bool) {
	out := make([]Frame, r.n)
	for i := range out {
		out[i] = Frame{Type: "event", Topic: "tickets", EventID: strconv.Itoa(i), Event: events.MessageCreated, Data: strings.Repeat("x", 256)}
	}
	return out, true
}

// TestResumeReplaysBeyondSendBuffer (TKT-032): a resume larger than the
// 64-frame send buffer is delivered completely, in order, with the ack last
// and no 4008, while live frames keep being published.
func TestResumeReplaysBeyondSendBuffer(t *testing.T) {
	const n = 1500
	h, srv := newHub(t, Options{})
	h.SetReplayer(bigReplayer{n: n})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() { _ = c.CloseNow() }()
	subscribe(t, c, "tickets")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // live traffic during the replay
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
				h.Publish("tickets", Frame{Type: "event", Event: "live"}, nil)
			}
		}
	}()
	send(t, c, map[string]any{"op": "resume", "id": "r", "last_event_id": "e0"})
	next := 0
	for {
		m := recv(t, c)
		if m["type"] == "ack" {
			break
		}
		if m["event"] == "live" {
			continue
		}
		if m["event_id"] != strconv.Itoa(next) {
			t.Fatalf("replay out of order: want %d, got %v", next, m["event_id"])
		}
		next++
	}
	close(stop)
	wg.Wait()
	if next != n {
		t.Fatalf("replayed %d of %d frames before the ack", next, n)
	}
}

// TestResumeStalledClientClosedSlow: when the client stops draining during a
// replay, the resume gives up after replayWait and closes it as a slow
// consumer (resync + 4008) instead of blocking its reader forever.
func TestResumeStalledClientClosedSlow(t *testing.T) {
	defer func(d time.Duration) { replayWait = d }(replayWait)
	replayWait = 200 * time.Millisecond
	h, srv := newHub(t, Options{SendBuffer: 8})
	h.SetReplayer(bigReplayer{n: 100})
	c, _, _ := dial(t, srv, bearer("staff:agent"), true)
	defer func() { _ = c.CloseNow() }()
	subscribe(t, c, "tickets")
	waitConns(t, h, 1)
	h.mu.RLock()
	var sc *conn
	for k := range h.conns {
		sc = k
	}
	h.mu.RUnlock()
	sc.writing.Store(true) // simulate a writer stuck on a client that stopped reading
	send(t, c, map[string]any{"op": "resume", "id": "r", "last_event_id": "e0"})
	if m := recv(t, c); m["type"] != "resync" || m["reason"] != "slow_consumer" {
		t.Fatalf("want resync slow_consumer, got %v", m)
	}
	if code := readClose(t, c); code != CloseSlowConsumer {
		t.Fatalf("want 4008, got %d", code)
	}
}
