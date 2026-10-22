// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"fmt"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
)

// CON-007 / AUTH-011 (ticket part): permissions_spec §3 (staff action
// matrix) and §4 (customer action matrix) encoded as data. Every row is
// tagged with the spec cell it proves (matrix_test.go lists the cells); every
// ◐ cell is expanded into the cases that make the condition true and false.
// TestPermissionMatrix checks the rows, their count and their cell coverage.

const (
	tmTeamA   = "team-a"
	tmTeamB   = "team-b"
	tmOrgX    = "org-x"
	tmOrgY    = "org-y"
	tmCustC   = "cust-c"   // requester in orgX
	tmCustC2  = "cust-c2"  // colleague in orgX
	tmMgrX    = "mgr-x"    // org_manager of orgX
	tmCustY   = "cust-y"   // requester in orgY
	tmOtherID = "staff-zz" // some other staff member
)

var (
	tmL1    = &domain.Principal{UserID: "l1", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1, Teams: []string{tmTeamA}, Projects: []string{domain.DefaultProjectID}}
	tmL2    = &domain.Principal{UserID: "l2", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 2, Teams: []string{tmTeamA}, Projects: []string{domain.DefaultProjectID}}
	tmL3    = &domain.Principal{UserID: "l3", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Teams: []string{tmTeamA}, Projects: []string{domain.DefaultProjectID}}
	tmSup   = &domain.Principal{UserID: "sup", Kind: domain.KindStaff, Role: domain.RoleSupervisor, Tier: 1}
	tmAdm   = &domain.Principal{UserID: "adm", Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 1}
	tmReq   = &domain.Principal{UserID: tmCustC, Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: tmOrgX}
	tmMgr   = &domain.Principal{UserID: tmMgrX, Kind: domain.KindCustomer, Role: domain.RoleOrgManager, OrganizationID: tmOrgX}
	tmStaff = map[string]*domain.Principal{"L1": tmL1, "L2": tmL2, "L3": tmL3, "supervisor": tmSup, "admin": tmAdm}
	tmOrder = []string{"L1", "L2", "L3", "supervisor", "admin"}
)

var tmNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// tk is the base ticket: team A, tier 1, open, requested by custC of orgX.
func tmTk(mods ...func(*TicketView)) TicketView {
	v := TicketView{ID: "t1", Status: domain.StatusOpen, Priority: domain.PriorityNormal, Tier: 1, TeamID: tmTeamA,
		RequesterID: tmCustC, OrganizationID: tmOrgX, Policy: DefaultTicketPolicy(), Now: tmNow}
	for _, m := range mods {
		m(&v)
	}
	return v
}

func tmStatus(s domain.TicketStatus) func(*TicketView) { return func(v *TicketView) { v.Status = s } }
func tmTier(n int) func(*TicketView)                   { return func(v *TicketView) { v.Tier = n } }
func tmTeam(id string) func(*TicketView)               { return func(v *TicketView) { v.TeamID = id } }
func tmAssignee(id string) func(*TicketView)           { return func(v *TicketView) { v.AssigneeID = id } }
func tmWatchers(ids ...string) func(*TicketView)       { return func(v *TicketView) { v.Watchers = ids } }
func tmRequester(id, org string) func(*TicketView) {
	return func(v *TicketView) { v.RequesterID, v.OrganizationID = id, org }
}

type tmRow struct {
	cell string // permissions_spec cell this row proves (see specCells)
	name string
	who  *domain.Principal
	ok   func() bool
	want bool
}

func tmCan(p *domain.Principal, a Action, v TicketView) func() bool {
	return func() bool { return Can(p, a, v) == nil }
}

// staffCell is the cell of the §3 staff action matrix for row and column k.
func staffCell(row, k string) string { return "staff-action/" + row + "/" + k }

// custCell is the cell of the §4 customer action matrix.
func custCell(row string, p *domain.Principal) string {
	return "customer-action/" + row + "/" + string(p.Role)
}

// tmAll5 expands a row that is ✅ (or ❌) for every staff column.
func tmAll5(row, name string, want bool, f func(p *domain.Principal) func() bool) []tmRow {
	var out []tmRow
	for _, k := range tmOrder {
		out = append(out, tmRow{cell: staffCell(row, k), name: name + "/" + k, who: tmStaff[k], ok: f(tmStaff[k]), want: want})
	}
	return out
}

// tmEach expands a row per staff column with a per-principal expectation.
func tmEach(row, name string, f func(p *domain.Principal) (func() bool, bool)) []tmRow {
	var out []tmRow
	for _, k := range tmOrder {
		ok, want := f(tmStaff[k])
		out = append(out, tmRow{cell: staffCell(row, k), name: name + "/" + k, who: tmStaff[k], ok: ok, want: want})
	}
	return out
}

func tmStaffMatrix() []tmRow {
	var rows []tmRow
	add := func(r ...tmRow) { rows = append(rows, r...) }
	one := func(row, k, name string, ok func() bool, want bool) {
		add(tmRow{cell: staffCell(row, k), name: name, who: tmStaff[k], ok: ok, want: want})
	}

	// Read ticket + internal notes: ✅ ×5 (visibility per I-4).
	add(tmAll5("read", "read", true, func(p *domain.Principal) func() bool {
		return func() bool { return CanSeeTicket(p, tmTk()) }
	})...)
	// Public reply, internal note, change status (non-closed), change
	// category/tags, transfer, split, link, edit staff-only custom fields: ✅ ×5.
	for row, a := range map[string]Action{"reply": ActReply, "note": ActNote, "set_status": ActSetStatus, "set_priority": ActSetPriority,
		"category_tags": ActSetCategory, "transfer": ActTransfer, "split": ActSplit, "link": ActLink, "edit_staff_fields": ActEditFields} {
		add(tmAll5(row, string(a), true, func(p *domain.Principal) func() bool { return tmCan(p, a, tmTk()) })...)
	}
	// Change status on a closed ticket: closed tickets are immutable for staff (M-7).
	add(tmAll5("set_status", "set_status-closed", false, func(p *domain.Principal) func() bool {
		return tmCan(p, ActSetStatus, tmTk(tmStatus(domain.StatusClosed)))
	})...)
	add(tmAll5("reply", "reply-closed", false, func(p *domain.Principal) func() bool {
		return tmCan(p, ActReply, tmTk(tmStatus(domain.StatusClosed)))
	})...)
	// Change priority to urgent with FD_URGENT_REQUIRES_SUPERVISOR: ◐ supervisor+.
	pol := DefaultTicketPolicy()
	pol.UrgentRequiresSupervisor = true
	add(tmEach("set_priority", "urgent-flag-on", func(p *domain.Principal) (func() bool, bool) {
		return func() bool { return CanSetPriority(p, domain.PriorityUrgent, pol) }, p.IsSupervisorOrAdmin()
	})...)
	add(tmEach("set_priority", "urgent-flag-off", func(p *domain.Principal) (func() bool, bool) {
		return func() bool { return CanSetPriority(p, domain.PriorityUrgent, DefaultTicketPolicy()) }, true
	})...)
	// Assign to self / to other staff: ◐ agents = member of the ticket's team;
	// ✅ sup/admin. The other half of both ◐ notes (the target, self included,
	// satisfies M-4: active, team member, tier ≥ ticket tier) is checked on the
	// target by tickets.checkAssign (TestAssign_RejectsLowerTierAssignee,
	// TestAssign_RejectsNonTeamMember, TestAssignable_EqualsM4Set).
	for _, row := range []string{"assign_self", "assign_other"} {
		add(tmEach(row, row+"-member", func(p *domain.Principal) (func() bool, bool) {
			return tmCan(p, ActAssign, tmTk()), true
		})...)
		// A team-B ticket the agent sees only as a watcher: not a team member.
		add(tmEach(row, row+"-nonmember", func(p *domain.Principal) (func() bool, bool) {
			return tmCan(p, ActAssign, tmTk(tmTeam(tmTeamB), tmWatchers(p.UserID))), p.IsSupervisorOrAdmin()
		})...)
	}
	// Unassign: ◐ agents = the actor is the assignee; ✅ sup/admin.
	add(tmEach("unassign", "unassign-self", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActUnassign, tmTk(tmAssignee(p.UserID))), true
	})...)
	add(tmEach("unassign", "unassign-other", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActUnassign, tmTk(tmAssignee(tmOtherID))), p.IsSupervisorOrAdmin()
	})...)
	// Escalate (tier+1): L1 ✅, L2 ✅, L3 ❌ (max), sup ✅, admin ✅.
	one("escalate", "L1", "escalate/L1", tmCan(tmL1, ActEscalate, tmTk(tmTier(1))), true)
	one("escalate", "L2", "escalate/L2", tmCan(tmL2, ActEscalate, tmTk(tmTier(2))), true)
	one("escalate", "L3", "escalate/L3-max", tmCan(tmL3, ActEscalate, tmTk(tmTier(3))), false)
	one("escalate", "supervisor", "escalate/supervisor", tmCan(tmSup, ActEscalate, tmTk(tmTier(2))), true)
	one("escalate", "admin", "escalate/admin", tmCan(tmAdm, ActEscalate, tmTk(tmTier(1))), true)
	one("escalate", "supervisor", "escalate/supervisor-max", tmCan(tmSup, ActEscalate, tmTk(tmTier(3))), false)
	// De-escalate: L1 ❌; L2/L3 ◐ (me.tier ≥ ticket.tier and assignee or team member); sup/admin ✅.
	one("deescalate", "L1", "deescalate/L1", tmCan(tmL1, ActDeescalate, tmTk(tmTier(2), tmTeam(tmTeamB), tmWatchers(tmL1.UserID))), false)
	one("deescalate", "L2", "deescalate/L2-member", tmCan(tmL2, ActDeescalate, tmTk(tmTier(2))), true)
	one("deescalate", "L2", "deescalate/L2-tier-too-low", tmCan(tmL2, ActDeescalate, tmTk(tmTier(3), tmWatchers(tmL2.UserID))), false)
	one("deescalate", "L2", "deescalate/L2-assignee-other-team", tmCan(tmL2, ActDeescalate, tmTk(tmTier(2), tmTeam(tmTeamB), tmAssignee(tmL2.UserID))), true)
	one("deescalate", "L3", "deescalate/L3-member", tmCan(tmL3, ActDeescalate, tmTk(tmTier(3))), true)
	one("deescalate", "L3", "deescalate/L3-not-member-not-assignee", tmCan(tmL3, ActDeescalate, tmTk(tmTier(3), tmTeam(tmTeamB), tmWatchers(tmL3.UserID))), false)
	one("deescalate", "supervisor", "deescalate/supervisor", tmCan(tmSup, ActDeescalate, tmTk(tmTier(2))), true)
	one("deescalate", "admin", "deescalate/admin", tmCan(tmAdm, ActDeescalate, tmTk(tmTier(3))), true)
	// Close (from resolved): ✅ ×5; not from open.
	add(tmAll5("close", "close-resolved", true, func(p *domain.Principal) func() bool {
		return tmCan(p, ActClose, tmTk(tmStatus(domain.StatusResolved)))
	})...)
	add(tmAll5("close", "close-open", false, func(p *domain.Principal) func() bool { return tmCan(p, ActClose, tmTk()) })...)
	// Re-open closed ticket: agents ❌, sup/admin ✅.
	add(tmEach("reopen_closed", "reopen-closed", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActReopen, tmTk(tmStatus(domain.StatusClosed))), p.IsSupervisorOrAdmin()
	})...)
	// Edit/delete own message: agents ◐ (own internal note within 15 min); sup/admin ✅.
	add(tmEach("edit_own_message", "edit-own-note-5m", func(p *domain.Principal) (func() bool, bool) {
		return func() bool {
			return CanEditMessage(p, p.UserID, domain.MessageInternal, tmNow.Add(-5*time.Minute), tmNow)
		}, true
	})...)
	add(tmEach("edit_own_message", "edit-own-note-16m", func(p *domain.Principal) (func() bool, bool) {
		return func() bool {
			return CanEditMessage(p, p.UserID, domain.MessageInternal, tmNow.Add(-16*time.Minute), tmNow)
		}, p.IsSupervisorOrAdmin()
	})...)
	add(tmEach("edit_own_message", "edit-others-note", func(p *domain.Principal) (func() bool, bool) {
		return func() bool { return CanEditMessage(p, tmOtherID, domain.MessageInternal, tmNow, tmNow) }, p.IsSupervisorOrAdmin()
	})...)
	add(tmAll5("edit_own_message", "edit-public-reply", false, func(p *domain.Principal) func() bool {
		return func() bool { return CanEditMessage(p, p.UserID, domain.MessagePublic, tmNow, tmNow) }
	})...)
	// Create ticket for a customer: ✅ ×5.
	add(tmAll5("create_for_customer", "create-for-customer", true, func(p *domain.Principal) func() bool {
		return func() bool { return CanCreateTicketFor(p, tmCustC, tmOrgX) }
	})...)
	// Delete ticket: admin only.
	add(tmEach("delete", "delete", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActDelete, tmTk()), p.IsAdmin()
	})...)
	// Merge: ◐ agents = sees both and is the assignee of the source; sup/admin ✅.
	// "Sees both" is the target's visibility, checked by the merge handler
	// (TestMerge_AgentNeedsBothVisibleAndSourceAssignee covers the pair).
	add(tmEach("merge", "merge-assignee", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActMerge, tmTk(tmAssignee(p.UserID))), true
	})...)
	add(tmEach("merge", "merge-not-assignee", func(p *domain.Principal) (func() bool, bool) {
		return tmCan(p, ActMerge, tmTk(tmAssignee(tmOtherID))), p.IsSupervisorOrAdmin()
	})...)
	return rows
}

func tmCustomerMatrix() []tmRow {
	var rows []tmRow
	add := func(row string, c *domain.Principal, name string, ok func() bool, want bool) {
		rows = append(rows, tmRow{cell: custCell(row, c), name: name + "/" + string(c.Role), who: c, ok: ok, want: want})
	}
	// Create ticket: requester self ✅ / colleague ❌; org_manager self or colleague ✅, other org ❌.
	add("create", tmReq, "create-self", func() bool { return CanCreateTicketFor(tmReq, tmReq.UserID, tmOrgX) }, true)
	add("create", tmReq, "create-colleague", func() bool { return CanCreateTicketFor(tmReq, tmCustC2, tmOrgX) }, false)
	add("create", tmMgr, "create-self", func() bool { return CanCreateTicketFor(tmMgr, tmMgr.UserID, tmOrgX) }, true)
	add("create", tmMgr, "create-colleague", func() bool { return CanCreateTicketFor(tmMgr, tmCustC2, tmOrgX) }, true)
	add("create", tmMgr, "create-other-org", func() bool { return CanCreateTicketFor(tmMgr, tmCustY, tmOrgY) }, false)
	// List: requester own only (+ CC, V1); org_manager whole organization.
	add("list", tmReq, "see-own", func() bool { return CanSeeTicket(tmReq, tmTk()) }, true)
	add("list", tmReq, "see-colleague", func() bool { return CanSeeTicket(tmReq, tmTk(tmRequester(tmCustC2, tmOrgX))) }, false)
	add("list", tmReq, "see-as-cc", func() bool { return CanSeeTicket(tmReq, tmTk(tmRequester(tmCustC2, tmOrgX), tmWatchers(tmCustC))) }, true)
	add("list", tmMgr, "see-colleague", func() bool { return CanSeeTicket(tmMgr, tmTk(tmRequester(tmCustC2, tmOrgX))) }, true)
	add("list", tmMgr, "see-other-org", func() bool { return CanSeeTicket(tmMgr, tmTk(tmRequester(tmCustY, tmOrgY))) }, false)
	for _, c := range []*domain.Principal{tmReq, tmMgr} {
		// Read public thread + attachments ✅; internal notes are never
		// customer-visible events (I-2/I-7) and staff-only actions are denied.
		add("read_public", c, "read-own", func() bool { return CanSeeTicket(c, tmTk(tmRequester(c.UserID, tmOrgX))) }, true)
		add("read_public", c, "note-event", func() bool {
			return CanSeeEvent(c, tmSnap(c.UserID), "note.created", DefaultTicketPolicy())
		}, false)
		add("read_public", c, "note-action", tmCan(c, ActNote, tmTk()), false)
		add("read_public", c, "escalate-action", tmCan(c, ActEscalate, tmTk()), false)
		// Reply ✅.
		add("reply", c, "reply", tmCan(c, ActReply, tmTk()), true)
		// Reply to closed: allowed, it creates a follow-up ticket (lifecycle §2).
		add("reply_closed", c, "reply-closed", tmCan(c, ActReply, tmTk(tmStatus(domain.StatusClosed))), true)
		// Set priority on create: low/normal/high ✅; urgent only with FD_CUSTOMER_URGENT.
		add("priority_on_create", c, "priority-high", func() bool { return CanSetPriority(c, domain.PriorityHigh, DefaultTicketPolicy()) }, true)
		add("priority_on_create", c, "priority-urgent-default", func() bool { return CanSetPriority(c, domain.PriorityUrgent, DefaultTicketPolicy()) }, false)
		add("priority_on_create", c, "priority-urgent-allowed", func() bool {
			return CanSetPriority(c, domain.PriorityUrgent, TicketPolicy{CustomerUrgentAllowed: true})
		}, true)
		// Mark resolved ✅.
		add("resolve", c, "resolve", tmCan(c, ActResolve, tmTk()), true)
		// Re-open resolved within the window ✅, outside ❌.
		inside := func(v *TicketView) { v.Status, v.ResolvedAt = domain.StatusResolved, tmNow.Add(-24*time.Hour) }
		outside := func(v *TicketView) { v.Status, v.ResolvedAt = domain.StatusResolved, tmNow.Add(-8*24*time.Hour) }
		add("reopen_resolved", c, "reopen-inside", tmCan(c, ActReopen, tmTk(inside)), true)
		add("reopen_resolved", c, "reopen-outside", tmCan(c, ActReopen, tmTk(outside)), false)
		// API tokens (own): tickets:read and tickets:write (+ kb:read, which
		// tickets:read implies); never directory, kb:write or admin.
		for _, s := range domain.Scopes {
			want := s == domain.ScopeTicketsRead || s == domain.ScopeTicketsWrite || s == domain.ScopeKBRead
			add("api_tokens", c, "scope-"+s, func() bool { return ScopeGrantable(c.Kind, c.Role, s) }, want)
		}
	}
	// Manage org members: requester ❌, org_manager ✅.
	add("manage_members", tmReq, "manage-members", func() bool { return CanManageOrgMembers(tmReq) }, false)
	add("manage_members", tmMgr, "manage-members", func() bool { return CanManageOrgMembers(tmMgr) }, true)
	// CSAT: own ticket only, after resolution, once per cycle.
	resolved := tmStatus(domain.StatusResolved)
	add("csat", tmReq, "csat-own", tmCan(tmReq, ActCSAT, tmTk(resolved)), true)
	add("csat", tmReq, "csat-own-open", tmCan(tmReq, ActCSAT, tmTk()), false)
	add("csat", tmReq, "csat-again", tmCan(tmReq, ActCSAT, tmTk(resolved, func(v *TicketView) { v.CSATScore = 4 })), false)
	add("csat", tmMgr, "csat-colleague", tmCan(tmMgr, ActCSAT, tmTk(resolved)), false)
	add("csat", tmMgr, "csat-own", tmCan(tmMgr, ActCSAT, tmTk(resolved, tmRequester(tmMgrX, tmOrgX))), true)
	return rows
}

// tmSnap is an event snapshot of the base ticket requested by requester.
func tmSnap(requester string) events.Snapshot {
	return events.Snapshot{Status: "open", Priority: "normal", Tier: 1, TeamID: tmTeamA, RequesterID: requester, OrganizationID: tmOrgX}
}

// TestAllowedActionsMatchCan: allowed_actions lists an action iff Can allows it.
func TestAllowedActionsMatchCan(t *testing.T) {
	principals := []*domain.Principal{tmL1, tmL2, tmL3, tmSup, tmAdm, tmReq, tmMgr,
		{UserID: "pat", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 2, Teams: []string{tmTeamA}, Projects: []string{domain.DefaultProjectID}, Scopes: []string{domain.ScopeTicketsRead}}}
	for _, p := range principals {
		for _, s := range domain.TicketStatuses {
			for _, tr := range []int{1, 2, 3} {
				v := tmTk(tmStatus(s), tmTier(tr), func(v *TicketView) { v.ResolvedAt = tmNow.Add(-time.Hour) })
				allowed := map[Action]bool{}
				for _, a := range AllowedActions(p, v) {
					allowed[a] = true
				}
				for _, a := range TicketActions {
					if (Can(p, a, v) == nil) != allowed[a] {
						t.Errorf("%s %s tmTier %d %s: allowed_actions=%v but Can=%v", p.UserID, s, tr, a, allowed[a], Can(p, a, v))
					}
				}
			}
		}
	}
}

func TestReadOnlyTokenHasNoActions(t *testing.T) {
	p := &domain.Principal{UserID: "l2", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 2, Teams: []string{tmTeamA}, Projects: []string{domain.DefaultProjectID}, Scopes: []string{domain.ScopeTicketsRead}}
	if !CanSeeTicket(p, tmTk()) {
		t.Fatal("tickets:read token must see the ticket")
	}
	if acts := AllowedActions(p, tmTk()); len(acts) != 0 {
		t.Fatalf("read-only token got actions %v", acts)
	}
	p.Scopes = []string{domain.ScopeKBRead}
	if CanSeeTicket(p, tmTk()) {
		t.Fatal("kb:read token must not see tickets (I-6)")
	}
	adminTok := &domain.Principal{UserID: "adm", Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3, Scopes: []string{domain.ScopeTicketsWrite}}
	if Can(adminTok, ActDelete, tmTk()) == nil {
		t.Fatal("delete needs the admin scope even for an admin owner (I-6)")
	}
}

func ExampleAllowedActions() {
	fmt.Println(AllowedActionStrings(tmL1, tmTk(tmTier(1))))
	// Output: [reply note set_status set_priority set_category assign transfer escalate link split edit_fields edit_subject watch resolve]
}
