// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// WebSocket topics (TKT-032, realtime_spec §3/§4). The platform's `realtime`
// queue consumer hands every dispatched event to route(). Visibility of each
// frame is decided in memory with authz.CanSeeEvent on the event snapshot and
// the subscriber's cached principal: the per-event payload is loaded once
// (one ticket read, plus the message for message events) and never per
// subscriber. Customer frames are serialised from the portal DTOs.

type realtimeRouter struct {
	s *Service
	h *realtime.Hub

	countsMu      sync.Mutex
	countsPending bool
	countsDelay   time.Duration
}

func (m *Module) registerRealtime(h *realtime.Hub) {
	rt := &realtimeRouter{s: m.svc, h: h, countsDelay: time.Second}
	m.rt = rt
	canRead := func(p *domain.Principal) bool {
		return p != nil && (p.HasScope(domain.ScopeTicketsRead) || p.HasScope(domain.ScopeTicketsWrite))
	}
	h.Authorize("tickets", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		return canRead(p)
	}))
	h.Authorize("me", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		return p != nil
	}))
	h.Authorize("counts", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		return p.IsStaff() && canRead(p)
	}))
	h.Authorize("queue:", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		team := strings.TrimPrefix(topic, "queue:")
		return p.IsStaff() && canRead(p) && (p.IsSupervisorOrAdmin() || p.InTeam(team))
	}))
	// project:<id> (D-20): staff working in the project. Frames are still
	// filtered per event with CanSeeEvent (team, tier, I-11).
	h.Authorize("project:", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		id := strings.TrimPrefix(topic, "project:")
		if !p.IsStaff() || !canRead(p) || !p.InProject(id) {
			return false
		}
		_, err := m.svc.dir.Project(ctx, id)
		return err == nil
	}))
	// ticket:<id> uses the same SQL predicate as REST (TKT-001), once per subscribe.
	h.Authorize("ticket:", realtime.TopicAuthorizerFunc(func(ctx context.Context, p *domain.Principal, topic string) bool {
		id := strings.TrimPrefix(topic, "ticket:")
		if !validID(id) || !canRead(p) {
			return false
		}
		pred, args := authz.TicketPredicate(p, m.svc.Policy(ctx), "t")
		var n int
		err := m.svc.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets t WHERE t.id = ? AND `+pred, append([]any{id}, args...)...).Scan(&n)
		return err == nil && n == 1
	}))
	h.OnEvent(rt.route)
	h.SetReplayer(rt)
}

// payload is everything a frame of one event may carry, built once.
type payload struct {
	staffTicket    *ticketSummaryDTO
	customerTicket *customerTicketSummaryDTO
	staffMsg       *messageDTO
	customerMsg    *customerMessageDTO
	staffChanges   map[string]any
	custChanges    map[string]any
}

// buildPayloads loads the tickets of evs in one batch (plus messages) and
// builds staff and customer payloads per event.
func (rt *realtimeRouter) buildPayloads(ctx context.Context, evs []events.Event) map[string]*payload {
	s := rt.s
	q := s.db.Read()
	out := map[string]*payload{}
	ids := map[string]bool{}
	var list []string
	for _, e := range evs {
		if !ids[e.TicketID] {
			ids[e.TicketID] = true
			list = append(list, e.TicketID)
		}
	}
	rows := map[string]*ticketRow{}
	for len(list) > 0 {
		n := min(len(list), 200)
		batch := list[:n]
		list = list[n:]
		rs, err := q.QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id IN (`+placeholders(len(batch))+`)`, anyStrings(batch)...)
		if err != nil {
			s.log.Warn("realtime: load tickets", "err", err)
			return out
		}
		for rs.Next() {
			if t, err := scanTicket(rs); err == nil {
				rows[t.ID] = t
			}
		}
		rs.Close()
	}
	var all []*ticketRow
	for _, t := range rows {
		all = append(all, t)
	}
	// Payload summaries use the staff principal-independent shape; fields are
	// filtered per audience below (I-9).
	staffP := &domain.Principal{Kind: domain.KindStaff, Role: domain.RoleAdmin}
	custP := &domain.Principal{Kind: domain.KindCustomer, Role: domain.RoleRequester}
	staffSums, _ := s.staffSummaries(ctx, q, staffP, all)
	custSums, _ := s.customerSummaries(ctx, q, custP, all)
	staffBy, custBy := map[string]*ticketSummaryDTO{}, map[string]*customerTicketSummaryDTO{}
	for i := range staffSums {
		staffBy[staffSums[i].ID] = &staffSums[i]
	}
	for i := range custSums {
		custBy[custSums[i].ID] = &custSums[i]
	}
	for _, e := range evs {
		p := &payload{staffTicket: staffBy[e.TicketID], customerTicket: custBy[e.TicketID], staffChanges: map[string]any{}}
		if len(e.Data) > 0 {
			_ = json.Unmarshal(e.Data, &p.staffChanges)
		}
		var ref struct {
			MessageID string `json:"message_id"`
		}
		_ = e.Decode(&ref)
		if ref.MessageID != "" && (e.Type == events.MessageCreated || e.Type == events.NoteCreated || e.Type == events.MessageEdited) {
			if m, err := s.StaffMessage(ctx, staffP, e.TicketID, ref.MessageID); err == nil {
				p.staffMsg = m
			}
			if e.Type == events.MessageCreated {
				// The portal query only ever returns public messages (I-2).
				if pm, err := s.portalMessageNoAuth(ctx, ref.MessageID); err == nil {
					p.customerMsg = pm
				}
			}
		}
		p.custChanges = s.customerChanges(ctx, e)
		out[e.ID] = p
	}
	return out
}

// customerChanges is the customer-safe `changes` of a customer-visible event:
// mapped status labels, customer-visible field keys only (I-7, I-9).
func (s *Service) customerChanges(ctx context.Context, e events.Event) map[string]any {
	out := map[string]any{}
	switch e.Type {
	case events.TicketStatusChanged:
		var c events.StatusChange
		if e.Decode(&c) == nil {
			out["from"], out["to"] = CustomerStatus(domain.TicketStatus(c.From)), CustomerStatus(domain.TicketStatus(c.To))
		}
	case events.TicketFieldsChanged:
		var c events.FieldsChange
		if e.Decode(&c) == nil {
			out["keys"] = s.customerVisibleKeys(ctx, c.Keys)
		}
	case events.TicketFollowupCreated:
		var c map[string]any
		if e.Decode(&c) == nil {
			out["new_ticket_id"] = c["new_ticket_id"]
		}
	case events.TicketSubjectChanged:
		var c events.Change
		if e.Decode(&c) == nil {
			out["to"] = c.To
		}
	}
	return out
}

// frameFor builds the frame of event e for subscriber p, or ok=false.
func frameFor(topic string, e events.Event, pl *payload, p *domain.Principal, pol authz.TicketPolicy) (realtime.Frame, bool) {
	if pl == nil || !authz.CanSeeEvent(p, e.Snapshot, e.Type, pol) {
		return realtime.Frame{}, false
	}
	data := map[string]any{}
	if p.IsCustomer() {
		if pl.customerTicket == nil {
			return realtime.Frame{}, false
		}
		data["ticket"] = pl.customerTicket
		if e.Type == events.MessageCreated {
			if pl.customerMsg == nil {
				return realtime.Frame{}, false
			}
			data["message"] = pl.customerMsg
		}
		if len(pl.custChanges) > 0 {
			data["changes"] = pl.custChanges
		}
	} else {
		if pl.staffTicket != nil {
			data["ticket"] = pl.staffTicket
		}
		if pl.staffMsg != nil {
			data["message"] = pl.staffMsg
		}
		data["changes"] = pl.staffChanges
	}
	return realtime.EventFrame(topic, e, data), true
}

// queueEvents are the events that put a ticket in front of a team's queue.
var queueEvents = map[string]bool{events.TicketCreated: true, events.TicketEscalated: true, events.TicketDeescalated: true, events.TicketTeamChanged: true}

// route fans one event out (the hub calls it from the realtime consumer).
func (rt *realtimeRouter) route(ctx context.Context, e events.Event) {
	pol := rt.s.Policy(ctx)
	pls := rt.buildPayloads(ctx, []events.Event{e})
	pl := pls[e.ID]
	build := func(topic string) func(p *domain.Principal) (realtime.Frame, bool) {
		return func(p *domain.Principal) (realtime.Frame, bool) { return frameFor(topic, e, pl, p, pol) }
	}
	rt.h.PublishFunc("tickets", build("tickets"))
	rt.h.PublishFunc("ticket:"+e.TicketID, build("ticket:"+e.TicketID))
	projectTopic := "project:" + e.Snapshot.Project()
	rt.h.PublishFunc(projectTopic, func(p *domain.Principal) (realtime.Frame, bool) {
		if !p.IsStaff() {
			return realtime.Frame{}, false
		}
		return frameFor(projectTopic, e, pl, p, pol)
	})
	if queueEvents[e.Type] && e.Snapshot.TeamID != "" {
		rt.h.PublishFunc("queue:"+e.Snapshot.TeamID, func(p *domain.Principal) (realtime.Frame, bool) {
			if !p.IsStaff() {
				return realtime.Frame{}, false
			}
			return frameFor("queue:"+e.Snapshot.TeamID, e, pl, p, pol)
		})
	}
	// Subscribers who can no longer see the ticket get ticket.access_lost and
	// their ticket:<id> subscription is dropped (realtime_spec §4).
	view := authz.ViewFromSnapshot(e.Snapshot, pol)
	rt.h.Revoke("ticket:"+e.TicketID, func(p *domain.Principal) bool { return !authz.CanSeeTicket(p, view) })

	switch e.Type {
	case events.TicketAssigned:
		var a events.Assignment
		if e.Decode(&a) == nil && a.To != "" && a.To != e.ActorID {
			rt.h.SendUser(a.To, rt.meFrame("assigned_to_you", e, pl))
		}
	case events.NoteCreated:
		var n struct {
			Mentioned []string `json:"mentioned"`
		}
		if e.Decode(&n) == nil {
			for _, u := range n.Mentioned {
				if u != e.ActorID {
					rt.h.SendUser(u, rt.meFrame("mentioned", e, pl))
				}
			}
		}
	}
	if e.Type != events.NoteCreated && e.Type != events.MessageEdited {
		rt.scheduleCounts()
	}
}

func (rt *realtimeRouter) meFrame(name string, e events.Event, pl *payload) realtime.Frame {
	at := e.CreatedAt
	data := map[string]any{}
	if pl != nil && pl.staffTicket != nil {
		data["ticket"] = pl.staffTicket
	}
	return realtime.Frame{Type: "event", EventID: e.ID, Event: name, At: &at, Data: data}
}

// scheduleCounts debounces count pushes: at most one recompute per second,
// whatever the event rate (≤ 1 frame/s per socket, realtime_spec §3).
func (rt *realtimeRouter) scheduleCounts() {
	rt.countsMu.Lock()
	defer rt.countsMu.Unlock()
	if rt.countsPending {
		return
	}
	rt.countsPending = true
	time.AfterFunc(rt.countsDelay, rt.flushCounts)
}

func (rt *realtimeRouter) flushCounts() {
	rt.countsMu.Lock()
	rt.countsPending = false
	rt.countsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cache := map[string]*Counts{}
	rt.h.PublishFunc("counts", func(p *domain.Principal) (realtime.Frame, bool) {
		if !p.IsStaff() {
			return realtime.Frame{}, false
		}
		c, ok := cache[p.UserID]
		if !ok {
			v, err := rt.s.Counts(ctx, p)
			if err != nil {
				return realtime.Frame{}, false
			}
			c = &v
			cache[p.UserID] = c
		}
		return realtime.Frame{Type: "counts", Data: c}, true
	})
}

// Replay implements realtime.Replayer: the events after lastEventID that p
// may see on its topics, capped at 1 000 events / 24 h (else resync,
// realtime_spec §5). That is at most 4 000 frames (one per subscribed
// topic among tickets, ticket:<id>, queue:<team> and project:<id>); the
// hub streams them through the bounded send buffer with backpressure, so
// there is no separate frame cap.
func (rt *realtimeRouter) Replay(ctx context.Context, p *domain.Principal, topics []string, lastEventID string) ([]realtime.Frame, bool) {
	if !validID(lastEventID) {
		return nil, false
	}
	q := rt.s.db.Read()
	var last time.Time
	if err := q.QueryRowContext(ctx, `SELECT created_at FROM ticket_events WHERE id = ?`, lastEventID).Scan(&last); err != nil || time.Since(last) > 24*time.Hour {
		return nil, false
	}
	evs, err := events.ReadAfter(ctx, q, lastEventID, 1001)
	if err != nil || len(evs) > 1000 {
		return nil, false
	}
	pol := rt.s.Policy(ctx)
	subs := map[string]bool{}
	for _, t := range topics {
		subs[t] = true
	}
	var visible []events.Event
	for _, e := range evs {
		if authz.CanSeeEvent(p, e.Snapshot, e.Type, pol) {
			visible = append(visible, e)
		}
	}
	if len(visible) == 0 {
		return []realtime.Frame{}, true
	}
	pls := rt.buildPayloads(ctx, visible)
	var frames []realtime.Frame
	for _, e := range visible {
		for _, topic := range []string{"tickets", "ticket:" + e.TicketID, "queue:" + e.Snapshot.TeamID, "project:" + e.Snapshot.Project()} {
			if !subs[topic] || (strings.HasPrefix(topic, "queue:") && !queueEvents[e.Type]) {
				continue
			}
			if strings.HasPrefix(topic, "project:") && !p.IsStaff() {
				continue
			}
			if f, ok := frameFor(topic, e, pls[e.ID], p, pol); ok {
				frames = append(frames, f)
			}
		}
	}
	return frames, true
}

// portalMessageNoAuth reads a public message in the portal shape for a
// fan-out payload (visibility is decided per subscriber afterwards).
func (s *Service) portalMessageNoAuth(ctx context.Context, msgID string) (*customerMessageDTO, error) {
	q := s.db.Read()
	var m customerMessageDTO
	var author store.NullString
	if err := q.QueryRowContext(ctx, portalMessageSQL, msgID).Scan(&m.ID, &m.TicketID, &author, &m.BodyMD, &m.CreatedAt); err != nil {
		return nil, store.NotFound(err)
	}
	m.Kind = string(domain.MessagePublic)
	m.CreatedAt = m.CreatedAt.UTC()
	refs, err := loadUserRefs(ctx, q, []string{author.String})
	if err != nil {
		return nil, err
	}
	if r, ok := refs[author.String]; ok {
		m.Author = &r
	}
	m.Attachments = []attachmentDTO{}
	atts, err := portalAttachments(ctx, q, m.TicketID)
	if err != nil {
		return nil, err
	}
	for _, a := range atts {
		if a.MessageID != nil && *a.MessageID == m.ID {
			m.Attachments = append(m.Attachments, a)
		}
	}
	return &m, nil
}
