// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// conn is one client. An idle connection costs one goroutine (the reader):
// frames are written by a short-lived writer goroutine spawned when the send
// buffer becomes non-empty, and ping/recheck run from time.AfterFunc timers.
// Topics are guarded by the hub mutex.
type conn struct {
	hub    *Hub
	ws     *websocket.Conn
	p      atomic.Pointer[domain.Principal]
	topics map[string]struct{}
	sendMu sync.Mutex
	send   chan []byte   // allocated on first frame: idle sockets carry no buffer
	sent   chan struct{} // writer progress signal (cap 1) for replay backpressure
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	writing atomic.Bool
	slow    atomic.Bool
	once    sync.Once

	closeMu     sync.Mutex
	closeCode   websocket.StatusCode
	closeReason string

	timerMu sync.Mutex
	ping    *time.Timer
	recheck *time.Timer

	// client message rate (token bucket), touched only by the reader
	tokens float64
	last   time.Time
}

func newConn(h *Hub, ws *websocket.Conn, p *domain.Principal) *conn {
	c := &conn{
		hub: h, ws: ws, topics: map[string]struct{}{},
		sent:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		tokens: float64(h.opt.RateBurst), last: time.Now(),
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.p.Store(p)
	return c
}

func (c *conn) principal() *domain.Principal { return c.p.Load() }

func (c *conn) subscribed(topic string) bool {
	c.hub.mu.RLock()
	defer c.hub.mu.RUnlock()
	_, ok := c.topics[topic]
	return ok
}

func (c *conn) start() {
	c.timerMu.Lock()
	c.ping = time.AfterFunc(c.hub.opt.PingInterval, c.doPing)
	c.recheck = time.AfterFunc(c.hub.opt.RecheckInterval, c.doRecheckTimer)
	c.timerMu.Unlock()
	go c.readLoop()
}

// sendBuf returns the send buffer, allocating it on first use.
func (c *conn) sendBuf() chan []byte {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.send == nil {
		c.send = make(chan []byte, c.hub.opt.SendBuffer)
	}
	return c.send
}

// enqueue never blocks: a full buffer marks the client slow (resync + 4008).
func (c *conn) enqueue(b []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	ch := c.sendBuf()
	select {
	case ch <- b:
		c.hub.total.Add(1)
		c.kickWriter()
	default:
		if c.slow.CompareAndSwap(false, true) {
			go c.closeSlow()
		}
	}
}

// replayWait bounds how long a resume waits for the client to drain its
// send buffer before the client is treated as a slow consumer (a variable
// only so tests can shorten it).
var replayWait = 10 * time.Second

// enqueueReplay queues one replayed frame with backpressure: a resume may
// replay up to 1 000 events (realtime_spec §5), far more than the send
// buffer, so it fills the buffer only up to a low-water mark (a quarter of
// it stays free for live frames published meanwhile) and waits for the
// writer to drain, instead of overflowing into 4008. It runs on the
// connection's reader goroutine. It reports false when the connection closed
// or the client did not read for replayWait (then it is closed as slow).
func (c *conn) enqueueReplay(ctx context.Context, b []byte) bool {
	ch := c.sendBuf()
	limit := max(1, cap(ch)-cap(ch)/4)
	var timer *time.Timer
	for {
		select {
		case <-c.done:
			return false
		default:
		}
		if len(ch) < limit {
			c.enqueue(b)
			return !c.slow.Load()
		}
		c.kickWriter()
		if timer == nil {
			timer = time.NewTimer(replayWait)
			defer timer.Stop()
		}
		select {
		case <-c.sent: // the writer made progress: re-check the level
		case <-c.done:
			return false
		case <-ctx.Done():
			return false
		case <-timer.C:
			if c.slow.CompareAndSwap(false, true) {
				go c.closeSlow()
			}
			return false
		}
	}
}

// kickWriter starts a writer goroutine unless one is running; it drains the
// buffer and exits when empty.
func (c *conn) kickWriter() {
	if !c.writing.CompareAndSwap(false, true) {
		return
	}
	go c.writer()
}

func (c *conn) sendCh() chan []byte {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.send
}

func (c *conn) writer() {
	send := c.sendCh()
	for {
		for {
			select {
			case b := <-send:
				if c.slow.Load() {
					continue
				}
				wctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
				err := c.ws.Write(wctx, websocket.MessageText, b)
				cancel()
				if err != nil {
					c.close(websocket.StatusGoingAway, "write failed")
					c.writing.Store(false)
					return
				}
				select { // wake a replay waiting for buffer space
				case c.sent <- struct{}{}:
				default:
				}
				continue
			default:
			}
			break
		}
		c.writing.Store(false)
		// A frame may have arrived after the drain and before the flag reset.
		if len(send) == 0 || !c.writing.CompareAndSwap(false, true) {
			return
		}
	}
}

// closeSlow drops the backlog, tells the client to refetch and closes 4008.
func (c *conn) closeSlow() {
	send := c.sendCh()
	for len(send) > 0 {
		<-send
	}
	wctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	_ = c.ws.Write(wctx, websocket.MessageText, mustJSON(Frame{Type: "resync", Reason: "slow_consumer"}))
	cancel()
	c.close(CloseSlowConsumer, "slow consumer")
}

// close sends a close frame (once) and releases everything.
func (c *conn) close(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		c.closeMu.Lock()
		c.closeCode, c.closeReason = code, reason
		c.closeMu.Unlock()
		close(c.done)
		c.timerMu.Lock()
		if c.ping != nil {
			c.ping.Stop()
		}
		if c.recheck != nil {
			c.recheck.Stop()
		}
		c.timerMu.Unlock()
		c.hub.remove(c)
		go func() {
			_ = c.ws.Close(code, reason) // waits briefly for the peer's close frame
			c.cancel()
		}()
	})
}

func (c *conn) doPing() {
	select {
	case <-c.done:
		return
	default:
	}
	pctx, cancel := context.WithTimeout(c.ctx, c.hub.opt.PingTimeout)
	err := c.ws.Ping(pctx)
	cancel()
	if err != nil {
		c.close(websocket.StatusGoingAway, "ping timeout")
		return
	}
	c.timerMu.Lock()
	if c.ping != nil {
		c.ping.Reset(c.hub.opt.PingInterval)
	}
	c.timerMu.Unlock()
}

func (c *conn) doRecheckTimer() {
	select {
	case <-c.done:
		return
	default:
	}
	c.doRecheck(c.ctx)
	c.timerMu.Lock()
	if c.recheck != nil {
		c.recheck.Reset(c.hub.opt.RecheckInterval)
	}
	c.timerMu.Unlock()
}

func (c *conn) doRecheck(ctx context.Context) {
	c.hub.mu.RLock()
	rc := c.hub.recheck
	c.hub.mu.RUnlock()
	if rc == nil {
		return
	}
	np, err := rc.Recheck(ctx, c.principal())
	if err != nil || np == nil {
		c.close(CloseUnauthenticated, "session no longer valid")
		return
	}
	old := c.principal()
	c.p.Store(np)
	if old.Role == np.Role && old.Tier == np.Tier && strings.Join(old.Teams, ",") == strings.Join(np.Teams, ",") {
		return
	}
	// Permissions changed: re-authorize every topic.
	c.hub.mu.RLock()
	topics := make([]string, 0, len(c.topics))
	for t := range c.topics {
		topics = append(topics, t)
	}
	c.hub.mu.RUnlock()
	dropped := false
	for _, t := range topics {
		if a := c.hub.authorizer(t); a == nil || !a.AuthorizeTopic(ctx, np, t) {
			c.hub.unsubscribe(c, t)
			dropped = true
		}
	}
	if dropped {
		c.enqueue(mustJSON(Frame{Type: "resync", Reason: "permissions_changed"}))
	}
}

type clientMsg struct {
	Op          string   `json:"op"`
	ID          string   `json:"id"`
	Topics      []string `json:"topics"`
	Topic       string   `json:"topic"`
	LastEventID string   `json:"last_event_id"`
	State       string   `json:"state"`
}

func (c *conn) allowMessage() bool {
	now := time.Now()
	c.tokens += now.Sub(c.last).Seconds() * c.hub.opt.RatePerSecond
	c.last = now
	if max := float64(c.hub.opt.RateBurst); c.tokens > max {
		c.tokens = max
	}
	if c.tokens < 1 {
		return false
	}
	c.tokens--
	return true
}

func (c *conn) readLoop() {
	defer c.close(websocket.StatusNormalClosure, "")
	for {
		typ, b, err := c.ws.Read(c.ctx)
		if err != nil {
			if strings.Contains(err.Error(), "read limited") || strings.Contains(err.Error(), "too big") {
				c.close(websocket.StatusMessageTooBig, "message too big")
			}
			return
		}
		if !c.allowMessage() {
			c.close(CloseRateLimited, "rate limited")
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m clientMsg
		if json.Unmarshal(b, &m) != nil {
			c.ack("", false, nil)
			continue
		}
		c.handle(c.ctx, m)
	}
}

func (c *conn) ack(id string, ok bool, denied []string) {
	c.enqueue(mustJSON(Frame{Type: "ack", ID: id, OK: &ok, Denied: denied}))
}

func (c *conn) handle(ctx context.Context, m clientMsg) {
	p := c.principal()
	switch m.Op {
	case "ping":
		c.enqueue(mustJSON(Frame{Type: "pong", ID: m.ID}))
	case "subscribe":
		var denied []string
		for _, t := range m.Topics {
			c.hub.mu.RLock()
			n := len(c.topics)
			_, already := c.topics[t]
			c.hub.mu.RUnlock()
			if already {
				continue
			}
			if n >= c.hub.opt.MaxTopics || len(t) > 200 {
				denied = append(denied, t)
				continue
			}
			if p.IsCustomer() && staffOnlyTopic(t) {
				// A staff-only topic class is a protocol violation for a
				// customer, not a visibility question (realtime_spec §7).
				c.close(CloseForbidden, "topic is staff-only")
				return
			}
			a := c.hub.authorizer(t)
			if a == nil || !a.AuthorizeTopic(ctx, p, t) {
				denied = append(denied, t)
				continue
			}
			c.hub.subscribe(c, t)
		}
		c.ack(m.ID, true, denied)
	case "unsubscribe":
		for _, t := range m.Topics {
			c.hub.unsubscribe(c, t)
		}
		c.ack(m.ID, true, nil)
	case "resume":
		c.hub.mu.RLock()
		rp := c.hub.replayer
		topics := make([]string, 0, len(c.topics))
		for t := range c.topics {
			topics = append(topics, t)
		}
		c.hub.mu.RUnlock()
		if rp == nil || m.LastEventID == "" {
			c.enqueue(mustJSON(Frame{Type: "resync", Reason: "gap_too_large"}))
			return
		}
		frames, ok := rp.Replay(ctx, p, topics, m.LastEventID)
		if !ok {
			c.enqueue(mustJSON(Frame{Type: "resync", Reason: "gap_too_large"}))
			return
		}
		for _, f := range frames {
			if !c.enqueueReplay(ctx, mustJSON(f)) {
				return
			}
		}
		c.ack(m.ID, true, nil)
	case "presence":
		if !p.IsStaff() || !strings.HasPrefix(m.Topic, "ticket:") || !c.subscribed(m.Topic) {
			c.ack(m.ID, false, nil)
			return
		}
		switch m.State {
		case "viewing", "typing", "idle":
			c.hub.setPresence(m.Topic, p.UserID, m.State)
			c.ack(m.ID, true, nil)
		default:
			c.ack(m.ID, false, nil)
		}
	default:
		c.ack(m.ID, false, nil)
	}
}
