// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Service is the ticket domain service. Every mutation runs in one write
// transaction that changes the domain rows and writes its ticket_events rows
// through events.Write, and nothing else (D-11, TKT-009).
type Service struct {
	db   *store.DB
	dir  *directory.Directory
	set  *settings.Service
	blob blob.Store
	hub  *realtime.Hub
	box  *crypto.Box
	log  *slog.Logger

	maxUpload int64
	now       func() time.Time
	sla       slaCache
	auto      autoState // automation rules cache + round-robin pointers (D-19 §A)
}

// Directory exposes the directory service.
func (s *Service) Directory() *directory.Directory { return s.dir }

// DB exposes the store (tests, other modules).
func (s *Service) DB() *store.DB { return s.db }

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now().UTC().Truncate(time.Microsecond)
	}
	return store.Now()
}

// Policy returns the permission-relevant support settings (cached in memory).
func (s *Service) Policy(ctx context.Context) authz.TicketPolicy {
	sup := s.dir.Support(ctx)
	return policyFrom(sup)
}

func policyFrom(sup settings.Support) authz.TicketPolicy {
	return authz.TicketPolicy{
		TriageVisibleToAgents:    sup.TriageVisibleToAgents,
		UrgentRequiresSupervisor: sup.UrgentRequiresSupervisor,
		CustomerUrgentAllowed:    sup.CustomerUrgentAllowed,
		ReopenWindow:             time.Duration(sup.ReopenWindowDays) * 24 * time.Hour,
	}
}

// emit writes one ticket_events row for t (its state after the change) in tx.
func (s *Service) emit(ctx context.Context, tx *store.Tx, t *ticketRow, actorID, typ string, data any) error {
	e := &events.Event{TicketID: t.ID, ActorID: actorID, Type: typ, Snapshot: t.snapshot()}
	return events.Write(ctx, tx, e, data)
}

// userRef is the UserRef schema.
type userRef struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	AvatarURL *string `json:"avatar_url,omitempty"`
}

// loadUserRefs fetches names for a set of user IDs in one query.
func loadUserRefs(ctx context.Context, q store.Querier, ids []string) (map[string]userRef, error) {
	out := map[string]userRef{}
	uniq := make([]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	for len(uniq) > 0 {
		n := min(len(uniq), 500)
		batch := uniq[:n]
		uniq = uniq[n:]
		rows, err := q.QueryContext(ctx, `SELECT id, name, kind FROM users WHERE id IN (`+placeholders(len(batch))+`)`, batch...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r userRef
			if err := rows.Scan(&r.ID, &r.Name, &r.Kind); err != nil {
				rows.Close()
				return nil, err
			}
			out[r.ID] = r
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// warm loads every cache a mutation consults (support settings, directory
// snapshot, SLA policies) BEFORE its write transaction starts. On Postgres the
// reader and writer share one pool: a cache miss inside a transaction would
// need a second connection and could exhaust the pool under load.
func (s *Service) warm(ctx context.Context) error {
	if s.set != nil {
		if _, err := s.set.Support(ctx); err != nil {
			return err
		}
	}
	if _, err := s.dir.Teams(ctx); err != nil {
		return err
	}
	_, err := s.slaPolicies(ctx)
	return err
}
