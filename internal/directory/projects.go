// SPDX-License-Identifier: AGPL-3.0-only

package directory

// Projects (D-20, PRJ-*): the snapshot, the lookups other modules use
// (routing, I-11, the portal project picker) and the administration routes.
//
//	GET    /admin/projects                    project admins (their projects), supervisors, admins
//	POST   /admin/projects                    supervisors, admins
//	GET    /admin/projects/{id}               project admin of {id}
//	PATCH  /admin/projects/{id}               project admin of {id} (key, active, sort: supervisors+)
//	DELETE /admin/projects/{id}               supervisors, admins (409 while anything references it)
//	GET    /admin/projects/{id}/members       project admin of {id}
//	PUT    /admin/projects/{id}/members       supervisors, admins
//	GET    /admin/projects/{id}/teams         project admin of {id}
//	PUT    /admin/projects/{id}/teams         project admin of {id}
//	GET    /agent/projects                    staff: the projects they work in
//	GET    /portal/projects                   customers: portal-visible projects their organization may use

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Project is a projects row with its team mapping (snapshot and API shape).
type Project struct {
	ID            string    `json:"id"`
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Color         string    `json:"color"`
	Icon          string    `json:"icon"`
	Active        bool      `json:"active"`
	DefaultTeamID *string   `json:"default_team_id"`
	PortalVisible bool      `json:"portal_visible"`
	PortalTitle   *string   `json:"portal_title"`
	PortalIntroMD *string   `json:"portal_intro_md"`
	LogoAssetID   *string   `json:"logo_asset_id"`
	LogoURL       *string   `json:"logo_url"`
	PrimaryColor  *string   `json:"primary_color"`
	Sort          int       `json:"sort"`
	TeamIDs       []string  `json:"team_ids"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProjectRef is the compact project shown next to tickets (ProjectRef schema).
type ProjectRef struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Ref returns the compact reference of the project.
func (p Project) Ref() ProjectRef {
	return ProjectRef{ID: p.ID, Key: p.Key, Name: p.Name, Color: p.Color}
}

// PortalProject is what customers (and the anonymous /p/{key} entry) see of
// a project: no team routing, no membership (PortalProject schema).
type PortalProject struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	Color         string  `json:"color"`
	Icon          string  `json:"icon"`
	PortalTitle   *string `json:"portal_title"`
	PortalIntroMD *string `json:"portal_intro_md"`
	LogoURL       *string `json:"logo_url"`
	PrimaryColor  *string `json:"primary_color"`
}

// Portal returns the customer view of the project.
func (p Project) Portal() PortalProject {
	return PortalProject{ID: p.ID, Key: p.Key, Name: p.Name, Description: p.Description, Color: p.Color, Icon: p.Icon,
		PortalTitle: p.PortalTitle, PortalIntroMD: p.PortalIntroMD, LogoURL: p.LogoURL, PrimaryColor: p.PrimaryColor}
}

func loadProjects(ctx context.Context, q store.Querier, s *dirSnapshot) error {
	rows, err := q.QueryContext(ctx, `SELECT p.id, p.key, p.name, p.description, p.color, p.icon, p.active, p.default_team_id, p.portal_visible,
		p.portal_title, p.portal_intro_md, p.logo_asset_id, p.primary_color, p.sort, p.created_at, p.updated_at, a.blob_key
		FROM projects p LEFT JOIN branding_assets a ON a.id = p.logo_asset_id
		ORDER BY p.sort, lower(p.name), p.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p Project
		var team, title, intro, logo, color, blobKey store.NullString
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.Color, &p.Icon, &p.Active, &team, &p.PortalVisible,
			&title, &intro, &logo, &color, &p.Sort, &p.CreatedAt, &p.UpdatedAt, &blobKey); err != nil {
			rows.Close()
			return err
		}
		p.DefaultTeamID, p.PortalTitle, p.PortalIntroMD, p.LogoAssetID, p.PrimaryColor = dirStrPtr(team), dirStrPtr(title), dirStrPtr(intro), dirStrPtr(logo), dirStrPtr(color)
		if blobKey.Valid && blobKey.String != "" {
			u := "/" + blobKey.String // branding/<sha>.<ext>, served by GET /branding/{file}
			p.LogoURL = &u
		}
		p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
		p.TeamIDs = []string{}
		s.projects = append(s.projects, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range s.projects {
		s.projByID[s.projects[i].ID] = &s.projects[i]
		s.projByKey[s.projects[i].Key] = &s.projects[i]
	}
	rows, err = q.QueryContext(ctx, `SELECT project_id, team_id FROM project_teams ORDER BY team_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, tid string
		if err := rows.Scan(&pid, &tid); err != nil {
			return err
		}
		if p := s.projByID[pid]; p != nil {
			p.TeamIDs = append(p.TeamIDs, tid)
		}
	}
	return rows.Err()
}

// ---- lookups (snapshot, no DB reads) ----

// Projects returns every project (snapshot), in display order.
func (d *Directory) Projects(ctx context.Context) ([]Project, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.projects, nil
}

// Project returns one project, or store.ErrNotFound.
func (d *Directory) Project(ctx context.Context, id string) (Project, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return Project{}, err
	}
	if p, ok := s.projByID[id]; ok {
		return *p, nil
	}
	return Project{}, store.ErrNotFound
}

// ResolveProject finds a project by id or by key (case-insensitive).
func (d *Directory) ResolveProject(ctx context.Context, idOrKey string) (Project, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return Project{}, err
	}
	if p, ok := s.projByID[idOrKey]; ok {
		return *p, nil
	}
	if p, ok := s.projByKey[strings.ToUpper(idOrKey)]; ok {
		return *p, nil
	}
	return Project{}, store.ErrNotFound
}

// DefaultProject is where a ticket goes when neither the caller, the category
// nor the mailbox names a project: the Default project of migration 0140 while
// it is active, else the first active project in display order.
func (d *Directory) DefaultProject(ctx context.Context) (Project, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return Project{}, err
	}
	if p, ok := s.projByID[domain.DefaultProjectID]; ok && p.Active {
		return *p, nil
	}
	for _, p := range s.projects {
		if p.Active {
			return p, nil
		}
	}
	return Project{}, store.ErrNotFound
}

// TeamServesProject reports whether team serves project (project_teams).
func (d *Directory) TeamServesProject(ctx context.Context, teamID, projectID string) bool {
	p, err := d.Project(ctx, projectID)
	return err == nil && slices.Contains(p.TeamIDs, teamID)
}

// ProjectRefOf returns the compact reference of a project; an unknown id
// yields a reference with the id only.
func (d *Directory) ProjectRefOf(ctx context.Context, id string) ProjectRef {
	if p, err := d.Project(ctx, id); err == nil {
		return p.Ref()
	}
	return ProjectRef{ID: id}
}

// StaffProjects lists the projects staff principal p works in: every project
// for supervisors and admins, the memberships for agents (I-11).
func (d *Directory) StaffProjects(ctx context.Context, p *domain.Principal) ([]Project, error) {
	all, err := d.Projects(ctx)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, pr := range all {
		if p.InProject(pr.ID) {
			out = append(out, pr)
		}
	}
	return out, nil
}

// OrgProjectIDs returns the projects an organization is limited to
// (organization_projects); empty = every portal-visible project.
func OrgProjectIDs(ctx context.Context, q store.Querier, orgID string) ([]string, error) {
	out := []string{}
	if orgID == "" {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT project_id FROM organization_projects WHERE organization_id = ? ORDER BY project_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CustomerProjects lists the projects customer p may open tickets in: active,
// portal-visible and, when the organization is limited, in its list (D-20).
func (d *Directory) CustomerProjects(ctx context.Context, p *domain.Principal) ([]Project, error) {
	return d.CustomerProjectsFor(ctx, p.OrganizationID)
}

// CustomerProjectsFor is CustomerProjects for an organization id ("" = none).
func (d *Directory) CustomerProjectsFor(ctx context.Context, orgID string) ([]Project, error) {
	all, err := d.Projects(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := OrgProjectIDs(ctx, d.db.Read(), orgID)
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, pr := range all {
		if !pr.Active || !pr.PortalVisible {
			continue
		}
		if len(allowed) > 0 && !slices.Contains(allowed, pr.ID) {
			continue
		}
		out = append(out, pr)
	}
	return out, nil
}

// PublicProject returns the portal view of an active, portal-visible
// project by key, for the anonymous /p/{key} entry (GET /auth/config?project=).
func (d *Directory) PublicProject(ctx context.Context, key string) (*PortalProject, error) {
	pr, err := d.ResolveProject(ctx, key)
	if err != nil || !pr.Active || !pr.PortalVisible || pr.Key != strings.ToUpper(key) {
		return nil, nil //nolint:nilerr // unknown or hidden projects are simply absent
	}
	v := pr.Portal()
	return &v, nil
}

// ---- membership helpers (staff creation, staff admin) ----

// Membership is one project_members row of a user (ProjectMembership schema).
type Membership struct {
	ProjectID string `json:"project_id"`
	Role      string `json:"role"`
}

// UserMemberships reads the project memberships of a user.
func UserMemberships(ctx context.Context, q store.Querier, userID string) ([]Membership, error) {
	rows, err := q.QueryContext(ctx, `SELECT project_id, role FROM project_members WHERE user_id = ? ORDER BY project_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.ProjectID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// validateMembershipsSQL is ValidateMemberships against the database (the
// staff admin module has no directory snapshot).
func validateMembershipsSQL(ctx context.Context, q store.Querier, field string, in []Membership) ([]problems.FieldError, error) {
	var errs []problems.FieldError
	seen := map[string]bool{}
	for i, m := range in {
		f := field + "[" + itoa(i) + "]"
		var id string
		err := q.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ? OR key = ?`, m.ProjectID, strings.ToUpper(m.ProjectID)).Scan(&id)
		switch {
		case errors.Is(store.NotFound(err), store.ErrNotFound):
			errs = append(errs, problems.Field(f+".project_id", "not_found", "unknown project"))
		case err != nil:
			return nil, err
		case seen[id]:
			errs = append(errs, problems.Field(f+".project_id", "unique", "listed twice"))
		default:
			in[i].ProjectID = id
		}
		seen[id] = true
		if m.Role == "" {
			in[i].Role = string(domain.ProjectMember)
		} else if !domain.ProjectRole(m.Role).Valid() {
			errs = append(errs, problems.Field(f+".role", "enum", "member or project_admin"))
		}
	}
	return errs, nil
}

// SetMemberships replaces the project memberships of a staff user inside tx
// and returns the projects the user left (their tickets there are
// re-validated by the caller through MembershipChanged).
func SetMemberships(ctx context.Context, tx *store.Tx, userID string, in []Membership) (left []string, err error) {
	cur, err := UserMemberships(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	want := map[string]string{}
	for _, m := range in {
		want[m.ProjectID] = m.Role
	}
	for _, m := range cur {
		if _, ok := want[m.ProjectID]; !ok {
			left = append(left, m.ProjectID)
			if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, m.ProjectID, userID); err != nil {
				return nil, err
			}
		}
	}
	now := store.Now()
	for _, m := range in {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, m.ProjectID, userID, m.Role, now); err != nil {
			return nil, err
		}
	}
	return left, nil
}

// JoinDefaultProject makes a new staff user a member of the default project
// (staff created without `projects`): the same project as DefaultProject,
// read from the database inside tx.
func JoinDefaultProject(ctx context.Context, tx *store.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role, created_at)
		SELECT id, ?, 'member', ? FROM projects WHERE id = (`+DefaultProjectSQL+`) ON CONFLICT DO NOTHING`,
		userID, store.Now(), domain.DefaultProjectID)
	return err
}

// DefaultProjectSQL selects the id of the default project (one bound arg:
// domain.DefaultProjectID): the Default project while active, else the
// first active project in display order. It mirrors DefaultProject.
const DefaultProjectSQL = `SELECT id FROM projects WHERE active ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, sort, lower(name), id LIMIT 1`

// ---- administration ----

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

type projectIn struct {
	Key           *string `json:"key"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Color         *string `json:"color"`
	Icon          *string `json:"icon"`
	Active        *bool   `json:"active"`
	DefaultTeamID optID   `json:"default_team_id"`
	PortalVisible *bool   `json:"portal_visible"`
	PortalTitle   optStr  `json:"portal_title"`
	PortalIntroMD optStr  `json:"portal_intro_md"`
	PrimaryColor  optStr  `json:"primary_color"`
	Sort          *int    `json:"sort"`
	// TeamIDs on create: the teams serving the project (default: none).
	TeamIDs []string `json:"team_ids"`
}

// optStr distinguishes an absent string from an explicit null (PATCH).
type optStr struct {
	Set bool
	V   *string
}

func (o *optStr) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.V = nil
		return nil
	}
	var s string
	if err := jsonUnmarshal(b, &s); err != nil {
		return err
	}
	o.V = &s
	return nil
}

// projectAdminDTO is the Project schema plus the administration extras.
type projectAdminDTO struct {
	Project
	KeyLocked   bool `json:"key_locked"`
	MemberCount int  `json:"member_count"`
	TicketCount int  `json:"ticket_count"`
}

func (d *Directory) projectAdminOut(ctx context.Context, q store.Querier, p Project) (projectAdminDTO, error) {
	out := projectAdminDTO{Project: p}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE project_id = ?`, p.ID).Scan(&out.TicketCount); err != nil {
		return out, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id = ?`, p.ID).Scan(&out.MemberCount); err != nil {
		return out, err
	}
	out.KeyLocked = out.TicketCount > 0
	return out, nil
}

// adminProject loads {id} for a caller who must administer it: unknown
// projects and projects the caller does not administer are 404 (§2).
func (d *Directory) adminProject(r *http.Request) (Project, error) {
	p := dirPrincipal(r)
	id := urlID(r)
	pr, err := d.Project(r.Context(), id)
	if err != nil || !p.AdminsProject(pr.ID) {
		return Project{}, problems.NotFound()
	}
	return pr, nil
}

// canProjectConfig: project-admin routes need the admin scope on a PAT for
// writes (like the support-configuration routes) and any of admin or
// directory:read for reads.
func canProjectConfig(r *http.Request, write bool) error {
	p := dirPrincipal(r)
	if !p.IsProjectAdmin() {
		return problems.Forbidden("")
	}
	if write && !authz.HasAnyScope(p, domain.ScopeAdmin) {
		return problems.Forbidden("token lacks admin")
	}
	if !write && !authz.HasAnyScope(p, domain.ScopeAdmin, domain.ScopeDirectoryRead) {
		return problems.Forbidden("token lacks directory:read")
	}
	return nil
}

func (d *Directory) validateProject(ctx context.Context, in *projectIn, create bool) []problems.FieldError {
	var errs []problems.FieldError
	if in.Key != nil {
		k := strings.ToUpper(strings.TrimSpace(*in.Key))
		in.Key = &k
		if !domain.ValidProjectKey(k) {
			errs = append(errs, problems.Field("key", "pattern", "2–10 characters: an upper-case letter, then letters or digits"))
		}
	} else if create {
		errs = append(errs, problems.Field("key", "required", "a project key is required"))
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 {
			errs = append(errs, problems.Field("name", "length", "1..100 characters"))
		}
	} else if create {
		errs = append(errs, problems.Field("name", "required", "a name is required"))
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 2000 {
		errs = append(errs, problems.Field("description", "max_length", "at most 2000 characters"))
	}
	if in.Color != nil && *in.Color != "" && !hexColorRe.MatchString(*in.Color) {
		errs = append(errs, problems.Field("color", "color", "#RRGGBB"))
	}
	if in.Icon != nil && utf8.RuneCountInString(*in.Icon) > 64 {
		errs = append(errs, problems.Field("icon", "max_length", "at most 64 characters"))
	}
	if in.PrimaryColor.Set && in.PrimaryColor.V != nil {
		if *in.PrimaryColor.V == "" {
			in.PrimaryColor.V = nil
		} else if !hexColorRe.MatchString(*in.PrimaryColor.V) {
			errs = append(errs, problems.Field("primary_color", "color", "#RRGGBB"))
		}
	}
	if in.PortalTitle.Set && in.PortalTitle.V != nil {
		t := strings.TrimSpace(*in.PortalTitle.V)
		if t == "" {
			in.PortalTitle.V = nil
		} else if utf8.RuneCountInString(t) > 120 {
			errs = append(errs, problems.Field("portal_title", "max_length", "at most 120 characters"))
		} else {
			in.PortalTitle.V = &t
		}
	}
	if in.PortalIntroMD.Set && in.PortalIntroMD.V != nil {
		if strings.TrimSpace(*in.PortalIntroMD.V) == "" {
			in.PortalIntroMD.V = nil
		} else if utf8.RuneCountInString(*in.PortalIntroMD.V) > 20000 {
			errs = append(errs, problems.Field("portal_intro_md", "max_length", "at most 20000 characters"))
		}
	}
	if in.Sort != nil && (*in.Sort < -100000 || *in.Sort > 100000) {
		errs = append(errs, problems.Field("sort", "range", "-100000..100000"))
	}
	if in.DefaultTeamID.Set && in.DefaultTeamID.Val != nil {
		if _, err := d.Team(ctx, *in.DefaultTeamID.Val); err != nil {
			errs = append(errs, problems.Field("default_team_id", "not_found", "unknown team"))
		}
	}
	for i, t := range in.TeamIDs {
		if _, err := d.Team(ctx, t); err != nil {
			errs = append(errs, problems.Field("team_ids["+itoa(i)+"]", "not_found", "unknown team"))
		}
	}
	return errs
}

// ProjectAdminJSON renders the administration view (Project schema) of a
// project, for routes served by other modules (the logo upload).
func (d *Directory) ProjectAdminJSON(ctx context.Context, id string) (any, error) {
	d.refresh(ctx)
	pr, err := d.Project(ctx, id)
	if err != nil {
		return nil, problems.NotFound()
	}
	return d.projectAdminOut(ctx, d.db.Read(), pr)
}

func (d *Directory) listProjects(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, false); err != nil {
		return err
	}
	p := dirPrincipal(r)
	ctx := r.Context()
	all, err := d.Projects(ctx)
	if err != nil {
		return err
	}
	out := []projectAdminDTO{}
	for _, pr := range all {
		if !p.AdminsProject(pr.ID) {
			continue
		}
		dto, err := d.projectAdminOut(ctx, d.db.Read(), pr)
		if err != nil {
			return err
		}
		out = append(out, dto)
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

func (d *Directory) getProject(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, false); err != nil {
		return err
	}
	pr, err := d.adminProject(r)
	if err != nil {
		return err
	}
	dto, err := d.projectAdminOut(r.Context(), d.db.Read(), pr)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, dto)
	return nil
}

func (d *Directory) createProject(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResProjects, authz.OpWrite); err != nil {
		return err
	}
	var in projectIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if errs := d.validateProject(ctx, &in, true); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	if in.DefaultTeamID.Val != nil && !slices.Contains(in.TeamIDs, *in.DefaultTeamID.Val) {
		in.TeamIDs = append(in.TeamIDs, *in.DefaultTeamID.Val)
	}
	id, now := store.NewID(), store.Now()
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	active, portal, sort := true, true, 0
	if in.Active != nil {
		active = *in.Active
	}
	if in.PortalVisible != nil {
		portal = *in.PortalVisible
	}
	if in.Sort != nil {
		sort = *in.Sort
	}
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO projects (id, key, name, description, color, icon, active, default_team_id, portal_visible,
			portal_title, portal_intro_md, primary_color, sort, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, *in.Key, *in.Name, str(in.Description), str(in.Color), str(in.Icon), active, in.DefaultTeamID.Val, portal,
			in.PortalTitle.V, in.PortalIntroMD.V, in.PrimaryColor.V, sort, now, now)
		if store.IsUniqueViolation(err) {
			return problems.Validation(problems.Field("key", "unique", "this key is already used"))
		}
		if err != nil {
			return err
		}
		// The sequence row exists from the start: the UPDATE … RETURNING of
		// the first ticket takes a row lock instead of racing an INSERT.
		if _, err := tx.ExecContext(ctx, `INSERT INTO counters (name, int_value, text_value) VALUES (?, 0, '') ON CONFLICT DO NOTHING`, store.ProjectSeqCounter(id)); err != nil {
			return err
		}
		return setProjectTeams(ctx, tx, id, in.TeamIDs)
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	pr, err := d.Project(ctx, id)
	if err != nil {
		return err
	}
	dto, err := d.projectAdminOut(ctx, d.db.Read(), pr)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, dto)
	return nil
}

func (d *Directory) patchProject(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, true); err != nil {
		return err
	}
	cur, err := d.adminProject(r)
	if err != nil {
		return err
	}
	var in projectIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := dirPrincipal(r)
	if !p.IsSupervisorOrAdmin() && (in.Key != nil || in.Active != nil || in.Sort != nil || in.TeamIDs != nil) {
		return problems.Forbidden("only supervisors and admins change a project's key, status, order or teams here")
	}
	if in.TeamIDs != nil {
		return problems.Validation(problems.Field("team_ids", "read_only", "use PUT /admin/projects/{id}/teams"))
	}
	if errs := d.validateProject(ctx, &in, false); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	next := cur
	if in.Key != nil {
		next.Key = *in.Key
	}
	if in.Name != nil {
		next.Name = *in.Name
	}
	if in.Description != nil {
		next.Description = strings.TrimSpace(*in.Description)
	}
	if in.Color != nil {
		next.Color = strings.TrimSpace(*in.Color)
	}
	if in.Icon != nil {
		next.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.Active != nil {
		next.Active = *in.Active
	}
	if in.PortalVisible != nil {
		next.PortalVisible = *in.PortalVisible
	}
	if in.Sort != nil {
		next.Sort = *in.Sort
	}
	if in.DefaultTeamID.Set {
		next.DefaultTeamID = in.DefaultTeamID.Val
	}
	if in.PortalTitle.Set {
		next.PortalTitle = in.PortalTitle.V
	}
	if in.PortalIntroMD.Set {
		next.PortalIntroMD = in.PortalIntroMD.V
	}
	if in.PrimaryColor.Set {
		next.PrimaryColor = in.PrimaryColor.V
	}
	if next.DefaultTeamID != nil && !slices.Contains(cur.TeamIDs, *next.DefaultTeamID) {
		return problems.Validation(problems.Field("default_team_id", "wrong_project", "the default team must serve the project"))
	}
	err = d.db.Tx(ctx, func(tx *store.Tx) error {
		if next.Key != cur.Key {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE project_id = ?`, cur.ID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return problems.Validation(problems.Field("key", "immutable", "the key cannot change once the project has tickets"))
			}
		}
		if !next.Active && cur.Active {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE active AND id <> ?`, cur.ID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return problems.Validation(problems.Field("active", "not_allowed", "at least one project must stay active"))
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE projects SET key = ?, name = ?, description = ?, color = ?, icon = ?, active = ?, default_team_id = ?,
			portal_visible = ?, portal_title = ?, portal_intro_md = ?, primary_color = ?, sort = ?, updated_at = ? WHERE id = ?`,
			next.Key, next.Name, next.Description, next.Color, next.Icon, next.Active, next.DefaultTeamID, next.PortalVisible,
			next.PortalTitle, next.PortalIntroMD, next.PrimaryColor, next.Sort, store.Now(), cur.ID)
		if store.IsUniqueViolation(err) {
			return problems.Validation(problems.Field("key", "unique", "this key is already used"))
		}
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	pr, err := d.Project(ctx, cur.ID)
	if err != nil {
		return err
	}
	dto, err := d.projectAdminOut(ctx, d.db.Read(), pr)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, dto)
	return nil
}

// deleteProject removes an unused project. Anything that would lose its
// project (tickets, categories, mailboxes, KB categories) makes it a 409;
// rules, macros, canned responses, SLA overrides, memberships, team and
// organization mappings go with it.
func (d *Directory) deleteProject(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResProjects, authz.OpWrite); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	if _, err := d.Project(ctx, id); err != nil {
		return problems.NotFound()
	}
	var members, logoKeys []string
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		for _, c := range []struct{ q, what string }{
			{`SELECT COUNT(*) FROM tickets WHERE project_id = ?`, "tickets"},
			{`SELECT COUNT(*) FROM categories WHERE project_id = ?`, "categories"},
			{`SELECT COUNT(*) FROM inbound_mailboxes WHERE project_id = ?`, "inbound mailboxes"},
			{`SELECT COUNT(*) FROM kb_categories WHERE project_id = ?`, "KB categories"},
		} {
			var n int
			if err := tx.QueryRowContext(ctx, c.q, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return problems.Conflict("the project still has " + c.what + "; move or delete them, or deactivate the project")
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE active AND id <> ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return problems.Conflict("the last active project cannot be deleted")
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM project_members WHERE project_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				rows.Close()
				return err
			}
			members = append(members, u)
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx, `DELETE FROM counters WHERE name = ?`, store.ProjectSeqCounter(id)); err != nil {
			return err
		}
		var k string
		if err := tx.QueryRowContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = 'logo_light' AND variant = ?`, "project:"+id).Scan(&k); err == nil {
			logoKeys = append(logoKeys, k)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE slot = 'logo_light' AND variant = ?`, "project:"+id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return err
	}
	d.deleteUnreferencedBlobs(ctx, logoKeys)
	d.refresh(ctx)
	d.fireTeamsChanged(members)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ProjectMemberDTO is the ProjectMember schema.
type ProjectMemberDTO struct {
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	StaffRole string    `json:"staff_role"`
	Tier      int       `json:"tier"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (d *Directory) projectMembers(ctx context.Context, q store.Querier, id string) ([]ProjectMemberDTO, error) {
	rows, err := q.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.role, u.tier, m.role, m.created_at FROM project_members m
		JOIN users u ON u.id = m.user_id WHERE m.project_id = ? ORDER BY lower(u.name), u.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectMemberDTO{}
	for rows.Next() {
		var m ProjectMemberDTO
		var tier sql.NullInt64
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.StaffRole, &tier, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Tier, m.CreatedAt = int(tier.Int64), m.CreatedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *Directory) listProjectMembers(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, false); err != nil {
		return err
	}
	pr, err := d.adminProject(r)
	if err != nil {
		return err
	}
	out, err := d.projectMembers(r.Context(), d.db.Read(), pr.ID)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// putProjectMembers replaces the member list. Users who leave lose access
// to the project's tickets at once (I-11): their tickets there are
// re-validated (M-4) and unassigned in the same transaction.
func (d *Directory) putProjectMembers(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResProjects, authz.OpWrite); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	if _, err := d.Project(ctx, id); err != nil {
		return problems.NotFound()
	}
	var in struct {
		Members []struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	if in.Members == nil {
		return problems.Validation(problems.Field("members", "required", "the full member list is required"))
	}
	if len(in.Members) > 5000 {
		return problems.Validation(problems.Field("members", "max_items", "at most 5000 members"))
	}
	p := dirPrincipal(r)
	var changed []string
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var errs []problems.FieldError
		want := map[string]string{}
		for i, m := range in.Members {
			f := "members[" + itoa(i) + "]"
			role := m.Role
			if role == "" {
				role = string(domain.ProjectMember)
			}
			if !domain.ProjectRole(role).Valid() {
				errs = append(errs, problems.Field(f+".role", "enum", "member or project_admin"))
			}
			var kind string
			err := tx.QueryRowContext(ctx, `SELECT kind FROM users WHERE id = ?`, m.UserID).Scan(&kind)
			switch {
			case !dirValidID(m.UserID) || errors.Is(store.NotFound(err), store.ErrNotFound):
				errs = append(errs, problems.Field(f+".user_id", "not_found", "unknown user"))
				continue
			case err != nil:
				return err
			case kind != string(domain.KindStaff):
				errs = append(errs, problems.Field(f+".user_id", "staff", "only staff can be project members"))
			case want[m.UserID] != "":
				errs = append(errs, problems.Field(f+".user_id", "unique", "listed twice"))
			}
			want[m.UserID] = role
		}
		if len(errs) > 0 {
			return problems.Validation(errs...)
		}
		cur := map[string]string{}
		rows, err := tx.QueryContext(ctx, `SELECT user_id, role FROM project_members WHERE project_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u, role string
			if err := rows.Scan(&u, &role); err != nil {
				rows.Close()
				return err
			}
			cur[u] = role
		}
		rows.Close()
		var left []string
		for u := range cur {
			if _, ok := want[u]; !ok {
				left = append(left, u)
				if _, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, id, u); err != nil {
					return err
				}
			}
		}
		now := store.Now()
		for u, role := range want {
			if cur[u] == role {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)
				ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, id, u, role, now); err != nil {
				return err
			}
			changed = append(changed, u)
		}
		slices.Sort(left)
		changed = append(changed, left...)
		if len(left) > 0 {
			return ProjectMembershipChanged(ctx, tx, p.UserID, left)
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.fireTeamsChanged(changed)
	out, err := d.projectMembers(ctx, d.db.Read(), id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// ProjectMembershipChanged must run inside the transaction of any change
// that removes staff from projects: their non-closed tickets that no longer
// satisfy M-4 (which includes project membership, I-11) are unassigned. It
// is MembershipChanged over every team of the users.
func ProjectMembershipChanged(ctx context.Context, tx *store.Tx, actorID string, userIDs []string) error {
	return MembershipChanged(ctx, tx, actorID, "", userIDs)
}

func setProjectTeams(ctx context.Context, tx *store.Tx, projectID string, teamIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_teams WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	now := store.Now()
	for _, t := range teamIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_teams (project_id, team_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, projectID, t, now); err != nil {
			return err
		}
	}
	return nil
}

type projectTeamsDTO struct {
	TeamIDs []string `json:"team_ids"`
}

func (d *Directory) getProjectTeams(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, false); err != nil {
		return err
	}
	pr, err := d.adminProject(r)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, projectTeamsDTO{TeamIDs: pr.TeamIDs})
	return nil
}

func (d *Directory) putProjectTeams(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, true); err != nil {
		return err
	}
	pr, err := d.adminProject(r)
	if err != nil {
		return err
	}
	var in projectTeamsDTO
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	if in.TeamIDs == nil {
		return problems.Validation(problems.Field("team_ids", "required", "the full team list is required"))
	}
	ctx := r.Context()
	var errs []problems.FieldError
	seen := map[string]bool{}
	ids := []string{}
	for i, t := range in.TeamIDs {
		if _, err := d.Team(ctx, t); err != nil {
			errs = append(errs, problems.Field("team_ids["+itoa(i)+"]", "not_found", "unknown team"))
			continue
		}
		if !seen[t] {
			seen[t] = true
			ids = append(ids, t)
		}
	}
	if pr.DefaultTeamID != nil && !seen[*pr.DefaultTeamID] {
		errs = append(errs, problems.Field("team_ids", "required", "the project's default team must stay in the list"))
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	if err := d.db.Tx(ctx, func(tx *store.Tx) error { return setProjectTeams(ctx, tx, pr.ID, ids) }); err != nil {
		return err
	}
	d.refresh(ctx)
	pr, err = d.Project(ctx, pr.ID)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, projectTeamsDTO{TeamIDs: pr.TeamIDs})
	return nil
}

// agentProjectDTO is the AgentProject schema: the project plus the caller's
// role in it.
type agentProjectDTO struct {
	Project
	MyRole   *string `json:"my_role"`
	CanAdmin bool    `json:"can_admin"`
}

func (d *Directory) agentProjects(w http.ResponseWriter, r *http.Request) error {
	p := dirPrincipal(r)
	if !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeTicketsWrite) && !p.HasScope(domain.ScopeDirectoryRead) {
		return problems.Forbidden("token lacks tickets:read")
	}
	ctx := r.Context()
	prs, err := d.StaffProjects(ctx, p)
	if err != nil {
		return err
	}
	out := make([]agentProjectDTO, 0, len(prs))
	for _, pr := range prs {
		dto := agentProjectDTO{Project: pr, CanAdmin: p.AdminsProject(pr.ID)}
		switch {
		case slices.Contains(p.AdminProjects, pr.ID):
			role := string(domain.ProjectAdmin)
			dto.MyRole = &role
		case slices.Contains(p.Projects, pr.ID):
			role := string(domain.ProjectMember)
			dto.MyRole = &role
		}
		out = append(out, dto)
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

func (d *Directory) portalProjects(w http.ResponseWriter, r *http.Request) error {
	p := dirPrincipal(r)
	if !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeTicketsWrite) {
		return problems.Forbidden("token lacks tickets:read")
	}
	prs, err := d.CustomerProjects(r.Context(), p)
	if err != nil {
		return err
	}
	out := make([]PortalProject, 0, len(prs))
	for _, pr := range prs {
		out = append(out, pr.Portal())
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

// deleteUnreferencedBlobs removes branding blobs no branding_assets row
// points at any more (a deleted project's logo).
func (d *Directory) deleteUnreferencedBlobs(ctx context.Context, keys []string) {
	if d.blob == nil {
		return
	}
	for _, k := range keys {
		var n int
		if err := d.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM branding_assets WHERE blob_key = ?`, k).Scan(&n); err == nil && n == 0 {
			if err := d.blob.Delete(context.WithoutCancel(ctx), k); err != nil && !errors.Is(err, blob.ErrNotFound) {
				d.log.Warn("directory: project logo blob delete", "key", k, "err", err)
			}
		}
	}
}
