// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// CreateInput is the union of PortalTicketCreate and AgentTicketCreate.
type CreateInput struct {
	Subject        string         `json:"subject"`
	BodyMD         string         `json:"body_md"`
	CategoryID     *string        `json:"category_id"`
	TeamID         *string        `json:"team_id"`
	Priority       string         `json:"priority"`
	AssigneeID     *string        `json:"assignee_id"`
	AttachmentIDs  []string       `json:"attachment_ids"`
	RequesterID    *string        `json:"requester_id"`
	RequesterEmail string         `json:"requester_email"`
	RequesterName  string         `json:"requester_name"`
	CustomFields   map[string]any `json:"custom_fields"`
	Tags           []string       `json:"tags"`

	// Set by the server, never by clients.
	channel          domain.Channel
	inboundMessageID string
	followupOf       *ticketRow
	fixedTeam        *string // mailbox team (email) or follow-up team
	authorID         string  // message author when not the principal (email)
}

// validSubject trims and checks a subject (1..200 characters).
func validSubject(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > 200 {
		return "", problems.Validation(problems.Field("subject", "length", "1..200 characters"))
	}
	return s, nil
}

// channelFor derives the channel from the route and the authentication (TKT-002).
func channelFor(p *domain.Principal) domain.Channel {
	switch {
	case p.TokenID != "":
		return domain.ChannelAPI
	case p.IsStaff():
		return domain.ChannelAgent
	}
	return domain.ChannelPortal
}

// Create creates a ticket for principal p (portal, agent or API) in one write
// transaction: number, routing, first public message, draft attachments,
// custom fields, tags, SLA due dates and the ticket.created event.
func (s *Service) Create(ctx context.Context, p *domain.Principal, in CreateInput) (string, error) {
	if in.channel == "" {
		in.channel = channelFor(p)
	}
	var id string
	if err := s.warm(ctx); err != nil {
		return "", err
	}
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := s.createTx(ctx, tx, p, in)
		if err != nil {
			return err
		}
		id = t.ID
		return nil
	})
	return id, err
}

// createTx is Create inside an existing transaction (follow-ups, split, email).
func (s *Service) createTx(ctx context.Context, tx *store.Tx, p *domain.Principal, in CreateInput) (*ticketRow, error) {
	subject, err := validSubject(in.Subject)
	if err != nil {
		return nil, err
	}
	body, err := validBody("body_md", in.BodyMD)
	if err != nil {
		return nil, err
	}
	pol := s.Policy(ctx)
	now := s.clock()
	t := &ticketRow{ID: store.NewID(), Subject: subject, Status: domain.StatusNew, Priority: domain.PriorityNormal, Tier: 1,
		Channel: in.channel, SLAState: map[string]string{}, CreatedAt: now, UpdatedAt: now, LastCustomerActivityAt: now}

	// Requester (M-3) and organization snapshot.
	req, err := s.resolveRequester(ctx, tx, p, &in)
	if err != nil {
		return nil, err
	}
	t.RequesterID, t.OrganizationID = req.ID, req.OrganizationID

	// Routing: category → default team/priority, else triage.
	if in.CategoryID != nil && *in.CategoryID != "" {
		cat, err := s.dir.Category(ctx, *in.CategoryID)
		if err != nil || (!cat.Active && p.IsCustomer()) {
			return nil, problems.Validation(problems.Field("category_id", "not_found", "unknown category"))
		}
		t.CategoryID = cat.ID
		if cat.DefaultTeamID != nil {
			t.TeamID = *cat.DefaultTeamID
		}
		if cat.DefaultPriority != nil {
			t.Priority = domain.Priority(*cat.DefaultPriority)
		}
	}
	if in.TeamID != nil && p.IsStaff() {
		if *in.TeamID == "" {
			t.TeamID = ""
		} else if _, err := s.dir.Team(ctx, *in.TeamID); err != nil {
			return nil, problems.Validation(problems.Field("team_id", "not_found", "unknown team"))
		} else {
			t.TeamID = *in.TeamID
		}
	}
	if in.fixedTeam != nil {
		t.TeamID = *in.fixedTeam
	}
	if in.followupOf != nil {
		t.TeamID, t.CategoryID, t.FollowupOfID = in.followupOf.TeamID, in.followupOf.CategoryID, in.followupOf.ID
		t.Priority = in.followupOf.Priority
	}
	if in.Priority != "" {
		pr := domain.Priority(in.Priority)
		if !pr.Valid() {
			return nil, problems.Validation(problems.Field("priority", "enum", "low, normal, high or urgent"))
		}
		if !authz.CanSetPriority(p, pr, pol) {
			if p.IsCustomer() {
				return nil, problems.Validation(problems.Field("priority", "not_allowed", "urgent is reserved for support staff"))
			}
			return nil, problems.Forbidden("urgent priority needs a supervisor")
		}
		t.Priority = pr
	}

	// Number (M-6) and insert.
	if t.Number, err = store.NextTicketNumber(ctx, tx); err != nil {
		return nil, err
	}
	if err := s.computeDue(ctx, t); err != nil {
		return nil, err
	}
	if err := insertTicket(ctx, tx, t); err != nil {
		return nil, err
	}

	// First public message + drafts.
	author := p.UserID
	if in.authorID != "" {
		author = in.authorID
	}
	msg := &msgRow{TicketID: t.ID, AuthorID: author, Kind: domain.MessagePublic, Channel: in.channel, Body: body,
		InboundMessageID: in.inboundMessageID, CreatedAt: now}
	if err := insertMessage(ctx, tx, msg); err != nil {
		return nil, err
	}
	if err := attachDrafts(ctx, tx, p.UserID, t.ID, msg.ID, in.AttachmentIDs); err != nil {
		return nil, err
	}
	if _, err := s.applyFieldValues(ctx, tx, t, p, in.CustomFields, true); err != nil {
		return nil, err
	}
	if p.IsStaff() && len(in.Tags) > 0 {
		if _, _, err := s.setTags(ctx, tx, t, in.Tags); err != nil {
			return nil, err
		}
	}
	if p.IsStaff() && in.AssigneeID != nil && *in.AssigneeID != "" {
		if err := s.checkAssign(ctx, tx, p, t, *in.AssigneeID); err != nil {
			return nil, err
		}
		t.AssigneeID = *in.AssigneeID
		t.Status = domain.StatusOpen // assignment opens a new ticket (lifecycle §2)
		if err := s.saveTicket(ctx, tx, t); err != nil {
			return nil, err
		}
	}
	data := map[string]any{"channel": string(t.Channel), "priority": string(t.Priority), "team": t.TeamID, "category": t.CategoryID,
		"message_id": msg.ID}
	if in.inboundMessageID != "" {
		data["inbound_message_id"] = in.inboundMessageID
	}
	if err := s.emit(ctx, tx, t, p.UserID, events.TicketCreated, data); err != nil {
		return nil, err
	}
	// Team assignment strategy (D-19 §A): written after ticket.created, as
	// the automation actor.
	am := &mutation{t: t}
	if err := s.autoAssign(ctx, tx, am, ""); err != nil {
		return nil, err
	}
	if err := s.flush(ctx, tx, am, p.UserID); err != nil {
		return nil, err
	}
	return t, nil
}

// resolveRequester applies §4 (who may file for whom) and M-3.
func (s *Service) resolveRequester(ctx context.Context, tx *store.Tx, p *domain.Principal, in *CreateInput) (directory.CustomerRef, error) {
	if in.followupOf != nil {
		return directory.LoadCustomer(ctx, tx, in.followupOf.RequesterID)
	}
	if p.IsCustomer() {
		if in.RequesterID == nil || *in.RequesterID == "" || *in.RequesterID == p.UserID {
			return directory.LoadCustomer(ctx, tx, p.UserID)
		}
		c, err := directory.LoadCustomer(ctx, tx, *in.RequesterID)
		if err != nil || !authz.CanCreateTicketFor(p, c.ID, c.OrganizationID) {
			// Customers outside the caller's organization are indistinguishable
			// from unknown IDs. OpenAPI createPortalTicket has no 404, so this
			// is a 422 on requester_id (GAP-TKT-04).
			return directory.CustomerRef{}, problems.Validation(problems.Field("requester_id", "not_found", "unknown requester"))
		}
		return c, nil
	}
	switch {
	case in.RequesterID != nil && *in.RequesterID != "":
		c, err := directory.LoadCustomer(ctx, tx, *in.RequesterID)
		if errors.Is(err, store.ErrNotFound) {
			return c, problems.Validation(problems.Field("requester_id", "customer", "the requester must be an existing customer"))
		}
		return c, err
	case strings.TrimSpace(in.RequesterEmail) != "":
		c, err := directory.FindOrCreateCustomer(ctx, tx, in.RequesterEmail, in.RequesterName, domain.UserActive)
		if pe, ok := err.(*problems.Error); ok && pe.Slug == problems.SlugConflict {
			// createAgentTicket documents no 409: report it on the field.
			return c, problems.Validation(problems.Field("requester_email", "not_customer", pe.Detail))
		}
		return c, err
	}
	return directory.CustomerRef{}, problems.Validation(problems.Field("requester_id", "required", "requester_id or requester_email is required"))
}
