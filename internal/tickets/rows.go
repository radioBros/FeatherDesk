// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ticketCols is the column list every ticket read uses (alias t).
const ticketCols = `t.id, t.number, t.subject, t.status, t.priority, t.tier, t.team_id, t.category_id, t.assignee_id,
	t.requester_id, t.organization_id, t.channel, t.first_response_due_at, t.resolution_due_at, t.first_responded_at,
	t.resolved_at, t.closed_at, t.sla_paused_at, t.sla_paused_minutes, t.sla_state, t.last_customer_activity_at,
	t.last_staff_activity_at, t.followup_of_id, t.csat_score, t.csat_comment, t.created_at, t.updated_at`

// ticketRow is one tickets row plus its watchers.
type ticketRow struct {
	ID                     string
	Number                 int64
	Subject                string
	Status                 domain.TicketStatus
	Priority               domain.Priority
	Tier                   int
	TeamID                 string
	CategoryID             string
	AssigneeID             string
	RequesterID            string
	OrganizationID         string
	Channel                domain.Channel
	FirstResponseDueAt     sql.NullTime
	ResolutionDueAt        sql.NullTime
	FirstRespondedAt       sql.NullTime
	ResolvedAt             sql.NullTime
	ClosedAt               sql.NullTime
	SLAPausedAt            sql.NullTime
	SLAPausedMinutes       int
	SLAState               map[string]string
	LastCustomerActivityAt time.Time
	LastStaffActivityAt    sql.NullTime
	FollowupOfID           string
	CSATScore              int
	CSATComment            string
	CreatedAt              time.Time
	UpdatedAt              time.Time

	Watchers []string // staff and customer watchers (V1)
}

type scanner interface{ Scan(...any) error }

func scanTicket(r scanner) (*ticketRow, error) {
	var t ticketRow
	var team, cat, asg, org, follow, comment sql.NullString
	var csat sql.NullInt64
	var state store.JSON[map[string]string]
	err := r.Scan(&t.ID, &t.Number, &t.Subject, &t.Status, &t.Priority, &t.Tier, &team, &cat, &asg,
		&t.RequesterID, &org, &t.Channel, &t.FirstResponseDueAt, &t.ResolutionDueAt, &t.FirstRespondedAt,
		&t.ResolvedAt, &t.ClosedAt, &t.SLAPausedAt, &t.SLAPausedMinutes, &state, &t.LastCustomerActivityAt,
		&t.LastStaffActivityAt, &follow, &csat, &comment, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, store.NotFound(err)
	}
	t.TeamID, t.CategoryID, t.AssigneeID, t.OrganizationID, t.FollowupOfID = team.String, cat.String, asg.String, org.String, follow.String
	t.CSATScore, t.CSATComment = int(csat.Int64), comment.String
	t.SLAState = state.V
	if t.SLAState == nil {
		t.SLAState = map[string]string{}
	}
	t.CreatedAt, t.UpdatedAt, t.LastCustomerActivityAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC(), t.LastCustomerActivityAt.UTC()
	for _, nt := range []*sql.NullTime{&t.FirstResponseDueAt, &t.ResolutionDueAt, &t.FirstRespondedAt, &t.ResolvedAt, &t.ClosedAt, &t.SLAPausedAt, &t.LastStaffActivityAt} {
		if nt.Valid {
			nt.Time = nt.Time.UTC()
		}
	}
	return &t, nil
}

// view builds the authz view of the ticket.
func (t *ticketRow) view(pol authz.TicketPolicy, now time.Time) authz.TicketView {
	v := authz.TicketView{
		ID: t.ID, Status: t.Status, Priority: t.Priority, Tier: t.Tier, TeamID: t.TeamID, AssigneeID: t.AssigneeID,
		RequesterID: t.RequesterID, OrganizationID: t.OrganizationID, Watchers: t.Watchers, CSATScore: t.CSATScore,
		Policy: pol, Now: now,
	}
	if t.ResolvedAt.Valid {
		v.ResolvedAt = t.ResolvedAt.Time
	}
	return v
}

// snapshot is the visibility snapshot written with every event (TKT-009).
func (t *ticketRow) snapshot() events.Snapshot {
	return events.Snapshot{
		Number: t.Number, Subject: t.Subject, Status: string(t.Status), Priority: string(t.Priority), Tier: t.Tier,
		TeamID: t.TeamID, CategoryID: t.CategoryID, AssigneeID: t.AssigneeID, RequesterID: t.RequesterID,
		OrganizationID: t.OrganizationID, Watchers: append([]string(nil), t.Watchers...), Channel: string(t.Channel),
	}
}

// etag is the weak validator of the ticket (TKT-030).
func (t *ticketRow) etag() string {
	return `W/"` + itoa64(t.UpdatedAt.UnixNano()) + `"`
}

func itoa64(n int64) string {
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// open reports whether the ticket is in a working (non-final) state.
func (t *ticketRow) openish() bool {
	return t.Status != domain.StatusResolved && t.Status != domain.StatusClosed
}

// slaBreached mirrors breachedSQL for one row.
func (t *ticketRow) slaBreached(now time.Time) bool {
	if !t.openish() || t.SLAPausedAt.Valid {
		return false
	}
	if !t.FirstRespondedAt.Valid && t.FirstResponseDueAt.Valid && t.FirstResponseDueAt.Time.Before(now) {
		return true
	}
	return t.ResolutionDueAt.Valid && t.ResolutionDueAt.Time.Before(now)
}

// loadWatchers fills t.Watchers from q.
func loadWatchers(ctx context.Context, q store.Querier, t *ticketRow) error {
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM ticket_watchers WHERE ticket_id = ? ORDER BY user_id`, t.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	t.Watchers = t.Watchers[:0]
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		t.Watchers = append(t.Watchers, id)
	}
	return rows.Err()
}

// forUpdate is the row-lock suffix of a single-ticket read inside a write tx.
// SQLite has one writer (BEGIN IMMEDIATE), so it needs none.
func forUpdate(d store.Dialect) string {
	if d == store.Postgres {
		return " FOR UPDATE OF t"
	}
	return ""
}

// lockTicket reads a ticket inside a write tx, with its watchers.
func lockTicket(ctx context.Context, tx *store.Tx, id string) (*ticketRow, error) {
	if !validID(id) {
		return nil, store.ErrNotFound
	}
	t, err := scanTicket(tx.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`+forUpdate(tx.Dialect()), id))
	if err != nil {
		return nil, err
	}
	return t, loadWatchers(ctx, tx, t)
}

// validID accepts canonical UUID text (Postgres would reject anything else
// with a type error only on uuid columns; we store TEXT, so validate here).
func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// priorityRankSQL orders priorities (urgent highest).
const priorityRankSQL = `CASE t.priority WHEN 'urgent' THEN 4 WHEN 'high' THEN 3 WHEN 'normal' THEN 2 ELSE 1 END`

// openishSQL matches tickets in a working state.
const openishSQL = `t.status NOT IN ('resolved', 'closed')`

// breachedSQL: an open, unpaused ticket past a due date (one bound arg: now).
const breachedSQL = `(` + openishSQL + ` AND t.sla_paused_at IS NULL AND ((t.first_responded_at IS NULL AND t.first_response_due_at < ?) OR t.resolution_due_at < ?))`
