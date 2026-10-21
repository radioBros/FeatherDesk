// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// staffInfo is what M-4 needs to know about a staff user.
type staffInfo struct {
	ID     string
	Name   string
	Role   domain.Role
	Tier   int
	Status domain.UserStatus
	Teams  []string
}

// EffectiveTier: supervisors and admins act as tier 3.
func (s staffInfo) EffectiveTier() int {
	if s.Role == domain.RoleAdmin || s.Role == domain.RoleSupervisor {
		return domain.MaxTier
	}
	return s.Tier
}

// InTeam reports team membership.
func (s staffInfo) InTeam(id string) bool {
	for _, t := range s.Teams {
		if t == id {
			return true
		}
	}
	return false
}

// loadStaff reads one staff user with teams (q may be a tx). Customers and
// unknown IDs return store.ErrNotFound.
func loadStaff(ctx context.Context, q store.Querier, id string) (staffInfo, error) {
	var s staffInfo
	var tier *int
	err := q.QueryRowContext(ctx, `SELECT id, name, role, tier, status FROM users WHERE id = ? AND kind = 'staff'`, id).Scan(&s.ID, &s.Name, &s.Role, &tier, &s.Status)
	if err != nil {
		return s, store.NotFound(err)
	}
	if tier != nil {
		s.Tier = *tier
	}
	rows, err := q.QueryContext(ctx, `SELECT team_id FROM team_members WHERE user_id = ? ORDER BY team_id`, id)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return s, err
		}
		s.Teams = append(s.Teams, t)
	}
	return s, rows.Err()
}

// satisfiesM4 reports whether staff s may own a ticket in team at tier (M-4):
// active staff, member of the ticket's team, effective tier ≥ ticket tier.
func satisfiesM4(s staffInfo, teamID string, tier int) bool {
	return s.Status == domain.UserActive && teamID != "" && s.InTeam(teamID) && s.EffectiveTier() >= tier
}
