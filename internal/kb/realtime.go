// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/realtime"
)

// Topic is the staff-only realtime topic for editors (KB-014).
const Topic = "kb"

// updateEvent is the data of a `kb.updated` frame. KB changes are not ticket
// events, so there is no replay: editors refetch on resync.
type updateEvent struct {
	Action     string `json:"action"`
	ArticleID  string `json:"article_id,omitempty"`
	CategoryID string `json:"category_id,omitempty"`
	Status     string `json:"status,omitempty"`
	Locale     string `json:"locale,omitempty"`
	ActorID    string `json:"actor_id,omitempty"`
}

// RegisterRealtime authorises the `kb` topic for staff only; customers and
// anonymous connections are denied at subscribe time.
func (m *Module) RegisterRealtime(h *realtime.Hub) {
	h.Authorize(Topic, realtime.TopicAuthorizerFunc(func(_ context.Context, p *domain.Principal, topic string) bool {
		return topic == Topic && p.IsStaff()
	}))
	m.hub.Store(h)
}

// publish sends kb.updated after commit (never inside the transaction).
func (m *Module) publish(ev updateEvent) {
	h := m.hub.Load()
	if h == nil {
		return
	}
	at := time.Now().UTC()
	h.Publish(Topic, realtime.Frame{Type: "event", Event: "kb.updated", At: &at, Data: ev}, func(p *domain.Principal) bool { return p.IsStaff() })
}
