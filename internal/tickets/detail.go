// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// loadVisible reads one ticket through the visibility predicate (TKT-001):
// a ticket p cannot see does not exist for p (404).
func (s *Service) loadVisible(ctx context.Context, q store.Querier, p *domain.Principal, where string, arg any) (*ticketRow, error) {
	pred, pargs := authz.TicketPredicate(p, s.Policy(ctx), "t")
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE `+where+` AND `+pred, append([]any{arg}, pargs...)...))
	if err != nil {
		if err == store.ErrNotFound {
			return nil, problems.NotFound()
		}
		return nil, err
	}
	if err := loadWatchers(ctx, q, t); err != nil {
		return nil, err
	}
	return t, nil
}

// StaffTicket returns the staff detail (all messages, internal notes included).
func (s *Service) StaffTicket(ctx context.Context, p *domain.Principal, id string) (*ticketDTO, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	q := s.db.Read()
	t, err := s.loadVisible(ctx, q, p, "t.id = ?", id)
	if err != nil {
		return nil, err
	}
	return s.staffDetail(ctx, q, p, t)
}

// TicketIDByRef resolves a KEY-123 reference (D-20) to a ticket id. It does
// not check visibility: callers load the ticket through the visibility
// predicate next, so an invisible and an unknown ref are the same 404.
func (s *Service) TicketIDByRef(ctx context.Context, ref string) (string, error) {
	key, seq, ok := domain.ParseTicketRef(ref)
	if !ok {
		return "", problems.NotFound()
	}
	pr, err := s.dir.ResolveProject(ctx, key)
	if err != nil {
		return "", problems.NotFound()
	}
	var id string
	err = s.db.Read().QueryRowContext(ctx, `SELECT id FROM tickets WHERE project_id = ? AND project_seq = ?`, pr.ID, seq).Scan(&id)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return "", problems.NotFound()
		}
		return "", err
	}
	return id, nil
}

// StaffTicketByRef is StaffTicket by KEY-123 reference (D-20).
func (s *Service) StaffTicketByRef(ctx context.Context, p *domain.Principal, ref string) (*ticketDTO, error) {
	id, err := s.TicketIDByRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	return s.StaffTicket(ctx, p, id)
}

// StaffTicketByNumber is StaffTicket by human number.
func (s *Service) StaffTicketByNumber(ctx context.Context, p *domain.Principal, n int64) (*ticketDTO, error) {
	q := s.db.Read()
	t, err := s.loadVisible(ctx, q, p, "t.number = ?", n)
	if err != nil {
		return nil, err
	}
	return s.staffDetail(ctx, q, p, t)
}

func (s *Service) staffDetail(ctx context.Context, q store.Querier, p *domain.Principal, t *ticketRow) (*ticketDTO, error) {
	now := s.clock()
	s.fillKey(ctx, t)
	d := &ticketDTO{ticketSummaryDTO: t.summary(now), etag: t.etag()}
	d.Project = s.dir.ProjectRefOf(ctx, t.ProjectID)
	d.FirstRespondedAt = nullTimePtr(t.FirstRespondedAt.Valid, t.FirstRespondedAt.Time)
	d.ResolvedAt = nullTimePtr(t.ResolvedAt.Valid, t.ResolvedAt.Time)
	d.ClosedAt = nullTimePtr(t.ClosedAt.Valid, t.ClosedAt.Time)
	d.SLAPausedAt = nullTimePtr(t.SLAPausedAt.Valid, t.SLAPausedAt.Time)
	d.FollowupOfID = strOrNil(t.FollowupOfID)
	d.CSATScore = intPtrOrNil(t.CSATScore)

	msgs, err := staffMessages(ctx, q, t.ID)
	if err != nil {
		return nil, err
	}
	atts, err := staffAttachments(ctx, q, t.ID)
	if err != nil {
		return nil, err
	}
	ids := []string{t.RequesterID, t.AssigneeID}
	ids = append(ids, t.Watchers...)
	for _, m := range msgs {
		ids = append(ids, m.authorID)
	}
	refs, err := loadUserRefs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	s.fillRefs(&d.ticketSummaryDTO, t, refs)
	byMsg := map[string][]attachmentDTO{}
	d.Attachments = []attachmentDTO{}
	for _, a := range atts {
		a.URL = "/api/v1/agent/attachments/" + a.ID
		d.Attachments = append(d.Attachments, a)
		if a.MessageID != nil {
			byMsg[*a.MessageID] = append(byMsg[*a.MessageID], a)
		}
	}
	d.Messages = make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		dto := m.dto
		if r, ok := refs[m.authorID]; ok {
			r := r
			dto.Author = &r
		}
		dto.Attachments = byMsg[dto.ID]
		if dto.Attachments == nil {
			dto.Attachments = []attachmentDTO{}
		}
		d.Messages = append(d.Messages, dto)
	}
	d.Watchers = []userRef{}
	for _, w := range t.Watchers {
		if r, ok := refs[w]; ok {
			d.Watchers = append(d.Watchers, r)
		}
	}
	if d.Tags, err = ticketTags(ctx, q, t.ID); err != nil {
		return nil, err
	}
	vals, err := loadFieldValues(ctx, q, []string{t.ID})
	if err != nil {
		return nil, err
	}
	d.CustomFields = s.presentFields(ctx, p, vals[t.ID])
	if d.Links, err = s.listLinks(ctx, q, p, t.ID); err != nil {
		return nil, err
	}
	d.AllowedActions = authz.AllowedActionStrings(p, t.view(s.Policy(ctx), now))
	return d, nil
}

// fillRefs sets requester/assignee names on a summary.
func (s *Service) fillRefs(d *ticketSummaryDTO, t *ticketRow, refs map[string]userRef) {
	if r, ok := refs[t.RequesterID]; ok {
		d.Requester = r
	}
	if t.AssigneeID != "" {
		if r, ok := refs[t.AssigneeID]; ok {
			r := r
			d.Assignee = &r
		} else {
			d.Assignee = &userRef{ID: t.AssigneeID, Kind: "staff"}
		}
	}
}

type staffMsg struct {
	dto      messageDTO
	authorID string
}

// staffMessages reads every message of a ticket (staff path only).
func staffMessages(ctx context.Context, q store.Querier, ticketID string) ([]staffMsg, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+staffMsgCols+`
		FROM ticket_messages m LEFT JOIN api_tokens vt ON vt.id = m.via_token_id WHERE m.ticket_id = ? ORDER BY m.created_at, m.id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []staffMsg
	for rows.Next() {
		var m staffMsg
		if err := scanStaffMsg(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// staffMsgCols reads a message with its integration token (I-12): alias m
// for ticket_messages, vt for the LEFT JOINed api_tokens.
const staffMsgCols = `m.id, m.ticket_id, m.author_id, m.kind, m.channel, m.body_md, m.created_at, m.edited_at, m.via_token_id, vt.name`

func scanStaffMsg(r scanner, m *staffMsg) error {
	var author, via, viaName sql.NullString
	var edited sql.NullTime
	if err := r.Scan(&m.dto.ID, &m.dto.TicketID, &author, &m.dto.Kind, &m.dto.Channel, &m.dto.BodyMD, &m.dto.CreatedAt, &edited, &via, &viaName); err != nil {
		return err
	}
	m.authorID = author.String
	m.dto.CreatedAt = m.dto.CreatedAt.UTC()
	m.dto.EditedAt = nullTimePtr(edited.Valid, edited.Time)
	m.dto.ViaIntegration = via.Valid
	if viaName.Valid {
		n := viaName.String
		m.dto.ViaName = &n
	}
	enrichMessageDTO(&m.dto)
	return nil
}

const attachmentCols = `a.id, a.ticket_id, a.message_id, a.filename, a.content_type, a.size_bytes, a.sha256, a.created_at`

func scanAttachment(r scanner) (attachmentDTO, error) {
	var a attachmentDTO
	var tid, mid sql.NullString
	err := r.Scan(&a.ID, &tid, &mid, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedAt)
	a.TicketID, a.MessageID = strOrNil(tid.String), strOrNil(mid.String)
	a.CreatedAt = a.CreatedAt.UTC()
	return a, err
}

// staffAttachments reads all attached files of a ticket (staff path only).
func staffAttachments(ctx context.Context, q store.Querier, ticketID string) ([]attachmentDTO, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+attachmentCols+` FROM attachments a WHERE a.ticket_id = ? AND a.message_id IS NOT NULL ORDER BY a.created_at, a.id`, ticketID)
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
		out = append(out, a)
	}
	return out, rows.Err()
}

// minimalStaffTicket is returned in TicketUpdateResult when the change removed
// the caller's visibility: the summary they just acted on, no thread.
func (s *Service) minimalStaffTicket(ctx context.Context, id string) (*ticketDTO, error) {
	q := s.db.Read()
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`, id))
	if err != nil {
		return nil, err
	}
	refs, err := loadUserRefs(ctx, q, []string{t.RequesterID})
	if err != nil {
		return nil, err
	}
	s.fillKey(ctx, t)
	d := &ticketDTO{ticketSummaryDTO: t.summary(s.clock()), Messages: []messageDTO{}, AllowedActions: []string{},
		Attachments: []attachmentDTO{}, Watchers: []userRef{}, Links: []linkDTO{}, etag: t.etag()}
	d.Project = s.dir.ProjectRefOf(ctx, t.ProjectID)
	// Team, tier and assignee are exactly what the caller lost access to.
	if r, ok := refs[t.RequesterID]; ok {
		d.Requester = r
	}
	return d, nil
}

// UpdateResult builds TicketUpdateResult for p after a mutation of id.
func (s *Service) UpdateResult(ctx context.Context, p *domain.Principal, id string, warnings []string) (*updateResult, error) {
	d, err := s.StaffTicket(ctx, p, id)
	if err == nil {
		return &updateResult{Ticket: d, Visible: true, Warnings: warnings}, nil
	}
	if pe := problems.From(err); pe.Slug != problems.SlugNotFound {
		return nil, err
	}
	d, err = s.minimalStaffTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	return &updateResult{Ticket: d, Visible: false, Warnings: warnings}, nil
}
