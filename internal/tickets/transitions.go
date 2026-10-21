// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"slices"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// Transitions is the literal status machine of ticket_lifecycle_spec §2
// (CON-008). It is the only source of allowed edges: every status change goes
// through Transition(), which consults this map. Who may trigger an edge is
// decided by mayTrigger.
var Transitions = map[domain.TicketStatus][]domain.TicketStatus{
	domain.StatusNew:      {domain.StatusOpen, domain.StatusPending, domain.StatusOnHold, domain.StatusResolved},
	domain.StatusOpen:     {domain.StatusPending, domain.StatusOnHold, domain.StatusResolved},
	domain.StatusPending:  {domain.StatusOpen, domain.StatusResolved},
	domain.StatusOnHold:   {domain.StatusPending, domain.StatusOpen, domain.StatusResolved},
	domain.StatusResolved: {domain.StatusOpen, domain.StatusClosed},
	domain.StatusClosed:   {domain.StatusOpen},
}

// CanTransition reports whether from → to is an edge of the status machine.
func CanTransition(from, to domain.TicketStatus) bool {
	return slices.Contains(Transitions[from], to)
}

// actorKind distinguishes who triggers a transition.
type actorKind int

const (
	actorStaff actorKind = iota
	actorSupervisor
	actorCustomer
	actorSystem
)

// mayTrigger encodes the "Who" column of lifecycle §2 for an edge that exists.
//   - closed → open: supervisor/admin only (staff re-open).
//   - customers: resolve (any non-final → resolved) and the automatic
//     pending/on_hold/resolved → open on reply or reopen.
//   - system: resolved → closed (auto-close) and nothing else.
func mayTrigger(a actorKind, from, to domain.TicketStatus) bool {
	if !CanTransition(from, to) {
		return false
	}
	switch a {
	case actorSupervisor:
		return true
	case actorStaff:
		return from != domain.StatusClosed
	case actorCustomer:
		switch to {
		case domain.StatusResolved:
			return true
		case domain.StatusOpen:
			return from == domain.StatusPending || from == domain.StatusOnHold || from == domain.StatusResolved
		}
		return false
	case actorSystem:
		return from == domain.StatusResolved && to == domain.StatusClosed
	}
	return false
}

// allowedTargets lists the statuses actor a may move a ticket to from `from`.
func allowedTargets(a actorKind, from domain.TicketStatus) []string {
	out := []string{}
	for _, to := range Transitions[from] {
		if mayTrigger(a, from, to) {
			out = append(out, string(to))
		}
	}
	return out
}

func actorOf(p *domain.Principal) actorKind {
	switch {
	case p == nil:
		return actorSystem
	case p.IsSupervisorOrAdmin():
		return actorSupervisor
	case p.IsStaff():
		return actorStaff
	}
	return actorCustomer
}

// CustomerStatus maps the internal status to the customer label (lifecycle §1).
func CustomerStatus(s domain.TicketStatus) string {
	switch s {
	case domain.StatusPending:
		return "awaiting_reply"
	case domain.StatusResolved:
		return "resolved"
	case domain.StatusClosed:
		return "closed"
	}
	return "open"
}

// customerStatusFilter maps a customer status filter back to internal statuses.
func customerStatusFilter(cs string) ([]domain.TicketStatus, bool) {
	switch cs {
	case "open":
		return []domain.TicketStatus{domain.StatusNew, domain.StatusOpen, domain.StatusOnHold}, true
	case "awaiting_reply":
		return []domain.TicketStatus{domain.StatusPending}, true
	case "resolved":
		return []domain.TicketStatus{domain.StatusResolved}, true
	case "closed":
		return []domain.TicketStatus{domain.StatusClosed}, true
	}
	return nil, false
}
