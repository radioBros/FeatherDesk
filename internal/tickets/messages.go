// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// MessageInput is AgentMessageCreate / ReplyCreate.
type MessageInput struct {
	Kind          string   `json:"kind"`
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
	SetStatus     string   `json:"set_status"`

	channel          domain.Channel
	inboundMessageID string
}

// MessageResult identifies the created message (and a follow-up ticket when a
// customer replied to a closed ticket).
type MessageResult struct {
	TicketID   string
	MessageID  string
	FollowupID string
}

// AddStaffMessage posts a public reply or an internal note, optionally with a
// status change in the same transaction (TKT-006). The first staff public
// reply sets first_responded_at and opens a `new` ticket; notes never do.
func (s *Service) AddStaffMessage(ctx context.Context, p *domain.Principal, id string, in MessageInput) (MessageResult, error) {
	kind := domain.MessageKind(in.Kind)
	if kind != domain.MessagePublic && kind != domain.MessageInternal {
		return MessageResult{}, problems.Validation(problems.Field("kind", "enum", "public or internal"))
	}
	body, err := validBody("body_md", in.BodyMD)
	if err != nil {
		return MessageResult{}, err
	}
	if in.channel == "" {
		in.channel = channelFor(p)
		if in.channel == domain.ChannelPortal {
			in.channel = domain.ChannelAgent
		}
	}
	pol := s.Policy(ctx)
	var res MessageResult
	if err := s.warm(ctx); err != nil {
		return MessageResult{}, err
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock()
		act := authz.ActReply
		if kind == domain.MessageInternal {
			act = authz.ActNote
		}
		if err := authz.Can(p, act, t.view(pol, now)); err != nil {
			return err
		}
		var mentioned []string
		if kind == domain.MessageInternal {
			if mentioned, err = s.resolveMentions(ctx, tx, pol, t, body); err != nil {
				return err
			}
		}
		msg := &msgRow{TicketID: t.ID, AuthorID: p.UserID, Kind: kind, Channel: in.channel, Body: body, InboundMessageID: in.inboundMessageID, CreatedAt: now}
		if err := insertMessage(ctx, tx, msg); err != nil {
			return err
		}
		if err := attachDrafts(ctx, tx, p.UserID, t.ID, msg.ID, in.AttachmentIDs); err != nil {
			return err
		}
		m := &mutation{t: t}
		ref := events.MessageRef{MessageID: msg.ID, Kind: string(kind), Channel: string(in.channel), InboundMessageID: in.inboundMessageID}
		if kind == domain.MessagePublic {
			t.LastStaffActivityAt = sql.NullTime{Time: now, Valid: true}
			if !t.FirstRespondedAt.Valid {
				t.FirstRespondedAt = sql.NullTime{Time: now, Valid: true}
			}
			m.add(events.MessageCreated, ref)
			if t.Status == domain.StatusNew && (in.SetStatus == "" || in.SetStatus == string(domain.StatusOpen)) {
				if _, err := s.applyStatus(ctx, tx, t, actorStaff, domain.StatusOpen, statusOpts{}); err != nil {
					return err
				}
				m.add(events.TicketStatusChanged, statusEvent(domain.StatusNew, domain.StatusOpen))
			}
		} else {
			data := map[string]any{"message_id": msg.ID, "kind": string(kind)}
			if len(mentioned) > 0 {
				data["mentioned"] = mentioned
			}
			m.add(events.NoteCreated, data)
		}
		if in.SetStatus != "" {
			if err := s.staffSetStatus(ctx, tx, p, pol, m, domain.TicketStatus(in.SetStatus)); err != nil {
				return err
			}
		}
		if err := s.flush(ctx, tx, m, p.UserID); err != nil {
			return err
		}
		res = MessageResult{TicketID: t.ID, MessageID: msg.ID}
		return nil
	})
	return res, err
}

// AddCustomerReply appends a customer's public reply (TKT-006/TKT-013):
// pending/on_hold → open, resolved → open inside the reopen window. A reply to
// a closed ticket (or a resolved one past the window) creates a follow-up
// ticket instead; FollowupID is then set and the original stays untouched (M-7).
func (s *Service) AddCustomerReply(ctx context.Context, p *domain.Principal, id string, in MessageInput) (MessageResult, error) {
	body, err := validBody("body_md", in.BodyMD)
	if err != nil {
		return MessageResult{}, err
	}
	if in.channel == "" {
		in.channel = channelFor(p)
	}
	pol := s.Policy(ctx)
	var res MessageResult
	if err := s.warm(ctx); err != nil {
		return MessageResult{}, err
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock()
		v := t.view(pol, now)
		if err := authz.Can(p, authz.ActReply, v); err != nil {
			return err
		}
		reopenable := t.Status == domain.StatusResolved && authz.Can(p, authz.ActReopen, v) == nil
		if t.Status == domain.StatusClosed || (t.Status == domain.StatusResolved && !reopenable) {
			f, msgID, err := s.followup(ctx, tx, p, t, body, in)
			if err != nil {
				return err
			}
			res = MessageResult{TicketID: f.ID, MessageID: msgID, FollowupID: f.ID}
			return nil
		}
		msg := &msgRow{TicketID: t.ID, AuthorID: p.UserID, Kind: domain.MessagePublic, Channel: in.channel, Body: body, InboundMessageID: in.inboundMessageID, CreatedAt: now}
		if err := insertMessage(ctx, tx, msg); err != nil {
			return err
		}
		if err := attachDrafts(ctx, tx, p.UserID, t.ID, msg.ID, in.AttachmentIDs); err != nil {
			return err
		}
		m := &mutation{t: t}
		t.LastCustomerActivityAt = now
		m.add(events.MessageCreated, events.MessageRef{MessageID: msg.ID, Kind: string(domain.MessagePublic), Channel: string(in.channel), InboundMessageID: in.inboundMessageID})
		switch t.Status {
		case domain.StatusPending, domain.StatusOnHold, domain.StatusResolved:
			from := t.Status
			if _, err := s.applyStatus(ctx, tx, t, actorCustomer, domain.StatusOpen, statusOpts{}); err != nil {
				return err
			}
			m.add(events.TicketStatusChanged, statusEvent(from, domain.StatusOpen))
		}
		if err := s.flush(ctx, tx, m, p.UserID); err != nil {
			return err
		}
		res = MessageResult{TicketID: t.ID, MessageID: msg.ID}
		return nil
	})
	return res, err
}

// followup creates the follow-up ticket for a reply to a closed ticket:
// same requester, team and category, tier 1, the reply as first message plus
// a link back to the original; ticket.followup_created on both tickets.
func (s *Service) followup(ctx context.Context, tx *store.Tx, p *domain.Principal, orig *ticketRow, body string, in MessageInput) (*ticketRow, string, error) {
	subject := orig.Subject
	if r := []rune("Re: " + subject); len(r) <= 200 {
		subject = string(r)
	}
	quoted := fmt.Sprintf("%s\n\n---\nFollow-up of #%d.", body, orig.Number)
	if len(quoted) > MaxBodyBytes {
		quoted = body
	}
	f, err := s.createTx(ctx, tx, p, CreateInput{Subject: subject, BodyMD: quoted, AttachmentIDs: in.AttachmentIDs,
		channel: in.channel, followupOf: orig, inboundMessageID: in.inboundMessageID})
	if err != nil {
		return nil, "", err
	}
	var msgID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ticket_messages WHERE ticket_id = ? ORDER BY created_at, id LIMIT 1`, f.ID).Scan(&msgID); err != nil {
		return nil, "", err
	}
	data := map[string]any{"new_ticket_id": f.ID, "original_ticket_id": orig.ID}
	if err := s.emit(ctx, tx, orig, p.UserID, events.TicketFollowupCreated, data); err != nil {
		return nil, "", err
	}
	if err := s.emit(ctx, tx, f, p.UserID, events.TicketFollowupCreated, data); err != nil {
		return nil, "", err
	}
	return f, msgID, nil
}

// EditNote edits the caller's internal note within 15 minutes (supervisors
// and admins any internal note). Public replies are immutable (TKT-006).
func (s *Service) EditNote(ctx context.Context, p *domain.Principal, ticketID, msgID, body string) error {
	body, err := validBody("body_md", body)
	if err != nil {
		return err
	}
	pol := s.Policy(ctx)
	if err := s.warm(ctx); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		now := s.clock()
		if err := authz.Can(p, authz.ActNote, t.view(pol, now)); err != nil {
			return err
		}
		var author sql.NullString
		var kind domain.MessageKind
		var created sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT author_id, kind, created_at FROM ticket_messages WHERE id = ? AND ticket_id = ?`, msgID, t.ID).Scan(&author, &kind, &created)
		if err != nil {
			return store.NotFound(err)
		}
		if !authz.CanEditMessage(p, author.String, kind, created.Time, now) {
			if kind != domain.MessageInternal {
				return problems.Forbidden("public replies are never edited, only appended")
			}
			return problems.Forbidden("only your own internal notes, within 15 minutes")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_messages SET body_md = ?, edited_at = ? WHERE id = ?`, body, now, msgID); err != nil {
			return err
		}
		m := &mutation{t: t}
		m.add(events.MessageEdited, events.MessageRef{MessageID: msgID, Kind: string(kind)})
		return s.flush(ctx, tx, m, p.UserID)
	})
}

// CustomerResolve marks the ticket resolved by the customer (TKT-013).
func (s *Service) CustomerResolve(ctx context.Context, p *domain.Principal, id string) error {
	return s.customerStatus(ctx, p, id, authz.ActResolve, domain.StatusResolved)
}

// CustomerReopen re-opens a resolved ticket inside the reopen window (TKT-013).
func (s *Service) CustomerReopen(ctx context.Context, p *domain.Principal, id string) error {
	return s.customerStatus(ctx, p, id, authz.ActReopen, domain.StatusOpen)
}

func (s *Service) customerStatus(ctx context.Context, p *domain.Principal, id string, act authz.Action, to domain.TicketStatus) error {
	pol := s.Policy(ctx)
	if err := s.warm(ctx); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		v := t.view(pol, s.clock())
		if !authz.CanSeeTicket(p, v) {
			return problems.NotFound()
		}
		if err := authz.Can(p, act, v); err != nil {
			// Visible but not in a state that allows it: 409 with the targets.
			return problems.InvalidTransition(string(t.Status), allowedTargets(actorCustomer, t.Status))
		}
		from := t.Status
		m := &mutation{t: t}
		if _, err := s.applyStatus(ctx, tx, t, actorCustomer, to, statusOpts{}); err != nil {
			return err
		}
		m.add(events.TicketStatusChanged, statusEvent(from, to))
		if to == domain.StatusResolved {
			if err := s.onResolved(ctx, tx, m, p.UserID); err != nil {
				return err
			}
		}
		return s.flush(ctx, tx, m, p.UserID)
	})
}
