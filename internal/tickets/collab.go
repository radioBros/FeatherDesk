// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// V1 collaboration features: watchers/CC (TKT-025), tag lookup (TKT-024),
// canned responses (TKT-023), saved views (TKT-026) and CSAT (TKT-027).

// ---- watchers ----

// AddWatcher adds a staff watcher (sees the ticket regardless of tier/team)
// or a customer CC (same organization only, I-3 extension).
func (s *Service) AddWatcher(ctx context.Context, p *domain.Principal, id, userID string) error {
	if err := s.warm(ctx); err != nil {
		return err
	}
	pol := s.Policy(ctx)
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authz.Can(p, authz.ActWatch, t.view(pol, s.clock())); err != nil {
			return err
		}
		var kind, status string
		var org sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT kind, status, organization_id FROM users WHERE id = ?`, userID).Scan(&kind, &status, &org)
		if err != nil || !validID(userID) {
			return problems.Validation(problems.Field("user_id", "not_found", "unknown user"))
		}
		if status != string(domain.UserActive) {
			return problems.Validation(problems.Field("user_id", "inactive", "the user is not active"))
		}
		if kind == string(domain.KindCustomer) && (org.String == "" || org.String != t.OrganizationID) {
			return problems.Validation(problems.Field("user_id", "other_org", "a CC must belong to the ticket's organization"))
		}
		for _, w := range t.Watchers {
			if w == userID {
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, t.ID, userID, s.clock()); err != nil {
			return err
		}
		t.Watchers = append(t.Watchers, userID)
		m := &mutation{t: t}
		m.add(events.TicketWatchersChanged, map[string]any{"added": userID})
		return s.flush(ctx, tx, m, p.UserID)
	})
}

// RemoveWatcher removes a watcher or CC.
func (s *Service) RemoveWatcher(ctx context.Context, p *domain.Principal, id, userID string) error {
	if err := s.warm(ctx); err != nil {
		return err
	}
	pol := s.Policy(ctx)
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authz.Can(p, authz.ActWatch, t.view(pol, s.clock())); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?`, t.ID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		if err := loadWatchers(ctx, tx, t); err != nil {
			return err
		}
		m := &mutation{t: t}
		m.add(events.TicketWatchersChanged, map[string]any{"removed": userID})
		return s.flush(ctx, tx, m, p.UserID)
	})
}

func (m *Module) agentAddWatcher(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in struct {
		UserID string `json:"user_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	if err := m.svc.AddWatcher(r.Context(), p, urlParam(r, "id"), in.UserID); err != nil {
		return err
	}
	res, err := m.svc.UpdateResult(r.Context(), p, urlParam(r, "id"), nil)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res.Ticket)
	return nil
}

func (m *Module) agentRemoveWatcher(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	if err := m.svc.RemoveWatcher(r.Context(), principal(r), urlParam(r, "id"), urlParam(r, "user_id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- tags ----

type tagDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UsageCount int    `json:"usage_count"`
}

func (m *Module) agentTags(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	where, args := "1=1", []any{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where = `lower(g.name) LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(strings.ToLower(q))+"%")
	}
	rows, err := m.svc.db.Read().QueryContext(r.Context(), `SELECT g.id, g.name, (SELECT COUNT(*) FROM ticket_tags tt WHERE tt.tag_id = g.id) AS n
		FROM tags g WHERE `+where+` ORDER BY n DESC, g.name LIMIT 50`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []tagDTO{}
	for rows.Next() {
		var t tagDTO
		if err := rows.Scan(&t.ID, &t.Name, &t.UsageCount); err != nil {
			return err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// ---- canned responses ----

type cannedDTO struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	BodyMD    string    `json:"body_md"`
	TeamID    *string   `json:"team_id"`
	ProjectID *string   `json:"project_id"` // D-20: nil = every project
	CreatedBy *userRef  `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Service) listCanned(ctx context.Context, where string, args []any) ([]cannedDTO, error) {
	q := s.db.Read()
	rows, err := q.QueryContext(ctx, `SELECT id, title, body_md, team_id, created_by, created_at, updated_at, project_id FROM canned_responses WHERE `+where+` ORDER BY lower(title), id`, args...)
	if err != nil {
		return nil, err
	}
	out := []cannedDTO{}
	var authors []string
	creators := map[int]string{}
	for rows.Next() {
		var c cannedDTO
		var team, by, project sql.NullString
		if err := rows.Scan(&c.ID, &c.Title, &c.BodyMD, &team, &by, &c.CreatedAt, &c.UpdatedAt, &project); err != nil {
			rows.Close()
			return nil, err
		}
		c.TeamID, c.CreatedAt, c.UpdatedAt = strOrNil(team.String), c.CreatedAt.UTC(), c.UpdatedAt.UTC()
		c.ProjectID = strOrNil(project.String)
		if by.Valid {
			creators[len(out)] = by.String
			authors = append(authors, by.String)
		}
		out = append(out, c)
	}
	rows.Close()
	refs, err := loadUserRefs(ctx, q, authors)
	if err != nil {
		return nil, err
	}
	for i, a := range creators {
		if r, ok := refs[a]; ok {
			r := r
			out[i].CreatedBy = &r
		}
	}
	return out, nil
}

// agentCanned lists global responses plus those of the caller's teams
// (supervisors and admins see all), optionally filtered by title (TKT-023).
// Project responses (D-20) are offered in the projects the caller works in;
// `project` narrows to one project's responses plus the unscoped ones.
func (m *Module) agentCanned(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	p := principal(r)
	where, args := "1=1", []any{}
	if !p.IsSupervisorOrAdmin() {
		where = "team_id IS NULL"
		if len(p.Teams) > 0 {
			where = "(team_id IS NULL OR team_id IN (" + placeholders(len(p.Teams)) + "))"
			args = anyStrings(p.Teams)
		}
		if len(p.Projects) > 0 {
			where += " AND (project_id IS NULL OR project_id IN (" + placeholders(len(p.Projects)) + "))"
			args = append(args, anyStrings(p.Projects)...)
		} else {
			where += " AND project_id IS NULL"
		}
	}
	if v := r.URL.Query().Get("project"); v != "" {
		pid, err := m.svc.projectParam(r.Context(), v)
		if err != nil {
			return err
		}
		where += " AND (project_id IS NULL OR project_id = ?)"
		args = append(args, pid)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where += ` AND lower(title) LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(strings.ToLower(q))+"%")
	}
	out, err := m.svc.listCanned(r.Context(), where, args)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// adminListCanned: every response for supervisors and admins; project
// admins see the responses of the projects they administer (D-20).
func (m *Module) adminListCanned(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if authz.CanAdmin(p, authz.ResCannedGlobal, authz.OpRead) != nil {
		return problems.Forbidden("")
	}
	where, args := "1=1", []any{}
	if !p.IsSupervisorOrAdmin() {
		if len(p.AdminProjects) == 0 {
			where = "1=0"
		} else {
			where, args = "project_id IN ("+placeholders(len(p.AdminProjects))+")", anyStrings(p.AdminProjects)
		}
	}
	out, err := m.svc.listCanned(r.Context(), where, args)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

type cannedIn struct {
	Title     string  `json:"title"`
	BodyMD    string  `json:"body_md"`
	TeamID    *string `json:"team_id"`
	ProjectID *string `json:"project_id"` // D-20: id or key; nil = every project
}

// canWriteCanned decides a change of a response scoped to team and project
// (either may be ""): a team response needs the team (and, for agents, the
// project to be one of theirs), a project response without team a project
// admin, a response without either a supervisor (D-20).
func canWriteCanned(p *domain.Principal, team, project string) error {
	switch {
	case team != "":
		if authz.CanWriteTeamCanned(p, team) != nil {
			return problems.Forbidden("not a member of the team")
		}
		if project != "" && !p.InProject(project) {
			return problems.Forbidden("not a member of the project")
		}
	case project != "":
		if authz.CanProjectConfig(p, authz.PCCanned, project) != nil {
			return problems.Forbidden("not a project admin of the response's project")
		}
	default:
		if authz.CanAdmin(p, authz.ResCannedGlobal, authz.OpWrite) != nil {
			return problems.Forbidden("global canned responses need a supervisor")
		}
	}
	return nil
}

func (m *Module) validCanned(ctx context.Context, r *http.Request, in *cannedIn) error {
	var errs []problems.FieldError
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 120 {
		errs = append(errs, problems.Field("title", "length", "1..120 characters"))
	}
	if strings.TrimSpace(in.BodyMD) == "" || len(in.BodyMD) > MaxBodyBytes {
		errs = append(errs, problems.Field("body_md", "length", "1..64 KiB"))
	}
	if in.TeamID != nil && *in.TeamID == "" {
		in.TeamID = nil
	}
	if in.TeamID != nil {
		if _, err := m.dir.Team(ctx, *in.TeamID); err != nil {
			errs = append(errs, problems.Field("team_id", "not_found", "unknown team"))
		}
	}
	if in.ProjectID != nil && *in.ProjectID == "" {
		in.ProjectID = nil
	}
	if in.ProjectID != nil {
		if pr, err := m.dir.ResolveProject(ctx, *in.ProjectID); err != nil {
			errs = append(errs, problems.Field("project_id", "not_found", "unknown project"))
		} else {
			id := pr.ID
			in.ProjectID = &id
		}
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	return canWriteCanned(principal(r), ptrOr(in.TeamID), ptrOr(in.ProjectID))
}

func ptrOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (m *Module) cannedOne(ctx context.Context, id string) (*cannedDTO, error) {
	out, err := m.svc.listCanned(ctx, "id = ?", []any{id})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, problems.NotFound()
	}
	return &out[0], nil
}

func (m *Module) adminCreateCanned(w http.ResponseWriter, r *http.Request) error {
	var in cannedIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := m.validCanned(ctx, r, &in); err != nil {
		return err
	}
	id, now := store.NewID(), store.Now()
	err := m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO canned_responses (id, title, body_md, team_id, project_id, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, in.Title, in.BodyMD, in.TeamID, in.ProjectID, principal(r).UserID, now, now)
		return err
	})
	if err != nil {
		return err
	}
	c, err := m.cannedOne(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, c)
	return nil
}

func (m *Module) adminUpdateCanned(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur, err := m.cannedOne(ctx, urlParam(r, "id"))
	if err != nil {
		return err
	}
	var in cannedIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if err := m.validCanned(ctx, r, &in); err != nil {
		return err
	}
	// Moving or editing an existing response also needs rights on its current
	// scope: a global response stays supervisor+ even when moved to a team.
	if err := canWriteCanned(principal(r), ptrOr(cur.TeamID), ptrOr(cur.ProjectID)); err != nil {
		return err
	}
	err = m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE canned_responses SET title = ?, body_md = ?, team_id = ?, project_id = ?, updated_at = ? WHERE id = ?`,
			in.Title, in.BodyMD, in.TeamID, in.ProjectID, store.Now(), cur.ID)
		return err
	})
	if err != nil {
		return err
	}
	c, err := m.cannedOne(ctx, cur.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

func (m *Module) adminDeleteCanned(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cur, err := m.cannedOne(ctx, urlParam(r, "id"))
	if err != nil {
		return err
	}
	if err := canWriteCanned(principal(r), ptrOr(cur.TeamID), ptrOr(cur.ProjectID)); err != nil {
		return err
	}
	err = m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM canned_responses WHERE id = ?`, cur.ID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- agent canned responses (TKT-023: own-team responses + preview) ----

// agentCannedOne loads a canned response the caller may use: global ones and
// those of the caller's teams (supervisors and admins: all). Others are 404,
// like tickets outside the caller's visibility.
func (m *Module) agentCannedOne(r *http.Request, id string) (*cannedDTO, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	c, err := m.cannedOne(r.Context(), id)
	if err != nil {
		return nil, err
	}
	p := principal(r)
	if c.TeamID != nil && !p.IsSupervisorOrAdmin() && !p.InTeam(*c.TeamID) {
		return nil, problems.NotFound()
	}
	if c.ProjectID != nil && !p.InProject(*c.ProjectID) {
		return nil, problems.NotFound()
	}
	return c, nil
}

// agentCreateCanned: agents create responses for their own teams; a global
// response (no team_id) still needs a supervisor (validCanned → 403).
func (m *Module) agentCreateCanned(w http.ResponseWriter, r *http.Request) error {
	return m.adminCreateCanned(w, r)
}

func (m *Module) agentUpdateCanned(w http.ResponseWriter, r *http.Request) error {
	if _, err := m.agentCannedOne(r, urlParam(r, "id")); err != nil {
		return err
	}
	return m.adminUpdateCanned(w, r)
}

func (m *Module) agentDeleteCanned(w http.ResponseWriter, r *http.Request) error {
	if _, err := m.agentCannedOne(r, urlParam(r, "id")); err != nil {
		return err
	}
	return m.adminDeleteCanned(w, r)
}

// agentPreviewCanned renders the placeholders of a canned response for a
// visible ticket: {{customer.name}}, {{ticket.number}}, {{ticket.ref}}, {{agent.name}};
// unknown placeholders stay literal.
func (m *Module) agentPreviewCanned(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	c, err := m.agentCannedOne(r, urlParam(r, "id"))
	if err != nil {
		return err
	}
	tid := r.URL.Query().Get("ticket_id")
	if !validID(tid) {
		return problems.Validation(problems.Field("ticket_id", "required", "a ticket id"))
	}
	vars, err := m.svc.CannedVars(r.Context(), principal(r), tid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"canned_id": c.ID, "ticket_id": tid, "body_md": RenderCanned(c.BodyMD, vars)})
	return nil
}

var placeholderRe = regexp.MustCompile(`\{\{\s*([a-z_]+\.[a-z_]+)\s*\}\}`)

// RenderCanned substitutes the known placeholders ({{customer.name}}, alias
// {{requester.name}}; {{ticket.number}}; {{agent.name}}); unknown
// placeholders stay literal.
func RenderCanned(body string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(body, func(m string) string {
		key := placeholderRe.FindStringSubmatch(m)[1]
		if v, ok := vars[key]; ok {
			return v
		}
		return m
	})
}

// CannedVars returns the placeholder values for a ticket and agent.
func (s *Service) CannedVars(ctx context.Context, p *domain.Principal, ticketID string) (map[string]string, error) {
	q := s.db.Read()
	t, err := s.loadVisible(ctx, q, p, "t.id = ?", ticketID)
	if err != nil {
		return nil, err
	}
	refs, err := loadUserRefs(ctx, q, []string{t.RequesterID, p.UserID})
	if err != nil {
		return nil, err
	}
	// {{customer.name}} is canonical; {{requester.name}} is an accepted alias.
	name := refs[t.RequesterID].Name
	s.fillKey(ctx, t)
	return map[string]string{"customer.name": name, "requester.name": name, "ticket.number": strconv.FormatInt(t.Number, 10),
		"ticket.ref": t.Ref(), "agent.name": refs[p.UserID].Name}, nil
}

// ---- saved views ----

type viewDTO struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Filter    map[string]any `json:"filter"`
	Shared    bool           `json:"shared"`
	UserID    string         `json:"user_id"`
	CreatedAt time.Time      `json:"created_at"`
}

type viewIn struct {
	Name   string         `json:"name"`
	Filter map[string]any `json:"filter"`
	Shared bool           `json:"shared"`
}

// viewQuery converts a stored filter into the list query parameters.
func viewQuery(filter map[string]any) url.Values {
	v := url.Values{}
	for k, x := range filter {
		switch t := x.(type) {
		case string:
			v.Set(k, t)
		case float64:
			v.Set(k, strconv.FormatFloat(t, 'f', -1, 64))
		case bool:
			v.Set(k, strconv.FormatBool(t))
		case []any:
			parts := make([]string, 0, len(t))
			for _, e := range t {
				parts = append(parts, fmt.Sprint(e))
			}
			v.Set(k, strings.Join(parts, ","))
		default:
			v.Set(k, fmt.Sprint(t))
		}
	}
	return v
}

func (m *Module) validView(r *http.Request, in *viewIn) error {
	var errs []problems.FieldError
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 80 {
		errs = append(errs, problems.Field("name", "length", "1..80 characters"))
	}
	if in.Filter == nil {
		errs = append(errs, problems.Field("filter", "required", "a filter object"))
	} else {
		for _, k := range []string{"cursor", "limit"} {
			delete(in.Filter, k)
		}
		if _, err := ParseAgentListQuery(viewQuery(in.Filter)); err != nil {
			if pe, ok := err.(*problems.Error); ok {
				for _, f := range pe.Fields {
					errs = append(errs, problems.Field("filter."+f.Field, f.Code, f.Message))
				}
			} else {
				return err
			}
		}
		for k := range in.Filter {
			if strings.HasPrefix(k, "cf.") {
				if _, _, err := m.svc.cfFilter(r.Context(), principal(r), k[3:], viewQuery(in.Filter).Get(k)); err != nil {
					errs = append(errs, problems.Field("filter."+k, "invalid", "invalid custom field filter"))
				}
			}
		}
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	if in.Shared && !principal(r).IsSupervisorOrAdmin() {
		return problems.Forbidden("shared views need a supervisor")
	}
	return nil
}

func (s *Service) views(ctx context.Context, where string, args ...any) ([]viewDTO, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, name, filter, shared, user_id, created_at FROM saved_views WHERE `+where+` ORDER BY lower(name), id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []viewDTO{}
	for rows.Next() {
		var v viewDTO
		var f store.JSON[map[string]any]
		if err := rows.Scan(&v.ID, &v.Name, &f, &v.Shared, &v.UserID, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Filter, v.CreatedAt = f.V, v.CreatedAt.UTC()
		if v.Filter == nil {
			v.Filter = map[string]any{}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// view returns a view the caller may read (own or shared) or 404.
func (m *Module) view(r *http.Request) (*viewDTO, error) {
	id := urlParam(r, "id")
	if !validID(id) {
		return nil, problems.NotFound()
	}
	vs, err := m.svc.views(r.Context(), "id = ? AND (user_id = ? OR shared)", id, principal(r).UserID)
	if err != nil {
		return nil, err
	}
	if len(vs) == 0 {
		return nil, problems.NotFound()
	}
	return &vs[0], nil
}

func (m *Module) listViews(w http.ResponseWriter, r *http.Request) error {
	vs, err := m.svc.views(r.Context(), "user_id = ? OR shared", principal(r).UserID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, vs)
	return nil
}

func (m *Module) getView(w http.ResponseWriter, r *http.Request) error {
	v, err := m.view(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (m *Module) createView(w http.ResponseWriter, r *http.Request) error {
	var in viewIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if err := m.validView(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id, now := store.NewID(), store.Now()
	err := m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO saved_views (id, user_id, name, filter, shared, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, principal(r).UserID, in.Name, store.MustJSON(in.Filter), in.Shared, now, now)
		return err
	})
	if err != nil {
		return err
	}
	vs, err := m.svc.views(ctx, "id = ?", id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, vs[0])
	return nil
}

func (m *Module) updateView(w http.ResponseWriter, r *http.Request) error {
	v, err := m.view(r)
	if err != nil {
		return err
	}
	p := principal(r)
	if v.UserID != p.UserID && !p.IsAdmin() {
		return problems.Forbidden("only the owner edits a view")
	}
	var in viewIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if err := m.validView(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	err = m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE saved_views SET name = ?, filter = ?, shared = ?, updated_at = ? WHERE id = ?`, in.Name, store.MustJSON(in.Filter), in.Shared, store.Now(), v.ID)
		return err
	})
	if err != nil {
		return err
	}
	vs, err := m.svc.views(ctx, "id = ?", v.ID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, vs[0])
	return nil
}

func (m *Module) deleteView(w http.ResponseWriter, r *http.Request) error {
	v, err := m.view(r)
	if err != nil {
		return err
	}
	p := principal(r)
	if v.UserID != p.UserID && !p.IsAdmin() {
		return problems.Forbidden("only the owner deletes a view")
	}
	ctx := r.Context()
	err = m.svc.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM saved_views WHERE id = ?`, v.ID)
		return err
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- CSAT ----

const csatPurpose = "csat-token"

// CSATTokenTTL bounds how long the link in the resolve e-mail works.
const CSATTokenTTL = 30 * 24 * time.Hour

// CSATToken returns the signed, single-use rating token for the current
// resolution of a ticket (embedded by notify in customer.resolved mails).
// It binds the ticket, its requester and resolved_at, so a reopen/re-resolve
// invalidates older links; it is spent once a score is stored.
func (s *Service) CSATToken(ctx context.Context, ticketID string) (string, error) {
	var req string
	var resolved sql.NullTime
	if err := s.db.Read().QueryRowContext(ctx, `SELECT requester_id, resolved_at FROM tickets WHERE id = ?`, ticketID).Scan(&req, &resolved); err != nil {
		return "", store.NotFound(err)
	}
	if !resolved.Valid {
		return "", problems.Conflict("the ticket is not resolved")
	}
	exp := s.clock().Add(CSATTokenTTL).Unix()
	msg := fmt.Sprintf("%s|%s|%d|%d", ticketID, req, resolved.Time.UTC().UnixMicro(), exp)
	mac := s.box.MAC(csatPurpose, []byte(msg))
	return base64.RawURLEncoding.EncodeToString([]byte(msg)) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// verifyCSATToken returns the requester the token was issued to.
func (s *Service) verifyCSATToken(ctx context.Context, ticketID, token string) (string, error) {
	bad := problems.Unauthenticated()
	m, sig, ok := strings.Cut(token, ".")
	msg, err1 := base64.RawURLEncoding.DecodeString(m)
	mac, err2 := base64.RawURLEncoding.DecodeString(sig)
	if !ok || err1 != nil || err2 != nil || !s.box.VerifyMAC(csatPurpose, msg, mac) {
		return "", bad
	}
	parts := strings.Split(string(msg), "|")
	if len(parts) != 4 || parts[0] != ticketID {
		return "", bad
	}
	exp, _ := strconv.ParseInt(parts[3], 10, 64)
	if s.clock().Unix() > exp {
		return "", bad
	}
	var resolved sql.NullTime
	if err := s.db.Read().QueryRowContext(ctx, `SELECT resolved_at FROM tickets WHERE id = ?`, ticketID).Scan(&resolved); err != nil {
		return "", problems.NotFound()
	}
	if !resolved.Valid || strconv.FormatInt(resolved.Time.UTC().UnixMicro(), 10) != parts[2] {
		return "", problems.Conflict("the rating link belongs to an earlier resolution")
	}
	return parts[1], nil
}

// RateTicket stores a CSAT score (1..5) and optional comment once per
// resolution cycle, requester only (TKT-027). The comment becomes an
// internal system message; ticket.csat carries the score.
func (s *Service) RateTicket(ctx context.Context, p *domain.Principal, id string, score int, comment string) error {
	if score < 1 || score > 5 {
		return problems.Validation(problems.Field("score", "range", "1..5"))
	}
	if utf8.RuneCountInString(comment) > 2000 {
		return problems.Validation(problems.Field("comment", "max_length", "at most 2000 characters"))
	}
	if err := s.warm(ctx); err != nil {
		return err
	}
	pol := s.Policy(ctx)
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		v := t.view(pol, s.clock())
		if !authz.CanSeeTicket(p, v) {
			return problems.NotFound()
		}
		if err := authz.Can(p, authz.ActCSAT, v); err != nil {
			if t.RequesterID != p.UserID {
				return err // 403: only the requester rates
			}
			return problems.Conflict("rating is possible once, after resolution")
		}
		t.CSATScore, t.CSATComment = score, strings.TrimSpace(comment)
		if t.CSATComment != "" {
			if err := insertMessage(ctx, tx, &msgRow{TicketID: t.ID, AuthorID: p.UserID, Kind: domain.MessageSystem, Channel: domain.ChannelPortal,
				Body: fmt.Sprintf("CSAT %d/5: %s", score, t.CSATComment), CreatedAt: s.clock()}); err != nil {
				return err
			}
		}
		m := &mutation{t: t}
		m.add(events.TicketCSAT, map[string]any{"score": score})
		return s.flush(ctx, tx, m, p.UserID)
	})
}

// portalCSAT accepts a customer session/PAT or a signed csat_token.
func (m *Module) portalCSAT(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Score     int    `json:"score"`
		Comment   string `json:"comment"`
		CSATToken string `json:"csat_token"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlParam(r, "id")
	if !validID(id) {
		return problems.NotFound()
	}
	p := principal(r)
	switch {
	case in.CSATToken != "":
		uid, err := m.svc.verifyCSATToken(ctx, id, in.CSATToken)
		if err != nil {
			return err
		}
		c, err := loadCustomerPrincipal(ctx, m.svc.db.Read(), uid)
		if err != nil {
			return err
		}
		p = c
	case p == nil:
		return problems.Unauthenticated()
	case !p.IsCustomer():
		return problems.NotFound()
	case !p.HasScope(domain.ScopeTicketsWrite):
		return problems.Forbidden("token lacks tickets:write")
	}
	if err := m.svc.RateTicket(ctx, p, id, in.Score, in.Comment); err != nil {
		return err
	}
	d, err := m.svc.CustomerTicket(ctx, p, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

func loadCustomerPrincipal(ctx context.Context, q store.Querier, id string) (*domain.Principal, error) {
	var role, status string
	var org sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT role, status, organization_id FROM users WHERE id = ? AND kind = 'customer'`, id).Scan(&role, &status, &org); err != nil {
		return nil, problems.Unauthenticated()
	}
	if status == string(domain.UserDisabled) {
		return nil, problems.Unauthenticated()
	}
	return &domain.Principal{UserID: id, Kind: domain.KindCustomer, Role: domain.Role(role), OrganizationID: org.String}, nil
}
