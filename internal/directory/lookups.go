// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"net/http"
	"slices"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// Read-only lookups for pickers and filters (TKT-037). Teams, categories and
// custom-field definitions come from the in-memory snapshot (0 DB reads).

func (d *Directory) lookupTeams(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpRead); err != nil {
		return err
	}
	pid, filtered, err := d.projectFilter(r)
	if err != nil {
		return err
	}
	ts, err := d.Teams(r.Context())
	if err != nil {
		return err
	}
	out := []Team{}
	for _, t := range ts {
		if !filtered || slices.Contains(t.ProjectIDs, pid) {
			out = append(out, t)
		}
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

func (d *Directory) lookupCategories(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCategories, authz.OpRead); err != nil {
		return err
	}
	pid, filtered, err := d.projectFilter(r)
	if err != nil {
		return err
	}
	cs, err := d.Categories(r.Context())
	if err != nil {
		return err
	}
	p := dirPrincipal(r)
	out := []Category{}
	for _, c := range cs {
		// Agents see the categories of the projects they work in (I-11).
		if p.InProject(c.ProjectID) && (!filtered || c.ProjectID == pid) {
			out = append(out, c)
		}
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// portalCategories lists active categories only, sorted (TKT-018).
func (d *Directory) portalCategories(w http.ResponseWriter, r *http.Request) error {
	p := dirPrincipal(r)
	if !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeTicketsWrite) {
		return problems.Forbidden("token lacks tickets:read")
	}
	pid, filtered, err := d.projectFilter(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	cs, err := d.Categories(ctx)
	if err != nil {
		return err
	}
	prs, err := d.CustomerProjects(ctx, p)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, pr := range prs {
		allowed[pr.ID] = true
	}
	out := []Category{}
	for _, c := range cs {
		// Only the projects the customer may open tickets in (D-20).
		if c.Active && allowed[c.ProjectID] && (!filtered || c.ProjectID == pid) {
			// Routing is internal: customers do not learn the default team.
			c.DefaultTeamID = nil
			out = append(out, c)
		}
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

type staffRef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Role    string   `json:"role"`
	Tier    int      `json:"tier"`
	TeamIDs []string `json:"team_ids"`
}

// lookupStaff: active staff only, prefix search on name or e-mail, ≤ 50.
func (d *Directory) lookupStaff(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResStaffDirectory, authz.OpRead); err != nil {
		return err
	}
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	where, args := `u.kind = 'staff' AND u.status = 'active'`, []any{}
	pid, filtered, err := d.projectFilter(r)
	if err != nil {
		return err
	}
	if filtered {
		// D-20: staff who can work in the project (mention and assignee
		// pickers): its members plus every supervisor and admin.
		where += ` AND (u.role IN ('admin', 'supervisor') OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = u.id AND pm.project_id = ?))`
		args = append(args, pid)
	}
	if q != "" {
		like := prefixLike(q)
		where += ` AND (lower(u.name) LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	rows, err := d.db.Read().QueryContext(ctx, `SELECT u.id, u.name, u.email, u.role, u.tier FROM users u WHERE `+where+` ORDER BY lower(u.name), u.id LIMIT 50`, args...)
	if err != nil {
		return err
	}
	out := []staffRef{}
	idx := map[string]int{}
	for rows.Next() {
		var s staffRef
		if err := rows.Scan(&s.ID, &s.Name, &s.Email, &s.Role, &s.Tier); err != nil {
			rows.Close()
			return err
		}
		s.TeamIDs = []string{}
		idx[s.ID] = len(out)
		out = append(out, s)
	}
	rows.Close()
	if len(out) > 0 {
		ids := make([]any, 0, len(out))
		for _, s := range out {
			ids = append(ids, s.ID)
		}
		tr, err := d.db.Read().QueryContext(ctx, `SELECT user_id, team_id FROM team_members WHERE user_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) ORDER BY team_id`, ids...)
		if err != nil {
			return err
		}
		for tr.Next() {
			var u, t string
			if err := tr.Scan(&u, &t); err == nil {
				out[idx[u]].TeamIDs = append(out[idx[u]].TeamIDs, t)
			}
		}
		tr.Close()
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// fieldsFor filters active definitions by category and audience (I-9).
func (d *Directory) fieldsFor(r *http.Request, customer bool) ([]FieldDef, error) {
	defs, err := d.FieldDefs(r.Context())
	if err != nil {
		return nil, err
	}
	cat := r.URL.Query().Get("category_id")
	project, _, err := d.projectFilter(r)
	if err != nil {
		return nil, err
	}
	out := []FieldDef{}
	for _, f := range defs {
		if !f.Active || (customer && !f.CustomerVisible) {
			continue
		}
		if cat != "" && !f.AppliesTo(cat, project) {
			continue
		}
		if project != "" && len(f.ProjectIDs) > 0 && !slices.Contains(f.ProjectIDs, project) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (d *Directory) lookupFieldsAgent(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomFieldLookup, authz.OpRead); err != nil {
		return err
	}
	out, err := d.fieldsFor(r, false)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// lookupFieldsPortal exposes only customer-visible definitions (I-9). The
// category scope stays: the new-ticket form loads the definitions once per
// project and shows, requires and sends a field only for the categories it
// applies to (with no category, only unscoped fields apply).
func (d *Directory) lookupFieldsPortal(w http.ResponseWriter, r *http.Request) error {
	out, err := d.fieldsFor(r, true)
	if err != nil {
		return err
	}
	for i := range out {
		out[i].ProjectIDs = nil // the portal asks per project
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}
