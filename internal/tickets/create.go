// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// CreateInput is the union of PortalTicketCreate and AgentTicketCreate.
type CreateInput struct {
	Subject        string         `json:"subject"`
	BodyMD         string         `json:"body_md"`
	BodyHTML       string         `json:"body_html"`
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
	// ProjectID (D-20): id or key. Omitted: the category's project, else
	// the only project the customer may use, else the default project.
	ProjectID *string `json:"project_id"`

	// Set by the server, never by clients.
	channel          domain.Channel
	inboundMessageID string
	followupOf       *ticketRow
	fixedTeam        *string // mailbox team (email) or follow-up team
	fixedProject     string  // mailbox project (email)
	authorID         string  // message author when not the principal (email)
	// onBehalf (I-12): written with tickets:act_as_customer. A staff
	// principal's ticket then gets the requester as first-message author.
	onBehalf *onBehalf
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
	body, err := resolveBody(in.BodyMD, in.BodyHTML)
	if err != nil {
		return nil, err
	}
	in.BodyMD = body
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
	if in.onBehalf != nil && !p.IsCustomer() && req.Status == domain.UserDisabled {
		return nil, problems.New(problems.SlugOnBehalfDisabled, "the customer account is disabled")
	}

	// Project (D-20), then routing: category → default team/priority, else
	// the project's default team, else triage.
	var cat *directory.Category
	if in.CategoryID != nil && *in.CategoryID != "" && in.followupOf == nil {
		c, err := s.dir.Category(ctx, *in.CategoryID)
		if err != nil || (!c.Active && p.IsCustomer()) {
			return nil, problems.Validation(problems.Field("category_id", "not_found", "unknown category"))
		}
		cat = &c
	}
	proj, err := s.ticketProject(ctx, p, &in, cat)
	if err != nil {
		return nil, err
	}
	t.ProjectID, t.ProjectKey = proj.ID, proj.Key
	if proj.DefaultTeamID != nil {
		t.TeamID = *proj.DefaultTeamID
	}
	if cat != nil {
		t.CategoryID = cat.ID
		if cat.DefaultTeamID != nil && s.dir.TeamServesProject(ctx, *cat.DefaultTeamID, proj.ID) {
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
		} else if !s.dir.TeamServesProject(ctx, *in.TeamID, proj.ID) {
			return nil, problems.Validation(problems.Field("team_id", "wrong_project", "the team does not serve the ticket's project"))
		} else {
			t.TeamID = *in.TeamID
		}
	}
	if in.fixedTeam != nil {
		t.TeamID = *in.fixedTeam
		if t.TeamID != "" && !s.dir.TeamServesProject(ctx, t.TeamID, proj.ID) {
			t.TeamID = "" // a mailbox team that stopped serving the project: triage
		}
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

	// Number (M-6), per-project sequence (D-20) and insert.
	if t.Number, err = store.NextTicketNumber(ctx, tx); err != nil {
		return nil, err
	}
	if t.ProjectSeq, err = store.NextProjectSeq(ctx, tx, t.ProjectID); err != nil {
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
	staffOnBehalf := in.onBehalf != nil && !p.IsCustomer()
	if staffOnBehalf {
		author = req.ID // I-12: the customer wrote it, the integration posted it
	}
	msg := &msgRow{TicketID: t.ID, AuthorID: author, Kind: domain.MessagePublic, Channel: in.channel, Body: body,
		InboundMessageID: in.inboundMessageID, ViaTokenID: in.onBehalf.tokenID(), CreatedAt: now}
	if err := insertMessage(ctx, tx, msg); err != nil {
		return nil, err
	}
	if err := attachDrafts(ctx, tx, in.onBehalf.uploader(p.UserID), t.ID, msg.ID, in.AttachmentIDs); err != nil {
		return nil, err
	}
	if staffOnBehalf {
		if err := auditOnBehalf(ctx, tx, in.onBehalf, req.ID, t.ID, msg.ID); err != nil {
			return nil, err
		}
	}
	// Required customer fields (M-9) only bind a customer filling in a form:
	// an inbound e-mail or a follow-up (reply to a closed ticket) cannot
	// carry custom field values.
	formCreate := in.followupOf == nil && in.channel != domain.ChannelEmail
	if _, err := s.applyFieldValues(ctx, tx, t, p, in.CustomFields, formCreate); err != nil {
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
	if in.onBehalf != nil {
		data["via_integration"] = true
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

// ticketProject decides the project of a new ticket (D-20): a follow-up keeps
// its original's, then the explicit project_id, the mailbox's, the category's;
// otherwise the only project a customer may use, else the default project.
// Customers must be allowed to use the project (portal-visible, organization
// limits) and agents must be members of it (I-11).
func (s *Service) ticketProject(ctx context.Context, p *domain.Principal, in *CreateInput, cat *directory.Category) (directory.Project, error) {
	if in.followupOf != nil {
		pr, err := s.dir.Project(ctx, in.followupOf.ProjectID)
		if err != nil {
			return directory.Project{ID: in.followupOf.ProjectID, Key: in.followupOf.ProjectKey}, nil
		}
		return pr, nil
	}
	var pr directory.Project
	explicit := in.ProjectID != nil && *in.ProjectID != ""
	switch {
	case explicit:
		x, err := s.dir.ResolveProject(ctx, *in.ProjectID)
		if err != nil {
			return pr, problems.Validation(problems.Field("project_id", "not_found", "unknown project"))
		}
		pr = x
	case in.fixedProject != "":
		x, err := s.dir.Project(ctx, in.fixedProject)
		if err != nil {
			return pr, problems.Validation(problems.Field("project_id", "not_found", "unknown project"))
		}
		pr = x
	case cat != nil:
		x, err := s.dir.Project(ctx, cat.ProjectID)
		if err != nil {
			return pr, problems.Validation(problems.Field("category_id", "not_found", "unknown category"))
		}
		pr = x
	case p.IsCustomer():
		allowed, err := s.dir.CustomerProjects(ctx, p)
		if err != nil {
			return pr, err
		}
		def, derr := s.dir.DefaultProject(ctx)
		switch {
		case len(allowed) == 1:
			pr = allowed[0]
		case derr == nil && slices.ContainsFunc(allowed, func(x directory.Project) bool { return x.ID == def.ID }):
			pr = def
		case len(allowed) == 0:
			return pr, problems.Validation(problems.Field("project_id", "not_allowed", "no project is open to your organization"))
		default:
			return pr, problems.Validation(problems.Field("project_id", "required", "choose a project"))
		}
	default:
		x, err := s.dir.DefaultProject(ctx)
		if err != nil {
			return pr, problems.Validation(problems.Field("project_id", "required", "no active project"))
		}
		pr = x
	}
	if cat != nil && cat.ProjectID != pr.ID {
		return pr, problems.Validation(problems.Field("category_id", "wrong_project", "the category belongs to another project"))
	}
	if in.fixedProject != "" && !explicit {
		return pr, nil // inbound mail: the mailbox decides, whatever its state
	}
	if !pr.Active {
		return pr, problems.Validation(problems.Field("project_id", "inactive", "the project is not active"))
	}
	if p.IsCustomer() {
		allowed, err := s.dir.CustomerProjects(ctx, p)
		if err != nil {
			return pr, err
		}
		if !slices.ContainsFunc(allowed, func(x directory.Project) bool { return x.ID == pr.ID }) {
			return pr, problems.Validation(problems.Field("project_id", "not_allowed", "you cannot open tickets in this project"))
		}
	} else if !p.InProject(pr.ID) {
		return pr, problems.Validation(problems.Field("project_id", "not_allowed", "you are not a member of this project"))
	}
	return pr, nil
}
