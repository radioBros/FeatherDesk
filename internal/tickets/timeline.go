// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ticket.deleted is written in the purge transaction of a hard delete, so it
// is never readable through a live ticket; every other stored type is part
// of the HTTP timeline (OpenAPI TimelineEventType, GAP-TKT-03 closed).
type eventDTO struct {
	ID        string         `json:"id"`
	TicketID  string         `json:"ticket_id"`
	Actor     *userRef       `json:"actor"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
}

// Timeline pages the events of a visible ticket, oldest first (staff only;
// customers get no events endpoint, TKT-009).
func (s *Service) Timeline(ctx context.Context, p *domain.Principal, id, cursor string, limit int) (*pageDTO[eventDTO], error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	q := s.db.Read()
	if _, err := s.loadVisible(ctx, q, p, "t.id = ?", id); err != nil {
		return nil, err
	}
	after := ""
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !validID(string(b)) {
			return nil, problems.Validation(problems.Field("cursor", "invalid", "invalid cursor"))
		}
		after = string(b)
	}
	rows, err := q.QueryContext(ctx, `SELECT id, actor_id, type, data, created_at FROM ticket_events WHERE ticket_id = ? AND id > ? ORDER BY id LIMIT ?`, id, after, limit+1)
	if err != nil {
		return nil, err
	}
	var items []eventDTO
	var actors []string
	actorOf := map[int]string{}
	var lastID string
	n := 0
	more := false
	for rows.Next() {
		if n == limit {
			more = true
			break
		}
		n++
		var e eventDTO
		var actor sql.NullString
		var data store.RawJSON
		if err := rows.Scan(&e.ID, &actor, &e.Type, &data, &e.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		lastID = e.ID
		if e.Type == events.TicketDeleted {
			continue
		}
		e.TicketID, e.CreatedAt = id, e.CreatedAt.UTC()
		e.Data = map[string]any{}
		_ = json.Unmarshal(data, &e.Data)
		if e.Data == nil {
			e.Data = map[string]any{}
		}
		if actor.Valid {
			actorOf[len(items)] = actor.String
			actors = append(actors, actor.String)
		}
		items = append(items, e)
	}
	rows.Close()
	refs, err := loadUserRefs(ctx, q, actors)
	if err != nil {
		return nil, err
	}
	for i, a := range actorOf {
		if r, ok := refs[a]; ok {
			r := r
			items[i].Actor = &r
		}
	}
	if items == nil {
		items = []eventDTO{}
	}
	page := &pageDTO[eventDTO]{Items: items}
	if more {
		c := base64.RawURLEncoding.EncodeToString([]byte(lastID))
		page.NextCursor = &c
	}
	return page, nil
}

// StaffMessage returns one message in the staff shape.
func (s *Service) StaffMessage(ctx context.Context, p *domain.Principal, ticketID, msgID string) (*messageDTO, error) {
	q := s.db.Read()
	if _, err := s.loadVisible(ctx, q, p, "t.id = ?", ticketID); err != nil {
		return nil, err
	}
	var m messageDTO
	var author sql.NullString
	var edited sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT id, ticket_id, author_id, kind, channel, body_md, created_at, edited_at FROM ticket_messages WHERE id = ? AND ticket_id = ?`, msgID, ticketID).
		Scan(&m.ID, &m.TicketID, &author, &m.Kind, &m.Channel, &m.BodyMD, &m.CreatedAt, &edited)
	if err != nil {
		return nil, store.NotFound(err)
	}
	m.CreatedAt = m.CreatedAt.UTC()
	m.EditedAt = nullTimePtr(edited.Valid, edited.Time)
	refs, err := loadUserRefs(ctx, q, []string{author.String})
	if err != nil {
		return nil, err
	}
	if r, ok := refs[author.String]; ok {
		m.Author = &r
	}
	atts, err := staffAttachments(ctx, q, ticketID)
	if err != nil {
		return nil, err
	}
	m.Attachments = []attachmentDTO{}
	for _, a := range atts {
		if a.MessageID != nil && *a.MessageID == m.ID {
			a.URL = "/api/v1/agent/attachments/" + a.ID
			m.Attachments = append(m.Attachments, a)
		}
	}
	return &m, nil
}
