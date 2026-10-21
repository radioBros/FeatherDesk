// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Inbound-email entry points (TKT-041), called by the mailin worker through
// an adapter in app/modules.go. Sender resolution, self-registration and
// staff trust (AUTH-034) happen in mailin before these calls.

// EmailAttachment is one attachment (or inline image, or original.eml).
type EmailAttachment struct {
	Filename    string
	ContentType string
	ContentID   string
	Inline      bool
	Data        []byte
}

// EmailTicket is a new ticket from email.
type EmailTicket struct {
	InboundMessageID string // inbound_messages.id, stored on the message
	MailboxID        string
	TeamID           string // the mailbox's team; "" = triage
	RequesterID      string // customer, already resolved by mailin
	CCUserIDs        []string
	Subject          string
	BodyMD           string
	Attachments      []EmailAttachment
}

// EmailReply is a reply to an existing ticket.
type EmailReply struct {
	InboundMessageID string
	TicketID         string
	AuthorID         string // requester, CC watcher or staff
	Internal         bool   // staff "#internal" first line → internal note
	BodyMD           string
	Attachments      []EmailAttachment
}

// ErrNotParticipant: the sender is neither requester, CC watcher nor staff
// who can see the ticket; mailin quarantines the message (`not-a-participant`).
var ErrNotParticipant = errors.New("tickets: sender is not a participant of the ticket")

// CreateFromEmail opens a ticket (channel=email) and returns its id.
func (s *Service) CreateFromEmail(ctx context.Context, in EmailTicket) (ticketID string, err error) {
	ticketID, _, err = s.CreateFromEmailMessage(ctx, in)
	return ticketID, err
}

// ReplyFromEmail appends a reply and returns the ticket id (a follow-up's id
// when the ticket was closed).
func (s *Service) ReplyFromEmail(ctx context.Context, in EmailReply) (ticketID string, err error) {
	ticketID, _, err = s.ReplyFromEmailMessage(ctx, in)
	return ticketID, err
}

// byInboundID makes both entry points idempotent on the inbound message.
func (s *Service) byInboundID(ctx context.Context, inboundID string) (ticketID, msgID string, ok bool) {
	if inboundID == "" {
		return "", "", false
	}
	err := s.db.Read().QueryRowContext(ctx, `SELECT ticket_id, id FROM ticket_messages WHERE inbound_message_id = ? ORDER BY created_at LIMIT 1`, inboundID).Scan(&ticketID, &msgID)
	return ticketID, msgID, err == nil
}

// storeEmailAttachments applies the upload limits and sniffing of TKT-011:
// files over the size limit and beyond 10 per message are dropped.
func (s *Service) storeEmailAttachments(ctx context.Context, uploader string, atts []EmailAttachment) ([]string, error) {
	var ids []string
	for _, a := range atts {
		if len(ids) == MaxAttachmentsPerMessage {
			break
		}
		if int64(len(a.Data)) > s.maxUpload {
			continue
		}
		dto, err := s.StoreAttachment(ctx, uploader, a.Filename, bytes.NewReader(a.Data))
		if err != nil {
			if pe, ok := err.(*problems.Error); ok && pe.Slug == problems.SlugPayloadTooLarge {
				continue
			}
			return ids, err
		}
		ids = append(ids, dto.ID)
	}
	return ids, nil
}

func emailSubject(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "(no subject)"
	}
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:200])
	}
	return s
}

func emailBody(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(empty message)"
	}
	if len(s) > MaxBodyBytes {
		s = s[:MaxBodyBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// CreateFromEmailMessage is CreateFromEmail returning the message id too.
func (s *Service) CreateFromEmailMessage(ctx context.Context, in EmailTicket) (ticketID, messageID string, err error) {
	if t, m, ok := s.byInboundID(ctx, in.InboundMessageID); ok {
		return t, m, nil
	}
	p, err := loadCustomerPrincipal(ctx, s.db.Read(), in.RequesterID)
	if err != nil {
		return "", "", problems.Validation(problems.Field("requester_id", "customer", "the requester must be an active customer"))
	}
	if in.TeamID != "" {
		if _, err := s.dir.Team(ctx, in.TeamID); err != nil {
			in.TeamID = "" // a deleted mailbox team falls back to triage
		}
	}
	atts, err := s.storeEmailAttachments(ctx, p.UserID, in.Attachments)
	if err != nil {
		return "", "", err
	}
	ci := CreateInput{Subject: emailSubject(in.Subject), BodyMD: emailBody(in.BodyMD), AttachmentIDs: atts,
		channel: domain.ChannelEmail, inboundMessageID: in.InboundMessageID}
	if in.TeamID != "" {
		ci.fixedTeam = &in.TeamID
	}
	id, err := s.Create(ctx, p, ci)
	if err != nil {
		return "", "", err
	}
	if len(in.CCUserIDs) > 0 {
		if err := s.addCustomerCCs(ctx, id, p.UserID, in.CCUserIDs); err != nil {
			return "", "", err
		}
	}
	_, mid, _ := s.byInboundID(ctx, in.InboundMessageID)
	return id, mid, nil
}

// addCustomerCCs adds e-mail CC recipients of the same organization as
// watchers (others are ignored: CCs never widen across organizations).
func (s *Service) addCustomerCCs(ctx context.Context, ticketID, actor string, ids []string) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		var added []string
		for _, u := range ids {
			if u == t.RequesterID || containsStr(t.Watchers, u) || t.OrganizationID == "" {
				continue
			}
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ? AND kind = 'customer' AND status = 'active' AND organization_id = ?`, u, t.OrganizationID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, t.ID, u, s.clock()); err != nil {
				return err
			}
			t.Watchers = append(t.Watchers, u)
			added = append(added, u)
		}
		if len(added) == 0 {
			return nil
		}
		m := &mutation{t: t}
		m.add(events.TicketWatchersChanged, map[string]any{"added": added})
		return s.flush(ctx, tx, m, actor)
	})
}

// ReplyFromEmailMessage is ReplyFromEmail returning the message id too.
func (s *Service) ReplyFromEmailMessage(ctx context.Context, in EmailReply) (ticketID, messageID string, err error) {
	if t, m, ok := s.byInboundID(ctx, in.InboundMessageID); ok {
		return t, m, nil
	}
	if err := s.warm(ctx); err != nil {
		return "", "", err
	}
	q := s.db.Read()
	if !validID(in.TicketID) {
		return "", "", store.ErrNotFound
	}
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`, in.TicketID))
	if err != nil {
		return "", "", err
	}
	if err := loadWatchers(ctx, q, t); err != nil {
		return "", "", err
	}
	var kind string
	if err := q.QueryRowContext(ctx, `SELECT kind FROM users WHERE id = ?`, in.AuthorID).Scan(&kind); err != nil {
		return "", "", ErrNotParticipant
	}
	pol := s.Policy(ctx)
	if kind == string(domain.KindStaff) {
		st, err := loadStaff(ctx, q, in.AuthorID)
		if err != nil || st.Status != domain.UserActive {
			return "", "", ErrNotParticipant
		}
		p := &domain.Principal{UserID: st.ID, Kind: domain.KindStaff, Role: st.Role, Tier: st.Tier, Teams: st.Teams}
		if !authz.CanSeeTicket(p, t.view(pol, s.clock())) {
			return "", "", ErrNotParticipant
		}
		atts, err := s.storeEmailAttachments(ctx, p.UserID, in.Attachments)
		if err != nil {
			return "", "", err
		}
		k := string(domain.MessagePublic)
		if in.Internal {
			k = string(domain.MessageInternal)
		}
		res, err := s.AddStaffMessage(ctx, p, t.ID, MessageInput{Kind: k, BodyMD: emailBody(in.BodyMD), AttachmentIDs: atts,
			channel: domain.ChannelEmail, inboundMessageID: in.InboundMessageID})
		return res.TicketID, res.MessageID, err
	}
	p, err := loadCustomerPrincipal(ctx, q, in.AuthorID)
	if err != nil || !authz.CanSeeTicket(p, t.view(pol, s.clock())) {
		return "", "", ErrNotParticipant
	}
	atts, err := s.storeEmailAttachments(ctx, p.UserID, in.Attachments)
	if err != nil {
		return "", "", err
	}
	res, err := s.AddCustomerReply(ctx, p, t.ID, MessageInput{BodyMD: emailBody(in.BodyMD), AttachmentIDs: atts,
		channel: domain.ChannelEmail, inboundMessageID: in.InboundMessageID})
	return res.TicketID, res.MessageID, err
}
