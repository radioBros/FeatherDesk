// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// D-20 (PRJ-*): projects, refs, scoping and I-11 through the real router.
// Every response is validated against api/openapi.yaml by env.do.

// projFix is two projects: the Default project (HD) and MOB, served by
// teamA; teamB serves HD only.
type projFix struct {
	e            *env
	hd, mob      string
	teamA, teamB string
	hdAgent      *domain.Principal // L2, teamA, HD only
	mobAgent     *domain.Principal // L2, teamA, MOB only
	both         *domain.Principal // L3, teamA+teamB, HD and MOB
	mobAdmin     *domain.Principal // L1, teamA, project admin of MOB
	sup          *domain.Principal
	orgA, orgB   string
	reqA, reqB   *domain.Principal
}

// agentIn creates an agent whose project memberships are exactly projects
// (admin: those with the project_admin role) and returns its principal.
func (e *env) agentIn(tier int, teams []string, projects, admin []string) *domain.Principal {
	e.t.Helper()
	p := e.staff(domain.RoleAgent, tier, teams...)
	storetest.RemoveProjectMember(e.t, e.db, domain.DefaultProjectID, p.UserID)
	for _, pr := range projects {
		role := domain.ProjectMember
		if slices.Contains(admin, pr) {
			role = domain.ProjectAdmin
		}
		storetest.AddProjectMember(e.t, e.db, pr, p.UserID, role)
	}
	p.Projects, p.AdminProjects = append([]string{}, projects...), append([]string{}, admin...)
	return e.register(p)
}

func newProjFix(t *testing.T, db *store.DB) *projFix {
	e := newEnv(t, db)
	f := &projFix{e: e, hd: domain.DefaultProjectID}
	f.mob = storetest.CreateProject(t, db, "MOB")
	f.teamA, f.teamB = storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
	storetest.MapProjectTeam(t, db, f.mob, f.teamA)
	f.hdAgent = e.agentIn(2, []string{f.teamA}, []string{f.hd}, nil)
	f.mobAgent = e.agentIn(2, []string{f.teamA}, []string{f.mob}, nil)
	f.both = e.agentIn(3, []string{f.teamA, f.teamB}, []string{f.hd, f.mob}, nil)
	f.mobAdmin = e.agentIn(1, []string{f.teamA}, []string{f.mob}, []string{f.mob})
	f.sup = e.staff(domain.RoleSupervisor, 1)
	f.orgA, f.orgB = storetest.CreateOrg(t, db, ""), storetest.CreateOrg(t, db, "")
	f.reqA = e.customer(f.orgA, domain.RoleRequester)
	f.reqB = e.customer(f.orgB, domain.RoleRequester)
	e.svc.dir.Invalidate() // fixtures wrote projects and mappings behind the directory's back
	return f
}

// ticket inserts a fixture ticket in project.
func (f *projFix) ticket(t *testing.T, project string, req *domain.Principal, team string, tier int, assignee string) string {
	return storetest.CreateTicket(t, f.e.db, storetest.Ticket{Requester: req.UserID, OrgID: req.OrganizationID, TeamID: team, Tier: tier,
		AssigneeID: assignee, ProjectID: project, Subject: "Fixture " + store.NewID()[:8]})
}

func (f *projFix) refOf(t *testing.T, id string) string {
	t.Helper()
	row, err := lockFreeLoad(context.Background(), f.e.svc, id)
	if err != nil {
		t.Fatal(err)
	}
	f.e.svc.fillKey(context.Background(), row)
	return row.Ref()
}

func ids(items []any) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.(map[string]any)["id"].(string))
	}
	slices.Sort(out)
	return out
}

func (f *projFix) listIDs(t *testing.T, p *domain.Principal, query string) []string {
	t.Helper()
	page := f.e.do(p, "GET", "/api/v1/agent/tickets"+query, nil).expect(t, 200).m(t)
	return ids(page["items"].([]any))
}

func sorted(v ...string) []string { slices.Sort(v); return v }

// TestProjects_I11_ListDetailSearchCountsReports: an agent sees only the
// tickets of the projects they are members of, in every read path.
func TestProjects_I11_ListDetailSearchCountsReports(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		ctx := context.Background()
		hdT := f.ticket(t, f.hd, f.reqA, f.teamA, 1, f.hdAgent.UserID)
		mobT := f.ticket(t, f.mob, f.reqA, f.teamA, 1, f.mobAgent.UserID)
		for _, id := range []string{hdT, mobT} {
			f.e.exec(`UPDATE tickets SET subject = 'Zebra crossing broken' WHERE id = ?`, id)
			if err := f.e.svc.IndexOne(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		hdRef, mobRef := f.refOf(t, hdT), f.refOf(t, mobT)
		if !strings.HasPrefix(hdRef, "HD-") || mobRef != "MOB-1" {
			t.Fatalf("refs %s %s", hdRef, mobRef)
		}
		cases := []struct {
			p        *domain.Principal
			own, not string
			ownRef   string
			notRef   string
		}{{f.hdAgent, hdT, mobT, hdRef, mobRef}, {f.mobAgent, mobT, hdT, mobRef, hdRef}}
		for _, c := range cases {
			if got := f.listIDs(t, c.p, ""); !slices.Equal(got, []string{c.own}) {
				t.Errorf("%s list %v, want only %s", c.p.UserID, got, c.own)
			}
			f.e.do(c.p, "GET", "/api/v1/agent/tickets/"+c.own, nil).expect(t, 200)
			f.e.do(c.p, "GET", "/api/v1/agent/tickets/"+c.not, nil).expect(t, 404)
			f.e.do(c.p, "GET", "/api/v1/agent/tickets/by-ref/"+c.ownRef, nil).expect(t, 200)
			f.e.do(c.p, "GET", "/api/v1/agent/tickets/by-ref/"+c.notRef, nil).expect(t, 404)
			if got := f.listIDs(t, c.p, "?q=zebra"); !slices.Equal(got, []string{c.own}) {
				t.Errorf("%s text search %v", c.p.UserID, got)
			}
			if got := f.listIDs(t, c.p, "?q="+c.notRef); len(got) != 0 {
				t.Errorf("%s ref search of a foreign project: %v", c.p.UserID, got)
			}
			if got := f.listIDs(t, c.p, "?q="+strings.ToLower(c.ownRef)); !slices.Equal(got, []string{c.own}) {
				t.Errorf("%s ref search %v", c.p.UserID, got)
			}
			counts := f.e.do(c.p, "GET", "/api/v1/agent/tickets/counts", nil).expect(t, 200).m(t)
			if counts["mine"].(float64) != 1 || counts["unassigned"].(float64) != 0 {
				t.Errorf("%s counts %v", c.p.UserID, counts)
			}
			bp := counts["by_project"].(map[string]any)
			if len(bp) != 1 {
				t.Errorf("%s by_project lists foreign projects: %v", c.p.UserID, bp)
			}
			rep := f.e.do(c.p, "GET", "/api/v1/agent/reports/summary?from=2000-01-01T00:00:00Z", nil).expect(t, 200).m(t)
			if n := rep["totals"].(map[string]any)["created"].(float64); n != 1 {
				t.Errorf("%s report created %v, want 1 (own project only)", c.p.UserID, n)
			}
		}
		// Supervisors see both; the project filter narrows lists, counts and reports.
		if got := f.listIDs(t, f.sup, ""); !slices.Equal(got, sorted(hdT, mobT)) {
			t.Fatalf("supervisor list %v", got)
		}
		if got := f.listIDs(t, f.sup, "?project=mob"); !slices.Equal(got, []string{mobT}) {
			t.Fatalf("project filter by key %v", got)
		}
		if got := f.listIDs(t, f.sup, "?project="+f.hd); !slices.Equal(got, []string{hdT}) {
			t.Fatalf("project filter by id %v", got)
		}
		f.e.do(f.sup, "GET", "/api/v1/agent/tickets?project=NOPE", nil).expect(t, 422)
		counts := f.e.do(f.sup, "GET", "/api/v1/agent/tickets/counts?project=MOB", nil).expect(t, 200).m(t)
		bp := counts["by_project"].(map[string]any)
		if counts["unassigned"].(float64) != 0 || len(bp) != 2 || bp[f.mob].(map[string]any)["tier"].(map[string]any)["1"].(float64) != 1 {
			t.Fatalf("supervisor counts %v", counts)
		}
		rep := f.e.do(f.sup, "GET", "/api/v1/agent/reports/summary?from=2000-01-01T00:00:00Z&project=MOB", nil).expect(t, 200).m(t)
		if rep["totals"].(map[string]any)["created"].(float64) != 1 || rep["project_id"] != f.mob || len(rep["by_project"].([]any)) != 1 {
			t.Fatalf("project report %v", rep)
		}
		// An agent who belongs to no project sees nothing at all.
		none := f.e.agentIn(3, []string{f.teamA}, nil, nil)
		if got := f.listIDs(t, none, ""); len(got) != 0 {
			t.Fatalf("agent without projects sees %v", got)
		}
	})
}

// TestProjects_I11_BeatsAssigneeWatcherAndTriage: being the assignee or a
// watcher of a ticket, or triage visibility, never opens another project.
func TestProjects_I11_BeatsAssigneeWatcherAndTriage(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		asg := f.ticket(t, f.mob, f.reqA, f.teamB, 1, f.hdAgent.UserID)
		wat := f.ticket(t, f.mob, f.reqA, f.teamA, 1, "")
		f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, wat, f.hdAgent.UserID, store.Now())
		tri := f.ticket(t, f.mob, f.reqA, "", 1, "")
		for _, id := range []string{asg, wat, tri} {
			f.e.do(f.hdAgent, "GET", "/api/v1/agent/tickets/"+id, nil).expect(t, 404)
			f.e.do(f.both, "GET", "/api/v1/agent/tickets/"+id, nil)
		}
		f.e.do(f.mobAgent, "GET", "/api/v1/agent/tickets/"+tri, nil).expect(t, 200) // triage of its project
		// Assignment keeps an owner able to see the ticket (M-4 + I-11).
		hdT := f.ticket(t, f.hd, f.reqA, f.teamA, 1, "")
		r := f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+hdT, map[string]any{"assignee_id": f.mobAgent.UserID}).expect(t, 422)
		if !strings.Contains(string(r.body), "wrong_project") {
			t.Fatalf("assigning a non-member: %s", r.body)
		}
		list := f.e.do(f.sup, "GET", "/api/v1/agent/assignable?ticket_id="+hdT, nil).expect(t, 200).body
		if strings.Contains(string(list), f.mobAgent.UserID) || !strings.Contains(string(list), f.hdAgent.UserID) {
			t.Fatalf("assignable must list project members only: %s", list)
		}
		// Losing the membership unassigns (M-4 re-validation).
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+hdT, map[string]any{"assignee_id": f.both.UserID}).expect(t, 200)
		ctx := context.Background()
		err := db.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, f.hd, f.both.UserID); err != nil {
				return err
			}
			return f.e.svc.revalidateMembership(ctx, tx, f.sup.UserID, "", []string{f.both.UserID})
		})
		if err != nil {
			t.Fatal(err)
		}
		if row, _ := lockFreeLoad(ctx, f.e.svc, hdT); row.AssigneeID != "" {
			t.Fatal("an assignee who left the project must be unassigned")
		}
	})
}

// TestCanRead_AgreesWithVisibilityPredicate_Projects extends the agreement
// test with two projects and agents with zero, one and two memberships.
func TestCanRead_AgreesWithVisibilityPredicate_Projects(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		ctx := context.Background()
		rnd := rand.New(rand.NewSource(20))
		none := f.e.agentIn(3, []string{f.teamA, f.teamB}, nil, nil)
		staff := []*domain.Principal{f.hdAgent, f.mobAgent, f.both, f.mobAdmin, f.sup, none}
		projects, teams := []string{f.hd, f.mob}, []string{"", f.teamA, f.teamB}
		var tids []string
		for i := 0; i < 30; i++ {
			asg := ""
			if rnd.Intn(3) == 0 {
				asg = staff[rnd.Intn(len(staff))].UserID
			}
			id := f.ticket(t, projects[rnd.Intn(2)], f.reqA, teams[rnd.Intn(3)], 1+rnd.Intn(3), asg)
			if rnd.Intn(4) == 0 {
				f.e.exec(`INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, id,
					staff[rnd.Intn(len(staff))].UserID, store.Now())
			}
			tids = append(tids, id)
		}
		pol := authz.DefaultTicketPolicy()
		for i := 0; i < 400; i++ {
			p, id := staff[rnd.Intn(len(staff))], tids[rnd.Intn(len(tids))]
			row, err := lockFreeLoad(ctx, f.e.svc, id)
			if err != nil {
				t.Fatal(err)
			}
			mem := authz.CanSeeTicket(p, row.view(pol, time.Now()))
			pred, args := authz.TicketPredicate(p, pol, "t")
			n := f.e.count(`SELECT COUNT(*) FROM tickets t WHERE t.id = ? AND `+pred, append([]any{id}, args...)...)
			if mem != (n == 1) {
				t.Fatalf("disagree for %s on %s (project %s): memory=%v sql=%d", p.UserID, id, row.ProjectID, mem, n)
			}
			if mem && !p.IsSupervisorOrAdmin() && !slices.Contains(p.Projects, row.ProjectID) {
				t.Fatalf("I-11: %s sees a ticket of project %s", p.UserID, row.ProjectID)
			}
			if err := authz.Can(p, authz.ActReply, row.view(pol, time.Now())); (err != nil && problems.From(err).Status() == 404) != (n == 0) {
				t.Fatalf("Can disagrees with SQL for %s on %s", p.UserID, id)
			}
		}
	})
}

// TestProjects_ByRef: agent and portal lookups by KEY-123, case-insensitive,
// with the global number still working.
func TestProjects_ByRef(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		id := f.e.createTicket(f.reqA, CreateInput{ProjectID: sp("MOB")})
		d := f.e.do(f.sup, "GET", "/api/v1/agent/tickets/by-ref/MOB-1", nil).expect(t, 200)
		m := d.m(t)
		if m["id"] != id || m["ref"] != "MOB-1" || m["project_id"] != f.mob || m["project"].(map[string]any)["key"] != "MOB" {
			t.Fatalf("by-ref: %v", m)
		}
		if d.Header.Get("ETag") == "" {
			t.Fatal("by-ref must carry the ETag like by-number")
		}
		f.e.do(f.sup, "GET", "/api/v1/agent/tickets/by-ref/mob-1", nil).expect(t, 200)
		f.e.do(f.sup, "GET", fmt.Sprintf("/api/v1/agent/tickets/by-number/%v", m["number"]), nil).expect(t, 200)
		for _, bad := range []string{"MOB-2", "XX-1", "MOB-0", "MOB-01"} {
			f.e.do(f.sup, "GET", "/api/v1/agent/tickets/by-ref/"+bad, nil).expect(t, 404)
		}
		pm := f.e.do(f.reqA, "GET", "/api/v1/portal/tickets/by-ref/MOB-1", nil).expect(t, 200).m(t)
		if pm["ref"] != "MOB-1" || pm["project"].(map[string]any)["key"] != "MOB" {
			t.Fatalf("portal by-ref: %v", pm)
		}
		f.e.do(f.reqB, "GET", "/api/v1/portal/tickets/by-ref/MOB-1", nil).expect(t, 404)
	})
}

// TestProjects_CreateDerivesAndValidatesProject: project_id, the category's
// project, the team mapping, membership and the customer's allowed projects.
func TestProjects_CreateDerivesAndValidatesProject(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		catMob := f.e.do(f.sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "App", "project_id": "MOB", "default_team_id": f.teamA}).expect(t, 201).m(t)
		if catMob["project_id"] != f.mob {
			t.Fatalf("category project %v", catMob)
		}
		catHD := f.e.do(f.sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "Web"}).expect(t, 201).m(t)
		if catHD["project_id"] != f.hd {
			t.Fatalf("category without project_id goes to the default project: %v", catHD)
		}
		f.e.do(f.sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "X", "project_id": "MOB", "default_team_id": f.teamB}).expect(t, 422)
		create := func(p *domain.Principal, body map[string]any) resp {
			body["subject"], body["body_md"] = "Subject", "Body"
			if p.IsCustomer() {
				return f.e.do(p, "POST", "/api/v1/portal/tickets", body)
			}
			if _, ok := body["requester_id"]; !ok {
				body["requester_id"] = f.reqA.UserID
			}
			return f.e.do(p, "POST", "/api/v1/agent/tickets", body)
		}
		// The category decides the project; its default team routes.
		r := create(f.sup, map[string]any{"category_id": catMob["id"]}).expect(t, 201).m(t)
		if r["project_id"] != f.mob || r["ref"] != "MOB-1" || r["team_id"] != f.teamA {
			t.Fatalf("category routing: %v", r)
		}
		r = create(f.sup, map[string]any{"project_id": "MOB"}).expect(t, 201).m(t)
		if r["ref"] != "MOB-2" {
			t.Fatalf("explicit project: %v", r)
		}
		r = create(f.sup, map[string]any{}).expect(t, 201).m(t)
		if r["project_id"] != f.hd || !strings.HasPrefix(r["ref"].(string), "HD-") {
			t.Fatalf("default project: %v", r)
		}
		for name, c := range map[string]struct {
			p    *domain.Principal
			body map[string]any
			code string
		}{
			"category of another project": {f.sup, map[string]any{"project_id": "HD", "category_id": catMob["id"]}, "wrong_project"},
			"team not serving project":    {f.sup, map[string]any{"project_id": "MOB", "team_id": f.teamB}, "wrong_project"},
			"agent not a member":          {f.hdAgent, map[string]any{"project_id": "MOB"}, "not_allowed"},
			"unknown project":             {f.sup, map[string]any{"project_id": "ZZZ"}, "not_found"},
		} {
			res := create(c.p, c.body).expect(t, 422)
			if !strings.Contains(string(res.body), `"`+c.code+`"`) || !strings.Contains(string(res.body), "project_id") && !strings.Contains(string(res.body), "_id") {
				t.Errorf("%s: %s", name, res.body)
			}
		}
		// Customers: portal-visible projects only, limited by their organization.
		f.e.exec(`UPDATE projects SET portal_visible = FALSE WHERE id = ?`, f.mob)
		f.e.svc.dir.Invalidate()
		create(f.reqA, map[string]any{"project_id": "MOB"}).expect(t, 422)
		if pl := f.e.do(f.reqA, "GET", "/api/v1/portal/projects", nil).expect(t, 200).body; strings.Contains(string(pl), `"key":"MOB"`) {
			t.Fatalf("a hidden project is listed in the portal: %s", pl)
		}
		f.e.exec(`UPDATE projects SET portal_visible = TRUE WHERE id = ?`, f.mob)
		f.e.exec(`INSERT INTO organization_projects (organization_id, project_id, created_at) VALUES (?, ?, ?)`, f.orgB, f.mob, store.Now())
		f.e.svc.dir.Invalidate()
		list := f.e.do(f.reqB, "GET", "/api/v1/portal/projects", nil).expect(t, 200).body
		if !strings.Contains(string(list), `"key":"MOB"`) || strings.Contains(string(list), `"key":"HD"`) {
			t.Fatalf("org-limited portal projects: %s", list)
		}
		create(f.reqB, map[string]any{"project_id": "HD"}).expect(t, 422)
		r = create(f.reqB, map[string]any{}).expect(t, 201).m(t) // the only allowed project
		if r["project_id"] != f.mob {
			t.Fatalf("single allowed project: %v", r)
		}
		cats := f.e.do(f.reqB, "GET", "/api/v1/portal/categories", nil).expect(t, 200).body
		if strings.Contains(string(cats), catHD["id"].(string)) || !strings.Contains(string(cats), catMob["id"].(string)) {
			t.Fatalf("portal categories ignore the org limit: %s", cats)
		}
		r = create(f.reqA, map[string]any{}).expect(t, 201).m(t) // several allowed: the default project
		if r["project_id"] != f.hd {
			t.Fatalf("default for an unrestricted customer: %v", r)
		}
		// Customers still see every ticket of theirs (I-3 unchanged), whatever the project.
		f.e.do(f.reqB, "GET", "/api/v1/portal/tickets", nil).expect(t, 200)
	})
}

// TestProjects_SequenceUnderConcurrency: parallel creators in two projects
// get gap-free per-project sequences 1..N and unique global numbers.
func TestProjects_SequenceUnderConcurrency(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		const perProject = 12
		var wg sync.WaitGroup
		errs := make(chan error, 2*perProject)
		for i := 0; i < perProject; i++ {
			for _, pr := range []string{"MOB", "HD"} {
				wg.Add(1)
				go func(pr string) {
					defer wg.Done()
					_, err := f.e.svc.Create(context.Background(), f.sup, CreateInput{Subject: "Parallel", BodyMD: "x", ProjectID: sp(pr),
						RequesterID: sp(f.reqA.UserID)})
					errs <- err
				}(pr)
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		seqs := func(project string) []int {
			rows, err := db.Read().QueryContext(context.Background(), `SELECT project_seq FROM tickets WHERE project_id = ? ORDER BY project_seq`, project)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []int
			for rows.Next() {
				var n int
				_ = rows.Scan(&n)
				out = append(out, n)
			}
			return out
		}
		mob := seqs(f.mob)
		for i, n := range mob {
			if n != i+1 {
				t.Fatalf("MOB sequence has a gap or duplicate: %v", mob)
			}
		}
		if hd := seqs(f.hd); len(hd) != perProject || hd[len(hd)-1]-hd[0] != perProject-1 {
			t.Fatalf("HD sequence: %v", hd)
		}
		if n := f.e.count(`SELECT COUNT(DISTINCT number) FROM tickets`); n != 2*perProject {
			t.Fatalf("global numbers not unique: %d distinct", n)
		}
	})
}

// TestProjects_MoveTicket: PATCH project_id gives the next ref of the
// target, clears a category of the old project, re-routes a team that does
// not serve the target and records ticket.project_changed.
func TestProjects_MoveTicket(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		cat := f.e.do(f.sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "Web"}).expect(t, 201).m(t)
		id := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), CategoryID: sp(cat["id"].(string)), TeamID: sp(f.teamB),
			AssigneeID: sp(f.both.UserID)})
		old := f.refOf(t, id)
		f.e.do(f.hdAgent, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"project_id": "MOB"}).expect(t, 404) // teamB: not visible to it
		f.e.do(f.both, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"project_id": "NOPE"}).expect(t, 422)
		res := f.e.do(f.both, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"project_id": "MOB"}).expect(t, 200).m(t)
		tk := res["ticket"].(map[string]any)
		if tk["ref"] != "MOB-1" || tk["project_id"] != f.mob || tk["category_id"] != nil || tk["team_id"] != nil {
			t.Fatalf("moved ticket: %v", tk)
		}
		f.e.do(f.sup, "GET", "/api/v1/agent/tickets/by-ref/"+old, nil).expect(t, 404)
		f.e.do(f.sup, "GET", "/api/v1/agent/tickets/by-ref/MOB-1", nil).expect(t, 200)
		evs := f.e.eventsOf(id)
		if !slices.Contains(evs, events.TicketProjectChanged) || !slices.Contains(evs, events.TicketCategoryChanged) || !slices.Contains(evs, events.TicketTeamChanged) {
			t.Fatalf("events %v", evs)
		}
		tl := f.e.do(f.sup, "GET", "/api/v1/agent/tickets/"+id+"/events", nil).expect(t, 200).body
		if !strings.Contains(string(tl), `"from_ref":"`+old+`"`) || !strings.Contains(string(tl), `"to_ref":"MOB-1"`) {
			t.Fatalf("timeline lacks the ref change: %s", tl)
		}
		// Back to HD as the agent working in both: allowed, new HD ref.
		f.e.do(f.both, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"project_id": f.hd}).expect(t, 200)
		if ref := f.refOf(t, id); !strings.HasPrefix(ref, "HD-") || ref == old {
			t.Fatalf("moved back: %s (was %s)", ref, old)
		}
		// A member of MOB only cannot move a ticket it can see into HD.
		mobT := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp("MOB"), TeamID: sp(f.teamA)})
		f.e.do(f.mobAgent, "PATCH", "/api/v1/agent/tickets/"+mobT, map[string]any{"project_id": "HD"}).expect(t, 422)
	})
}

// TestProjects_SLAOverrideFallback: a project override wins for its
// priorities; others and other projects use the global defaults.
func TestProjects_SLAOverrideFallback(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		f.e.do(f.mobAdmin, "PUT", "/api/v1/admin/projects/"+f.mob+"/sla-policies", map[string]any{"policies": []map[string]any{
			{"priority": "urgent", "first_response_minutes": 7, "resolution_minutes": 70}}}).expect(t, 200)
		got := f.e.do(f.mobAdmin, "GET", "/api/v1/admin/projects/"+f.mob+"/sla-policies", nil).expect(t, 200).body
		if !strings.Contains(string(got), `"first_response_minutes":7`) || !strings.Contains(string(got), `"inherited":true`) {
			t.Fatalf("effective policies: %s", got)
		}
		f.e.do(f.mobAdmin, "GET", "/api/v1/admin/projects/"+f.hd+"/sla-policies", nil).expect(t, 404)
		f.e.do(f.mobAdmin, "PUT", "/api/v1/admin/projects/"+f.hd+"/sla-policies", map[string]any{"policies": []any{}}).expect(t, 404)
		due := func(project string) time.Duration {
			id := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp(project), Priority: "urgent"})
			row, _ := lockFreeLoad(context.Background(), f.e.svc, id)
			return row.FirstResponseDueAt.Time.Sub(row.CreatedAt)
		}
		if d := due("MOB"); d != 7*time.Minute {
			t.Fatalf("MOB urgent first response %v, want the 7 min override", d)
		}
		if d := due("HD"); d != 30*time.Minute {
			t.Fatalf("HD urgent first response %v, want the 30 min global default", d)
		}
		id := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp("MOB"), Priority: "high"})
		row, _ := lockFreeLoad(context.Background(), f.e.svc, id)
		if d := row.FirstResponseDueAt.Time.Sub(row.CreatedAt); d != 120*time.Minute {
			t.Fatalf("MOB high falls back to the global 120 min: %v", d)
		}
		f.e.do(f.mobAdmin, "PUT", "/api/v1/admin/projects/"+f.mob+"/sla-policies", map[string]any{"policies": []any{}}).expect(t, 200)
		if d := due("MOB"); d != 30*time.Minute {
			t.Fatalf("after clearing the override: %v", d)
		}
	})
}

// TestProjects_AutomationScopeAndCondition: a project rule only runs on its
// project's tickets; the `project` condition works in global rules.
func TestProjects_AutomationScopeAndCondition(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		f.e.rule(f.mobAdmin, map[string]any{"name": "mobile tag", "event": "ticket.created", "project_id": "MOB",
			"actions": []map[string]any{{"type": "add_tag", "value": "mobile"}}})
		f.e.rule(f.sup, map[string]any{"name": "web tag", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "project", "op": "is", "value": "HD"}},
			"actions":    []map[string]any{{"type": "add_tag", "value": "web"}}})
		cur := f.e.lastEventID()
		mob := f.e.createTicket(f.reqA, CreateInput{ProjectID: sp("MOB")})
		hd := f.e.createTicket(f.reqA, CreateInput{ProjectID: sp("HD")})
		f.e.automate(&cur)
		if tags := f.e.tagsOf(mob); !slices.Equal(tags, []string{"mobile"}) {
			t.Fatalf("MOB ticket tags %v", tags)
		}
		if tags := f.e.tagsOf(hd); !slices.Equal(tags, []string{"web"}) {
			t.Fatalf("HD ticket tags %v", tags)
		}
		rules := f.e.do(f.mobAdmin, "GET", "/api/v1/admin/automation/rules", nil).expect(t, 200).body
		if strings.Contains(string(rules), "web tag") || !strings.Contains(string(rules), "mobile tag") {
			t.Fatalf("project admin sees foreign rules: %s", rules)
		}
	})
}

// TestProjects_ProjectAdminLimits: a project admin manages its project's
// configuration and nothing global or foreign.
func TestProjects_ProjectAdminLimits(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		pa := f.mobAdmin
		list := f.e.do(pa, "GET", "/api/v1/admin/projects", nil).expect(t, 200).body
		if !strings.Contains(string(list), `"key":"MOB"`) || strings.Contains(string(list), `"key":"HD"`) {
			t.Fatalf("project admin list: %s", list)
		}
		f.e.do(pa, "GET", "/api/v1/admin/projects/"+f.hd, nil).expect(t, 404)
		f.e.do(pa, "PATCH", "/api/v1/admin/projects/"+f.mob, map[string]any{"portal_title": "Mobile help", "primary_color": "#112233"}).expect(t, 200)
		f.e.do(pa, "PATCH", "/api/v1/admin/projects/"+f.mob, map[string]any{"key": "APP"}).expect(t, 403)
		f.e.do(pa, "PATCH", "/api/v1/admin/projects/"+f.mob, map[string]any{"active": false}).expect(t, 403)
		f.e.do(pa, "POST", "/api/v1/admin/projects", map[string]any{"key": "NEW", "name": "New"}).expect(t, 403)
		f.e.do(pa, "DELETE", "/api/v1/admin/projects/"+f.mob, nil).expect(t, 403)
		f.e.do(pa, "PUT", "/api/v1/admin/projects/"+f.mob+"/members", map[string]any{"members": []any{}}).expect(t, 403)
		f.e.do(pa, "GET", "/api/v1/admin/projects/"+f.mob+"/members", nil).expect(t, 200)
		f.e.do(pa, "PUT", "/api/v1/admin/projects/"+f.mob+"/teams", map[string]any{"team_ids": []string{f.teamA, f.teamB}}).expect(t, 200)
		f.e.do(pa, "PUT", "/api/v1/admin/projects/"+f.hd+"/teams", map[string]any{"team_ids": []string{f.teamA}}).expect(t, 404)
		// Categories.
		f.e.do(pa, "POST", "/api/v1/admin/categories", map[string]any{"name": "Mine", "project_id": f.mob}).expect(t, 201)
		f.e.do(pa, "POST", "/api/v1/admin/categories", map[string]any{"name": "Theirs", "project_id": f.hd}).expect(t, 403)
		f.e.do(pa, "POST", "/api/v1/admin/categories", map[string]any{"name": "Default"}).expect(t, 403)
		hdCat := f.e.do(f.sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "HD only"}).expect(t, 201).m(t)
		f.e.do(pa, "PATCH", "/api/v1/admin/categories/"+hdCat["id"].(string), map[string]any{"name": "x"}).expect(t, 404)
		cats := f.e.do(pa, "GET", "/api/v1/admin/categories", nil).expect(t, 200).body
		if strings.Contains(string(cats), "HD only") {
			t.Fatalf("project admin lists foreign categories: %s", cats)
		}
		// Rules, macros, canned responses.
		f.e.do(pa, "POST", "/api/v1/admin/automation/rules", map[string]any{"name": "global", "event": "ticket.created",
			"actions": []map[string]any{{"type": "add_tag", "value": "x"}}}).expect(t, 403)
		rid := f.e.rule(pa, map[string]any{"name": "mine", "event": "ticket.created", "project_id": "MOB", "actions": []map[string]any{{"type": "add_tag", "value": "x"}}})
		f.e.do(pa, "POST", "/api/v1/admin/automation/rules/reorder", map[string]any{"ids": []string{rid}}).expect(t, 403)
		f.e.do(pa, "PATCH", "/api/v1/admin/automation/rules/"+rid, map[string]any{"project_id": nil}).expect(t, 403)
		f.e.do(pa, "POST", "/api/v1/admin/macros", map[string]any{"name": "m", "status": "pending"}).expect(t, 403)
		mac := f.e.do(pa, "POST", "/api/v1/admin/macros", map[string]any{"name": "m", "status": "pending", "project_id": "MOB"}).expect(t, 201).m(t)
		if mac["project_id"] != f.mob {
			t.Fatalf("macro %v", mac)
		}
		f.e.do(pa, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "c", "body_md": "b"}).expect(t, 403)
		f.e.do(pa, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "c", "body_md": "b", "project_id": "MOB"}).expect(t, 201)
		// A plain agent never reaches project administration.
		f.e.do(f.mobAgent, "GET", "/api/v1/admin/projects", nil).expect(t, 403)
		f.e.do(f.mobAgent, "GET", "/api/v1/admin/categories", nil).expect(t, 403)
		// A project macro only applies to that project's tickets.
		hdT := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), TeamID: sp(f.teamA), AssigneeID: sp(f.both.UserID)})
		f.e.do(f.both, "POST", "/api/v1/agent/tickets/"+hdT+"/macros/"+mac["id"].(string)+"/apply", nil).expect(t, 422)
		// Supervisors manage everything, including members.
		f.e.do(f.sup, "PUT", "/api/v1/admin/projects/"+f.mob+"/members", map[string]any{"members": []map[string]any{
			{"user_id": f.mobAgent.UserID}, {"user_id": pa.UserID, "role": "project_admin"}}}).expect(t, 200)
		f.e.do(f.sup, "PUT", "/api/v1/admin/projects/"+f.mob+"/members", map[string]any{"members": []map[string]any{{"user_id": f.reqA.UserID}}}).expect(t, 422)
	})
}

// TestProjects_AdminCRUD: create, key rules, delete refusals.
func TestProjects_AdminCRUD(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		p := f.e.do(f.sup, "POST", "/api/v1/admin/projects", map[string]any{"key": "ops", "name": "Ops", "team_ids": []string{f.teamA},
			"default_team_id": f.teamA, "portal_visible": false}).expect(t, 201).m(t)
		if p["key"] != "OPS" || p["key_locked"] != false || p["portal_visible"] != false {
			t.Fatalf("created %v", p)
		}
		id := p["id"].(string)
		f.e.do(f.sup, "POST", "/api/v1/admin/projects", map[string]any{"key": "OPS", "name": "Dup"}).expect(t, 422)
		f.e.do(f.sup, "POST", "/api/v1/admin/projects", map[string]any{"key": "1X", "name": "Bad"}).expect(t, 422)
		f.e.do(f.sup, "PATCH", "/api/v1/admin/projects/"+id, map[string]any{"key": "OPZ"}).expect(t, 200)
		tid := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp("OPZ")})
		if ref := f.refOf(t, tid); ref != "OPZ-1" {
			t.Fatalf("first ref %s", ref)
		}
		f.e.do(f.sup, "PATCH", "/api/v1/admin/projects/"+id, map[string]any{"key": "OPS"}).expect(t, 422) // immutable with tickets
		f.e.do(f.sup, "DELETE", "/api/v1/admin/projects/"+id, nil).expect(t, 409)
		empty := f.e.do(f.sup, "POST", "/api/v1/admin/projects", map[string]any{"key": "TMP", "name": "Temp"}).expect(t, 201).m(t)
		f.e.do(f.sup, "DELETE", "/api/v1/admin/projects/"+empty["id"].(string), nil).expect(t, 204)
		f.e.do(f.sup, "GET", "/api/v1/admin/projects/"+empty["id"].(string), nil).expect(t, 404)
		ag := f.e.do(f.both, "GET", "/api/v1/agent/projects", nil).expect(t, 200).body
		if !strings.Contains(string(ag), `"my_role":"member"`) || strings.Contains(string(ag), "OPZ") {
			t.Fatalf("agent projects: %s", ag)
		}
	})
}

// TestProjects_WS: project:<id> for members only (customers 4403), and the
// tickets topic never carries events of other projects (I-11).
func TestProjects_WS(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		hdWS, supWS := f.e.wsDial(f.hdAgent), f.e.wsDial(f.sup)
		if denied := hdWS.subscribe("a", "tickets", "project:"+f.hd, "project:"+f.mob); !slices.Equal(denied, []string{"project:" + f.mob}) {
			t.Fatalf("hd agent denied %v", denied)
		}
		if denied := supWS.subscribe("a", "tickets", "project:"+f.mob); len(denied) != 0 {
			t.Fatalf("supervisor denied %v", denied)
		}
		cust := f.e.wsDial(f.reqA)
		cust.send(map[string]any{"op": "subscribe", "id": "x", "topics": []string{"project:" + f.hd}})
		select {
		case code := <-cust.closed:
			if code != 4403 {
				t.Fatalf("customer close code %d", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a customer subscribing to project: must be closed with 4403")
		}
		cur := f.e.lastEventID()
		mob := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp("MOB"), TeamID: sp(f.teamA)})
		hd := f.e.createTicket(f.sup, CreateInput{RequesterID: sp(f.reqA.UserID), ProjectID: sp("HD"), TeamID: sp(f.teamA)})
		f.e.dispatch(&cur)
		if supWS.waitFor(3*time.Second, func(m map[string]any) bool {
			return m["topic"] == "project:"+f.mob && strings.Contains(fmt.Sprint(m), mob)
		}) == nil {
			t.Fatal("supervisor got no project:MOB frame")
		}
		if hdWS.waitFor(3*time.Second, func(m map[string]any) bool { return m["type"] == "event" && strings.Contains(fmt.Sprint(m), hd) }) == nil {
			t.Fatal("hd agent got no frame of its own project")
		}
		time.Sleep(150 * time.Millisecond)
		if raw := hdWS.rawJoined(); strings.Contains(raw, mob) || strings.Contains(raw, "MOB-1") {
			t.Fatal("I-11: the hd agent received a frame of project MOB")
		}
		for _, ev := range supWS.events() {
			snapProj := ev["data"].(map[string]any)["ticket"].(map[string]any)["project_id"]
			if snapProj == nil {
				t.Fatalf("event frame without project_id: %v", ev)
			}
		}
	})
}

// TestProjects_SnapshotCarriesProjectAndRef (D-20 realtime): every event
// snapshot carries project_id and ref.
func TestProjects_SnapshotCarriesProjectAndRef(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		id := f.e.createTicket(f.reqA, CreateInput{ProjectID: sp("MOB")})
		evs, err := events.ForTicket(context.Background(), db.Read(), id)
		if err != nil || len(evs) == 0 {
			t.Fatal(err)
		}
		if s := evs[0].Snapshot; s.ProjectID != f.mob || s.Ref != "MOB-1" {
			t.Fatalf("snapshot %+v", s)
		}
	})
}

// TestProjects_StaffAndTeamLookups: /agent/staff?project lists members plus
// supervisors; /agent/teams and /agent/categories filter by project.
func TestProjects_StaffAndTeamLookups(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		staff := f.e.do(f.both, "GET", "/api/v1/agent/staff?project=MOB", nil).expect(t, 200).body
		if strings.Contains(string(staff), f.hdAgent.UserID) || !strings.Contains(string(staff), f.mobAgent.UserID) || !strings.Contains(string(staff), f.sup.UserID) {
			t.Fatalf("project staff: %s", staff)
		}
		teams := f.e.do(f.both, "GET", "/api/v1/agent/teams?project=MOB", nil).expect(t, 200).body
		if strings.Contains(string(teams), f.teamB) || !strings.Contains(string(teams), f.teamA) {
			t.Fatalf("project teams: %s", teams)
		}
		f.e.do(f.both, "GET", "/api/v1/agent/teams?project=ZZZ", nil).expect(t, 422)
	})
}

// TestProjects_Logo: a project admin uploads and removes the project's
// portal logo; the global logo slot is untouched and the project view
// carries logo_url.
func TestProjects_Logo(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newProjFix(t, db)
		up := func(p *domain.Principal, project string) resp {
			body, ct := multipartBody("logo.jpg", testJPEG(t, 600, 200, true))
			return f.e.do(p, "POST", "/api/v1/admin/projects/"+project+"/logo", body, "Content-Type", ct)
		}
		up(f.mobAdmin, f.hd).expect(t, 404)
		up(f.mobAgent, f.mob).expect(t, 403)
		p := up(f.mobAdmin, f.mob).expect(t, 201).m(t)
		url, _ := p["logo_url"].(string)
		if !strings.HasPrefix(url, "/branding/") || p["logo_asset_id"] == nil {
			t.Fatalf("logo: %v", p)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM branding_assets WHERE variant = ''`); n != 0 {
			t.Fatalf("the global logo slot changed: %d rows", n)
		}
		pub, err := f.e.svc.dir.PublicProject(context.Background(), "mob")
		if err != nil || pub == nil || pub.LogoURL == nil || *pub.LogoURL != url {
			t.Fatalf("public project %+v %v", pub, err)
		}
		f.e.do(f.mobAdmin, "DELETE", "/api/v1/admin/projects/"+f.mob+"/logo", nil).expect(t, 204)
		if n := f.e.count(`SELECT COUNT(*) FROM branding_assets`); n != 0 {
			t.Fatalf("logo rows left: %d", n)
		}
		g := f.e.do(f.mobAdmin, "GET", "/api/v1/admin/projects/"+f.mob, nil).expect(t, 200).m(t)
		if g["logo_url"] != nil || g["portal_title"] != nil {
			t.Fatalf("after delete: %v", g)
		}
	})
}
