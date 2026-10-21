// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Ticket links (TKT-038, M-8): parent_of (from = parent, to = child),
// related_to (stored once, shown on both sides) and duplicate_of (source must
// be closed). At most one parent per ticket and no cycles.

// LinkInput is LinkCreate.
type LinkInput struct {
	ToTicketID string `json:"to_ticket_id"`
	Type       string `json:"type"`
}

// maxLinkDepth caps the cycle walk (M-8).
const maxLinkDepth = 50

// CreateLink links ticket id to in.ToTicketID. The actor must see both
// tickets (an invisible target is a 404).
func (s *Service) CreateLink(ctx context.Context, p *domain.Principal, id string, in LinkInput) (string, error) {
	typ := domain.LinkType(in.Type)
	switch typ {
	case domain.LinkParentOf, domain.LinkRelatedTo, domain.LinkDuplicateOf:
	default:
		return "", problems.Validation(problems.Field("type", "enum", "parent_of, related_to or duplicate_of"))
	}
	if in.ToTicketID == id {
		return "", problems.Validation(problems.Field("to_ticket_id", "self", "a ticket cannot link to itself"))
	}
	pol := s.Policy(ctx)
	var linkID string
	if err := s.warm(ctx); err != nil {
		return "", err
	}
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		from, to, err := lockPair(ctx, tx, id, in.ToTicketID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return problems.NotFound()
			}
			return err
		}
		now := s.clock()
		if err := authz.Can(p, authz.ActLink, from.view(pol, now)); err != nil {
			return err
		}
		if !authz.CanSeeTicket(p, to.view(pol, now)) {
			return problems.NotFound()
		}
		if err := checkLink(ctx, tx, from, to, typ); err != nil {
			return err
		}
		linkID, err = insertLink(ctx, tx, from.ID, to.ID, typ, p.UserID, now)
		if err != nil {
			return err
		}
		if err := s.emit(ctx, tx, from, p.UserID, events.TicketLinked, events.Link{ToTicketID: to.ID, Type: string(typ)}); err != nil {
			return err
		}
		return s.emit(ctx, tx, to, p.UserID, events.TicketLinked, events.Link{ToTicketID: from.ID, Type: string(typ)})
	})
	return linkID, err
}

// checkLink enforces M-8 for a new link from → to.
func checkLink(ctx context.Context, tx *store.Tx, from, to *ticketRow, typ domain.LinkType) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_links WHERE (from_ticket_id = ? AND to_ticket_id = ? AND type = ?)
		OR (from_ticket_id = ? AND to_ticket_id = ? AND type = 'related_to' AND ? = 'related_to')`,
		from.ID, to.ID, string(typ), to.ID, from.ID, string(typ)).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return problems.Conflict("the tickets are already linked")
	}
	switch typ {
	case domain.LinkDuplicateOf:
		if from.Status != domain.StatusClosed {
			return problems.Conflict("a duplicate_of source must be closed")
		}
	case domain.LinkParentOf:
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_links WHERE to_ticket_id = ? AND type = 'parent_of'`, to.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("the ticket already has a parent")
		}
	}
	if typ == domain.LinkParentOf || typ == domain.LinkDuplicateOf {
		// A cycle exists when `to` already reaches `from` along links of the same type.
		cyc, err := reaches(ctx, tx, to.ID, from.ID, typ)
		if err != nil {
			return err
		}
		if cyc {
			return problems.New(problems.SlugLinkCycle, "")
		}
	}
	return nil
}

// reaches walks same-type links from `start` (depth ≤ 50) looking for target.
func reaches(ctx context.Context, q store.Querier, start, target string, typ domain.LinkType) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `WITH RECURSIVE walk(id, depth) AS (
			SELECT to_ticket_id, 1 FROM ticket_links WHERE from_ticket_id = ? AND type = ?
			UNION
			SELECT l.to_ticket_id, w.depth + 1 FROM ticket_links l JOIN walk w ON l.from_ticket_id = w.id
			WHERE l.type = ? AND w.depth < ?
		) SELECT COUNT(*) FROM walk WHERE id = ?`, start, string(typ), string(typ), maxLinkDepth, target).Scan(&n)
	return n > 0, err
}

func insertLink(ctx context.Context, tx *store.Tx, from, to string, typ domain.LinkType, actor string, now time.Time) (string, error) {
	id := store.NewID()
	_, err := tx.ExecContext(ctx, `INSERT INTO ticket_links (id, from_ticket_id, to_ticket_id, type, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, from, to, string(typ), store.Nullable(actor), now)
	if store.IsUniqueViolation(err) {
		return "", problems.Conflict("the tickets are already linked, or the child already has a parent")
	}
	return id, err
}

// DeleteLink removes a link that involves ticket id.
func (s *Service) DeleteLink(ctx context.Context, p *domain.Principal, id, linkID string) error {
	pol := s.Policy(ctx)
	if err := s.warm(ctx); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		var from, to, typ string
		err := tx.QueryRowContext(ctx, `SELECT from_ticket_id, to_ticket_id, type FROM ticket_links WHERE id = ? AND (from_ticket_id = ? OR to_ticket_id = ?)`, linkID, id, id).Scan(&from, &to, &typ)
		if err != nil {
			if !validID(id) || errors.Is(store.NotFound(err), store.ErrNotFound) {
				return problems.NotFound()
			}
			return err
		}
		other := to
		if other == id {
			other = from
		}
		t, o, err := lockPair(ctx, tx, id, other)
		if err != nil {
			return err
		}
		now := s.clock()
		if err := authz.Can(p, authz.ActLink, t.view(pol, now)); err != nil {
			return err
		}
		if !authz.CanSeeTicket(p, o.view(pol, now)) {
			return problems.NotFound()
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_links WHERE id = ?`, linkID); err != nil {
			return err
		}
		if err := s.emit(ctx, tx, t, p.UserID, events.TicketUnlinked, events.Link{ToTicketID: o.ID, Type: typ}); err != nil {
			return err
		}
		return s.emit(ctx, tx, o, p.UserID, events.TicketUnlinked, events.Link{ToTicketID: t.ID, Type: typ})
	})
}

// listLinks returns the links of a ticket with refs of the tickets p can see
// (invisible tickets: number only, empty subject — staff view, TKT-038).
func (s *Service) listLinks(ctx context.Context, q store.Querier, p *domain.Principal, id string) ([]linkDTO, error) {
	rows, err := q.QueryContext(ctx, `SELECT l.id, l.from_ticket_id, l.to_ticket_id, l.type, l.created_at FROM ticket_links l
		WHERE l.from_ticket_id = ? OR l.to_ticket_id = ? ORDER BY l.created_at, l.id`, id, id)
	if err != nil {
		return nil, err
	}
	var out []linkDTO
	var others []string
	for rows.Next() {
		var l linkDTO
		if err := rows.Scan(&l.ID, &l.FromTicketID, &l.ToTicketID, &l.Type, &l.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		l.CreatedAt = l.CreatedAt.UTC()
		out = append(out, l)
		others = append(others, l.FromTicketID, l.ToTicketID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		return []linkDTO{}, nil
	}
	refs, err := s.ticketRefs(ctx, q, p, others)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if r, ok := refs[out[i].ToTicketID]; ok {
			r := r
			out[i].ToTicket = &r
		}
	}
	return out, nil
}

// ticketRefs builds TicketRefs; tickets p cannot see keep only their number.
func (s *Service) ticketRefs(ctx context.Context, q store.Querier, p *domain.Principal, ids []string) (map[string]ticketRefDTO, error) {
	out := map[string]ticketRefDTO{}
	if len(ids) == 0 {
		return out, nil
	}
	pol := s.Policy(ctx)
	pred, pargs := authz.TicketPredicate(p, pol, "t")
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.number, t.subject, t.status, CASE WHEN `+pred+` THEN 1 ELSE 0 END,
		(SELECT pj.key FROM projects pj WHERE pj.id = t.project_id), t.project_seq FROM tickets t WHERE t.id IN (`+placeholders(len(ids))+`)`,
		append(pargs, anyStrings(ids)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ticketRefDTO
		var vis int
		var key string
		var seq int64
		if err := rows.Scan(&r.ID, &r.Number, &r.Subject, &r.Status, &vis, &key, &seq); err != nil {
			return nil, err
		}
		r.Ref = domain.TicketRef(key, seq)
		if vis == 0 {
			r.Subject = ""
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

// onResolved runs when a ticket becomes resolved: warn about open children
// (never blocks) and, when it was the last open child of a parent, emit
// ticket.children_resolved on the parent (lifecycle §2b).
func (s *Service) onResolved(ctx context.Context, tx *store.Tx, m *mutation, actor string) error {
	t := m.t
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_links l JOIN tickets c ON c.id = l.to_ticket_id
		WHERE l.from_ticket_id = ? AND l.type = 'parent_of' AND c.status NOT IN ('resolved', 'closed')`, t.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		m.warnings = append(m.warnings, "open_children")
	}
	var parentID string
	err := tx.QueryRowContext(ctx, `SELECT from_ticket_id FROM ticket_links WHERE to_ticket_id = ? AND type = 'parent_of'`, t.ID).Scan(&parentID)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_links l JOIN tickets c ON c.id = l.to_ticket_id
		WHERE l.from_ticket_id = ? AND l.type = 'parent_of' AND c.id <> ? AND c.status NOT IN ('resolved', 'closed')`, parentID, t.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	parent, err := lockTicket(ctx, tx, parentID)
	if err != nil {
		return err
	}
	return s.emit(ctx, tx, parent, actor, events.TicketChildrenResolved, map[string]any{"parent_id": parent.ID, "child_id": t.ID})
}
