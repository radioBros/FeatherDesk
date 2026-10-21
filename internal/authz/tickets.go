// SPDX-License-Identifier: AGPL-3.0-only

package authz

// Ticket authorisation (permissions_spec §2–§4, §6.4): pure functions over a
// TicketView so REST, WebSocket fan-out, email and webhook payload builders all
// make the same decision without touching the database. The SQL twin of
// CanSeeTicket is TicketPredicate (tickets_visibility.go); a property test keeps
// the two in agreement.

import (
	"slices"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
)

// Action is one entry of the `allowed_actions` vocabulary (OpenAPI AllowedAction).
type Action string

const (
	ActReply       Action = "reply"
	ActNote        Action = "note"
	ActSetStatus   Action = "set_status"
	ActSetPriority Action = "set_priority"
	ActSetCategory Action = "set_category"
	ActAssign      Action = "assign"
	ActUnassign    Action = "unassign"
	ActTransfer    Action = "transfer"
	ActEscalate    Action = "escalate"
	ActDeescalate  Action = "deescalate"
	ActLink        Action = "link"
	ActMerge       Action = "merge"
	ActSplit       Action = "split"
	ActEditFields  Action = "edit_fields"
	ActEditSubject Action = "edit_subject"
	ActWatch       Action = "watch"
	ActClose       Action = "close"
	ActReopen      Action = "reopen"
	ActDelete      Action = "delete"
	ActResolve     Action = "resolve"
	ActCSAT        Action = "csat"
)

// TicketActions is the full vocabulary, in OpenAPI order.
var TicketActions = []Action{
	ActReply, ActNote, ActSetStatus, ActSetPriority, ActSetCategory, ActAssign, ActUnassign,
	ActTransfer, ActEscalate, ActDeescalate, ActLink, ActMerge, ActSplit, ActEditFields,
	ActEditSubject, ActWatch, ActClose, ActReopen, ActDelete, ActResolve, ActCSAT,
}

// TicketPolicy carries the instance settings (settings.Support) that change
// ticket permissions. It travels with the view so the functions stay pure.
type TicketPolicy struct {
	TriageVisibleToAgents    bool
	UrgentRequiresSupervisor bool
	CustomerUrgentAllowed    bool
	ReopenWindow             time.Duration // 0 disables customer reopen
}

// DefaultTicketPolicy mirrors settings.DefaultSupport().
func DefaultTicketPolicy() TicketPolicy {
	return TicketPolicy{TriageVisibleToAgents: true, ReopenWindow: 7 * 24 * time.Hour}
}

// TicketView is what a decision needs to know about a ticket.
type TicketView struct {
	ID             string
	Status         domain.TicketStatus
	Priority       domain.Priority
	Tier           int
	TeamID         string
	AssigneeID     string
	RequesterID    string
	OrganizationID string
	Watchers       []string
	ResolvedAt     time.Time
	CSATScore      int // 0 = not rated in this resolution cycle
	Policy         TicketPolicy
	Now            time.Time // zero = time.Now()
}

// ViewFromSnapshot builds a view from an event snapshot (WebSocket fan-out).
func ViewFromSnapshot(s events.Snapshot, pol TicketPolicy) TicketView {
	return TicketView{
		Status: domain.TicketStatus(s.Status), Priority: domain.Priority(s.Priority), Tier: s.Tier,
		TeamID: s.TeamID, AssigneeID: s.AssigneeID, RequesterID: s.RequesterID, OrganizationID: s.OrganizationID,
		Watchers: s.Watchers, Policy: pol,
	}
}

func (v TicketView) now() time.Time {
	if v.Now.IsZero() {
		return time.Now()
	}
	return v.Now
}

// canReadTickets: sessions always; PATs need tickets:read (tickets:write is
// accepted as well, since a writer must be able to read what it changes).
func canReadTickets(p *domain.Principal) bool {
	return p.HasScope(domain.ScopeTicketsRead) || p.HasScope(domain.ScopeTicketsWrite)
}

// CanSeeTicket is the in-memory visibility rule (I-3, I-4, I-8 + V1 watchers).
// It must agree with TicketPredicate.
func CanSeeTicket(p *domain.Principal, v TicketView) bool {
	if p == nil || !canReadTickets(p) {
		return false
	}
	watcher := slices.Contains(v.Watchers, p.UserID)
	switch p.Kind {
	case domain.KindStaff:
		if p.IsSupervisorOrAdmin() {
			return true
		}
		eff := p.EffectiveTier()
		switch {
		case v.AssigneeID != "" && v.AssigneeID == p.UserID:
			return true
		case v.TeamID != "" && p.InTeam(v.TeamID) && v.Tier <= eff:
			return true
		case v.TeamID == "" && v.Policy.TriageVisibleToAgents && v.Tier <= eff:
			return true
		}
		return watcher
	case domain.KindCustomer:
		switch {
		case v.RequesterID == p.UserID:
			return true
		case p.Role == domain.RoleOrgManager && p.OrganizationID != "" && v.OrganizationID == p.OrganizationID:
			return true
		}
		return watcher
	}
	return false
}

// Can decides whether p may perform a on the ticket. It returns nil, a 404
// problem when the ticket is not visible (reads outside visibility never
// disclose existence), or a 403 problem when it is visible but the action is
// not allowed (permissions_spec §2, last paragraph).
func Can(p *domain.Principal, a Action, v TicketView) error {
	if !CanSeeTicket(p, v) {
		return problems.NotFound()
	}
	if reason := deny(p, a, v); reason != "" {
		return problems.Forbidden(reason)
	}
	return nil
}

// AllowedActions lists every action Can allows, in vocabulary order. Handlers
// put it on every ticket payload; the UI never re-implements the rules.
func AllowedActions(p *domain.Principal, v TicketView) []Action {
	out := []Action{}
	if !CanSeeTicket(p, v) {
		return out
	}
	for _, a := range TicketActions {
		if deny(p, a, v) == "" {
			out = append(out, a)
		}
	}
	return out
}

// AllowedActionStrings is AllowedActions as plain strings (JSON payloads).
func AllowedActionStrings(p *domain.Principal, v TicketView) []string {
	acts := AllowedActions(p, v)
	out := make([]string, len(acts))
	for i, a := range acts {
		out[i] = string(a)
	}
	return out
}

// deny returns "" when the (visible) action is allowed, else a short reason.
func deny(p *domain.Principal, a Action, v TicketView) string {
	if a == ActDelete {
		if !p.IsAdmin() || !p.HasScope(domain.ScopeAdmin) {
			return "admin only"
		}
		return ""
	}
	if !p.HasScope(domain.ScopeTicketsWrite) {
		return "token lacks tickets:write"
	}
	if p.IsStaff() {
		return denyStaff(p, a, v)
	}
	return denyCustomer(p, a, v)
}

func denyStaff(p *domain.Principal, a Action, v TicketView) string {
	closed := v.Status == domain.StatusClosed
	sup := p.IsSupervisorOrAdmin()
	const immutable = "closed tickets are immutable"
	switch a {
	case ActReply, ActNote, ActSetStatus, ActSetPriority, ActSetCategory, ActTransfer,
		ActEditFields, ActEditSubject, ActSplit:
		if closed {
			return immutable
		}
		return ""
	case ActLink, ActWatch:
		return ""
	case ActResolve:
		if closed || v.Status == domain.StatusResolved {
			return "already resolved"
		}
		return ""
	case ActAssign:
		switch {
		case closed:
			return immutable
		case v.TeamID == "":
			return "triage tickets need a team before assignment"
		case !sup && !p.InTeam(v.TeamID):
			return "not a member of the ticket's team"
		}
		return ""
	case ActUnassign:
		switch {
		case closed:
			return immutable
		case v.AssigneeID == "":
			return "not assigned"
		case !sup && v.AssigneeID != p.UserID:
			return "only the assignee or a supervisor may unassign"
		}
		return ""
	case ActEscalate:
		switch {
		case closed:
			return immutable
		case v.Tier >= domain.MaxTier:
			return "already at the highest tier"
		}
		return ""
	case ActDeescalate:
		switch {
		case closed:
			return immutable
		case v.Tier <= domain.MinTier:
			return "already at the lowest tier"
		case sup:
			return ""
		case p.Tier <= domain.MinTier:
			return "L1 agents cannot de-escalate"
		case p.EffectiveTier() < v.Tier:
			return "tier too low"
		case v.AssigneeID != p.UserID && (v.TeamID == "" || !p.InTeam(v.TeamID)):
			return "only the assignee or a team member may de-escalate"
		}
		return ""
	case ActClose:
		if v.Status != domain.StatusResolved {
			return "only resolved tickets can be closed"
		}
		return ""
	case ActReopen:
		switch {
		case !closed:
			return "only closed tickets can be re-opened"
		case !sup:
			return "only supervisors and admins re-open closed tickets"
		}
		return ""
	case ActMerge:
		switch {
		case closed:
			return immutable
		case !sup && v.AssigneeID != p.UserID:
			return "only the assignee or a supervisor may merge"
		}
		return ""
	case ActCSAT:
		return "customers only"
	}
	return "unknown action"
}

func denyCustomer(p *domain.Principal, a Action, v TicketView) string {
	switch a {
	case ActReply:
		return "" // a reply on a closed ticket creates a follow-up (lifecycle §2)
	case ActResolve:
		if v.Status == domain.StatusResolved || v.Status == domain.StatusClosed {
			return "already resolved"
		}
		return ""
	case ActReopen:
		switch {
		case v.Status != domain.StatusResolved:
			return "only resolved tickets can be re-opened"
		case v.Policy.ReopenWindow <= 0:
			return "re-opening is disabled"
		case v.ResolvedAt.IsZero() || v.now().Sub(v.ResolvedAt) > v.Policy.ReopenWindow:
			return "the reopen window has passed"
		}
		return ""
	case ActCSAT:
		switch {
		case v.RequesterID != p.UserID:
			return "only the requester may rate"
		case v.Status != domain.StatusResolved && v.Status != domain.StatusClosed:
			return "rate after resolution"
		case v.CSATScore > 0:
			return "already rated"
		}
		return ""
	}
	return "staff only"
}

// CanSetPriority checks the value-dependent priority rules: `urgent` needs a
// supervisor when UrgentRequiresSupervisor is on (§3), and customers may pick
// `urgent` only when CustomerUrgentAllowed (§4).
func CanSetPriority(p *domain.Principal, pr domain.Priority, pol TicketPolicy) bool {
	if pr != domain.PriorityUrgent {
		return true
	}
	if p.IsCustomer() {
		return pol.CustomerUrgentAllowed
	}
	return !pol.UrgentRequiresSupervisor || p.IsSupervisorOrAdmin()
}

// NoteEditWindow is how long staff may edit their own internal notes (§3).
const NoteEditWindow = 15 * time.Minute

// CanEditMessage implements "Edit/delete own message" (§3): supervisors and
// admins may edit any internal note; agents only their own, within 15 minutes.
// Public replies are never edited, only appended; customers never edit.
func CanEditMessage(p *domain.Principal, authorID string, kind domain.MessageKind, createdAt, now time.Time) bool {
	if !p.IsStaff() || kind != domain.MessageInternal || !p.HasScope(domain.ScopeTicketsWrite) {
		return false
	}
	if p.IsSupervisorOrAdmin() {
		return true
	}
	return authorID == p.UserID && now.Sub(createdAt) <= NoteEditWindow
}

// CanCreateTicketFor: staff create on behalf of any customer (channel agent);
// a requester only for themselves; an org_manager for themselves or a
// colleague of the same organization (§4).
func CanCreateTicketFor(p *domain.Principal, requesterID, requesterOrgID string) bool {
	if p == nil || !p.HasScope(domain.ScopeTicketsWrite) {
		return false
	}
	if p.IsStaff() {
		return true
	}
	if requesterID == p.UserID {
		return true
	}
	return p.Role == domain.RoleOrgManager && p.OrganizationID != "" && requesterOrgID == p.OrganizationID
}

// CanManageOrgMembers: only org managers manage their organization's members (§4).
func CanManageOrgMembers(p *domain.Principal) bool {
	return p.IsCustomer() && p.Role == domain.RoleOrgManager && p.OrganizationID != ""
}

// CanSeeEvent decides, in memory, whether p may receive event typ about the
// ticket described by snapshot s (realtime_spec §4). Customers only ever get
// customer-visible event types (I-7); payload filtering (I-9) is the publisher's job.
func CanSeeEvent(p *domain.Principal, s events.Snapshot, typ string, pol TicketPolicy) bool {
	if p == nil {
		return false
	}
	if p.IsCustomer() && !events.CustomerVisible(typ) {
		return false
	}
	return CanSeeTicket(p, ViewFromSnapshot(s, pol))
}

// FieldAccess is the visibility of a custom field definition.
type FieldAccess struct {
	CustomerVisible  bool
	CustomerEditable bool
}

// CanSeeField implements I-9: staff see every field, customers only
// customer_visible ones. Every customer payload builder filters through it.
func CanSeeField(p *domain.Principal, f FieldAccess) bool {
	if p.IsStaff() {
		return true
	}
	return f.CustomerVisible
}

// CanEditField: staff edit every field; customers only customer_editable ones.
func CanEditField(p *domain.Principal, f FieldAccess) bool {
	if p.IsStaff() {
		return true
	}
	return f.CustomerVisible && f.CustomerEditable
}

// ParseAction validates an action name.
func ParseAction(s string) (Action, bool) {
	a := Action(strings.TrimSpace(s))
	return a, slices.Contains(TicketActions, a)
}
