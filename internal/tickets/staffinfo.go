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
	// Projects are the user's project memberships (D-20, I-11).
	Projects []string
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

// InProject: admins and supervisors work in every project, agents in the
// projects they are members of (I-11).
func (s staffInfo) InProject(id string) bool {
	if s.Role == domain.RoleAdmin || s.Role == domain.RoleSupervisor {
		return true
	}
	for _, p := range s.Projects {
		if p == id {
			return true
		}
	}
	return false
}

// principal is the session principal of the staff user (visibility checks
// on behalf of someone else: mentions, staff e-mail replies).
func (s staffInfo) principal() *domain.Principal {
	return &domain.Principal{UserID: s.ID, Kind: domain.KindStaff, Role: s.Role, Tier: s.Tier, Teams: s.Teams, Projects: s.Projects}
}

// loadStaff reads one staff user with teams and projects (q may be a tx). Customers and
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
	if err := rows.Err(); err != nil {
		return s, err
	}
	rows.Close()
	prs, err := q.QueryContext(ctx, `SELECT project_id FROM project_members WHERE user_id = ? ORDER BY project_id`, id)
	if err != nil {
		return s, err
	}
	defer prs.Close()
	for prs.Next() {
		var p string
		if err := prs.Scan(&p); err != nil {
			return s, err
		}
		s.Projects = append(s.Projects, p)
	}
	return s, prs.Err()
}

// satisfiesM4 reports whether staff s may own ticket t (M-4): active staff,
// member of the ticket's team, effective tier ≥ ticket tier, and (D-20, I-11)
// working in the ticket's project, so an owner can always see the ticket.
func satisfiesM4(s staffInfo, t *ticketRow) bool {
	return s.Status == domain.UserActive && t.TeamID != "" && s.InTeam(t.TeamID) && s.EffectiveTier() >= t.Tier &&
		s.InProject(t.ProjectID)
}

// projectMemberSQL is the SQL twin of staffInfo.InProject for users u (one
// bound arg: the project id).
const projectMemberSQL = `(u.role IN ('admin', 'supervisor') OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = u.id AND pm.project_id = ?))`
