// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
)

var seq atomic.Int64

func next() int64 { return seq.Add(1) }

func exec(t testing.TB, db *store.DB, q string, args ...any) {
	t.Helper()
	err := db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	})
	if err != nil {
		t.Fatalf("fixture: %v\n%s", err, q)
	}
}

// CreateOrg inserts an organization and returns its ID.
func CreateOrg(t testing.TB, db *store.DB, name string) string {
	t.Helper()
	if name == "" {
		name = fmt.Sprintf("Org %d", next())
	}
	id := store.NewID()
	exec(t, db, `INSERT INTO organizations (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, name, store.Now(), store.Now())
	return id
}

// CreateTeam inserts a team and returns its ID.
func CreateTeam(t testing.TB, db *store.DB, name string) string {
	t.Helper()
	if name == "" {
		name = fmt.Sprintf("Team %d", next())
	}
	id := store.NewID()
	exec(t, db, `INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, name, store.Now(), store.Now())
	return id
}

// CreateStaff inserts an active staff user with role and tier, adds it to the
// given teams, and returns its ID.
func CreateStaff(t testing.TB, db *store.DB, role domain.Role, tier int, teams ...string) string {
	t.Helper()
	if tier == 0 {
		tier = 1
	}
	id := store.NewID()
	n := next()
	exec(t, db, `INSERT INTO users (id, kind, email, name, role, tier, status, created_at, updated_at) VALUES (?, 'staff', ?, ?, ?, ?, 'active', ?, ?)`,
		id, fmt.Sprintf("staff%d@example.test", n), fmt.Sprintf("Staff %d", n), string(role), tier, store.Now(), store.Now())
	for _, team := range teams {
		exec(t, db, `INSERT INTO team_members (team_id, user_id, created_at) VALUES (?, ?, ?)`, team, id, store.Now())
	}
	return id
}

// CreateCustomer inserts an active requester (orgID may be "") and returns its ID.
func CreateCustomer(t testing.TB, db *store.DB, orgID string) string {
	t.Helper()
	return CreateCustomerRole(t, db, orgID, domain.RoleRequester)
}

// CreateCustomerRole inserts an active customer with the given role.
func CreateCustomerRole(t testing.TB, db *store.DB, orgID string, role domain.Role) string {
	t.Helper()
	id := store.NewID()
	n := next()
	var org any
	if orgID != "" {
		org = orgID
	}
	exec(t, db, `INSERT INTO users (id, kind, email, name, role, organization_id, status, created_at, updated_at) VALUES (?, 'customer', ?, ?, ?, ?, 'active', ?, ?)`,
		id, fmt.Sprintf("customer%d@example.test", n), fmt.Sprintf("Customer %d", n), string(role), org, store.Now(), store.Now())
	return id
}

// CreateCategory inserts an active category routed to teamID ("" for triage).
func CreateCategory(t testing.TB, db *store.DB, name, teamID string) string {
	t.Helper()
	if name == "" {
		name = fmt.Sprintf("Category %d", next())
	}
	id := store.NewID()
	var team any
	if teamID != "" {
		team = teamID
	}
	exec(t, db, `INSERT INTO categories (id, name, default_team_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, name, team, store.Now(), store.Now())
	return id
}

// Ticket describes a fixture ticket; zero values take sensible defaults.
type Ticket struct {
	Subject    string
	Status     domain.TicketStatus
	Priority   domain.Priority
	Tier       int
	TeamID     string
	CategoryID string
	AssigneeID string
	Requester  string // required
	OrgID      string
	Channel    domain.Channel
	UpdatedAt  time.Time
}

// CreateTicket inserts a ticket (with a real ticket number) and returns its ID.
func CreateTicket(t testing.TB, db *store.DB, tk Ticket) string {
	t.Helper()
	if tk.Requester == "" {
		t.Fatal("storetest.CreateTicket: Requester is required")
	}
	if tk.Subject == "" {
		tk.Subject = fmt.Sprintf("Ticket %d", next())
	}
	if tk.Status == "" {
		tk.Status = domain.StatusNew
	}
	if tk.Priority == "" {
		tk.Priority = domain.PriorityNormal
	}
	if tk.Tier == 0 {
		tk.Tier = 1
	}
	if tk.Channel == "" {
		tk.Channel = domain.ChannelPortal
	}
	if tk.UpdatedAt.IsZero() {
		tk.UpdatedAt = store.Now()
	}
	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	id := store.NewID()
	ctx := context.Background()
	err := db.Tx(ctx, func(tx *store.Tx) error {
		n, err := store.NextTicketNumber(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, category_id, assignee_id, requester_id, organization_id, channel, last_customer_activity_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, n, tk.Subject, string(tk.Status), string(tk.Priority), tk.Tier, nullable(tk.TeamID), nullable(tk.CategoryID), nullable(tk.AssigneeID),
			tk.Requester, nullable(tk.OrgID), string(tk.Channel), tk.UpdatedAt, tk.UpdatedAt, tk.UpdatedAt)
		return err
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return id
}
