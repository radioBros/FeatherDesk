// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import "context"

// TicketSink is implemented by the tickets module (TKT-*). mailin hands every
// accepted inbound mail to it so the normal ticket service writes the ticket
// or message, its ticket_events row, notifications and WebSocket pushes
// (D-16 "Processing path"). Wiring: see docs/dev/internals.md
// "Notifications wiring".
type TicketSink interface {
	// CreateFromEmail opens a new ticket (channel=email) for a customer
	// requester. TeamID "" means triage.
	CreateFromEmail(ctx context.Context, in EmailTicket) (EmailResult, error)
	// ReplyFromEmail adds a message to an existing ticket. The sink applies
	// the lifecycle rules: a reply to a closed ticket follows the follow-up
	// rule (ticket_lifecycle_spec) and returns the follow-up ticket id.
	ReplyFromEmail(ctx context.Context, in EmailReply) (EmailResult, error)
}

// EmailAttachment is one attachment (or inline image, or original.eml) of an
// inbound mail. Size limits and content sniffing are applied by the sink
// through the normal upload path (TKT-011).
type EmailAttachment struct {
	Filename    string
	ContentType string
	ContentID   string // inline images: referenced from BodyMD as cid:<ContentID>
	Inline      bool
	Data        []byte
}

// EmailTicket is a new ticket from email.
type EmailTicket struct {
	InboundMessageID string // inbound_messages.id (stored as ticket_messages.inbound_message_id)
	MailboxID        string
	TeamID           string // "" = triage
	ProjectID        string // the mailbox's project (D-20)
	RequesterID      string // customer user id (already resolved/created by mailin)
	CCUserIDs        []string
	Subject          string
	BodyMD           string // quote-stripped, Markdown
	Attachments      []EmailAttachment
}

// EmailReply is a reply to an existing ticket.
type EmailReply struct {
	InboundMessageID string
	TicketID         string
	AuthorID         string // customer (requester/CC) or staff user id
	Internal         bool   // staff "#internal" first line → internal note
	BodyMD           string
	Attachments      []EmailAttachment
}

// EmailResult is what the sink created.
type EmailResult struct {
	TicketID  string
	MessageID string // ticket_messages.id
}

// ErrNoSink is recorded (status failed, retryable) when no TicketSink is wired.
type noSinkError struct{}

func (noSinkError) Error() string { return "mailin: no ticket sink wired (tickets module missing)" }

// ErrNoSink is returned while no TicketSink is set.
var ErrNoSink error = noSinkError{}
