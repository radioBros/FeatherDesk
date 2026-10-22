// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func seedForCopy(t *testing.T, db *store.DB) {
	org := storetest.CreateOrg(t, db, "Acme")
	team := storetest.CreateTeam(t, db, "Front desk")
	agent := storetest.CreateStaff(t, db, domain.RoleAgent, 2, team)
	cust := storetest.CreateCustomer(t, db, org)
	a := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, OrgID: org, TeamID: team, AssigneeID: agent, Subject: "Original"})
	b := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, OrgID: org, Subject: "Follow-up"})
	mustExec(t, db, `UPDATE tickets SET followup_of_id = ? WHERE id = ?`, a, b)
	mustExec(t, db, `INSERT INTO ticket_events (id, ticket_id, actor_id, type, data, snapshot, created_at) VALUES (?, ?, ?, 'ticket.created', '{"b":2,"a":[1,2]}', '{"tier":1}', ?)`, store.NewID(), a, agent, store.Now())
	mustExec(t, db, `INSERT INTO users (id, kind, email, role, tier, break_glass, last_login_at) VALUES ('adm', 'staff', 'adm@x', 'admin', 3, TRUE, ?)`, store.Now())
	parent := store.NewID()
	mustExec(t, db, `INSERT INTO kb_categories (id, slug, title, visibility) VALUES (?, 'p', '{"en":"P"}', 'public')`, parent)
	mustExec(t, db, `INSERT INTO kb_categories (id, parent_id, slug, visibility) VALUES ('child', ?, 'c', 'customers')`, parent)
	mustExec(t, db, `INSERT INTO custom_field_defs (id, key, type, required) VALUES ('f1', 'contract', 'number', TRUE)`)
	mustExec(t, db, `INSERT INTO ticket_field_values (ticket_id, field_id, value_num, value_date) VALUES (?, 'f1', 42.5, ?)`, a, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	mustExec(t, db, `UPDATE settings SET value = '{"product_name":"Migrated"}' WHERE key = 'branding'`)
	// D-20 project data: a second project with a logo, members, team mapping,
	// an organization limit, an SLA override, scoped config, a ticket, and
	// the deferred users.current_project_id reference.
	mob := storetest.CreateProject(t, db, "MOB")
	storetest.MapProjectTeam(t, db, mob, team)
	storetest.AddProjectMember(t, db, mob, agent, domain.ProjectAdmin)
	mustExec(t, db, `INSERT INTO branding_assets (id, slot, variant, blob_key, content_type, sha256) VALUES ('logo1', 'logo_light', ?, 'branding/x.png', 'image/png', 'x')`, "project:"+mob)
	mustExec(t, db, `UPDATE projects SET logo_asset_id = 'logo1', default_team_id = ?, portal_title = 'Mobile' WHERE id = ?`, team, mob)
	mustExec(t, db, `INSERT INTO organization_projects (organization_id, project_id) VALUES (?, ?)`, org, mob)
	mustExec(t, db, `INSERT INTO sla_policies (id, project_id, priority, first_response_minutes, resolution_minutes) VALUES ('sla-mob', ?, 'urgent', 5, 50)`, mob)
	mustExec(t, db, `INSERT INTO categories (id, name, project_id) VALUES ('cat-mob', 'App', ?)`, mob)
	mustExec(t, db, `INSERT INTO automation_rules (id, name, event, project_id) VALUES ('rule-mob', 'r', 'ticket.created', ?)`, mob)
	mustExec(t, db, `UPDATE users SET current_project_id = ? WHERE id = ?`, mob, agent)
	storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, OrgID: org, TeamID: team, ProjectID: mob, Subject: "Mobile"})
}

func TestMigrateEngineBothDirections(t *testing.T) {
	if storetest.PostgresURL() == "" {
		t.Skip("needs FD_TEST_POSTGRES_URL")
	}
	lite := storetest.Open(t, store.SQLite)
	pg := storetest.Open(t, store.Postgres)
	seedForCopy(t, lite)
	rep, err := store.MigrateEngine(ctx, lite, pg)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NeedsReindex || len(rep.Tables) < 30 {
		t.Fatalf("report %+v", rep)
	}
	var name string
	var bg bool
	if err := pg.Read().QueryRowContext(ctx, `SELECT value->>'product_name' FROM settings WHERE key = 'branding'`).Scan(&name); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if err := pg.Read().QueryRowContext(ctx, `SELECT break_glass FROM users WHERE id = 'adm'`).Scan(&bg); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if name != "Migrated" || !bg {
		t.Fatalf("values not carried: %q %v", name, bg)
	}
	var n int64
	if err := pg.Read().QueryRowContext(ctx, `SELECT int_value FROM counters WHERE name = 'ticket_number'`).Scan(&n); err != nil {
		t.Fatalf("pg.Read: %v", err)
	}
	if n != 1003 {
		t.Fatalf("counter %d", n)
	}
	var cur, logo, ref string
	if err := pg.Read().QueryRowContext(ctx, `SELECT u.current_project_id, p.logo_asset_id, p.key || '-' || t.project_seq FROM users u
		JOIN projects p ON p.id = u.current_project_id JOIN tickets t ON t.project_id = p.id WHERE u.current_project_id IS NOT NULL`).Scan(&cur, &logo, &ref); err != nil {
		t.Fatalf("project data not carried: %v", err)
	}
	if logo != "logo1" || ref != "MOB-1" {
		t.Fatalf("project data %s %s %s", cur, logo, ref)
	}
	// And back again into a fresh SQLite.
	lite2 := storetest.Open(t, store.SQLite)
	if _, err := store.MigrateEngine(ctx, pg, lite2); err != nil {
		t.Fatal(err)
	}
	// Refuses a non-empty target.
	if _, err := store.MigrateEngine(ctx, lite, lite2); err == nil {
		t.Fatal("copied into a non-empty target")
	}
}
