// SPDX-License-Identifier: AGPL-3.0-only

// Package realtime is the WebSocket hub at /api/v1/ws (D-10, realtime_spec).
//
// The hub owns connections, topics, limits and the wire protocol. It knows
// nothing about tickets: feature modules register TopicAuthorizers (who may
// subscribe), a Replayer (resume) and event routers that call PublishFunc
// with a per-subscriber builder, so visibility (I-2…I-4, I-7, I-9) is decided
// in memory for every frame before it is sent.
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// Subprotocol is required on every connection.
const Subprotocol = "featherdesk.v1"

// Close codes (realtime_spec §7).
const (
	CloseUnauthenticated websocket.StatusCode = 4401
	CloseForbidden       websocket.StatusCode = 4403
	CloseSlowConsumer    websocket.StatusCode = 4008
	CloseRateLimited     websocket.StatusCode = 4029
)

// Authenticator resolves the caller of the upgrade request (cookie, PAT or ws-ticket).
type Authenticator interface {
	Authenticate(r *http.Request) (*domain.Principal, error)
}

// TopicAuthorizer decides whether p may subscribe to topic. Registered per prefix.
type TopicAuthorizer interface {
	AuthorizeTopic(ctx context.Context, p *domain.Principal, topic string) bool
}

// TopicAuthorizerFunc adapts a function.
type TopicAuthorizerFunc func(ctx context.Context, p *domain.Principal, topic string) bool

func (f TopicAuthorizerFunc) AuthorizeTopic(ctx context.Context, p *domain.Principal, topic string) bool {
	return f(ctx, p, topic)
}

// Admitter lets the authenticator refuse a principal that authenticated fine
// (the customer portal is switched off): the socket is accepted and closed
// with 4403 so the client sees a close code, not an HTTP error.
type Admitter interface {
	Admit(p *domain.Principal) bool
}

// Rechecker re-validates a principal periodically; an error closes the socket
// with 4401. The returned principal replaces the old one (role/tier changes).
type Rechecker interface {
	Recheck(ctx context.Context, p *domain.Principal) (*domain.Principal, error)
}

// Replayer serves `resume`: the frames after lastEventID that p may see on its
// subscribed topics. ok=false means the gap is too large → resync.
type Replayer interface {
	Replay(ctx context.Context, p *domain.Principal, topics []string, lastEventID string) (frames []Frame, ok bool)
}

// Frame is any server → client message.
type Frame struct {
	Type    string     `json:"type"`
	ID      string     `json:"id,omitempty"`
	OK      *bool      `json:"ok,omitempty"`
	Denied  []string   `json:"denied,omitempty"`
	Topic   string     `json:"topic,omitempty"`
	EventID string     `json:"event_id,omitempty"`
	Event   string     `json:"event,omitempty"`
	At      *time.Time `json:"at,omitempty"`
	Data    any        `json:"data,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	// Users is set on presence frames only; omitzero keeps an empty (non-nil)
	// list on the wire, as WsServerFrame requires `users` on presence.
	Users []Presence `json:"users,omitzero"`
}

// stamp gives an event frame its required `at` (WsServerFrame) when the
// publisher did not set one.
func stamp(f *Frame) {
	if f.Type == "event" && f.At == nil {
		at := time.Now().UTC()
		f.At = &at
	}
}

// staffOnlyTopic reports the topic classes realtime_spec §3 reserves to
// staff: a customer asking for one breaks the protocol and is closed with
// 4403 (§7), unlike a visibility denial, which is listed in ack.denied.
func staffOnlyTopic(topic string) bool {
	switch {
	case topic == "counts", topic == "kb":
		return true
	case strings.HasPrefix(topic, "queue:"), strings.HasPrefix(topic, "admin:"), strings.HasPrefix(topic, "project:"):
		return true
	}
	return false
}

type Presence struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// EventFrame builds the standard frame for a domain event.
func EventFrame(topic string, e events.Event, data any) Frame {
	at := e.CreatedAt
	return Frame{Type: "event", Topic: topic, EventID: e.ID, Event: e.Type, At: &at, Data: data}
}

// Options configure limits; zero values take realtime_spec §6 defaults.
type Options struct {
	PublicOrigin    string   // FD_PUBLIC_URL origin, required for cookie auth
	AllowedOrigins  []string // extra accepted origins (FD_ALLOWED_ORIGINS)
	MaxConns        int      // 1000
	MaxConnsPerUser int      // 5
	MaxTopics       int      // 200
	SendBuffer      int      // 64
	ReadLimit       int64    // 4096
	RatePerSecond   float64
	RateBurst       int
	PingInterval    time.Duration // 25s
	PingTimeout     time.Duration // 60s
	RecheckInterval time.Duration // 60s
	Logger          *slog.Logger
}

// Hub holds every live connection.
type Hub struct {
	opt  Options
	log  *slog.Logger
	auth atomic.Pointer[authBox]

	mu       sync.RWMutex
	conns    map[*conn]struct{}
	byUser   map[string]map[*conn]struct{}
	topics   map[string]map[*conn]struct{}
	authz    map[string]TopicAuthorizer // prefix → authorizer
	routers  []func(ctx context.Context, e events.Event)
	replayer Replayer
	recheck  Rechecker
	presence map[string]map[string]string // topic → user → state

	// Slots reserved by ServeHTTP between the cap check and add(): the
	// check-and-reserve is atomic, so concurrent upgrades cannot overshoot
	// the caps and a client sees the 429 deterministically.
	pending     int
	pendingUser map[string]int

	closed atomic.Bool
	total  atomic.Int64 // frames sent (metrics)
}

type authBox struct{ a Authenticator }

func NewHub(opt Options) *Hub {
	if opt.MaxConns <= 0 {
		opt.MaxConns = 1000
	}
	if opt.MaxConnsPerUser <= 0 {
		opt.MaxConnsPerUser = 5
	}
	if opt.MaxTopics <= 0 {
		opt.MaxTopics = 200
	}
	if opt.SendBuffer <= 0 {
		opt.SendBuffer = 64
	}
	if opt.ReadLimit <= 0 {
		opt.ReadLimit = 4096
	}
	if opt.RatePerSecond <= 0 {
		opt.RatePerSecond = 20
	}
	if opt.RateBurst <= 0 {
		opt.RateBurst = 40
	}
	if opt.PingInterval <= 0 {
		opt.PingInterval = 25 * time.Second
	}
	if opt.PingTimeout <= 0 {
		opt.PingTimeout = 60 * time.Second
	}
	if opt.RecheckInterval <= 0 {
		opt.RecheckInterval = 60 * time.Second
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	h := &Hub{
		opt: opt, log: opt.Logger.With("component", "realtime"),
		conns: map[*conn]struct{}{}, byUser: map[string]map[*conn]struct{}{}, topics: map[string]map[*conn]struct{}{},
		authz: map[string]TopicAuthorizer{}, presence: map[string]map[string]string{},
	}
	h.auth.Store(&authBox{a: nil})
	return h
}

// SetAuthenticator plugs in authn (ws-ticket, cookie, PAT).
func (h *Hub) SetAuthenticator(a Authenticator) { h.auth.Store(&authBox{a: a}) }

// SetRechecker plugs in the periodic principal re-validation.
func (h *Hub) SetRechecker(r Rechecker) {
	h.mu.Lock()
	h.recheck = r
	h.mu.Unlock()
}

// SetReplayer plugs in `resume` support (tickets module).
func (h *Hub) SetReplayer(r Replayer) {
	h.mu.Lock()
	h.replayer = r
	h.mu.Unlock()
}

// Authorize registers a TopicAuthorizer for topics equal to prefix or
// starting with it ("ticket:" matches "ticket:<id>"; "me" matches "me").
// The longest matching prefix wins; unknown topics are denied.
func (h *Hub) Authorize(prefix string, a TopicAuthorizer) {
	h.mu.Lock()
	h.authz[prefix] = a
	h.mu.Unlock()
}

// OnEvent registers an event router; HandleEvent calls every router.
func (h *Hub) OnEvent(fn func(ctx context.Context, e events.Event)) {
	h.mu.Lock()
	h.routers = append(h.routers, fn)
	h.mu.Unlock()
}

// HandleEvent is the queue's "realtime" consumer (non-durable, D-11).
func (h *Hub) HandleEvent(ctx context.Context, e events.Event) error {
	h.mu.RLock()
	routers := h.routers
	h.mu.RUnlock()
	for _, r := range routers {
		r(ctx, e)
	}
	return nil
}

// Overflow is the realtime queue's OnOverflow: frames were dropped, so every
// client must refetch.
func (h *Hub) Overflow(events.Event) {
	h.mu.RLock()
	cs := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		cs = append(cs, c)
	}
	h.mu.RUnlock()
	for _, c := range cs {
		c.enqueue(mustJSON(Frame{Type: "resync", Reason: "slow_consumer"}))
	}
}

// Publish sends frame to subscribers of topic for whom allow(p) is true (nil = all).
func (h *Hub) Publish(topic string, frame Frame, allow func(p *domain.Principal) bool) {
	frame.Topic = topic
	stamp(&frame)
	b := mustJSON(frame)
	h.publish(topic, func(p *domain.Principal) ([]byte, bool) {
		if allow != nil && !allow(p) {
			return nil, false
		}
		return b, true
	})
}

// PublishFunc builds the frame per subscriber (payload filtering by kind,
// e.g. customer frames omit staff-only fields). build returns ok=false to skip.
// Builds are cached per (kind, role, tier) within one call.
func (h *Hub) PublishFunc(topic string, build func(p *domain.Principal) (Frame, bool)) {
	h.publish(topic, func(p *domain.Principal) ([]byte, bool) {
		f, ok := build(p)
		if !ok {
			return nil, false
		}
		f.Topic = topic
		stamp(&f)
		return mustJSON(f), true
	})
}

func (h *Hub) publish(topic string, fn func(p *domain.Principal) ([]byte, bool)) {
	h.mu.RLock()
	subs := make([]*conn, 0, len(h.topics[topic]))
	for c := range h.topics[topic] {
		subs = append(subs, c)
	}
	h.mu.RUnlock()
	for _, c := range subs {
		if b, ok := fn(c.principal()); ok {
			c.enqueue(b)
		}
	}
}

// SendUser sends a frame on the "me" topic of every connection of userID.
func (h *Hub) SendUser(userID string, frame Frame) {
	frame.Topic = "me"
	stamp(&frame)
	b := mustJSON(frame)
	for _, c := range h.userConns(userID) {
		if c.subscribed("me") {
			c.enqueue(b)
		}
	}
}

// Broadcast sends a frame to every connection subscribed to "me" (e.g.
// settings.branding_changed).
func (h *Hub) Broadcast(frame Frame) {
	frame.Topic = "me"
	stamp(&frame)
	b := mustJSON(frame)
	h.mu.RLock()
	cs := make([]*conn, 0, len(h.topics["me"]))
	for c := range h.topics["me"] {
		cs = append(cs, c)
	}
	h.mu.RUnlock()
	for _, c := range cs {
		c.enqueue(b)
	}
}

// Revoke drops topics a subscriber may no longer see after an event (e.g.
// escalated past their tier): sends ticket.access_lost and unsubscribes.
func (h *Hub) Revoke(topic string, lost func(p *domain.Principal) bool) {
	h.mu.RLock()
	var drop []*conn
	for c := range h.topics[topic] {
		if lost(c.principal()) {
			drop = append(drop, c)
		}
	}
	h.mu.RUnlock()
	lostFrame := Frame{Type: "event", Topic: topic, Event: events.TicketAccessLost}
	stamp(&lostFrame)
	b := mustJSON(lostFrame)
	for _, c := range drop {
		c.enqueue(b)
		h.unsubscribe(c, topic)
	}
}

// CloseUser closes every connection of userID (disable, logout, session revoke).
func (h *Hub) CloseUser(userID string, code websocket.StatusCode, reason string) {
	for _, c := range h.userConns(userID) {
		c.close(code, reason)
	}
}

// CloseWhere closes every connection whose principal matches pred.
func (h *Hub) CloseWhere(pred func(*domain.Principal) bool, code websocket.StatusCode, reason string) {
	h.mu.RLock()
	var cs []*conn
	for c := range h.conns {
		if pred(c.principal()) {
			cs = append(cs, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range cs {
		c.close(code, reason)
	}
}

// CloseSession closes connections authenticated by one session.
func (h *Hub) CloseSession(sessionID string, code websocket.StatusCode, reason string) {
	h.mu.RLock()
	var cs []*conn
	for c := range h.conns {
		if p := c.principal(); p.SessionID == sessionID {
			cs = append(cs, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range cs {
		c.close(code, reason)
	}
}

// Shutdown closes every connection with 1001 (clients reconnect + resume).
func (h *Hub) Shutdown() {
	h.closed.Store(true)
	h.mu.RLock()
	cs := make([]*conn, 0, len(h.conns))
	for c := range h.conns {
		cs = append(cs, c)
	}
	h.mu.RUnlock()
	for _, c := range cs {
		c.close(websocket.StatusGoingAway, "server shutdown")
	}
}

// Conns is the number of live connections (metrics).
func (h *Hub) Conns() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// FramesSent counts frames enqueued (metrics).
func (h *Hub) FramesSent() int64 { return h.total.Load() }

func (h *Hub) userConns(userID string) []*conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*conn, 0, len(h.byUser[userID]))
	for c := range h.byUser[userID] {
		out = append(out, c)
	}
	return out
}

func (h *Hub) authorizer(topic string) TopicAuthorizer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	best := ""
	var a TopicAuthorizer
	for prefix, az := range h.authz {
		match := topic == prefix || (strings.HasSuffix(prefix, ":") && strings.HasPrefix(topic, prefix))
		if match && len(prefix) > len(best) {
			best, a = prefix, az
		}
	}
	return a
}

// ---- HTTP upgrade ----

// ServeHTTP handles GET /api/v1/ws.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.closed.Load() {
		problems.Write(w, r, problems.New(problems.SlugUnavailable, "shutting down"))
		return
	}
	ab := h.auth.Load()
	if ab.a == nil {
		problems.Write(w, r, problems.Unauthenticated())
		return
	}
	p, err := ab.a.Authenticate(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if p == nil {
		problems.Write(w, r, problems.Unauthenticated())
		return
	}
	cookieAuth := p.SessionID != "" && p.TokenID == ""
	if cookieAuth && !h.originAllowed(r.Header.Get("Origin")) {
		// Cross-site WebSocket hijacking guard.
		problems.Write(w, r, problems.Forbidden("origin not allowed"))
		return
	}
	if p.TokenID != "" && !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeKBRead) {
		problems.Write(w, r, problems.Forbidden("token lacks tickets:read"))
		return
	}
	if !h.reserve(p.UserID) {
		problems.Write(w, r, problems.RateLimited(5*time.Second))
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}, InsecureSkipVerify: true})
	if err != nil {
		h.release(p.UserID)
		return
	}
	if ws.Subprotocol() != Subprotocol {
		h.release(p.UserID)
		_ = ws.Close(websocket.StatusProtocolError, "subprotocol featherdesk.v1 required")
		return
	}
	if ad, ok := ab.a.(Admitter); ok && !ad.Admit(p) {
		h.release(p.UserID)
		_ = ws.Close(CloseForbidden, "not available")
		return
	}
	ws.SetReadLimit(h.opt.ReadLimit)
	// The hub manages its own deadlines; clear the server's write deadline.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	c := newConn(h, ws, p)
	if !h.add(c) {
		_ = ws.Close(CloseRateLimited, "too many connections")
		return
	}
	// Return from the handler: the hijacked connection lives on with a single
	// reader goroutine; writers and timers are on demand (idle cost budget).
	c.start()
	// The client may have seen the 101 (and the user may have been disabled
	// or the session revoked) before the conn was registered, so a CloseUser /
	// CloseSession fired in that window found nothing. Re-validate now that
	// the conn is registered: any later revocation reaches it via CloseUser.
	c.doRecheck(c.ctx)
}

// reserve atomically checks both caps (counting reserved slots) and takes a
// slot for uid; it is consumed by add or returned by release.
func (h *Hub) reserve(uid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.conns)+h.pending >= h.opt.MaxConns || len(h.byUser[uid])+h.pendingUser[uid] >= h.opt.MaxConnsPerUser {
		return false
	}
	if h.pendingUser == nil {
		h.pendingUser = map[string]int{}
	}
	h.pending++
	h.pendingUser[uid]++
	return true
}

func (h *Hub) unreserveLocked(uid string) {
	h.pending--
	if h.pendingUser[uid]--; h.pendingUser[uid] <= 0 {
		delete(h.pendingUser, uid)
	}
}

func (h *Hub) release(uid string) {
	h.mu.Lock()
	h.unreserveLocked(uid)
	h.mu.Unlock()
}

// add consumes the slot reserved by reserve.
func (h *Hub) add(c *conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	uid := c.principal().UserID
	h.unreserveLocked(uid)
	h.conns[c] = struct{}{}
	if h.byUser[uid] == nil {
		h.byUser[uid] = map[*conn]struct{}{}
	}
	h.byUser[uid][c] = struct{}{}
	return true
}

func (h *Hub) remove(c *conn) {
	h.mu.Lock()
	delete(h.conns, c)
	uid := c.principal().UserID
	if m := h.byUser[uid]; m != nil {
		delete(m, c)
		if len(m) == 0 {
			delete(h.byUser, uid)
		}
	}
	var presenceTopics []string
	for t := range c.topics {
		if m := h.topics[t]; m != nil {
			delete(m, c)
			if len(m) == 0 {
				delete(h.topics, t)
			}
		}
		if ps := h.presence[t]; ps != nil {
			if _, ok := ps[uid]; ok && len(h.byUser[uid]) == 0 {
				delete(ps, uid)
				presenceTopics = append(presenceTopics, t)
			}
		}
	}
	h.mu.Unlock()
	for _, t := range presenceTopics {
		h.broadcastPresence(t)
	}
}

func (h *Hub) subscribe(c *conn, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.topics[topic] == nil {
		h.topics[topic] = map[*conn]struct{}{}
	}
	h.topics[topic][c] = struct{}{}
	c.topics[topic] = struct{}{}
}

func (h *Hub) unsubscribe(c *conn, topic string) {
	h.mu.Lock()
	if m := h.topics[topic]; m != nil {
		delete(m, c)
		if len(m) == 0 {
			delete(h.topics, topic)
		}
	}
	delete(c.topics, topic)
	h.mu.Unlock()
}

func (h *Hub) setPresence(topic, userID, state string) {
	h.mu.Lock()
	if h.presence[topic] == nil {
		h.presence[topic] = map[string]string{}
	}
	if state == "idle" || state == "" {
		delete(h.presence[topic], userID)
	} else {
		h.presence[topic][userID] = state
	}
	h.mu.Unlock()
	h.broadcastPresence(topic)
}

func (h *Hub) broadcastPresence(topic string) {
	h.mu.RLock()
	users := make([]Presence, 0, len(h.presence[topic])) // never nil: `users` is required
	for id, st := range h.presence[topic] {
		users = append(users, Presence{ID: id, State: st})
	}
	h.mu.RUnlock()
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	f := Frame{Type: "presence", Users: users}
	// Presence is staff-only (realtime_spec §2).
	h.Publish(topic, f, func(p *domain.Principal) bool { return p.IsStaff() })
}

func mustJSON(f Frame) []byte {
	b, err := json.Marshal(f)
	if err != nil {
		// The frame is lost: the client must refetch, like after a gap.
		b, _ = json.Marshal(Frame{Type: "resync", Reason: "gap_too_large"})
	}
	return b
}

// originAllowed reports whether a cookie-authenticated upgrade comes from
// FD_PUBLIC_URL or one of FD_ALLOWED_ORIGINS (cross-site hijacking guard).
func (h *Hub) originAllowed(origin string) bool {
	if h.opt.PublicOrigin == "" || origin == "" {
		return false
	}
	if strings.EqualFold(origin, h.opt.PublicOrigin) {
		return true
	}
	for _, o := range h.opt.AllowedOrigins {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}
