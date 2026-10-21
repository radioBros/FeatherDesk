// SPDX-License-Identifier: AGPL-3.0-only

package tickets

// Portal (customer) read paths. I-2: every query on ticket_messages in this
// file has `kind = 'public'` written into the SQL text; internal notes and
// system messages can only leak if someone edits this file, and
// TestPortalQueries_AllFilterPublic greps it.

import (
	"context"
	"database/sql"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

const portalMessagesSQL = `SELECT m.id, m.ticket_id, m.author_id, m.body_md, m.created_at
	FROM ticket_messages m WHERE m.ticket_id = ? AND m.kind = 'public' ORDER BY m.created_at, m.id`

const portalAttachmentsSQL = `SELECT ` + attachmentCols + ` FROM attachments a
	JOIN ticket_messages m ON m.id = a.message_id AND m.kind = 'public'
	WHERE a.ticket_id = ? ORDER BY a.created_at, a.id`

const portalMessageSQL = `SELECT m.id, m.ticket_id, m.author_id, m.body_md, m.created_at
	FROM ticket_messages m WHERE m.id = ? AND m.kind = 'public'`

// CustomerTicket returns the portal detail: public messages only (I-2),
// customer-visible fields only (I-9), mapped status labels.
func (s *Service) CustomerTicket(ctx context.Context, p *domain.Principal, id string) (*customerTicketDTO, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	return s.customerDetail(ctx, p, "t.id = ?", id)
}

// CustomerTicketByNumber is CustomerTicket by human number (the portal and
// the MCP tools let customers say "#1042").
func (s *Service) CustomerTicketByNumber(ctx context.Context, p *domain.Principal, n int64) (*customerTicketDTO, error) {
	return s.customerDetail(ctx, p, "t.number = ?", n)
}

// CustomerTicketByRef is CustomerTicket by KEY-123 reference (D-20).
func (s *Service) CustomerTicketByRef(ctx context.Context, p *domain.Principal, ref string) (*customerTicketDTO, error) {
	id, err := s.TicketIDByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.customerDetail(ctx, p, "t.id = ?", id)
}

func (s *Service) customerDetail(ctx context.Context, p *domain.Principal, where string, arg any) (*customerTicketDTO, error) {
	q := s.db.Read()
	t, err := s.loadVisible(ctx, q, p, where, arg)
	if err != nil {
		return nil, err
	}
	s.fillKey(ctx, t)
	sum := t.customerSummary()
	d := &customerTicketDTO{
		ID: sum.ID, Number: sum.Number, Ref: sum.Ref, ProjectID: sum.ProjectID, Project: s.dir.ProjectRefOf(ctx, t.ProjectID), Subject: sum.Subject, Status: sum.Status, Priority: sum.Priority, CategoryID: sum.CategoryID,
		OrganizationID: sum.OrganizationID, Channel: sum.Channel, CreatedAt: sum.CreatedAt, UpdatedAt: sum.UpdatedAt,
		ResolvedAt: sum.ResolvedAt, ClosedAt: sum.ClosedAt, FollowupOfID: strOrNil(t.FollowupOfID), CSATScore: intPtrOrNil(t.CSATScore),
	}
	rows, err := q.QueryContext(ctx, portalMessagesSQL, t.ID)
	if err != nil {
		return nil, err
	}
	type pm struct {
		dto    customerMessageDTO
		author string
	}
	var msgs []pm
	for rows.Next() {
		var m pm
		var author sql.NullString
		if err := rows.Scan(&m.dto.ID, &m.dto.TicketID, &author, &m.dto.BodyMD, &m.dto.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		m.dto.Kind = string(domain.MessagePublic)
		m.dto.CreatedAt = m.dto.CreatedAt.UTC()
		m.author = author.String
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	atts, err := portalAttachments(ctx, q, t.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{t.RequesterID}
	for _, m := range msgs {
		ids = append(ids, m.author)
	}
	refs, err := loadUserRefs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	if r, ok := refs[t.RequesterID]; ok {
		d.Requester = r
	} else {
		d.Requester = sum.Requester
	}
	byMsg := map[string][]attachmentDTO{}
	d.Attachments = []attachmentDTO{}
	for _, a := range atts {
		d.Attachments = append(d.Attachments, a)
		byMsg[*a.MessageID] = append(byMsg[*a.MessageID], a)
	}
	d.Messages = make([]customerMessageDTO, 0, len(msgs))
	for _, m := range msgs {
		dto := m.dto
		if r, ok := refs[m.author]; ok {
			r := r
			dto.Author = &r
		}
		dto.Attachments = byMsg[dto.ID]
		if dto.Attachments == nil {
			dto.Attachments = []attachmentDTO{}
		}
		d.Messages = append(d.Messages, dto)
	}
	vals, err := loadFieldValues(ctx, q, []string{t.ID})
	if err != nil {
		return nil, err
	}
	d.CustomFields = s.presentFields(ctx, p, vals[t.ID])
	d.AllowedActions = authz.AllowedActionStrings(p, t.view(s.Policy(ctx), s.clock()))
	return d, nil
}

func portalAttachments(ctx context.Context, q store.Querier, ticketID string) ([]attachmentDTO, error) {
	rows, err := q.QueryContext(ctx, portalAttachmentsSQL, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []attachmentDTO
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		a.URL = "/api/v1/portal/attachments/" + a.ID
		out = append(out, a)
	}
	return out, rows.Err()
}

// CustomerMessage returns one public message in the portal shape.
func (s *Service) CustomerMessage(ctx context.Context, p *domain.Principal, msgID string) (*customerMessageDTO, error) {
	q := s.db.Read()
	var m customerMessageDTO
	var author sql.NullString
	if err := q.QueryRowContext(ctx, portalMessageSQL, msgID).Scan(&m.ID, &m.TicketID, &author, &m.BodyMD, &m.CreatedAt); err != nil {
		return nil, store.NotFound(err)
	}
	if _, err := s.loadVisible(ctx, q, p, "t.id = ?", m.TicketID); err != nil {
		return nil, err
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
	atts, err := portalAttachments(ctx, q, m.TicketID)
	if err != nil {
		return nil, err
	}
	m.Attachments = []attachmentDTO{}
	for _, a := range atts {
		if a.MessageID != nil && *a.MessageID == m.ID {
			m.Attachments = append(m.Attachments, a)
		}
	}
	return &m, nil
}

// portalAttachmentSQL: an attached file is downloadable by a customer only
// when its message is public and the ticket is visible (I-2, I-3).
const portalAttachmentSQL = `SELECT a.filename, a.content_type, a.storage_key, a.created_at FROM attachments a
	JOIN ticket_messages m ON m.id = a.message_id AND m.kind = 'public'
	JOIN tickets t ON t.id = a.ticket_id
	WHERE a.id = ? AND `

const portalDraftSQL = `SELECT a.filename, a.content_type, a.storage_key, a.created_at FROM attachments a
	WHERE a.id = ? AND a.message_id IS NULL AND a.uploader_id = ?`

func (s *Service) customerAttachment(ctx context.Context, p *domain.Principal, id string) (attachmentFile, error) {
	var f attachmentFile
	q := s.db.Read()
	err := q.QueryRowContext(ctx, portalDraftSQL, id, p.UserID).Scan(&f.filename, &f.contentType, &f.key, &f.created)
	if err == nil {
		return f, nil
	}
	pred, pargs := authz.TicketPredicate(p, s.Policy(ctx), "t")
	err = q.QueryRowContext(ctx, portalAttachmentSQL+pred, append([]any{id}, pargs...)...).Scan(&f.filename, &f.contentType, &f.key, &f.created)
	return f, store.NotFound(err)
}
