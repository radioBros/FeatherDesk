// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Queue names.
const (
	ConsumerName = "slack"      // durable event consumer
	JobPost      = "slack.post" // retry job (deferred_jobs.kind)
)

// MaxAttempts is the total number of post attempts (first + retries).
const MaxAttempts = 8

// RegisterQueue subscribes the durable consumer and the retry job. One
// worker: an update must not overtake its ticket's first post, or it would
// miss the thread (D-21 §8).
func (m *Module) RegisterQueue(q *queue.Queue) {
	m.q = q
	q.Subscribe(ConsumerName, queue.SubscribeOptions{Durable: true, Workers: 1, Timeout: 60 * time.Second},
		func(e events.Event) bool { return eventTypes[e.Type] }, m.handleEvent)
	q.Handle(JobPost, m.handleRetry, queue.JobOptions{Timeout: 30 * time.Second, MaxAttempts: MaxAttempts})
}

// matches reports whether rt selects the event (trigger, scope, filters).
func (rt *route) matches(e events.Event, trig string) bool {
	if trig == "" || !slices.Contains(rt.Events, trig) {
		return false
	}
	if trig == TrigNote && !rt.Internal {
		return false
	}
	if len(rt.ProjectIDs) > 0 && !slices.Contains(rt.ProjectIDs, e.Snapshot.Project()) {
		return false
	}
	return len(rt.Priorities) == 0 || slices.Contains(rt.Priorities, e.Snapshot.Priority)
}

// envelopes renders an event once per visibility scope.
type envelopes struct {
	m    *Module
	e    events.Event
	have map[bool]*envelope
}

func (s *envelopes) get(ctx context.Context, internal bool) (*envelope, error) {
	if env, ok := s.have[internal]; ok {
		return env, nil
	}
	b, err := s.m.render.Envelope(ctx, s.e, internal)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	if s.e.Type == events.TicketCreated && env.Data.Message == nil {
		// The webhook envelope of ticket.created has no message; the post
		// quotes the first one under the same rule (public, or internal
		// only for include_internal routes).
		var c events.Created
		if s.e.Decode(&c) == nil && c.MessageID != "" {
			var kind, body string
			err := s.m.db.Read().QueryRowContext(ctx, `SELECT kind, body_md FROM ticket_messages WHERE id = ?`, c.MessageID).Scan(&kind, &body)
			if err == nil && (kind == "public" || (internal && kind == "internal")) {
				env.Data.Message = &envMessage{Kind: kind, BodyMD: body}
			}
		}
	}
	s.have[internal] = &env
	return &env, nil
}

// handleEvent posts the event to every matching route. A failed first
// attempt becomes one deferred job for that route, so a slow channel never
// replays the event to the others.
func (m *Module) handleEvent(ctx context.Context, e events.Event) error {
	d, err := m.load(ctx)
	if err != nil {
		return err
	}
	if d.BotTokenEnc == "" || d.Error != "" {
		return nil // not connected or broken: nothing is queued meanwhile
	}
	tok, err := m.token(d)
	if err != nil {
		return err
	}
	routes, err := m.activeRoutes(ctx)
	if err != nil || len(routes) == 0 {
		return err
	}
	envs := &envelopes{m: m, e: e, have: map[bool]*envelope{}}
	pub, err := envs.get(ctx, false)
	if err != nil {
		return err
	}
	trig := pub.trigger()
	for i, rt := range routes {
		if !rt.matches(e, trig) {
			continue
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 20*time.Second {
			for _, rest := range routes[i:] {
				if rest.matches(e, trig) {
					if err := m.deferJob(context.WithoutCancel(ctx), rest.ID, e, 0, "deferred: consumer time budget", 0); err != nil {
						return err
					}
				}
			}
			return nil
		}
		env, err := envs.get(ctx, rt.Internal)
		if err != nil {
			return err
		}
		perr := m.post(ctx, tok, rt, e, env, trig)
		if perr == nil {
			continue
		}
		if retry, delay := m.classify(ctx, rt, perr); retry {
			if err := m.deferJob(context.WithoutCancel(ctx), rt.ID, e, 1, perr.Error(), delay); err != nil {
				return err
			}
		}
	}
	return nil
}

// post sends one event to one route: into the ticket's thread when the
// route threads and a root exists, else top-level (and the post becomes
// the root).
func (m *Module) post(ctx context.Context, tok string, rt *route, e events.Event, env *envelope, trig string) error {
	var root string
	if rt.Threads {
		err := m.db.Read().QueryRowContext(ctx, `SELECT ts FROM slack_threads WHERE route_id = ? AND ticket_id = ? AND channel_id = ?`, rt.ID, e.TicketID, rt.ChannelID).Scan(&root)
		if err != nil && !errors.Is(store.NotFound(err), store.ErrNotFound) {
			return err
		}
	}
	msg := render(env, trig, m.ticketURL(env), rt.IncludeBody, root != "")
	msg.Channel, msg.ThreadTS = rt.ChannelID, root
	// Breaches and escalations also show in the channel, not only the thread.
	msg.ReplyBroadcast = root != "" && (trig == TrigSLABreach || (trig == TrigEscalated && e.Type == events.TicketEscalated))
	ts, err := m.api.Post(ctx, tok, msg)
	m.routeResult(context.WithoutCancel(ctx), rt.ID, err)
	if err != nil {
		return err
	}
	if rt.Threads && root == "" && ts != "" {
		werr := m.db.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO slack_threads (route_id, ticket_id, channel_id, ts, created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (route_id, ticket_id) DO UPDATE SET channel_id = excluded.channel_id, ts = excluded.ts`,
				rt.ID, e.TicketID, rt.ChannelID, ts, store.Now())
			return err
		})
		if werr != nil && !errors.Is(werr, context.Canceled) {
			// The ticket may be gone (hard delete): the next update starts a new thread.
			m.log.Warn("slack: remember thread", "route", rt.ID, "ticket", e.TicketID, "err", werr)
		}
	}
	return nil
}

func (m *Module) ticketURL(env *envelope) string {
	t := env.Data.Ticket
	if t.Ref != "" && t.Ref[0] != '#' {
		return m.agent + "/tickets/" + t.Ref
	}
	return m.agent + "/tickets/" + jsonNumber(t.Number)
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

// classify decides what a failed post means: auth errors break the
// connection, channel and request errors are final, the rest retries.
func (m *Module) classify(ctx context.Context, rt *route, err error) (retry bool, delay time.Duration) {
	e, ok := AsAPIError(err)
	if !ok {
		return true, m.FirstRetry()
	}
	switch {
	case e.Auth():
		m.markBroken(context.WithoutCancel(ctx), e.Code)
		return false, 0
	case e.Permanent():
		m.log.Warn("slack: post failed", "route", rt.ID, "channel", rt.ChannelID, "error", e.Code)
		return false, 0
	case e.RetryAfter > 0:
		return true, e.RetryAfter
	}
	return true, m.FirstRetry()
}

// retryPayload is the deferred_jobs payload of JobPost.
type retryPayload struct {
	RouteID string       `json:"route_id"`
	Event   events.Event `json:"event"`
}

// deferJob writes one deferred_jobs row (attempts = already made); the
// queue's retry loop backs off from there.
func (m *Module) deferJob(ctx context.Context, routeID string, e events.Event, attempts int, lastErr string, delay time.Duration) error {
	b, err := json.Marshal(retryPayload{RouteID: routeID, Event: e})
	if err != nil {
		return err
	}
	now := m.Now().UTC().Truncate(time.Microsecond)
	if len(lastErr) > 2000 {
		lastErr = lastErr[:2000]
	}
	return m.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO deferred_jobs (id, kind, payload, attempts, max_attempts, next_run_at, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'retrying', ?, ?, ?)`,
			store.NewID(), JobPost, string(b), attempts, MaxAttempts, now.Add(delay), lastErr, now, now)
		return err
	})
}

// handleRetry runs one deferred post. A deleted or paused route, or a
// broken connection, ends the job; transient errors return to the queue.
func (m *Module) handleRetry(ctx context.Context, payload json.RawMessage) error {
	var p retryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	rt, err := m.loadRoute(ctx, p.RouteID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !rt.Active) {
		return nil
	}
	if err != nil {
		return err
	}
	d, err := m.load(ctx)
	if err != nil {
		return err
	}
	if d.BotTokenEnc == "" || d.Error != "" {
		return nil
	}
	tok, err := m.token(d)
	if err != nil {
		return err
	}
	envs := &envelopes{m: m, e: p.Event, have: map[bool]*envelope{}}
	pub, err := envs.get(ctx, false)
	if err != nil {
		return err
	}
	trig := pub.trigger()
	if !rt.matches(p.Event, trig) {
		return nil
	}
	env, err := envs.get(ctx, rt.Internal)
	if err != nil {
		return err
	}
	perr := m.post(ctx, tok, rt, p.Event, env, trig)
	if perr == nil {
		return nil
	}
	if retry, _ := m.classify(ctx, rt, perr); !retry {
		return nil
	}
	return perr
}
