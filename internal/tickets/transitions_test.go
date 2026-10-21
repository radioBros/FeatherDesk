// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"slices"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// TestTransitions enumerates all 36 (from, to) pairs against the literal
// table of ticket_lifecycle_spec §2 (CON-008).
func TestTransitions(t *testing.T) {
	spec := map[domain.TicketStatus][]domain.TicketStatus{
		"new":      {"open", "pending", "on_hold", "resolved"},
		"open":     {"pending", "on_hold", "resolved"},
		"pending":  {"open", "resolved"},
		"on_hold":  {"pending", "open", "resolved"},
		"resolved": {"open", "closed"},
		"closed":   {"open"},
	}
	pairs := 0
	for _, from := range domain.TicketStatuses {
		for _, to := range domain.TicketStatuses {
			pairs++
			want := slices.Contains(spec[from], to)
			if got := CanTransition(from, to); got != want {
				t.Errorf("%s → %s: got %v want %v", from, to, got, want)
			}
		}
	}
	if pairs != 36 {
		t.Fatalf("%d pairs", pairs)
	}
	// Who may trigger: closed → open is supervisor-only; system only auto-closes.
	if mayTrigger(actorStaff, "closed", "open") || !mayTrigger(actorSupervisor, "closed", "open") {
		t.Error("closed → open must be supervisor/admin only")
	}
	if !mayTrigger(actorSystem, "resolved", "closed") || mayTrigger(actorSystem, "open", "resolved") {
		t.Error("system may only auto-close")
	}
	if mayTrigger(actorCustomer, "open", "pending") || !mayTrigger(actorCustomer, "open", "resolved") {
		t.Error("customer may resolve but not set pending")
	}
}

// TestTransitions_WhoMayTrigger encodes the "Who" column of lifecycle §2 for
// every edge and actor: staff may take every edge explicitly except
// re-opening a closed ticket (supervisor/admin only); customers resolve and
// re-open (pending/on_hold/resolved → open) by replying or "reopen"; the
// system only auto-closes.
func TestTransitions_WhoMayTrigger(t *testing.T) {
	type edge struct{ from, to domain.TicketStatus }
	who := map[edge][]actorKind{
		{"new", "open"}: {actorStaff, actorSupervisor}, {"new", "pending"}: {actorStaff, actorSupervisor},
		{"new", "on_hold"}: {actorStaff, actorSupervisor}, {"new", "resolved"}: {actorStaff, actorSupervisor, actorCustomer},
		{"open", "pending"}: {actorStaff, actorSupervisor}, {"open", "on_hold"}: {actorStaff, actorSupervisor},
		{"open", "resolved"}: {actorStaff, actorSupervisor, actorCustomer},
		{"pending", "open"}:  {actorStaff, actorSupervisor, actorCustomer}, {"pending", "resolved"}: {actorStaff, actorSupervisor, actorCustomer},
		{"on_hold", "pending"}: {actorStaff, actorSupervisor}, {"on_hold", "open"}: {actorStaff, actorSupervisor, actorCustomer},
		{"on_hold", "resolved"}: {actorStaff, actorSupervisor, actorCustomer},
		{"resolved", "open"}:    {actorStaff, actorSupervisor, actorCustomer}, {"resolved", "closed"}: {actorStaff, actorSupervisor, actorSystem},
		{"closed", "open"}: {actorSupervisor},
	}
	edges := 0
	for _, from := range domain.TicketStatuses {
		for _, to := range domain.TicketStatuses {
			allowed, isEdge := who[edge{from, to}]
			if isEdge {
				edges++
			}
			for _, a := range []actorKind{actorStaff, actorSupervisor, actorCustomer, actorSystem} {
				if got, want := mayTrigger(a, from, to), slices.Contains(allowed, a); got != want {
					t.Errorf("actor %d %s → %s: got %v want %v", a, from, to, got, want)
				}
			}
		}
	}
	if edges != len(who) || edges != 15 {
		t.Fatalf("%d edges", edges)
	}
}
