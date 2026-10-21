// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// SeedOptions configure SeedDemo (`simpletickets seed`, DB-021).
type SeedOptions struct {
	Tickets      int    // number of demo tickets (default 50)
	PasswordHash string // argon2id PHC hash applied to every demo account ("" = no local login)
	Now          time.Time
}

// SeedResult lists the demo accounts created.
type SeedResult struct {
	Accounts map[string]string // email -> role description
	Tickets  int
}

// ErrAlreadySeeded is returned when the database already holds users.
var ErrAlreadySeeded = errors.New("store: database already has users; demo seed refused")

// SeedDemo fills an empty database with a small, realistic data set: two
// organizations, three teams, staff across every role and tier, customers,
// categories and tickets (with ticket.created events so timelines render).
// Search indexes are left for the index worker / `simpletickets reindex`.
func SeedDemo(ctx context.Context, db *DB, opt SeedOptions) (SeedResult, error) {
	if opt.Tickets <= 0 {
		opt.Tickets = 50
	}
	if opt.Now.IsZero() {
		opt.Now = Now()
	}
	res := SeedResult{Accounts: map[string]string{}}
	var users int
	if err := db.R.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return res, err
	}
	if users > 0 {
		return res, ErrAlreadySeeded
	}
	hash := Nullable(opt.PasswordHash)
	err := db.Tx(ctx, func(tx *Tx) error {
		ex := func(q string, args ...any) error { _, err := tx.ExecContext(ctx, q, args...); return err }
		now := opt.Now
		orgs := map[string]string{"Acme Corp": NewID(), "Globex": NewID()}
		for name, id := range orgs {
			domain := map[string]string{"Acme Corp": "acme.test", "Globex": "globex.test"}[name]
			if err := ex(`INSERT INTO organizations (id, name, email_domains, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, id, name, MustJSON([]string{domain}), now, now); err != nil {
				return err
			}
		}
		teams := map[string]string{"Front desk": NewID(), "Technical": NewID(), "Billing": NewID()}
		for name, id := range teams {
			if err := ex(`INSERT INTO teams (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`, id, name, now, now); err != nil {
				return err
			}
			if err := ex(`INSERT INTO project_teams (project_id, team_id, created_at) VALUES (?, ?, ?)`, domain.DefaultProjectID, id, now); err != nil {
				return err
			}
		}
		type staff struct {
			email, name, role string
			tier              int
			breakGlass        bool
			teams             []string
		}
		staffList := []staff{
			{"admin@demo.test", "Ada Admin", "admin", 3, true, nil},
			{"supervisor@demo.test", "Sam Supervisor", "supervisor", 3, false, []string{"Front desk", "Technical", "Billing"}},
			{"l1@demo.test", "Lia L1", "agent", 1, false, []string{"Front desk", "Billing"}},
			{"l2@demo.test", "Leo L2", "agent", 2, false, []string{"Technical"}},
			{"l3@demo.test", "Lou L3", "agent", 3, false, []string{"Technical"}},
		}
		staffIDs := map[string]string{}
		for _, s := range staffList {
			id := NewID()
			staffIDs[s.email] = id
			if err := ex(`INSERT INTO users (id, kind, email, name, role, tier, break_glass, password_hash, created_at, updated_at) VALUES (?, 'staff', ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, s.email, s.name, s.role, s.tier, s.breakGlass, hash, now, now); err != nil {
				return err
			}
			if err := ex(`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)`, domain.DefaultProjectID, id, now); err != nil {
				return err
			}
			for _, tn := range s.teams {
				if err := ex(`INSERT INTO team_members (team_id, user_id, created_at) VALUES (?, ?, ?)`, teams[tn], id, now); err != nil {
					return err
				}
			}
			res.Accounts[s.email] = fmt.Sprintf("staff %s tier %d", s.role, s.tier)
		}
		type cust struct{ email, name, role, org string }
		custs := []cust{
			{"manager@acme.test", "Mia Manager", "org_manager", "Acme Corp"},
			{"user@acme.test", "Uma User", "requester", "Acme Corp"},
			{"user@globex.test", "Gus Globex", "requester", "Globex"},
		}
		var custIDs []string
		custOrg := map[string]string{}
		for _, c := range custs {
			id := NewID()
			custIDs = append(custIDs, id)
			custOrg[id] = orgs[c.org]
			if err := ex(`INSERT INTO users (id, kind, email, name, role, organization_id, password_hash, created_at, updated_at) VALUES (?, 'customer', ?, ?, ?, ?, ?, ?, ?)`,
				id, c.email, c.name, c.role, orgs[c.org], hash, now, now); err != nil {
				return err
			}
			res.Accounts[c.email] = "customer " + c.role
		}
		cats := []struct{ name, team string }{{"General question", "Front desk"}, {"Technical issue", "Technical"}, {"Billing", "Billing"}}
		catIDs := make([]string, len(cats))
		for i, c := range cats {
			catIDs[i] = NewID()
			if err := ex(`INSERT INTO categories (id, name, default_team_id, sort, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, catIDs[i], c.name, teams[c.team], i, now, now); err != nil {
				return err
			}
		}
		subjects := []string{"Cannot log in", "Invoice is wrong", "VPN keeps disconnecting", "Printer offline", "Request a new laptop",
			"Password reset not arriving", "App crashes on start", "Refund request", "Slow dashboard", "Export to CSV fails"}
		statuses := []string{"new", "open", "pending", "on_hold", "resolved", "closed"}
		priorities := []string{"low", "normal", "normal", "high", "urgent"}
		agents := []string{staffIDs["l1@demo.test"], staffIDs["l2@demo.test"], staffIDs["l3@demo.test"]}
		for i := 0; i < opt.Tickets; i++ {
			id := NewID()
			n, err := NextTicketNumber(ctx, tx)
			if err != nil {
				return err
			}
			ci := i % len(cats)
			team := teams[cats[ci].team]
			tier := 1 + i%3
			if cats[ci].team != "Technical" {
				tier = 1
			}
			requester := custIDs[i%len(custIDs)]
			status := statuses[i%len(statuses)]
			var assignee any
			if status != "new" {
				switch {
				case cats[ci].team == "Technical" && tier == 2:
					assignee = agents[1]
				case cats[ci].team == "Technical" && tier == 3:
					assignee = agents[2]
				case cats[ci].team != "Technical":
					assignee = agents[0]
				}
			}
			at := now.Add(-time.Duration(opt.Tickets-i) * 37 * time.Minute)
			subject := subjects[i%len(subjects)]
			if err := ex(`INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, category_id, assignee_id, requester_id, organization_id, channel, last_customer_activity_at, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'portal', ?, ?, ?)`,
				id, n, subject, status, priorities[i%len(priorities)], tier, team, catIDs[ci], assignee, requester, custOrg[requester], at, at, at); err != nil {
				return err
			}
			if err := ex(`INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, created_at) VALUES (?, ?, ?, 'public', 'portal', ?, ?)`,
				NewID(), id, requester, "Hello, "+subject+". Can you help?", at); err != nil {
				return err
			}
			snap := MustJSON(map[string]any{"number": n, "subject": subject, "status": status, "tier": tier, "team_id": team,
				"assignee_id": assignee, "requester_id": requester, "organization_id": custOrg[requester]})
			if err := ex(`INSERT INTO ticket_events (id, ticket_id, actor_id, type, data, snapshot, created_at) VALUES (?, ?, ?, 'ticket.created', ?, ?, ?)`,
				NewID(), id, requester, MustJSON(map[string]any{"channel": "portal"}), snap, at); err != nil {
				return err
			}
		}
		res.Tickets = opt.Tickets
		return nil
	})
	return res, err
}
