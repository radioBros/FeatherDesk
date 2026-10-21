// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// mountAutomation serves the D-19 §A routes: rules and shared macros
// (supervisor or admin), personal macros, and the agent availability flag.
// The macro apply route is mounted with the other ticket routes.
func (m *Module) mountAutomation(g server.Groups) {
	g.Support.Route("/automation/rules", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.listRules))
		r.Method(http.MethodPost, "/", handler(m.createRule))
		r.Method(http.MethodPost, "/reorder", handler(m.reorderRules))
		r.Method(http.MethodGet, "/{id}", handler(m.getRule))
		r.Method(http.MethodPatch, "/{id}", handler(m.updateRule))
		r.Method(http.MethodDelete, "/{id}", handler(m.deleteRule))
		r.Method(http.MethodPost, "/{id}/dry-run", handler(m.dryRunRule))
	})
	g.Support.Route("/macros", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.adminListMacros))
		r.Method(http.MethodPost, "/", handler(m.adminCreateMacro))
		r.Method(http.MethodPatch, "/{id}", handler(m.adminUpdateMacro))
		r.Method(http.MethodDelete, "/{id}", handler(m.adminDeleteMacro))
	})
	g.Agent.Route("/macros", func(r chi.Router) {
		r.Method(http.MethodGet, "/", handler(m.agentListMacros))
		r.Method(http.MethodPost, "/", handler(m.agentCreateMacro))
		r.Method(http.MethodPatch, "/{id}", handler(m.agentUpdateMacro))
		r.Method(http.MethodDelete, "/{id}", handler(m.agentDeleteMacro))
	})
	g.Authed.Method(http.MethodGet, "/me/availability", handler(m.getAvailability))
	g.Authed.Method(http.MethodPatch, "/me/availability", handler(m.setAvailability))
}

// ---- availability (staff only) ----

type availabilityDTO struct {
	Away      bool       `json:"away"`
	AwaySince *time.Time `json:"away_since"`
}

func (s *Service) availability(ctx context.Context, q store.Querier, userID string) (availabilityDTO, error) {
	var since sql.NullTime
	if err := q.QueryRowContext(ctx, `SELECT away_since FROM users WHERE id = ?`, userID).Scan(&since); err != nil {
		return availabilityDTO{}, store.NotFound(err)
	}
	out := availabilityDTO{Away: since.Valid}
	if since.Valid {
		t := since.Time.UTC()
		out.AwaySince = &t
	}
	return out, nil
}

// SetAway marks a staff user away (skipped by assignment strategies) or back.
func (s *Service) SetAway(ctx context.Context, userID string, away bool) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if away {
			_, err := tx.ExecContext(ctx, `UPDATE users SET away_since = ? WHERE id = ? AND kind = 'staff' AND away_since IS NULL`, s.clock(), userID)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET away_since = NULL WHERE id = ? AND kind = 'staff'`, userID)
		return err
	})
}

func (m *Module) getAvailability(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if !p.IsStaff() {
		return problems.NotFound()
	}
	out, err := m.svc.availability(r.Context(), m.svc.db.Read(), p.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (m *Module) setAvailability(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if !p.IsStaff() {
		return problems.NotFound()
	}
	var in struct {
		Away *bool `json:"away"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if in.Away == nil {
		return problems.Validation(problems.Field("away", "required", "true or false"))
	}
	if err := m.svc.SetAway(r.Context(), p.UserID, *in.Away); err != nil {
		return err
	}
	out, err := m.svc.availability(r.Context(), m.svc.db.Read(), p.UserID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}
