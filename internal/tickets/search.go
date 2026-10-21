// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/cli"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Full-text search (TKT-014). The index is written asynchronously by the
// non-durable `index` queue consumer from dispatched events, never inside a
// ticket write transaction. Public text (subject, public messages,
// customer-visible field values) and staff text (notes, system messages,
// staff-only fields) are separate, so customer search can never match a word
// that only appears in staff text (I-2, I-9).

// indexEvents are the events that change indexed text.
var indexEvents = map[string]bool{
	events.TicketCreated: true, events.MessageCreated: true, events.NoteCreated: true, events.MessageEdited: true,
	events.TicketSubjectChanged: true, events.TicketFieldsChanged: true, events.TicketMerged: true, events.TicketSplit: true,
	events.TicketEscalated: true, events.TicketDeescalated: true, events.TicketCSAT: true,
}

func (m *Module) registerIndex(q *queue.Queue) {
	q.Subscribe("index", queue.SubscribeOptions{Workers: 1, Capacity: 1024,
		OnOverflow: func(e events.Event) {
			m.svc.log.Warn("index queue full; run `simpletickets reindex`", "ticket", e.TicketID)
		}},
		func(e events.Event) bool { return indexEvents[e.Type] },
		func(ctx context.Context, e events.Event) error { return m.svc.IndexOne(ctx, e.TicketID) })
}

// buildDoc reads the indexable text of one ticket.
func buildDoc(ctx context.Context, q store.Querier, dir *directory.Directory, id string) (store.TicketDoc, error) {
	doc := store.TicketDoc{TicketID: id}
	var subject string
	if err := q.QueryRowContext(ctx, `SELECT subject FROM tickets WHERE id = ?`, id).Scan(&subject); err != nil {
		return doc, store.NotFound(err)
	}
	var pub, internal, staffFields []string
	pub = append(pub, subject)
	rows, err := q.QueryContext(ctx, `SELECT kind, body_md FROM ticket_messages WHERE ticket_id = ? ORDER BY created_at, id`, id)
	if err != nil {
		return doc, err
	}
	for rows.Next() {
		var kind, body string
		if err := rows.Scan(&kind, &body); err != nil {
			rows.Close()
			return doc, err
		}
		if kind == "public" {
			pub = append(pub, body)
		} else {
			internal = append(internal, body)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return doc, err
	}
	vals, err := loadFieldValues(ctx, q, []string{id})
	if err != nil {
		return doc, err
	}
	for fid, v := range vals[id] {
		def, ok := dir.FieldByID(ctx, fid)
		if !ok {
			continue
		}
		if txt := def.SearchText(v); txt != "" {
			if def.CustomerVisible {
				pub = append(pub, txt)
			} else {
				staffFields = append(staffFields, txt)
			}
		}
	}
	doc.Public, doc.Internal, doc.StaffFields = strings.Join(pub, "\n"), strings.Join(internal, "\n"), strings.Join(staffFields, "\n")
	return doc, nil
}

// IndexOne rebuilds the index entry of one ticket (removes it when the
// ticket no longer exists).
func (s *Service) IndexOne(ctx context.Context, id string) error {
	if err := s.warm(ctx); err != nil {
		return err
	}
	doc, err := buildDoc(ctx, s.db.Read(), s.dir, id)
	if errors.Is(err, store.ErrNotFound) {
		return s.db.Tx(ctx, func(tx *store.Tx) error { return store.UnindexTicket(ctx, tx, id) })
	}
	if err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error { return store.IndexTicket(ctx, tx, doc) })
}

// Reindex rebuilds the whole index in batches of 200 tickets per write
// transaction (`simpletickets reindex`).
func Reindex(ctx context.Context, db *store.DB, progress func(n int)) (int, error) {
	dir := directory.NewDirectory(directory.DirDeps{DB: db})
	after, total := "", 0
	for {
		rows, err := db.Read().QueryContext(ctx, `SELECT id FROM tickets WHERE id > ? ORDER BY id LIMIT 200`, after)
		if err != nil {
			return total, err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return total, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			return total, nil
		}
		docs := make([]store.TicketDoc, 0, len(ids))
		for _, id := range ids {
			doc, err := buildDoc(ctx, db.Read(), dir, id)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return total, err
			}
			if err == nil {
				docs = append(docs, doc)
			}
		}
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			for _, d := range docs {
				if err := store.IndexTicket(ctx, tx, d); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return total, err
		}
		total += len(docs)
		after = ids[len(ids)-1]
		if progress != nil {
			progress(total)
		}
	}
}

func init() {
	cli.Register(cli.Command{Name: "reindex", Summary: "Rebuild the ticket search index from scratch", Run: func(ctx context.Context, args []string) (err error) {
		fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
		if err := fs.Parse(args); err != nil {
			return err
		}
		cfg, err := config.Load(os.Getenv)
		if err != nil {
			return err
		}
		db, err := store.Open(ctx, cfg.EffectiveDatabaseURL(), store.Options{})
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, db.Close()) }()
		n, err := Reindex(ctx, db, nil)
		if err == nil {
			fmt.Printf("reindexed %d tickets\n", n)
		}
		return err
	}})
}
