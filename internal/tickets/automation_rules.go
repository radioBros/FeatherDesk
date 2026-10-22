// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/automation"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Automation rules CRUD, reorder and the in-memory rules cache (D-19 §A,
// TKT-044). Supervisor or admin (the Support route group). The cache holds
// every rule, prepared (regexes compiled), in position order; it is loaded on
// first use and dropped after each committed write, so the consumer reads the
// store only after a change.

type rulesCache struct {
	mu     sync.RWMutex
	loaded bool
	rules  []*automation.Rule
}

const ruleCols = `id, name, event, match_mode, conditions, actions, enabled, stop_processing, position, created_at, updated_at, project_id`

func scanRule(r scanner) (*automation.Rule, error) {
	var x automation.Rule
	var conds store.JSON[[]automation.Condition]
	var acts store.JSON[[]automation.Action]
	var project sql.NullString
	if err := r.Scan(&x.ID, &x.Name, &x.Event, &x.Match, &conds, &acts, &x.Enabled, &x.StopProcessing, &x.Position, &x.CreatedAt, &x.UpdatedAt, &project); err != nil {
		return nil, store.NotFound(err)
	}
	x.ProjectID = strPtrOf(project)
	x.Conditions, x.Actions = conds.V, acts.V
	if x.Conditions == nil {
		x.Conditions = []automation.Condition{}
	}
	if x.Actions == nil {
		x.Actions = []automation.Action{}
	}
	x.CreatedAt, x.UpdatedAt = x.CreatedAt.UTC(), x.UpdatedAt.UTC()
	x.Prepare()
	return &x, nil
}

func loadRules(ctx context.Context, q store.Querier) ([]*automation.Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+ruleCols+` FROM automation_rules ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*automation.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// automationRules returns every rule (cached, position order). Callers must
// not modify the returned rules.
func (s *Service) automationRules(ctx context.Context) ([]*automation.Rule, error) {
	c := &s.auto.rules
	c.mu.RLock()
	if c.loaded {
		r := c.rules
		c.mu.RUnlock()
		return r, nil
	}
	c.mu.RUnlock()
	rules, err := loadRules(ctx, s.db.Read())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.rules, c.loaded = rules, true
	c.mu.Unlock()
	return rules, nil
}

// enabledRules returns the enabled rules of one event, in order.
func (s *Service) enabledRules(ctx context.Context, event string) ([]*automation.Rule, error) {
	all, err := s.automationRules(ctx)
	if err != nil {
		return nil, err
	}
	var out []*automation.Rule
	for _, r := range all {
		if r.Enabled && r.Event == event {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) invalidateRules() {
	c := &s.auto.rules
	c.mu.Lock()
	c.loaded, c.rules = false, nil
	c.mu.Unlock()
}

// ruleIn is AutomationRuleCreate / AutomationRulePatch.
type ruleIn struct {
	Name           *string                 `json:"name"`
	Event          *string                 `json:"event"`
	Match          *string                 `json:"match"`
	Conditions     *[]automation.Condition `json:"conditions"`
	Actions        *[]automation.Action    `json:"actions"`
	Enabled        *bool                   `json:"enabled"`
	StopProcessing *bool                   `json:"stop_processing"`
	ProjectID      optStr                  `json:"project_id"` // D-20: id or key; null = global
}

func (in ruleIn) apply(r *automation.Rule) {
	if in.Name != nil {
		r.Name = *in.Name
	}
	if in.Event != nil {
		r.Event = *in.Event
	}
	if in.Match != nil {
		r.Match = *in.Match
	}
	if in.Conditions != nil {
		r.Conditions = *in.Conditions
	}
	if in.Actions != nil {
		r.Actions = *in.Actions
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.StopProcessing != nil {
		r.StopProcessing = *in.StopProcessing
	}
	if in.ProjectID.Set {
		r.ProjectID = in.ProjectID.Val
		if r.ProjectID != nil && *r.ProjectID == "" {
			r.ProjectID = nil
		}
	}
}

// validateRule checks the structure, then every reference an action or a
// custom-field condition makes (teams, categories, staff, canned responses,
// custom field keys).
func (s *Service) validateRule(ctx context.Context, r *automation.Rule) error {
	var errs []problems.FieldError
	project := ""
	if r.ProjectID != nil {
		if pr, err := s.dir.ResolveProject(ctx, *r.ProjectID); err != nil {
			errs = append(errs, problems.Field("project_id", "not_found", "unknown project"))
		} else {
			project = pr.ID
			r.ProjectID = &project
		}
	}
	// A `project` condition names projects by id or key; store ids.
	for i := range r.Conditions {
		c := &r.Conditions[i]
		if c.Field != automation.FieldProject {
			continue
		}
		var vals []any
		switch v := c.Value.(type) {
		case string:
			vals = []any{v}
		case []any:
			vals = v
		}
		ids := make([]any, 0, len(vals))
		for _, v := range vals {
			str, _ := v.(string)
			pr, err := s.dir.ResolveProject(ctx, str)
			if err != nil {
				errs = append(errs, problems.Field(fmt.Sprintf("conditions[%d].value", i), "not_found", "unknown project"))
				break
			}
			ids = append(ids, pr.ID)
		}
		if _, single := c.Value.(string); single && len(ids) == 1 {
			c.Value = ids[0]
		} else if len(ids) > 0 {
			c.Value = ids
		}
	}
	errs = append(errs, r.Validate()...)
	for i, c := range r.Conditions {
		if c.Field == automation.FieldCustomField && c.Key != "" {
			if _, ok := s.dir.FieldByKey(ctx, c.Key); !ok {
				errs = append(errs, problems.Field(fmt.Sprintf("conditions[%d].key", i), "not_found", "unknown custom field"))
			}
		}
	}
	q := s.db.Read()
	for i, a := range r.Actions {
		path := fmt.Sprintf("actions[%d]", i)
		switch a.Type {
		case automation.ActSetTeam:
			if v := a.Str(); v != "" {
				if _, err := s.dir.Team(ctx, v); err != nil {
					errs = append(errs, problems.Field(path+".value", "not_found", "unknown team"))
				} else if project != "" && !s.dir.TeamServesProject(ctx, v, project) {
					errs = append(errs, problems.Field(path+".value", "wrong_project", "the team does not serve the rule's project"))
				}
			}
		case automation.ActSetCategory:
			if v := a.Str(); v != "" {
				if c, err := s.dir.Category(ctx, v); err != nil {
					errs = append(errs, problems.Field(path+".value", "not_found", "unknown category"))
				} else if project != "" && c.ProjectID != project {
					errs = append(errs, problems.Field(path+".value", "wrong_project", "the category belongs to another project"))
				}
			}
		case automation.ActAssign, automation.ActAddWatcher:
			if a.UserID == "" {
				continue
			}
			if !validID(a.UserID) {
				errs = append(errs, problems.Field(path+".user_id", "staff", "a staff member"))
				continue
			}
			st, err := loadStaff(ctx, q, a.UserID)
			switch {
			case err != nil:
				errs = append(errs, problems.Field(path+".user_id", "staff", "a staff member"))
			case st.Status != domain.UserActive:
				errs = append(errs, problems.Field(path+".user_id", "inactive", "the user is not active"))
			}
		case automation.ActSendReply:
			if !validID(a.CannedResponseID) {
				errs = append(errs, problems.Field(path+".canned_response_id", "not_found", "unknown canned response"))
				continue
			}
			var cannedProject sql.NullString
			err := q.QueryRowContext(ctx, `SELECT project_id FROM canned_responses WHERE id = ?`, a.CannedResponseID).Scan(&cannedProject)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				errs = append(errs, problems.Field(path+".canned_response_id", "not_found", "unknown canned response"))
			case err != nil:
				return err
			case cannedProject.Valid && cannedProject.String != project:
				// D-20: a project's response is only offered in that project.
				errs = append(errs, problems.Field(path+".canned_response_id", "wrong_project", "the canned response belongs to another project"))
			}
		}
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	return nil
}

func (s *Service) loadRule(ctx context.Context, q store.Querier, id string) (*automation.Rule, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	r, err := scanRule(q.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM automation_rules WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ruleScope checks that p may manage rules of project ("" = global rules,
// supervisors and admins only), D-20.
func ruleScope(p *domain.Principal, project *string) error {
	pid := ""
	if project != nil {
		pid = *project
	}
	if authz.CanProjectConfig(p, authz.PCAutomation, pid) != nil {
		if pid == "" {
			return problems.Forbidden("global rules need a supervisor or admin")
		}
		return problems.Forbidden("not a project admin of the rule's project")
	}
	return nil
}

// CreateRule appends a rule at the end of the order.
func (s *Service) CreateRule(ctx context.Context, p *domain.Principal, in ruleIn) (*automation.Rule, error) {
	r := &automation.Rule{Match: "all", Enabled: true}
	in.apply(r)
	if err := s.validateRule(ctx, r); err != nil {
		return nil, err
	}
	if err := ruleScope(p, r.ProjectID); err != nil {
		return nil, err
	}
	id, now := store.NewID(), s.clock()
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM automation_rules`).Scan(&pos); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO automation_rules (`+ruleCols+`, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.Name, r.Event, r.Match, store.MustJSON(r.Conditions), store.MustJSON(r.Actions), r.Enabled, r.StopProcessing, pos, now, now,
			r.ProjectID, store.Nullable(p.UserID))
		tx.AfterCommit(s.invalidateRules)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.loadRule(ctx, s.db.Read(), id)
}

// UpdateRule changes the given fields of a rule.
func (s *Service) UpdateRule(ctx context.Context, p *domain.Principal, id string, in ruleIn) (*automation.Rule, error) {
	r, err := s.visibleRule(ctx, p, id)
	if err != nil {
		return nil, err
	}
	in.apply(r)
	if err := s.validateRule(ctx, r); err != nil {
		return nil, err
	}
	if err := ruleScope(p, r.ProjectID); err != nil {
		return nil, err
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE automation_rules SET name = ?, event = ?, match_mode = ?, conditions = ?, actions = ?, enabled = ?,
			stop_processing = ?, project_id = ?, updated_at = ? WHERE id = ?`,
			r.Name, r.Event, r.Match, store.MustJSON(r.Conditions), store.MustJSON(r.Actions), r.Enabled, r.StopProcessing, r.ProjectID, s.clock(), r.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		tx.AfterCommit(s.invalidateRules)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.loadRule(ctx, s.db.Read(), r.ID)
}

// visibleRule loads a rule the caller may manage: supervisors and admins
// every rule, project admins those of their projects (others are 404).
func (s *Service) visibleRule(ctx context.Context, p *domain.Principal, id string) (*automation.Rule, error) {
	r, err := s.loadRule(ctx, s.db.Read(), id)
	if err != nil {
		return nil, err
	}
	if !p.IsSupervisorOrAdmin() && (r.ProjectID == nil || !p.AdminsProject(*r.ProjectID)) {
		return nil, problems.NotFound()
	}
	return r, nil
}

// DeleteRule removes a rule (its time-rule markers cascade).
func (s *Service) DeleteRule(ctx context.Context, p *domain.Principal, id string) error {
	r, err := s.visibleRule(ctx, p, id)
	if err != nil {
		return err
	}
	if err := ruleScope(p, r.ProjectID); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM automation_rules WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		tx.AfterCommit(s.invalidateRules)
		return nil
	})
}

// ReorderRules sets the execution order; ids must list every rule once.
func (s *Service) ReorderRules(ctx context.Context, ids []string) ([]*automation.Rule, error) {
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM automation_rules`)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			have[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !have[id] || seen[id] {
				return problems.Validation(problems.Field("ids", "invalid", "every rule id exactly once"))
			}
			seen[id] = true
		}
		if len(seen) != len(have) {
			return problems.Validation(problems.Field("ids", "invalid", "every rule id exactly once"))
		}
		now := s.clock()
		for i, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE automation_rules SET position = ?, updated_at = ? WHERE id = ?`, i+1, now, id); err != nil {
				return err
			}
		}
		tx.AfterCommit(s.invalidateRules)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadRules(ctx, s.db.Read())
}

// ---- HTTP (ProjectAdmin group: supervisors, admins and project admins) ----

func adminScope(r *http.Request) error { return requireScope(r, domain.ScopeAdmin) }

// listRules: supervisors and admins see every rule, project admins the
// rules of their projects; `project` narrows to one project (and its
// global rules are left out).
func (m *Module) listRules(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	p := principal(r)
	filter := ""
	if v := r.URL.Query().Get("project"); v != "" {
		pid, err := m.svc.projectParam(r.Context(), v)
		if err != nil {
			return err
		}
		filter = pid
	}
	rules, err := loadRules(r.Context(), m.svc.db.Read())
	if err != nil {
		return err
	}
	out := make([]*automation.Rule, 0, len(rules))
	for _, rule := range rules {
		pid := ""
		if rule.ProjectID != nil {
			pid = *rule.ProjectID
		}
		if (filter != "" && pid != filter) || (!p.IsSupervisorOrAdmin() && (pid == "" || !p.AdminsProject(pid))) {
			continue
		}
		out = append(out, rule)
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (m *Module) getRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	rule, err := m.svc.visibleRule(r.Context(), principal(r), urlParam(r, "id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rule)
	return nil
}

func (m *Module) createRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	var in ruleIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := m.svc.CreateRule(r.Context(), principal(r), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, rule)
	return nil
}

func (m *Module) updateRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	var in ruleIn
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	rule, err := m.svc.UpdateRule(r.Context(), principal(r), urlParam(r, "id"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rule)
	return nil
}

func (m *Module) deleteRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	if err := m.svc.DeleteRule(r.Context(), principal(r), urlParam(r, "id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *Module) reorderRules(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	// The order is global (every project's rules share it): supervisors and
	// admins only (D-20).
	if !principal(r).IsSupervisorOrAdmin() {
		return problems.Forbidden("the rule order is managed by supervisors and admins")
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if in.IDs == nil {
		return problems.Validation(problems.Field("ids", "required", "every rule id in the new order"))
	}
	rules, err := m.svc.ReorderRules(r.Context(), in.IDs)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rules)
	return nil
}

func (m *Module) dryRunRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	var in struct {
		TicketID string `json:"ticket_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if _, err := m.svc.visibleRule(r.Context(), principal(r), urlParam(r, "id")); err != nil {
		return err
	}
	res, err := m.svc.DryRun(r.Context(), principal(r), urlParam(r, "id"), in.TicketID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}
