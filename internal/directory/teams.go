// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Teams and membership (TKT-017) and categories (TKT-018), supervisor or admin.

type teamIn struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	NotificationEmail *string `json:"notification_email"`
	NotifyMode        string  `json:"notify_mode"`
	CalendarID        *string `json:"calendar_id"`
	// AssignmentStrategy: nil keeps the current value on PATCH (manual on create).
	AssignmentStrategy *string `json:"assignment_strategy"`
	// ProjectIDs (D-20): the projects the team serves. nil keeps the current
	// list on PATCH; omitted on create = the default project.
	ProjectIDs []string `json:"project_ids"`
}

func (d *Directory) validateTeam(ctx context.Context, in *teamIn) []problems.FieldError {
	var errs []problems.FieldError
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		errs = append(errs, problems.Field("name", "length", "1..120 characters"))
	}
	if in.NotifyMode == "" {
		in.NotifyMode = "agents"
	}
	if in.NotifyMode != "agents" && in.NotifyMode != "mailbox" && in.NotifyMode != "both" {
		errs = append(errs, problems.Field("notify_mode", "enum", "agents, mailbox or both"))
	}
	if in.NotificationEmail != nil && strings.TrimSpace(*in.NotificationEmail) == "" {
		in.NotificationEmail = nil
	}
	if in.NotificationEmail != nil {
		e, ok := ParseCustomerEmail(*in.NotificationEmail)
		if !ok {
			errs = append(errs, problems.Field("notification_email", "email", "a valid e-mail address"))
		} else {
			in.NotificationEmail = &e
		}
	}
	if (in.NotifyMode == "mailbox" || in.NotifyMode == "both") && in.NotificationEmail == nil {
		errs = append(errs, problems.Field("notification_email", "required", "required when notify_mode is mailbox or both"))
	}
	if in.AssignmentStrategy != nil {
		switch *in.AssignmentStrategy {
		case "manual", "round_robin", "least_busy":
		default:
			errs = append(errs, problems.Field("assignment_strategy", "enum", "manual, round_robin or least_busy"))
		}
	}
	if in.CalendarID != nil && *in.CalendarID == "" {
		in.CalendarID = nil
	}
	if in.CalendarID != nil {
		if _, ok := d.CalendarByID(ctx, *in.CalendarID); !ok {
			errs = append(errs, problems.Field("calendar_id", "not_found", "unknown calendar"))
		}
	}
	for i, pid := range in.ProjectIDs {
		if pr, err := d.ResolveProject(ctx, pid); err != nil {
			errs = append(errs, problems.Field("project_ids["+itoa(i)+"]", "not_found", "unknown project"))
		} else {
			in.ProjectIDs[i] = pr.ID
		}
	}
	return errs
}

// setTeamProjects replaces the projects team serves; a project whose
// default team no longer serves it loses that default.
func setTeamProjects(ctx context.Context, tx *store.Tx, teamID string, projectIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_teams WHERE team_id = ?`, teamID); err != nil {
		return err
	}
	now := store.Now()
	for _, p := range projectIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_teams (project_id, team_id, created_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, p, teamID, now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE projects SET default_team_id = NULL WHERE default_team_id = ?
		AND NOT EXISTS (SELECT 1 FROM project_teams pt WHERE pt.project_id = projects.id AND pt.team_id = ?)`, teamID, teamID)
	return err
}

func (d *Directory) teamOut(ctx context.Context, id string) (Team, error) {
	t, err := d.Team(ctx, id)
	if err != nil {
		return t, problems.NotFound()
	}
	return t, nil
}

func (d *Directory) listTeams(w http.ResponseWriter, r *http.Request) error {
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

func (d *Directory) getTeam(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpRead); err != nil {
		return err
	}
	t, err := d.teamOut(r.Context(), urlID(r))
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, t)
	return nil
}

func (d *Directory) createTeam(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpWrite); err != nil {
		return err
	}
	var in teamIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if errs := d.validateTeam(ctx, &in); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	id, now := store.NewID(), store.Now()
	strategy := "manual"
	if in.AssignmentStrategy != nil {
		strategy = *in.AssignmentStrategy
	}
	if in.ProjectIDs == nil {
		in.ProjectIDs = []string{}
		if pr, err := d.DefaultProject(ctx); err == nil {
			in.ProjectIDs = []string{pr.ID}
		}
	}
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO teams (id, name, description, notification_email, notify_mode, calendar_id, assignment_strategy, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.Name, in.Description, in.NotificationEmail, in.NotifyMode, in.CalendarID, strategy, now, now)
		if store.IsUniqueViolation(err) {
			return problems.Conflict("a team with this name exists")
		}
		if err != nil {
			return err
		}
		return setTeamProjects(ctx, tx, id, in.ProjectIDs)
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	t, err := d.teamOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, t)
	return nil
}

func (d *Directory) patchTeam(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpWrite); err != nil {
		return err
	}
	var in teamIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	old, err := d.teamOut(ctx, id)
	if err != nil {
		return err
	}
	if errs := d.validateTeam(ctx, &in); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	strategy := old.AssignmentStrategy
	if in.AssignmentStrategy != nil {
		strategy = *in.AssignmentStrategy
	}
	err = d.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE teams SET name = ?, description = ?, notification_email = ?, notify_mode = ?, calendar_id = ?, assignment_strategy = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.Description, in.NotificationEmail, in.NotifyMode, in.CalendarID, strategy, store.Now(), id)
		if store.IsUniqueViolation(err) {
			return problems.Validation(problems.Field("name", "unique", "a team with this name exists"))
		}
		if err != nil || in.ProjectIDs == nil {
			return err
		}
		return setTeamProjects(ctx, tx, id, in.ProjectIDs)
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	if ptrStr(old.CalendarID) != ptrStr(in.CalendarID) {
		d.fireCalendarChanged(ctx, "")
	}
	t, err := d.teamOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, t)
	return nil
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// deleteTeam refuses while tickets reference the team (move them first);
// categories routed to it fall back to triage.
func (d *Directory) deleteTeam(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpWrite); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	var members []string
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE team_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("tickets still belong to this team; transfer them first")
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbound_mailboxes WHERE team_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("an inbound mailbox routes to this team")
		}
		var err error
		if members, err = teamMemberIDs(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE categories SET default_team_id = NULL WHERE default_team_id = ?`, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	d.fireTeamsChanged(members)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func teamMemberIDs(ctx context.Context, q store.Querier, teamID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM team_members WHERE team_id = ?`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// TeamMember is the TeamMember schema.
type TeamMember struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Tier   int    `json:"tier"`
	Source string `json:"source"`
}

func (d *Directory) listMembers(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpRead); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	if _, err := d.teamOut(ctx, id); err != nil {
		return err
	}
	rows, err := d.db.Read().QueryContext(ctx, `SELECT u.id, u.name, u.role, u.tier, m.source FROM team_members m JOIN users u ON u.id = m.user_id
		WHERE m.team_id = ? ORDER BY lower(u.name), u.id`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []TeamMember{}
	for rows.Next() {
		var m TeamMember
		var tier sql.NullInt64
		if err := rows.Scan(&m.UserID, &m.Name, &m.Role, &tier, &m.Source); err != nil {
			return err
		}
		m.Tier = int(tier.Int64)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

func (d *Directory) addMember(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpWrite); err != nil {
		return err
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	if _, err := d.teamOut(ctx, id); err != nil {
		return err
	}
	var m TeamMember
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var kind string
		var tier sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT kind, name, role, tier FROM users WHERE id = ?`, in.UserID).Scan(&kind, &m.Name, &m.Role, &tier)
		if errors.Is(store.NotFound(err), store.ErrNotFound) || !dirValidID(in.UserID) {
			return problems.Validation(problems.Field("user_id", "not_found", "unknown user"))
		}
		if err != nil {
			return err
		}
		if kind != string(domain.KindStaff) {
			return problems.Validation(problems.Field("user_id", "staff", "only staff can be team members"))
		}
		m.UserID, m.Tier, m.Source = in.UserID, int(tier.Int64), "local"
		_, err = tx.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id, source, created_at) VALUES (?, ?, 'local', ?)`, id, in.UserID, store.Now())
		if store.IsUniqueViolation(err) {
			return problems.Conflict("already a member")
		}
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	d.fireTeamsChanged([]string{in.UserID})
	dirJSON(w, http.StatusCreated, m)
	return nil
}

// removeMember drops a local membership; tickets the member owns in the team
// are re-validated against M-4 in the same transaction (TKT-017).
func (d *Directory) removeMember(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResTeams, authz.OpWrite); err != nil {
		return err
	}
	ctx := r.Context()
	id, uid := urlID(r), chi.URLParam(r, "user_id")
	p := dirPrincipal(r)
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var source string
		err := tx.QueryRowContext(ctx, `SELECT source FROM team_members WHERE team_id = ? AND user_id = ?`, id, uid).Scan(&source)
		if err != nil {
			return store.NotFound(err)
		}
		if source == "idp" {
			return problems.Forbidden("memberships from the identity provider are read-only")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ?`, id, uid); err != nil {
			return err
		}
		return d.runMembershipHooks(ctx, tx, p.UserID, id, []string{uid})
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	d.fireTeamsChanged([]string{uid})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- categories ----

type categoryIn struct {
	Name            string  `json:"name"`
	DefaultTeamID   *string `json:"default_team_id"`
	DefaultPriority *string `json:"default_priority"`
	Active          *bool   `json:"active"`
	Sort            *int    `json:"sort"`
	// ProjectID (D-20): the owning project; omitted on create = the default
	// project, omitted on PATCH = unchanged.
	ProjectID *string `json:"project_id"`
}

func (d *Directory) validateCategory(ctx context.Context, in *categoryIn, projectID string) []problems.FieldError {
	var errs []problems.FieldError
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		errs = append(errs, problems.Field("name", "length", "1..120 characters"))
	}
	if in.DefaultTeamID != nil && *in.DefaultTeamID == "" {
		in.DefaultTeamID = nil
	}
	if in.DefaultTeamID != nil {
		if _, err := d.Team(ctx, *in.DefaultTeamID); err != nil {
			errs = append(errs, problems.Field("default_team_id", "not_found", "unknown team"))
		} else if !d.TeamServesProject(ctx, *in.DefaultTeamID, projectID) {
			errs = append(errs, problems.Field("default_team_id", "wrong_project", "the team does not serve the category's project"))
		}
	}
	if in.DefaultPriority != nil && *in.DefaultPriority == "" {
		in.DefaultPriority = nil
	}
	if in.DefaultPriority != nil && !domain.Priority(*in.DefaultPriority).Valid() {
		errs = append(errs, problems.Field("default_priority", "enum", "low, normal, high or urgent"))
	}
	return errs
}

func (d *Directory) categoryOut(ctx context.Context, id string) (Category, error) {
	c, err := d.Category(ctx, id)
	if err != nil {
		return c, problems.NotFound()
	}
	return c, nil
}

// projectFilter resolves the optional `project` query parameter (id or key).
// ok is false when no filter was given.
func (d *Directory) projectFilter(r *http.Request) (id string, ok bool, err error) {
	v := r.URL.Query().Get("project")
	if v == "" {
		return "", false, nil
	}
	pr, perr := d.ResolveProject(r.Context(), v)
	if perr != nil {
		return "", true, problems.Validation(problems.Field("project", "not_found", "unknown project"))
	}
	return pr.ID, true, nil
}

func (d *Directory) listCategories(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, false); err != nil {
		return err
	}
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
		if p.AdminsProject(c.ProjectID) && (!filtered || c.ProjectID == pid) {
			out = append(out, c)
		}
	}
	dirJSON(w, http.StatusOK, out)
	return nil
}

func (d *Directory) createCategory(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, true); err != nil {
		return err
	}
	var in categoryIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := dirPrincipal(r)
	projectID := ""
	if in.ProjectID != nil && *in.ProjectID != "" {
		pr, err := d.ResolveProject(ctx, *in.ProjectID)
		if err != nil {
			return problems.Validation(problems.Field("project_id", "not_found", "unknown project"))
		}
		projectID = pr.ID
	} else if pr, err := d.DefaultProject(ctx); err == nil {
		projectID = pr.ID
	}
	if !p.AdminsProject(projectID) {
		return problems.Forbidden("not a project admin of the category's project")
	}
	if errs := d.validateCategory(ctx, &in, projectID); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	active, sort := true, 0
	if in.Active != nil {
		active = *in.Active
	}
	if in.Sort != nil {
		sort = *in.Sort
	}
	id, now := store.NewID(), store.Now()
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO categories (id, name, default_team_id, default_priority, active, sort, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.Name, in.DefaultTeamID, in.DefaultPriority, active, sort, projectID, now, now)
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	c, err := d.categoryOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, c)
	return nil
}

func (d *Directory) patchCategory(w http.ResponseWriter, r *http.Request) error {
	if err := canProjectConfig(r, true); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	p := dirPrincipal(r)
	cur, err := d.categoryOut(ctx, id)
	if err != nil {
		return err
	}
	if !p.AdminsProject(cur.ProjectID) {
		return problems.NotFound()
	}
	var in categoryIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	projectID := cur.ProjectID
	if in.ProjectID != nil && *in.ProjectID != "" && *in.ProjectID != cur.ProjectID {
		pr, err := d.ResolveProject(ctx, *in.ProjectID)
		if err != nil {
			return problems.Validation(problems.Field("project_id", "not_found", "unknown project"))
		}
		if !p.AdminsProject(pr.ID) {
			return problems.Forbidden("not a project admin of the target project")
		}
		projectID = pr.ID
	}
	if errs := d.validateCategory(ctx, &in, projectID); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	active, sort := cur.Active, cur.Sort
	if in.Active != nil {
		active = *in.Active
	}
	if in.Sort != nil {
		sort = *in.Sort
	}
	err = d.db.Tx(ctx, func(tx *store.Tx) error {
		if projectID != cur.ProjectID {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE category_id = ?`, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return problems.Validation(problems.Field("project_id", "immutable", "a category with tickets cannot move to another project"))
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE categories SET name = ?, default_team_id = ?, default_priority = ?, active = ?, sort = ?, project_id = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.DefaultTeamID, in.DefaultPriority, active, sort, projectID, store.Now(), id)
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	c, err := d.categoryOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, c)
	return nil
}
