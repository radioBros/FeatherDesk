// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Merge and split (TKT-034, ticket_lifecycle_spec §2b).

// Merge moves source A into target B: messages, attachments and watchers are
// re-parented (original ids and timestamps kept), A becomes closed with a
// duplicate_of link to B, a notice is written on both and ticket.merged is
// emitted on both. Different requesters must share an organization (A's
// requester becomes a CC of B); otherwise 409 merge-cross-requester.
func (s *Service) Merge(ctx context.Context, p *domain.Principal, sourceID, targetID string) (Outcome, error) {
	if sourceID == targetID {
		return Outcome{}, problems.Validation(problems.Field("into_ticket_id", "self", "a ticket cannot be merged into itself"))
	}
	if err := s.warm(ctx); err != nil {
		return Outcome{}, err
	}
	pol := s.Policy(ctx)
	var out Outcome
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		a, b, err := lockPair(ctx, tx, sourceID, targetID)
		if errors.Is(err, store.ErrNotFound) {
			return problems.NotFound()
		}
		if err != nil {
			return err
		}
		now := s.clock()
		if err := authz.Can(p, authz.ActMerge, a.view(pol, now)); err != nil {
			return err
		}
		if !authz.CanSeeTicket(p, b.view(pol, now)) {
			return problems.NotFound()
		}
		if b.Status == domain.StatusClosed {
			return problems.Conflict("cannot merge into a closed ticket")
		}
		addCC := ""
		if a.RequesterID != b.RequesterID {
			if a.OrganizationID == "" || a.OrganizationID != b.OrganizationID {
				return problems.New(problems.SlugMergeCrossRequester, "the tickets belong to different customers")
			}
			addCC = a.RequesterID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_messages SET ticket_id = ?, moved_from_ticket_id = ? WHERE ticket_id = ?`, b.ID, a.ID, a.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET ticket_id = ? WHERE ticket_id = ?`, b.ID, a.ID); err != nil {
			return err
		}
		// Watchers move to B (customer CCs only when they belong to B's organization).
		watchers := append([]string(nil), a.Watchers...)
		if addCC != "" {
			watchers = append(watchers, addCC)
		}
		for _, w := range watchers {
			var kind, org string
			if err := tx.QueryRowContext(ctx, `SELECT kind, COALESCE(organization_id, '') FROM users WHERE id = ?`, w).Scan(&kind, &org); err != nil {
				return err
			}
			if kind == string(domain.KindCustomer) && org != b.OrganizationID {
				continue
			}
			if w == b.RequesterID || containsStr(b.Watchers, w) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, b.ID, w, now); err != nil {
				return err
			}
			b.Watchers = append(b.Watchers, w)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_watchers WHERE ticket_id = ?`, a.ID); err != nil {
			return err
		}
		a.Watchers = nil
		// A: closed (through the status machine, as the merging staff member).
		from := a.Status
		if a.Status != domain.StatusResolved {
			if _, err := s.applyStatus(ctx, tx, a, actorSupervisor, domain.StatusResolved, statusOpts{skipFieldCheck: true}); err != nil {
				return err
			}
		}
		if _, err := s.applyStatus(ctx, tx, a, actorSupervisor, domain.StatusClosed, statusOpts{}); err != nil {
			return err
		}
		if _, err := insertLink(ctx, tx, a.ID, b.ID, domain.LinkDuplicateOf, p.UserID, now); err != nil {
			return err
		}
		// The customer of A learns where the conversation continues; B's
		// staff see where the thread came from.
		if err := insertMessage(ctx, tx, &msgRow{TicketID: a.ID, Kind: domain.MessagePublic, Channel: domain.ChannelAgent,
			Body: fmt.Sprintf("This request was merged into #%d.", b.Number), CreatedAt: now}); err != nil {
			return err
		}
		if err := insertMessage(ctx, tx, &msgRow{TicketID: b.ID, AuthorID: p.UserID, Kind: domain.MessageSystem, Channel: domain.ChannelAgent,
			Body: fmt.Sprintf("Ticket #%d (%s) was merged into this ticket.", a.Number, a.Subject), CreatedAt: now}); err != nil {
			return err
		}
		data := events.Merge{SourceID: a.ID, TargetID: b.ID}
		ma := &mutation{t: a}
		ma.add(events.TicketStatusChanged, statusEvent(from, domain.StatusClosed))
		ma.add(events.TicketMerged, data)
		if err := s.flush(ctx, tx, ma, p.UserID); err != nil {
			return err
		}
		mb := &mutation{t: b}
		mb.add(events.TicketMerged, data)
		if err := s.flush(ctx, tx, mb, p.UserID); err != nil {
			return err
		}
		out = Outcome{TicketID: b.ID}
		return nil
	})
	return out, err
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SplitInput is SplitRequest.
type SplitInput struct {
	MessageIDs []string `json:"message_ids"`
	Subject    string   `json:"subject"`
}

// Split copies the selected messages (bodies and attachment rows sharing the
// same blobs) into a new child ticket: same requester, organization and
// team, tier 1, a parent_of link from the source; the originals are marked
// "split to #n" and stay in place. Internal notes keep their kind (I-2).
func (s *Service) Split(ctx context.Context, p *domain.Principal, id string, in SplitInput) (string, error) {
	subject, err := validSubject(in.Subject)
	if err != nil {
		return "", err
	}
	if len(in.MessageIDs) == 0 || len(in.MessageIDs) > 200 {
		return "", problems.Validation(problems.Field("message_ids", "length", "1..200 messages"))
	}
	if err := s.warm(ctx); err != nil {
		return "", err
	}
	pol := s.Policy(ctx)
	var childID string
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		src, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authz.Can(p, authz.ActSplit, src.view(pol, s.clock())); err != nil {
			return err
		}
		uniq := map[string]bool{}
		var ids []string
		for _, m := range in.MessageIDs {
			if !uniq[m] {
				uniq[m] = true
				ids = append(ids, m)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, author_id, kind, channel, body_md, created_at, via_token_id FROM ticket_messages
			WHERE ticket_id = ? AND id IN (`+placeholders(len(ids))+`) ORDER BY created_at, id`, append([]any{src.ID}, anyStrings(ids)...)...)
		if err != nil {
			return err
		}
		var msgs []msgRow
		for rows.Next() {
			var m msgRow
			var author, via store.NullString
			if err := rows.Scan(&m.ID, &author, &m.Kind, &m.Channel, &m.Body, &m.CreatedAt, &via); err != nil {
				rows.Close()
				return err
			}
			m.AuthorID, m.ViaTokenID = author.String, via.String
			msgs = append(msgs, m)
		}
		rows.Close()
		if len(msgs) != len(ids) {
			return problems.Validation(problems.Field("message_ids", "not_found", "every message must belong to the ticket"))
		}
		s.fillKey(ctx, src)
		child, err := s.createTx(ctx, tx, p, CreateInput{Subject: subject, BodyMD: fmt.Sprintf("Split from %s.", src.Ref()),
			RequesterID: &src.RequesterID, channel: src.Channel, fixedTeam: &src.TeamID, fixedProject: src.ProjectID})
		if err != nil {
			return err
		}
		childID = child.ID
		for _, m := range msgs {
			copyID := store.NewID()
			if err := insertMessage(ctx, tx, &msgRow{ID: copyID, TicketID: child.ID, AuthorID: m.AuthorID, Kind: m.Kind, Channel: m.Channel,
				Body: m.Body, ViaTokenID: m.ViaTokenID, CreatedAt: m.CreatedAt}); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO attachments (id, ticket_id, message_id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at)
				SELECT `+uuidExprs(tx.Dialect())+`, ?, ?, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at FROM attachments WHERE message_id = ?`,
				child.ID, copyID, m.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ticket_messages SET split_to_ticket_id = ? WHERE id = ?`, child.ID, m.ID); err != nil {
				return err
			}
		}
		if _, err := insertLink(ctx, tx, src.ID, child.ID, domain.LinkParentOf, p.UserID, s.clock()); err != nil {
			return err
		}
		if err := insertMessage(ctx, tx, &msgRow{TicketID: src.ID, AuthorID: p.UserID, Kind: domain.MessageSystem, Channel: domain.ChannelAgent,
			Body: fmt.Sprintf("Split to #%d: %d message(s).", child.Number, len(msgs)), CreatedAt: s.clock()}); err != nil {
			return err
		}
		m := &mutation{t: src}
		m.add(events.TicketSplit, events.Split{ParentID: src.ID, ChildID: child.ID, MessageIDs: ids})
		return s.flush(ctx, tx, m, p.UserID)
	})
	return childID, err
}

// uuidExprs generates a fresh text id in SQL for row copies. SQLite: a
// random 32-hex string formatted as a UUID; Postgres: gen_random_uuid().
func uuidExprs(d store.Dialect) string {
	if d == store.Postgres {
		return "CAST(gen_random_uuid() AS TEXT)"
	}
	return `lower(substr(hex(randomblob(4)),1,8) || '-' || substr(hex(randomblob(2)),1,4) || '-' || substr(hex(randomblob(2)),1,4) || '-' || substr(hex(randomblob(2)),1,4) || '-' || substr(hex(randomblob(6)),1,12))`
}

func (m *Module) agentMerge(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in struct {
		IntoTicketID string `json:"into_ticket_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	out, err := m.svc.Merge(r.Context(), principal(r), urlParam(r, "id"), strings.TrimSpace(in.IntoTicketID))
	if err != nil {
		return err
	}
	return m.writeUpdate(w, r, out)
}

func (m *Module) agentSplit(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in SplitInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	id, err := m.svc.Split(r.Context(), p, urlParam(r, "id"), in)
	if err != nil {
		return err
	}
	res, err := m.svc.UpdateResult(r.Context(), p, id, nil)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, res.Ticket)
	return nil
}
