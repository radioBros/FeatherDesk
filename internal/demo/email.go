// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

// inboundRow writes one inbound-log row (the mailbox log has no create API;
// the real rows are written by the mail pipeline, which a seeder must not run).
func (s *seeder) inboundRow(id, from, subject, status, reason, ticketID string) error {
	at := s.cur()
	domain := "mail.demo.test"
	if i := strings.IndexByte(from, '@'); i >= 0 {
		domain = from[i+1:]
	}
	return s.db.Tx(s.ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(s.ctx, `INSERT INTO inbound_messages (id, mailbox_id, message_id, from_addr, subject, status, reason, ticket_id, raw_blob_key, received_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			id, s.mailboxID, fmt.Sprintf("<%s@%s>", id, domain), from, subject, status, reason, store.Nullable(ticketID), at, at)
		return err
	})
}

// emailOpen creates a ticket the way the mail pipeline does after it
// accepted a message (channel "email"), through the tickets service entry
// point, and logs the inbound message as processed. team "" = triage.
func (s *seeder) emailOpen(c *person, sp spec, team string) (*tk, error) {
	teamID := ""
	if team != "" {
		teamID = s.teams[team].ID
	}
	msgID := store.NewID()
	tid, _, err := s.svc.CreateFromEmailMessage(s.ctx, tickets.EmailTicket{InboundMessageID: msgID, MailboxID: s.mailboxID, TeamID: teamID,
		RequesterID: c.id, Subject: sp.subject, BodyMD: sp.body})
	if err != nil {
		return nil, fmt.Errorf("email ticket %q: %w", sp.subject, err)
	}
	if err := s.inboundRow(msgID, c.email, sp.subject, "processed", "", tid); err != nil {
		return nil, err
	}
	t := &tk{id: tid, subject: sp.subject, req: c, team: team, tier: 1, status: "new", created: s.cur(), project: "web"}
	var r ticketResp
	if err := s.staff["sup"].cl.get("/agent/tickets/"+tid, &r); err != nil {
		return nil, err
	}
	s.track(t, r)
	s.tickets = append(s.tickets, t)
	return t, nil
}

// emailReply appends a customer's emailed reply to t.
func (s *seeder) emailReply(t *tk, body string) error {
	msgID := store.NewID()
	if _, _, err := s.svc.ReplyFromEmailMessage(s.ctx, tickets.EmailReply{InboundMessageID: msgID, TicketID: t.id, AuthorID: t.req.id, BodyMD: body}); err != nil {
		return fmt.Errorf("email reply #%d: %w", t.number, err)
	}
	if t.status == "pending" || t.status == "on_hold" {
		t.status = "open"
	}
	return s.inboundRow(msgID, t.req.email, "Re: "+t.subject, "processed", "", t.id)
}
