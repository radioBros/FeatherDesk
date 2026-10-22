// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"net/http"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// staffRefDTO is the StaffRef schema.
type staffRefDTO struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Email   string   `json:"email"`
	Role    string   `json:"role"`
	Tier    int      `json:"tier"`
	TeamIDs []string `json:"team_ids"`
}

// Assignable returns exactly the staff satisfying M-4 for the ticket: active,
// member of its team, effective tier ≥ ticket tier (TKT-007). Triage tickets
// have no team, so nobody is assignable until they are routed.
func (s *Service) Assignable(ctx context.Context, p *domain.Principal, ticketID string) ([]staffRefDTO, error) {
	if !validID(ticketID) {
		return nil, problems.NotFound()
	}
	q := s.db.Read()
	t, err := s.loadVisible(ctx, q, p, "t.id = ?", ticketID)
	if err != nil {
		return nil, err
	}
	out := []staffRefDTO{}
	if t.TeamID == "" {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.role, u.tier FROM users u
		JOIN team_members m ON m.user_id = u.id AND m.team_id = ?
		WHERE u.kind = 'staff' AND u.status = 'active'
		  AND (u.role IN ('admin', 'supervisor') OR u.tier >= ?) AND `+projectMemberSQL+`
		ORDER BY lower(u.name), u.id`, t.TeamID, t.Tier, t.ProjectID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var r staffRefDTO
		if err := rows.Scan(&r.ID, &r.Name, &r.Email, &r.Role, &r.Tier); err != nil {
			rows.Close()
			return nil, err
		}
		r.TeamIDs = []string{}
		out = append(out, r)
		ids = append(ids, r.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	tr, err := q.QueryContext(ctx, `SELECT user_id, team_id FROM team_members WHERE user_id IN (`+placeholders(len(ids))+`) ORDER BY team_id`, anyStrings(ids)...)
	if err != nil {
		return nil, err
	}
	defer tr.Close()
	idx := map[string]int{}
	for i, r := range out {
		idx[r.ID] = i
	}
	for tr.Next() {
		var u, team string
		if err := tr.Scan(&u, &team); err != nil {
			return nil, err
		}
		out[idx[u]].TeamIDs = append(out[idx[u]].TeamIDs, team)
	}
	return out, tr.Err()
}

func (m *Module) agentAssignable(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	id := r.URL.Query().Get("ticket_id")
	if id == "" {
		return problems.Validation(problems.Field("ticket_id", "required", "ticket_id is required"))
	}
	list, err := m.svc.Assignable(r.Context(), principal(r), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, list)
	return nil
}
