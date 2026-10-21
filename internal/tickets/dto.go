// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"time"

	"github.com/radioBros/SimpleTickets/internal/directory"
)

// Staff DTOs follow the OpenAPI Ticket/TicketSummary/Message schemas.
// Customer DTOs are separate structs (portal DTOs): the customer schemas are
// strict (additionalProperties: false), and a staff field forgotten here is a
// compile error, not a leak (TKT-032 implementation note).

type attachmentDTO struct {
	ID          string    `json:"id"`
	TicketID    *string   `json:"ticket_id"`
	MessageID   *string   `json:"message_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256,omitempty"`
	URL         string    `json:"url"`
	CreatedAt   time.Time `json:"created_at"`
}

type messageDTO struct {
	ID          string          `json:"id"`
	TicketID    string          `json:"ticket_id"`
	Kind        string          `json:"kind"`
	Author      *userRef        `json:"author"`
	BodyMD      string          `json:"body_md"`
	Channel     string          `json:"channel"`
	Attachments []attachmentDTO `json:"attachments"`
	CreatedAt   time.Time       `json:"created_at"`
	EditedAt    *time.Time      `json:"edited_at"`
}

type customerMessageDTO struct {
	ID          string          `json:"id"`
	TicketID    string          `json:"ticket_id"`
	Kind        string          `json:"kind"` // always "public"
	Author      *userRef        `json:"author"`
	BodyMD      string          `json:"body_md"`
	Attachments []attachmentDTO `json:"attachments"`
	CreatedAt   time.Time       `json:"created_at"`
}

type ticketRefDTO struct {
	ID      string `json:"id"`
	Number  int64  `json:"number"`
	Ref     string `json:"ref"`
	Subject string `json:"subject"`
	Status  string `json:"status"`
}

type linkDTO struct {
	ID           string        `json:"id"`
	FromTicketID string        `json:"from_ticket_id"`
	ToTicketID   string        `json:"to_ticket_id"`
	Type         string        `json:"type"`
	ToTicket     *ticketRefDTO `json:"to_ticket,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
}

type ticketSummaryDTO struct {
	ID                     string               `json:"id"`
	Number                 int64                `json:"number"`
	Ref                    string               `json:"ref"`
	ProjectID              string               `json:"project_id"`
	Project                directory.ProjectRef `json:"project"`
	Subject                string               `json:"subject"`
	Status                 string               `json:"status"`
	Priority               string               `json:"priority"`
	Tier                   int                  `json:"tier"`
	TeamID                 *string              `json:"team_id"`
	CategoryID             *string              `json:"category_id"`
	Assignee               *userRef             `json:"assignee"`
	Requester              userRef              `json:"requester"`
	OrganizationID         *string              `json:"organization_id"`
	Channel                string               `json:"channel"`
	Tags                   []string             `json:"tags"`
	FirstResponseDueAt     *time.Time           `json:"first_response_due_at"`
	ResolutionDueAt        *time.Time           `json:"resolution_due_at"`
	SLABreached            bool                 `json:"sla_breached"`
	LastCustomerActivityAt *time.Time           `json:"last_customer_activity_at"`
	LastStaffActivityAt    *time.Time           `json:"last_staff_activity_at"`
	CustomFields           map[string]any       `json:"custom_fields"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

type ticketDTO struct {
	ticketSummaryDTO
	FirstRespondedAt *time.Time      `json:"first_responded_at"`
	ResolvedAt       *time.Time      `json:"resolved_at"`
	ClosedAt         *time.Time      `json:"closed_at"`
	SLAPausedAt      *time.Time      `json:"sla_paused_at"`
	FollowupOfID     *string         `json:"followup_of_id"`
	CSATScore        *int            `json:"csat_score"`
	Watchers         []userRef       `json:"watchers"`
	Links            []linkDTO       `json:"links"`
	Messages         []messageDTO    `json:"messages"`
	Attachments      []attachmentDTO `json:"attachments"`
	AllowedActions   []string        `json:"allowed_actions"`

	etag string
}

type customerTicketSummaryDTO struct {
	ID             string               `json:"id"`
	Number         int64                `json:"number"`
	Ref            string               `json:"ref"`
	ProjectID      string               `json:"project_id"`
	Project        directory.ProjectRef `json:"project"`
	Subject        string               `json:"subject"`
	Status         string               `json:"status"`
	Priority       string               `json:"priority"`
	CategoryID     *string              `json:"category_id"`
	Requester      userRef              `json:"requester"`
	OrganizationID *string              `json:"organization_id"`
	Channel        string               `json:"channel"`
	CustomFields   map[string]any       `json:"custom_fields"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	ResolvedAt     *time.Time           `json:"resolved_at"`
	ClosedAt       *time.Time           `json:"closed_at"`
}

type customerTicketDTO struct {
	ID             string               `json:"id"`
	Number         int64                `json:"number"`
	Ref            string               `json:"ref"`
	ProjectID      string               `json:"project_id"`
	Project        directory.ProjectRef `json:"project"`
	Subject        string               `json:"subject"`
	Status         string               `json:"status"`
	Priority       string               `json:"priority"`
	CategoryID     *string              `json:"category_id"`
	Requester      userRef              `json:"requester"`
	OrganizationID *string              `json:"organization_id"`
	Channel        string               `json:"channel"`
	CustomFields   map[string]any       `json:"custom_fields"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
	ResolvedAt     *time.Time           `json:"resolved_at"`
	ClosedAt       *time.Time           `json:"closed_at"`
	FollowupOfID   *string              `json:"followup_of_id"`
	CSATScore      *int                 `json:"csat_score"`
	Messages       []customerMessageDTO `json:"messages"`
	Attachments    []attachmentDTO      `json:"attachments"`
	AllowedActions []string             `json:"allowed_actions"`
}

// updateResult is TicketUpdateResult. Warnings (e.g. "open_children") are an
// extension the schema allows (no additionalProperties restriction).
type updateResult struct {
	Ticket   *ticketDTO `json:"ticket"`
	Visible  bool       `json:"visible"`
	Warnings []string   `json:"warnings,omitempty"`
}

type pageDTO[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// summary builds the staff summary of a row (refs, tags and fields filled by the caller).
func (t *ticketRow) summary(now time.Time) ticketSummaryDTO {
	d := ticketSummaryDTO{
		ID: t.ID, Number: t.Number, Ref: t.Ref(), ProjectID: t.ProjectID, Project: directory.ProjectRef{ID: t.ProjectID, Key: t.ProjectKey, Name: t.ProjectKey},
		Subject: t.Subject, Status: string(t.Status), Priority: string(t.Priority),
		Tier: t.Tier, TeamID: strOrNil(t.TeamID), CategoryID: strOrNil(t.CategoryID), OrganizationID: strOrNil(t.OrganizationID),
		Channel: string(t.Channel), Tags: []string{}, SLABreached: t.slaBreached(now), CustomFields: map[string]any{},
		LastCustomerActivityAt: timePtr(t.LastCustomerActivityAt), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Requester: userRef{ID: t.RequesterID, Kind: "customer"},
	}
	if t.FirstResponseDueAt.Valid {
		d.FirstResponseDueAt = timePtr(t.FirstResponseDueAt.Time)
	}
	if t.ResolutionDueAt.Valid {
		d.ResolutionDueAt = timePtr(t.ResolutionDueAt.Time)
	}
	if t.LastStaffActivityAt.Valid {
		d.LastStaffActivityAt = timePtr(t.LastStaffActivityAt.Time)
	}
	return d
}

// customerSummary builds the portal summary (no tier, team, assignee, SLA).
func (t *ticketRow) customerSummary() customerTicketSummaryDTO {
	d := customerTicketSummaryDTO{
		ID: t.ID, Number: t.Number, Ref: t.Ref(), ProjectID: t.ProjectID, Project: directory.ProjectRef{ID: t.ProjectID, Key: t.ProjectKey, Name: t.ProjectKey},
		Subject: t.Subject, Status: CustomerStatus(t.Status), Priority: string(t.Priority),
		CategoryID: strOrNil(t.CategoryID), OrganizationID: strOrNil(t.OrganizationID), Channel: string(t.Channel),
		CustomFields: map[string]any{}, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		Requester: userRef{ID: t.RequesterID, Kind: "customer"},
	}
	if t.ResolvedAt.Valid {
		d.ResolvedAt = timePtr(t.ResolvedAt.Time)
	}
	if t.ClosedAt.Valid {
		d.ClosedAt = timePtr(t.ClosedAt.Time)
	}
	return d
}

func nullTimePtr(valid bool, t time.Time) *time.Time {
	if !valid {
		return nil
	}
	return timePtr(t)
}

func intPtrOrNil(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
