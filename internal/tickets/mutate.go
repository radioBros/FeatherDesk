// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

func nullTime(nt sql.NullTime) any {
	if !nt.Valid {
		return nil
	}
	return nt.Time.UTC()
}

// insertTicket inserts a new row (number already allocated).
func insertTicket(ctx context.Context, tx *store.Tx, t *ticketRow) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, category_id, assignee_id,
		requester_id, organization_id, channel, first_response_due_at, resolution_due_at, first_responded_at, resolved_at, closed_at,
		sla_paused_at, sla_paused_minutes, sla_state, last_customer_activity_at, last_staff_activity_at, followup_of_id,
		csat_score, csat_comment, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Number, t.Subject, string(t.Status), string(t.Priority), t.Tier, store.Nullable(t.TeamID), store.Nullable(t.CategoryID),
		store.Nullable(t.AssigneeID), t.RequesterID, store.Nullable(t.OrganizationID), string(t.Channel),
		nullTime(t.FirstResponseDueAt), nullTime(t.ResolutionDueAt), nullTime(t.FirstRespondedAt), nullTime(t.ResolvedAt), nullTime(t.ClosedAt),
		nullTime(t.SLAPausedAt), t.SLAPausedMinutes, store.MustJSON(t.SLAState), t.LastCustomerActivityAt, nullTime(t.LastStaffActivityAt),
		store.Nullable(t.FollowupOfID), nullableInt(t.CSATScore), store.Nullable(t.CSATComment), t.CreatedAt, t.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "requester_id must be a customer") {
		return problems.Validation(problems.Field("requester_id", "customer", "the requester must be a customer"))
	}
	return err
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// saveTicket writes every mutable column of t and bumps updated_at.
func (s *Service) saveTicket(ctx context.Context, tx *store.Tx, t *ticketRow) error {
	t.UpdatedAt = s.clock()
	_, err := tx.ExecContext(ctx, `UPDATE tickets SET subject = ?, status = ?, priority = ?, tier = ?, team_id = ?, category_id = ?,
		assignee_id = ?, organization_id = ?, first_response_due_at = ?, resolution_due_at = ?, first_responded_at = ?, resolved_at = ?,
		closed_at = ?, sla_paused_at = ?, sla_paused_minutes = ?, sla_state = ?, last_customer_activity_at = ?,
		last_staff_activity_at = ?, csat_score = ?, csat_comment = ?, updated_at = ? WHERE id = ?`,
		t.Subject, string(t.Status), string(t.Priority), t.Tier, store.Nullable(t.TeamID), store.Nullable(t.CategoryID),
		store.Nullable(t.AssigneeID), store.Nullable(t.OrganizationID), nullTime(t.FirstResponseDueAt), nullTime(t.ResolutionDueAt),
		nullTime(t.FirstRespondedAt), nullTime(t.ResolvedAt), nullTime(t.ClosedAt), nullTime(t.SLAPausedAt), t.SLAPausedMinutes,
		store.MustJSON(t.SLAState), t.LastCustomerActivityAt, nullTime(t.LastStaffActivityAt), nullableInt(t.CSATScore),
		store.Nullable(t.CSATComment), t.UpdatedAt, t.ID)
	return err
}

// msgRow is one ticket_messages row.
type msgRow struct {
	ID               string
	TicketID         string
	AuthorID         string
	Kind             domain.MessageKind
	Channel          domain.Channel
	Body             string
	InboundMessageID string
	MovedFrom        string
	SplitTo          string
	CreatedAt        time.Time
	EditedAt         sql.NullTime
}

// MaxBodyBytes is the message body limit (64 KiB, domain model).
const MaxBodyBytes = 65536

// validBody trims and checks a Markdown body.
func validBody(field, body string) (string, error) {
	b := strings.TrimSpace(body)
	switch {
	case b == "":
		return "", problems.Validation(problems.Field(field, "required", "must not be empty"))
	case len(body) > MaxBodyBytes || !utf8.ValidString(body):
		return "", problems.Validation(problems.Field(field, "max_length", "at most 64 KiB of UTF-8 text"))
	}
	return body, nil
}

func insertMessage(ctx context.Context, tx *store.Tx, m *msgRow) error {
	if m.ID == "" {
		m.ID = store.NewID()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, inbound_message_id, moved_from_ticket_id, split_to_ticket_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.TicketID, store.Nullable(m.AuthorID), string(m.Kind), string(m.Channel), m.Body, store.Nullable(m.InboundMessageID),
		store.Nullable(m.MovedFrom), store.Nullable(m.SplitTo), m.CreatedAt)
	return err
}

// MaxAttachmentsPerMessage limits attachment_ids (TKT-011).
const MaxAttachmentsPerMessage = 10

// attachDrafts binds draft uploads of uploaderID to a message. Another user's
// draft, an already attached file or an unknown id is a 422 (TKT-011).
func attachDrafts(ctx context.Context, tx *store.Tx, uploaderID, ticketID, messageID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > MaxAttachmentsPerMessage {
		return problems.Validation(problems.Field("attachment_ids", "max_items", "at most 10 attachments per message"))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var uploader string
		var msg sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT uploader_id, message_id FROM attachments WHERE id = ?`, id).Scan(&uploader, &msg)
		if err != nil {
			if errors.Is(store.NotFound(err), store.ErrNotFound) || !validID(id) {
				return problems.Validation(problems.Field("attachment_ids", "not_found", "unknown attachment "+id))
			}
			return err
		}
		if uploader != uploaderID || msg.Valid {
			return problems.Validation(problems.Field("attachment_ids", "not_owner", "attachment "+id+" is not your draft"))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET ticket_id = ?, message_id = ? WHERE id = ?`, ticketID, messageID, id); err != nil {
			return err
		}
	}
	return nil
}

// statusOpts tune applyStatus.
type statusOpts struct {
	skipFieldCheck bool // M-9 check only applies to explicit staff resolves
}

// applyStatus moves t to `to` through the transition map, applying side
// effects (resolved_at, closed_at, SLA pause/resume, CSAT cycle) to the row.
// The caller saves the row; applyStatus emits ticket.status_changed AFTER the
// caller's save via the returned emitter so the snapshot is final.
func (s *Service) applyStatus(ctx context.Context, tx *store.Tx, t *ticketRow, actor actorKind, to domain.TicketStatus, opt statusOpts) (changed bool, err error) {
	from := t.Status
	if from == to {
		return false, nil
	}
	if !to.Valid() {
		return false, problems.Validation(problems.Field("status", "enum", "unknown status"))
	}
	if !mayTrigger(actor, from, to) {
		return false, problems.InvalidTransition(string(from), allowedTargets(actor, from))
	}
	if to == domain.StatusResolved && (actor == actorStaff || actor == actorSupervisor) && !opt.skipFieldCheck {
		missing, err := s.missingRequiredStaffFields(ctx, tx, t)
		if err != nil {
			return false, err
		}
		if len(missing) > 0 {
			return false, &problems.Error{Slug: problems.SlugConflict, Detail: "required-fields-missing", Extra: map[string]any{"fields": missing}}
		}
	}
	now := s.clock()
	if err := s.slaOnStatus(ctx, t, from, to, now); err != nil {
		return false, err
	}
	switch to {
	case domain.StatusResolved:
		t.ResolvedAt = sql.NullTime{Time: now, Valid: true}
	case domain.StatusClosed:
		t.ClosedAt = sql.NullTime{Time: now, Valid: true}
		if !t.ResolvedAt.Valid {
			t.ResolvedAt = sql.NullTime{Time: now, Valid: true}
		}
	case domain.StatusOpen, domain.StatusPending, domain.StatusOnHold, domain.StatusNew:
		if from == domain.StatusResolved || from == domain.StatusClosed {
			// A new resolution cycle starts: CSAT may be given again (TKT-027).
			t.ResolvedAt, t.ClosedAt = sql.NullTime{}, sql.NullTime{}
			t.CSATScore, t.CSATComment = 0, ""
		}
	}
	t.Status = to
	return true, nil
}

// statusEvent is the payload of ticket.status_changed.
func statusEvent(from, to domain.TicketStatus) events.StatusChange {
	return events.StatusChange{From: string(from), To: string(to)}
}
