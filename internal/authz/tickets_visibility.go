// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"strings"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// TicketPredicate returns the SQL visibility predicate (TKT-001) for the
// tickets table aliased as alias, plus its arguments. Every list, count,
// search, single-ticket load, attachment load, WebSocket subscribe/resume and
// assignable query composes it, so visibility is decided in SQL, never by
// filtering rows after reading them (permissions_spec §6.3).
//
//	staff agent:  project ∈ my projects (I-11, D-20) AND
//	              (assignee = me OR (team ∈ my teams AND tier ≤ eff) OR
//	               (team IS NULL AND triage visible AND tier ≤ eff) OR me ∈ watchers)
//	sup/admin:    TRUE
//	customer:     requester = me OR (org_manager AND org = my org) OR me ∈ watchers (CC)
//
// It must agree with CanSeeTicket (TestCanRead_AgreesWithVisibilityPredicate).
func TicketPredicate(p *domain.Principal, pol TicketPolicy, alias string) (string, []any) {
	if alias == "" {
		alias = "t"
	}
	col := func(c string) string { return alias + "." + c }
	if p == nil || !canReadTickets(p) {
		return "1=0", nil
	}
	watcher := "EXISTS (SELECT 1 FROM ticket_watchers tw WHERE tw.ticket_id = " + col("id") + " AND tw.user_id = ?)"
	switch p.Kind {
	case domain.KindStaff:
		if p.IsSupervisorOrAdmin() {
			return "1=1", nil
		}
		// I-11: an agent sees nothing outside their projects, whatever the
		// team, tier, assignee or watcher rules say.
		if len(p.Projects) == 0 {
			return "1=0", nil
		}
		eff := p.EffectiveTier()
		parts := []string{col("assignee_id") + " = ?"}
		args := []any{p.UserID}
		if len(p.Teams) > 0 {
			parts = append(parts, "("+col("team_id")+" IN ("+ticketPlaceholders(len(p.Teams))+") AND "+col("tier")+" <= ?)")
			for _, t := range p.Teams {
				args = append(args, t)
			}
			args = append(args, eff)
		}
		if pol.TriageVisibleToAgents {
			parts = append(parts, "("+col("team_id")+" IS NULL AND "+col("tier")+" <= ?)")
			args = append(args, eff)
		}
		parts = append(parts, watcher)
		args = append(args, p.UserID)
		proj := make([]any, 0, len(p.Projects)+len(args))
		for _, id := range p.Projects {
			proj = append(proj, id)
		}
		// `project_id || ''` keeps the project term out of index selection:
		// as a plain column the SQLite planner gives up the updated_at index
		// walk of the default list sort for a full scan and sort (0.7 ms →
		// 47 ms at 50k tickets, TestPerf_List50k). The term only filters.
		return "((" + col("project_id") + " || '') IN (" + ticketPlaceholders(len(p.Projects)) + ") AND (" + strings.Join(parts, " OR ") + "))", append(proj, args...)
	case domain.KindCustomer:
		parts := []string{col("requester_id") + " = ?"}
		args := []any{p.UserID}
		if p.Role == domain.RoleOrgManager && p.OrganizationID != "" {
			parts = append(parts, col("organization_id")+" = ?")
			args = append(args, p.OrganizationID)
		}
		parts = append(parts, watcher)
		args = append(args, p.UserID)
		return "(" + strings.Join(parts, " OR ") + ")", args
	}
	return "1=0", nil
}

func ticketPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
