// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"testing"

	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestTimeline_ListsEditEvents: subject edits, message edits and watcher
// changes are part of GET /agent/tickets/{id}/events (OpenAPI
// TimelineEventType, GAP-TKT-03); the response is validated by the harness.
func TestTimeline_ListsEditEvents(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"subject": "A clearer subject"}).expect(t, 200)
		msg := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "first draft"}).expect(t, 201).m(t)
		l.e.do(l.agent, "PATCH", l.path("/messages/"+msg["id"].(string)), map[string]any{"body_md": "second draft"}).expect(t, 200)
		l.e.do(l.agent, "POST", l.path("/watchers"), map[string]any{"user_id": l.agent.UserID}).expect(t, 200)
		page := l.e.do(l.agent, "GET", l.path("/events?limit=100"), nil).expect(t, 200).m(t)
		seen := map[string]bool{}
		for _, it := range page["items"].([]any) {
			seen[it.(map[string]any)["type"].(string)] = true
		}
		for _, typ := range []string{events.TicketSubjectChanged, events.MessageEdited, events.TicketWatchersChanged} {
			if !seen[typ] {
				t.Errorf("timeline lacks %s: %v", typ, seen)
			}
		}
	})
}
