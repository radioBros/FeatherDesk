// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Sender is the user an inbound From address resolves to.
type Sender struct {
	UserID         string
	Kind           string // staff | customer
	Role           string
	Status         string // active | pending_verification | disabled
	OrganizationID string
	Created        bool // created by this call (pending_verification customer)
}

// SenderResolver maps a From address to a user. When the address is unknown
// and create is true, it creates a customer as pending_verification (with
// domain → organization auto-join, same rule as AUTH-014) and returns it;
// when create is false it returns (nil, nil).
//
// authn.ResolveEmailSender does not exist yet; dbResolver is the built-in
// implementation and can be replaced with SetSenderResolver.
type SenderResolver interface {
	ResolveSender(ctx context.Context, addr, name string, create bool) (*Sender, error)
}

type dbResolver struct {
	db  *store.DB
	set *settings.Service
}

func (r *dbResolver) lookup(ctx context.Context, q store.Querier, addr string) (*Sender, error) {
	var s Sender
	var org sql.NullString
	err := q.QueryRowContext(ctx, `SELECT id, kind, role, status, organization_id FROM users WHERE lower(email) = ?`, addr).
		Scan(&s.UserID, &s.Kind, &s.Role, &s.Status, &org)
	if err != nil {
		return nil, store.NotFound(err)
	}
	s.OrganizationID = org.String
	return &s, nil
}

func (r *dbResolver) ResolveSender(ctx context.Context, addr, name string, create bool) (*Sender, error) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	s, err := r.lookup(ctx, r.db.Read(), addr)
	if err == nil {
		return s, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if !create {
		return nil, nil
	}
	locale := "en"
	if r.set != nil {
		if sup, err := r.set.Support(ctx); err == nil && sup.DefaultLocale != "" {
			locale = sup.DefaultLocale
		}
	}
	org, err := orgForDomain(ctx, r.db.Read(), addr)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name, _, _ = strings.Cut(addr, "@")
	}
	if len(name) > 120 {
		name = name[:120]
	}
	id := store.NewID()
	now := store.Now()
	err = r.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, organization_id, status, locale, created_at, updated_at) VALUES (?, 'customer', ?, ?, 'requester', ?, 'pending_verification', ?, ?, ?)`,
			id, addr, name, store.Nullable(org), locale, now, now)
		return err
	})
	if err != nil {
		if store.IsUniqueViolation(err) { // raced with another message
			return r.lookup(ctx, r.db.Read(), addr)
		}
		return nil, err
	}
	return &Sender{UserID: id, Kind: "customer", Role: "requester", Status: "pending_verification", OrganizationID: org, Created: true}, nil
}

// orgForDomain finds an active organization whose email_domains contain the
// address domain (D-03 auto-join).
func orgForDomain(ctx context.Context, q store.Querier, addr string) (string, error) {
	_, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return "", nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id, email_domains FROM organizations WHERE status = 'active' ORDER BY created_at, id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw store.RawJSON
		if err := rows.Scan(&id, &raw); err != nil {
			return "", err
		}
		var domains []string
		_ = json.Unmarshal(raw, &domains)
		for _, d := range domains {
			if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(d), "@"), domain) {
				return id, nil
			}
		}
	}
	return "", rows.Err()
}
