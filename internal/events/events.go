// SPDX-License-Identifier: AGPL-3.0-only

// Package events defines the domain event vocabulary (events_spec §1) and the
// ticket_events log, which is also the queue's outbox (D-11): a ticket
// mutation writes exactly one row inside its transaction and nothing else.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// Event types. Keep in sync with development/contracts/events_spec.md §1.
const (
	TicketCreated          = "ticket.created"
	TicketStatusChanged    = "ticket.status_changed"
	TicketPriorityChanged  = "ticket.priority_changed"
	TicketAssigned         = "ticket.assigned"
	TicketTeamChanged      = "ticket.team_changed"
	TicketEscalated        = "ticket.escalated"
	TicketDeescalated      = "ticket.deescalated"
	TicketCategoryChanged  = "ticket.category_changed"
	TicketTagsChanged      = "ticket.tags_changed"
	TicketFieldsChanged    = "ticket.fields_changed"
	TicketSubjectChanged   = "ticket.subject_changed"
	MessageCreated         = "message.created"
	NoteCreated            = "note.created"
	MessageEdited          = "message.edited"
	SLAWarning             = "sla.warning"
	SLABreached            = "sla.breached"
	TicketCSAT             = "ticket.csat"
	TicketFollowupCreated  = "ticket.followup_created"
	TicketMerged           = "ticket.merged"
	TicketSplit            = "ticket.split"
	TicketLinked           = "ticket.linked"
	TicketUnlinked         = "ticket.unlinked"
	TicketChildrenResolved = "ticket.children_resolved"
	TicketWatchersChanged  = "ticket.watchers_changed"
	TicketDeleted          = "ticket.deleted"
	// TicketAutomationApplied: an automation rule applied (D-19 §A); staff only.
	TicketAutomationApplied = "ticket.automation_applied"
	TicketAccessLost        = "ticket.access_lost" // realtime-only, never stored
)

// All lists every stored event type (TestEventsCustomerVisibility iterates it).
var All = []string{
	TicketCreated, TicketStatusChanged, TicketPriorityChanged, TicketAssigned, TicketTeamChanged,
	TicketEscalated, TicketDeescalated, TicketCategoryChanged, TicketTagsChanged, TicketFieldsChanged,
	TicketSubjectChanged, MessageCreated, NoteCreated, MessageEdited, SLAWarning, SLABreached, TicketCSAT,
	TicketFollowupCreated, TicketMerged, TicketSplit, TicketLinked, TicketUnlinked, TicketChildrenResolved,
	TicketWatchersChanged, TicketDeleted, TicketAutomationApplied,
}

// customerVisible: events a customer may ever learn about (I-7). Payloads of
// these may still need per-field filtering (I-9) by the publisher.
var customerVisible = map[string]bool{
	TicketCreated:         true,
	TicketStatusChanged:   true,
	MessageCreated:        true,
	TicketFollowupCreated: true,
	TicketMerged:          true,
	TicketFieldsChanged:   true, // only customer-visible keys, filtered by the publisher
	TicketCSAT:            false,
	TicketSubjectChanged:  true,
	// automation_applied stays staff-only (I-7): rule names and notes are internal.
	TicketAutomationApplied: false,
}

// CustomerVisible reports whether the event type may reach customer channels
// (email, customer WebSocket frames, customer-scoped webhooks).
func CustomerVisible(typ string) bool { return customerVisible[typ] }

// Snapshot is the ticket state after the change. It travels with the event so
// fan-out (WebSocket visibility, notification recipients) needs no DB reads.
type Snapshot struct {
	Number         int64    `json:"number"`
	Subject        string   `json:"subject"`
	Status         string   `json:"status"`
	Priority       string   `json:"priority"`
	Tier           int      `json:"tier"`
	TeamID         string   `json:"team_id,omitempty"`
	CategoryID     string   `json:"category_id,omitempty"`
	AssigneeID     string   `json:"assignee_id,omitempty"`
	RequesterID    string   `json:"requester_id"`
	OrganizationID string   `json:"organization_id,omitempty"`
	Watchers       []string `json:"watchers,omitempty"`
	Channel        string   `json:"channel,omitempty"`
}

// ActorAutomation is the actor_id of every event written by automation
// rules and assignment strategies (D-19 §A). It is not a users row: readers
// that resolve actors get no match and show the change as automatic. The
// automation consumer ignores these events (loop guard).
const ActorAutomation = "automation"

// Event is one row of ticket_events.
type Event struct {
	ID        string          `json:"id"`
	TicketID  string          `json:"ticket_id"`
	ActorID   string          `json:"actor_id,omitempty"` // empty = system
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
	Snapshot  Snapshot        `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

// Decode unmarshals Data into v.
func (e Event) Decode(v any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// Payload structs for the most common events.
type (
	StatusChange struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	Assignment struct {
		From string `json:"from,omitempty"`
		To   string `json:"to,omitempty"`
	}
	TierChange struct {
		FromTier int    `json:"from_tier"`
		ToTier   int    `json:"to_tier"`
		FromTeam string `json:"from_team,omitempty"`
		ToTeam   string `json:"to_team,omitempty"`
		Reason   string `json:"reason"`
	}
	MessageRef struct {
		MessageID        string `json:"message_id"`
		Kind             string `json:"kind"`
		Channel          string `json:"channel,omitempty"`
		InboundMessageID string `json:"inbound_message_id,omitempty"`
	}
	Change struct {
		From any `json:"from"`
		To   any `json:"to"`
	}
	SLA struct {
		Which string    `json:"which"` // first_response | resolution
		DueAt time.Time `json:"due_at"`
	}
	Link struct {
		ToTicketID string `json:"to_ticket_id"`
		Type       string `json:"type"`
	}
	Merge struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
	}
	Split struct {
		ParentID   string   `json:"parent_id"`
		ChildID    string   `json:"child_id"`
		MessageIDs []string `json:"message_ids"`
	}
	FieldsChange struct {
		Keys []string `json:"keys"`
	}
	// Created is the data of ticket.created.
	Created struct {
		Channel          string `json:"channel"`
		Priority         string `json:"priority"`
		Team             string `json:"team"`
		Category         string `json:"category"`
		MessageID        string `json:"message_id,omitempty"`
		InboundMessageID string `json:"inbound_message_id,omitempty"`
	}
	// NoteRef is the data of note.created; Mentioned lists the staff user
	// ids that get staff.mentioned.
	NoteRef struct {
		MessageID string   `json:"message_id"`
		Kind      string   `json:"kind"`
		Mentioned []string `json:"mentioned,omitempty"`
	}
	// Followup is the data of ticket.followup_created (written on both tickets).
	Followup struct {
		NewTicketID      string `json:"new_ticket_id"`
		OriginalTicketID string `json:"original_ticket_id"`
	}
	// CSAT is the data of ticket.csat.
	CSAT struct {
		Score int `json:"score"`
	}
	// ChildrenResolved is the data of ticket.children_resolved (the last
	// open child resolved).
	ChildrenResolved struct {
		ParentID string `json:"parent_id"`
		ChildID  string `json:"child_id"`
	}
	// Escalation is the data of ticket.escalated / ticket.deescalated as
	// written (TierChange with nullable teams and the cleared assignee).
	Escalation struct {
		FromTier   int     `json:"from_tier"`
		ToTier     int     `json:"to_tier"`
		FromTeam   *string `json:"from_team"`
		ToTeam     *string `json:"to_team"`
		Reason     string  `json:"reason"`
		Unassigned string  `json:"unassigned,omitempty"`
	}
	// WatchersChange is the data of ticket.watchers_changed: a user id, or a
	// list of ids when inbound email adds CCs.
	WatchersChange struct {
		Added   any `json:"added,omitempty"`
		Removed any `json:"removed,omitempty"`
	}
	// Deleted is the data of ticket.deleted (hard delete, written in the purge tx).
	Deleted struct{}
	// AutomationApplied is the data of ticket.automation_applied: one rule
	// that applied to the ticket, with the outcome of each of its actions.
	AutomationApplied struct {
		RuleID        string             `json:"rule_id"`
		RuleName      string             `json:"rule_name"`
		Trigger       string             `json:"trigger"`
		SourceEventID string             `json:"source_event_id,omitempty"`
		Actions       []AutomationAction `json:"actions"`
		Truncated     bool               `json:"truncated,omitempty"`
	}
	// AutomationAction is the outcome of one automation action.
	AutomationAction struct {
		Type    string `json:"type"`
		Outcome string `json:"outcome"` // applied | unchanged | skipped
		Detail  string `json:"detail,omitempty"`
	}
)

// newData maps every stored event type to its data struct (events_spec §1).
// Assignment keeps the `from`/`to` keys every consumer decodes, where the
// spec table names them from_user/to_user (documented deviation, CON-009).
var newData = map[string]func() any{
	TicketCreated:           func() any { return &Created{} },
	TicketStatusChanged:     func() any { return &StatusChange{} },
	TicketPriorityChanged:   func() any { return &Change{} },
	TicketAssigned:          func() any { return &Assignment{} },
	TicketTeamChanged:       func() any { return &Change{} },
	TicketEscalated:         func() any { return &Escalation{} },
	TicketDeescalated:       func() any { return &Escalation{} },
	TicketCategoryChanged:   func() any { return &Change{} },
	TicketTagsChanged:       func() any { return &Change{} },
	TicketFieldsChanged:     func() any { return &FieldsChange{} },
	TicketSubjectChanged:    func() any { return &Change{} },
	MessageCreated:          func() any { return &MessageRef{} },
	NoteCreated:             func() any { return &NoteRef{} },
	MessageEdited:           func() any { return &MessageRef{} },
	SLAWarning:              func() any { return &SLA{} },
	SLABreached:             func() any { return &SLA{} },
	TicketCSAT:              func() any { return &CSAT{} },
	TicketFollowupCreated:   func() any { return &Followup{} },
	TicketMerged:            func() any { return &Merge{} },
	TicketSplit:             func() any { return &Split{} },
	TicketLinked:            func() any { return &Link{} },
	TicketUnlinked:          func() any { return &Link{} },
	TicketChildrenResolved:  func() any { return &ChildrenResolved{} },
	TicketWatchersChanged:   func() any { return &WatchersChange{} },
	TicketDeleted:           func() any { return &Deleted{} },
	TicketAutomationApplied: func() any { return &AutomationApplied{} },
}

// NewData returns a pointer to a zero data struct for typ, ready for
// Event.Decode, or nil for an unknown type.
func NewData(typ string) any {
	if f, ok := newData[typ]; ok {
		return f()
	}
	return nil
}

// advisoryLockKey serialises event writers on Postgres so commit order equals
// ID order and the dispatcher cursor never skips a late-committing event.
// SQLite already serialises writers (single writer, BEGIN IMMEDIATE).
const advisoryLockKey = 7_234_001

// Write inserts the event inside tx. ID and CreatedAt are assigned here, after
// the ordering lock, so IDs are monotonic in commit order on both engines.
// data may be nil, a json.RawMessage, or any value to marshal.
func Write(ctx context.Context, tx *store.Tx, e *Event, data any) error {
	if e.TicketID == "" || e.Type == "" {
		return errors.New("events: ticket_id and type are required")
	}
	if tx.Dialect() == store.Postgres {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, advisoryLockKey); err != nil {
			return fmt.Errorf("events: lock: %w", err)
		}
	}
	if data != nil {
		switch d := data.(type) {
		case json.RawMessage:
			e.Data = d
		default:
			b, err := json.Marshal(d)
			if err != nil {
				return err
			}
			e.Data = b
		}
	}
	snap, err := json.Marshal(e.Snapshot)
	if err != nil {
		return err
	}
	e.ID = store.NewID()
	e.CreatedAt = store.Now()
	dataStr := "{}"
	if len(e.Data) > 0 {
		dataStr = string(e.Data)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ticket_events (id, ticket_id, actor_id, type, data, snapshot, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TicketID, nullable(e.ActorID), e.Type, dataStr, string(snap), e.CreatedAt)
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const selectCols = `id, ticket_id, actor_id, type, data, snapshot, created_at`

// ReadAfter returns up to limit events with id > cursor, oldest first.
func ReadAfter(ctx context.Context, q store.Querier, cursor string, limit int) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+selectCols+` FROM ticket_events WHERE id > ? ORDER BY id LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

// ForTicket returns the timeline of one ticket, oldest first.
func ForTicket(ctx context.Context, q store.Querier, ticketID string) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+selectCols+` FROM ticket_events WHERE ticket_id = ? ORDER BY id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows)
}

func scanAll(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var actor, data sql.NullString
		var snap string
		if err := rows.Scan(&e.ID, &e.TicketID, &actor, &e.Type, &data, &snap, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ActorID = actor.String
		if data.Valid && data.String != "" && data.String != "{}" {
			e.Data = json.RawMessage(data.String)
		}
		if err := json.Unmarshal([]byte(snap), &e.Snapshot); err != nil {
			return nil, fmt.Errorf("events: snapshot of %s: %w", e.ID, err)
		}
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
