// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/realtime"
)

// The `admin:system` WebSocket topic (realtime_spec, OpenAPI x-websocket):
// admins (PATs need the admin scope) receive `queue.changed` when the queue
// counters moved, checked at most every adminSystemEvery. The check reads
// in-memory counters only (no DB, nothing sent when idle); the admin queue
// page refetches GET /admin/queue on the event.

const adminSystemTopic = "admin:system"

var adminSystemEvery = 5 * time.Second

func (m *coreModule) RegisterRealtime(h *realtime.Hub) {
	h.Authorize(adminSystemTopic, realtime.TopicAuthorizerFunc(func(_ context.Context, p *domain.Principal, topic string) bool {
		return topic == adminSystemTopic && p != nil && p.IsStaff() && p.Role == domain.RoleAdmin && p.HasScope(domain.ScopeAdmin)
	}))
}

func (m *coreModule) RegisterQueue(q *queue.Queue) {
	var last [5]int64
	q.Every("realtime.admin_system", adminSystemEvery, func(context.Context) error {
		st := q.Stats()
		now := [5]int64{st.Dispatched.Load(), st.DeferredWrites.Load(), st.Retried.Load(), st.Dead.Load(), st.JobsHandled.Load()}
		if now == last {
			return nil
		}
		last = now
		at := time.Now().UTC()
		m.d.Hub.Publish(adminSystemTopic, realtime.Frame{Type: "event", Event: "queue.changed", At: &at,
			Data: map[string]any{"dispatched": now[0], "deferred_writes": now[1], "retried": now[2], "dead": now[3], "jobs_handled": now[4]}}, nil)
		return nil
	})
}
