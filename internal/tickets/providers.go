// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/realtime"
)

// RegisterRealtime implements app.RealtimeProvider (TKT-032).
func (m *Module) RegisterRealtime(h *realtime.Hub) { m.registerRealtime(h) }

// RegisterQueue implements app.QueueProvider: the non-durable index consumer
// (TKT-014) and the scheduled jobs (TKT-020 auto-close, TKT-022 SLA breach).
func (m *Module) RegisterQueue(q *queue.Queue) {
	m.registerIndex(q)
	m.registerJobs(q)
	m.registerAttachJobs(q)
	m.registerAutomation(q) // D-19 §A: rules consumer, time rules, round-robin flush
}
