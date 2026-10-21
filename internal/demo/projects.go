// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// Projects (D-20): the Default project becomes "Web App" (WEB), plus
// "Mobile" (MOB, portal-visible) and "Internal IT" (IT, staff-only). Every
// staff member works in all three except one agent limited to Mobile; one
// senior agent administers Mobile.

type projRef struct {
	ID, Key, Name string
}

type projectDef struct {
	key, code, name, desc, color, icon string
	portal                             bool
	title, intro                       string
	defaultTeam                        string
	teams                              []string // team keys serving the project
}

var projectDefs = []projectDef{
	{key: "web", code: "WEB", name: "Web App", desc: "The customer web application: accounts, dashboards, exports and billing.",
		color: "#1B5E8C", icon: "mdi-web", portal: true, title: "Web App support",
		intro: "Questions about the **web application**? Search the knowledge base first; our team answers within one business day.",
		teams: []string{"front", "tech", "billing"}},
	{key: "mob", code: "MOB", name: "Mobile", desc: "The iOS and Android apps, push notifications and in-app purchases.",
		color: "#7C3AED", icon: "mdi-cellphone", portal: true, defaultTeam: "tech",
		teams: []string{"front", "tech", "billing"}},
	{key: "it", code: "IT", name: "Internal IT", desc: "Laptops, accounts and onboarding for our own staff. Not visible in the customer portal.",
		color: "#0F766E", icon: "mdi-laptop", portal: false, defaultTeam: "tech",
		teams: []string{"front", "tech"}},
}

// singleProjectKey is the agent who works in Mobile only (I-11 demo).
const singleProjectKey = "mob1"

// projectAdminKey administers the Mobile project.
const projectAdminKey = "fd3"

// orgProjects limits organizations to projects (organization_projects).
var orgProjects = []struct {
	org      string
	projects []string
}{
	{"globex", []string{"web"}},
	{"hq", []string{"it"}},
}

func (s *seeder) setupProjects() error {
	a := s.admin
	for i, d := range projectDefs {
		var teams []string
		for _, k := range d.teams {
			teams = append(teams, s.teams[k].ID)
		}
		body := map[string]any{"key": d.code, "name": d.name, "description": d.desc, "color": d.color, "icon": d.icon,
			"portal_visible": d.portal, "sort": i}
		if d.title != "" {
			body["portal_title"] = d.title
		}
		if d.intro != "" {
			body["portal_intro_md"] = d.intro
		}
		var out idResp
		if i == 0 {
			// The Default project of migration 0140 becomes WEB (no tickets yet,
			// so the key can still change).
			if err := a.patch("/admin/projects/"+domain.DefaultProjectID, body, &out); err != nil {
				return fmt.Errorf("project %s: %w", d.code, err)
			}
			if err := a.put("/admin/projects/"+out.ID+"/teams", map[string]any{"team_ids": teams}, nil); err != nil {
				return fmt.Errorf("project %s teams: %w", d.code, err)
			}
		} else {
			body["team_ids"] = teams
			if d.defaultTeam != "" {
				body["default_team_id"] = s.teams[d.defaultTeam].ID
			}
			if err := a.post("/admin/projects", body, &out); err != nil {
				return fmt.Errorf("project %s: %w", d.code, err)
			}
		}
		s.projects[d.key] = &projRef{ID: out.ID, Key: d.code, Name: d.name}
		s.advance(time.Minute)
	}
	for _, op := range orgProjects {
		var ids []string
		for _, k := range op.projects {
			ids = append(ids, s.projects[k].ID)
		}
		if err := a.patch("/agent/organizations/"+s.orgs[op.org].ID, map[string]any{"project_ids": ids}, nil); err != nil {
			return fmt.Errorf("organization %s projects: %w", op.org, err)
		}
	}
	return nil
}

// memberships is the `projects` body of a staff member (nil = all three).
func (s *seeder) memberships(p *person) []map[string]string {
	keys := p.projects
	if keys == nil {
		for _, d := range projectDefs {
			keys = append(keys, d.key)
		}
	}
	var out []map[string]string
	for _, k := range keys {
		role := "member"
		for _, a := range p.projectAdmin {
			if a == k {
				role = "project_admin"
			}
		}
		out = append(out, map[string]string{"project_id": s.projects[k].ID, "role": role})
	}
	return out
}

// customerAllowed reports whether customer c may be the requester of a
// ticket in project (organization limits; IT tickets only for HQ staff).
func (s *seeder) customerAllowed(c *person, project string) bool {
	if project == "it" {
		return c.org == "hq"
	}
	for _, op := range orgProjects {
		if op.org != c.org {
			continue
		}
		for _, k := range op.projects {
			if k == project {
				return true
			}
		}
		return false
	}
	return true
}

// projectExtraTickets is the number of tickets projectExtras opens (they
// count toward --tickets).
const projectExtraTickets = 1

// projectExtras runs after the tickets: the Mobile-only agent takes over an
// open Mobile ticket of the tech queue (when there is one) and gets a fresh
// one assigned by the supervisor.
func (s *seeder) projectExtras() error {
	s.sim(s.now.Add(-50 * time.Minute))
	sup, mob := s.staff["sup"], s.staff[singleProjectKey]
	for _, t := range s.tickets {
		if t.project != "mob" || t.team != "tech" || t.tier > mob.tier || t.status == "resolved" || t.status == "closed" {
			continue
		}
		if err := s.assign(t, sup, mob); err != nil {
			return err
		}
		s.advance(3 * time.Minute)
		if err := s.note(t, mob, "Taking this one: I cover the mobile apps. Checking the crash logs from the store console."); err != nil {
			return err
		}
		break
	}
	s.advance(5 * time.Minute)
	sp := spec{subject: "Widget on the home screen shows yesterday's data", cat: "mobile", prio: "normal",
		body: "The home screen widget of the Android app does not refresh: it still shows yesterday's numbers until I open the app.",
		cf:   map[string]any{"os": "android", "app_version": "3.9.0"}}
	t, err := s.agentOpen(sup.cl, sup, s.custs["mia"], sp, mob)
	if err != nil {
		return err
	}
	s.advance(12 * time.Minute)
	return s.reply(t, mob, agentReply(s, t.req.name), "")
}

// ProjectInfo is one project of the summary.
type ProjectInfo struct {
	Key, Name     string
	PortalVisible bool
	Tickets       int
	SampleRefs    []string
}

func (s *seeder) projectSummary(ctx context.Context) ([]ProjectInfo, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT p.id, p.key, p.name, p.portal_visible, (SELECT COUNT(*) FROM tickets t WHERE t.project_id = p.id)
		FROM projects p ORDER BY p.sort, p.key`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id string
		ProjectInfo
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.Key, &r.Name, &r.PortalVisible, &r.Tickets); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, 0, len(list))
	for _, r := range list {
		seqs, err := s.db.Read().QueryContext(ctx, `SELECT project_seq FROM tickets WHERE project_id = ? ORDER BY project_seq DESC LIMIT 3`, r.id)
		if err != nil {
			return nil, err
		}
		for seqs.Next() {
			var n int64
			if err := seqs.Scan(&n); err != nil {
				seqs.Close()
				return nil, err
			}
			r.SampleRefs = append(r.SampleRefs, domain.TicketRef(r.Key, n))
		}
		seqs.Close()
		out = append(out, r.ProjectInfo)
	}
	return out, nil
}
