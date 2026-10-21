// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/radioBros/SimpleTickets/internal/automation"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
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

const ruleCols = `id, name, event, match_mode, conditions, actions, enabled, stop_processing, position, created_at, updated_at`

func scanRule(r scanner) (*automation.Rule, error) {
	var x automation.Rule
	var conds store.JSON[[]automation.Condition]
	var acts store.JSON[[]automation.Action]
	if err := r.Scan(&x.ID, &x.Name, &x.Event, &x.Match, &conds, &acts, &x.Enabled, &x.StopProcessing, &x.Position, &x.CreatedAt, &x.UpdatedAt); err != nil {
		return nil, store.NotFound(err)
	}
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
}

// validateRule checks the structure, then every reference an action or a
// custom-field condition makes (teams, categories, staff, canned responses,
// custom field keys).
func (s *Service) validateRule(ctx context.Context, r *automation.Rule) error {
	errs := r.Validate()
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
				}
			}
		case automation.ActSetCategory:
			if v := a.Str(); v != "" {
				if _, err := s.dir.Category(ctx, v); err != nil {
					errs = append(errs, problems.Field(path+".value", "not_found", "unknown category"))
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
			var n int
			if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM canned_responses WHERE id = ?`, a.CannedResponseID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				errs = append(errs, problems.Field(path+".canned_response_id", "not_found", "unknown canned response"))
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

// CreateRule appends a rule at the end of the order.
func (s *Service) CreateRule(ctx context.Context, p *domain.Principal, in ruleIn) (*automation.Rule, error) {
	r := &automation.Rule{Match: "all", Enabled: true}
	in.apply(r)
	if err := s.validateRule(ctx, r); err != nil {
		return nil, err
	}
	id, now := store.NewID(), s.clock()
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		var pos int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM automation_rules`).Scan(&pos); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO automation_rules (`+ruleCols+`, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.Name, r.Event, r.Match, store.MustJSON(r.Conditions), store.MustJSON(r.Actions), r.Enabled, r.StopProcessing, pos, now, now, store.Nullable(p.UserID))
		tx.AfterCommit(s.invalidateRules)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.loadRule(ctx, s.db.Read(), id)
}

// UpdateRule changes the given fields of a rule.
func (s *Service) UpdateRule(ctx context.Context, id string, in ruleIn) (*automation.Rule, error) {
	r, err := s.loadRule(ctx, s.db.Read(), id)
	if err != nil {
		return nil, err
	}
	in.apply(r)
	if err := s.validateRule(ctx, r); err != nil {
		return nil, err
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE automation_rules SET name = ?, event = ?, match_mode = ?, conditions = ?, actions = ?, enabled = ?,
			stop_processing = ?, updated_at = ? WHERE id = ?`,
			r.Name, r.Event, r.Match, store.MustJSON(r.Conditions), store.MustJSON(r.Actions), r.Enabled, r.StopProcessing, s.clock(), r.ID)
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

// DeleteRule removes a rule (its time-rule markers cascade).
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	if !validID(id) {
		return problems.NotFound()
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

// ---- HTTP (Support group: supervisor or admin) ----

func adminScope(r *http.Request) error { return requireScope(r, domain.ScopeAdmin) }

func (m *Module) listRules(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	rules, err := loadRules(r.Context(), m.svc.db.Read())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rules)
	return nil
}

func (m *Module) getRule(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
	}
	rule, err := m.svc.loadRule(r.Context(), m.svc.db.Read(), urlParam(r, "id"))
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
	rule, err := m.svc.UpdateRule(r.Context(), urlParam(r, "id"), in)
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
	if err := m.svc.DeleteRule(r.Context(), urlParam(r, "id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *Module) reorderRules(w http.ResponseWriter, r *http.Request) error {
	if err := adminScope(r); err != nil {
		return err
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
	res, err := m.svc.DryRun(r.Context(), urlParam(r, "id"), in.TicketID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}
