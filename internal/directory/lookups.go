// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"net/http"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
)

// Read-only lookups for pickers and filters (TKT-037). Teams, categories and
// custom-field definitions come from the in-memory snapshot (0 DB reads).

func (d *Directory) lookupTeams(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpRead); err != nil {
		return err
	}
	ts, err := d.Teams(r.Context())
	if err != nil {
		return err
	}
	if ts == nil {
		ts = []Team{}
	}
	dirJSON(w, http.StatusOK, ts)
	return nil
}

func (d *Directory) lookupCategories(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCategories, authz.OpRead); err != nil {
		return err
	}
	cs, err := d.Categories(r.Context())
	if err != nil {
		return err
	}
	if cs == nil {
		cs = []Category{}
	}
	dirJSON(w, http.StatusOK, cs)
	return nil
}

// portalCategories lists active categories only, sorted (TKT-018).
func (d *Directory) portalCategories(w http.ResponseWriter, r *http.Request) error {
	p := dirPrincipal(r)
	if !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeTicketsWrite) {
		return problems.Forbidden("token lacks tickets:read")
	}
	cs, err := d.Categories(r.Context())
	if err != nil {
		return err
	}
	out := []Category{}
	for _, c := range cs {
		if c.Active {
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
	out := []FieldDef{}
	for _, f := range defs {
		if !f.Active || (customer && !f.CustomerVisible) {
			continue
		}
		if cat != "" && !f.AppliesTo(cat) {
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

// lookupFieldsPortal exposes only customer-visible definitions (I-9).
func (d *Directory) lookupFieldsPortal(w http.ResponseWriter, r *http.Request) error {
	out, err := d.fieldsFor(r, true)
	if err != nil {
		return err
	}
	for i := range out {
		out[i].CategoryIDs = nil // routing detail; the portal asks per category
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}
