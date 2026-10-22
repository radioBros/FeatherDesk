// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
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
	// D-20: like the admin API without project_ids, the team serves the Default project.
	exec(t, db, `INSERT INTO project_teams (project_id, team_id, created_at) VALUES (?, ?, ?)`, domain.DefaultProjectID, id, store.Now())
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
	// D-20: like the admin API without project_ids, staff join the Default project (I-11).
	exec(t, db, `INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)`, domain.DefaultProjectID, id, store.Now())
	return id
}

// CreateProject inserts an active, portal-visible project with key (and its
// sequence counter row) and returns its ID.
func CreateProject(t testing.TB, db *store.DB, key string) string {
	t.Helper()
	id := store.NewID()
	exec(t, db, `INSERT INTO projects (id, key, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, key, "Project "+key, store.Now(), store.Now())
	exec(t, db, `INSERT INTO counters (name, int_value, text_value) VALUES (?, 0, '')`, store.ProjectSeqCounter(id))
	return id
}

// AddProjectMember adds a staff user to a project with role (member or project_admin).
func AddProjectMember(t testing.TB, db *store.DB, projectID, userID string, role domain.ProjectRole) {
	t.Helper()
	exec(t, db, `INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, projectID, userID, string(role), store.Now())
}

// RemoveProjectMember removes a staff user from a project.
func RemoveProjectMember(t testing.TB, db *store.DB, projectID, userID string) {
	t.Helper()
	exec(t, db, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
}

// MapProjectTeam lets team serve project.
func MapProjectTeam(t testing.TB, db *store.DB, projectID, teamID string) {
	t.Helper()
	exec(t, db, `INSERT INTO project_teams (project_id, team_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, projectID, teamID, store.Now())
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
	ProjectID  string // "" = the Default project
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
	if tk.ProjectID == "" {
		tk.ProjectID = domain.DefaultProjectID
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
		seq, err := store.NextProjectSeq(ctx, tx, tk.ProjectID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, number, project_id, project_seq, subject, status, priority, tier, team_id, category_id, assignee_id, requester_id, organization_id, channel, last_customer_activity_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, n, tk.ProjectID, seq, tk.Subject, string(tk.Status), string(tk.Priority), tk.Tier, nullable(tk.TeamID), nullable(tk.CategoryID), nullable(tk.AssigneeID),
			tk.Requester, nullable(tk.OrgID), string(tk.Channel), tk.UpdatedAt, tk.UpdatedAt, tk.UpdatedAt)
		return err
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return id
}
