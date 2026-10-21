// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// revalidateMembership is the directory's membership hook (TKT-017): after a
// member leaves a team, or a staff tier changes (teamID ""), every non-closed
// ticket they own that no longer satisfies M-4 is unassigned with an event.
//
// Lock order (Postgres): every affected ticket row is locked up front, in id
// order, before the first events.Write takes the event advisory lock; a row
// lock taken after that lock could deadlock with a single-ticket writer.
func (s *Service) revalidateMembership(ctx context.Context, tx *store.Tx, actor, teamID string, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	q := `SELECT ` + ticketCols + ` FROM tickets t WHERE t.status <> 'closed' AND t.assignee_id IN (` + placeholders(len(userIDs)) + `)`
	args := anyStrings(userIDs)
	if teamID != "" {
		q += ` AND t.team_id = ?`
		args = append(args, teamID)
	}
	tickets, err := lockTickets(ctx, tx, q+` ORDER BY t.id`, args...)
	if err != nil {
		return err
	}
	staff := map[string]*staffInfo{}
	for _, t := range tickets {
		st, ok := staff[t.AssigneeID]
		if !ok {
			info, err := loadStaff(ctx, tx, t.AssigneeID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err == nil {
				st = &info
			}
			staff[t.AssigneeID] = st
		}
		if st != nil && satisfiesM4(*st, t.TeamID, t.Tier) {
			continue
		}
		m := &mutation{t: t}
		prev := t.AssigneeID
		m.add(events.TicketAssigned, events.Assignment{From: t.AssigneeID})
		t.AssigneeID = ""
		if err := s.autoAssign(ctx, tx, m, prev); err != nil { // D-19 §A
			return err
		}
		if err := s.flush(ctx, tx, m, actor); err != nil {
			return err
		}
	}
	return nil
}

// lockTickets runs a ticket SELECT (ticketCols, ordered by id) with a row
// lock on Postgres and loads watchers. Use it to lock every ticket a
// transaction touches before its first events.Write.
func lockTickets(ctx context.Context, tx *store.Tx, q string, args ...any) ([]*ticketRow, error) {
	rows, err := tx.QueryContext(ctx, q+forUpdate(tx.Dialect()), args...)
	if err != nil {
		return nil, err
	}
	var out []*ticketRow
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range out {
		if err := loadWatchers(ctx, tx, t); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// lockPair locks two tickets in id order (merge, links) and returns them in
// argument order. store.ErrNotFound when either is missing.
func lockPair(ctx context.Context, tx *store.Tx, a, b string) (*ticketRow, *ticketRow, error) {
	if !validID(a) || !validID(b) {
		return nil, nil, store.ErrNotFound
	}
	ts, err := lockTickets(ctx, tx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id IN (?, ?) ORDER BY t.id`, a, b)
	if err != nil {
		return nil, nil, err
	}
	var ta, tb *ticketRow
	for _, t := range ts {
		if t.ID == a {
			ta = t
		}
		if t.ID == b {
			tb = t
		}
	}
	if ta == nil || tb == nil {
		return ta, tb, store.ErrNotFound
	}
	return ta, tb, nil
}
