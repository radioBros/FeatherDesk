// SPDX-License-Identifier: AGPL-3.0-only

package domain

import "slices"

// Principal is the authenticated caller, resolved per request by authn
// (session cookie or PAT) and consumed by authz, handlers and the WS hub.
type Principal struct {
	UserID         string
	Kind           UserKind
	Role           Role
	Tier           int // staff only; 0 for customers
	OrganizationID string
	Teams          []string // staff team IDs
	// Projects lists the staff user's project memberships (any role) and
	// AdminProjects the subset where the role is project_admin (D-20).
	// Admins and supervisors see every project whatever these say (I-11).
	Projects      []string
	AdminProjects []string
	Locale        string
	// Scopes is nil for session auth (full user permissions) and the PAT
	// scope list otherwise. Effective permission = role ∩ scopes (I-6).
	Scopes     []string
	SessionID  string
	TokenID    string
	IDPManaged bool
}

func (p *Principal) IsStaff() bool    { return p != nil && p.Kind == KindStaff }
func (p *Principal) IsCustomer() bool { return p != nil && p.Kind == KindCustomer }

// EffectiveTier: admins and supervisors act as tier 3 (D-05).
func (p *Principal) EffectiveTier() int {
	if p == nil || p.Kind != KindStaff {
		return 0
	}
	if p.Role == RoleAdmin || p.Role == RoleSupervisor {
		return MaxTier
	}
	return p.Tier
}

func (p *Principal) IsSupervisorOrAdmin() bool {
	return p.IsStaff() && (p.Role == RoleAdmin || p.Role == RoleSupervisor)
}

func (p *Principal) IsAdmin() bool { return p.IsStaff() && p.Role == RoleAdmin }

func (p *Principal) InTeam(teamID string) bool { return slices.Contains(p.Teams, teamID) }

// SeesAllProjects: admins and supervisors are not limited by project
// membership (D-20 I-11).
func (p *Principal) SeesAllProjects() bool { return p.IsSupervisorOrAdmin() }

// InProject reports whether staff user p may work in project id: admins and
// supervisors always, agents when they are members (I-11).
func (p *Principal) InProject(id string) bool {
	if !p.IsStaff() {
		return false
	}
	return p.SeesAllProjects() || slices.Contains(p.Projects, id)
}

// AdminsProject reports whether p may manage project id's configuration:
// admins and supervisors always, agents with the project_admin role.
func (p *Principal) AdminsProject(id string) bool {
	if !p.IsStaff() {
		return false
	}
	return p.SeesAllProjects() || slices.Contains(p.AdminProjects, id)
}

// IsProjectAdmin reports whether p administers at least one project (or is
// a supervisor or admin): the guard of the project-admin route group.
func (p *Principal) IsProjectAdmin() bool {
	return p.IsStaff() && (p.SeesAllProjects() || len(p.AdminProjects) > 0)
}

// HasScope reports whether a PAT scope allows the action; session auth
// (nil scopes) always passes. tickets:read implies kb:read.
// tickets:act_as_customer is the exception (I-12): only a token that lists it
// explicitly holds it; neither a session nor the admin scope implies it.
func (p *Principal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	if scope == ScopeActAsCustomer {
		return p.ActsAsCustomer()
	}
	if p.Scopes == nil {
		return true
	}
	if slices.Contains(p.Scopes, "admin") || slices.Contains(p.Scopes, scope) {
		return true
	}
	return scope == ScopeKBRead && slices.Contains(p.Scopes, ScopeTicketsRead)
}

// ActsAsCustomer reports whether p is a PAT that explicitly holds
// tickets:act_as_customer (I-12). Sessions and the admin scope never do.
func (p *Principal) ActsAsCustomer() bool {
	return p != nil && p.TokenID != "" && slices.Contains(p.Scopes, ScopeActAsCustomer)
}

// OnlyActsAsCustomer reports whether p's token holds tickets:act_as_customer
// and no other scope: it may call only the operations that list that scope.
func (p *Principal) OnlyActsAsCustomer() bool {
	if !p.ActsAsCustomer() {
		return false
	}
	for _, s := range p.Scopes {
		if s != ScopeActAsCustomer {
			return false
		}
	}
	return true
}

const (
	ScopeTicketsRead    = "tickets:read"
	ScopeTicketsWrite   = "tickets:write"
	ScopeDirectoryRead  = "directory:read"
	ScopeDirectoryWrite = "directory:write"
	ScopeKBRead         = "kb:read"
	ScopeKBWrite        = "kb:write"
	ScopeAdmin          = "admin"
	// ScopeActAsCustomer lets a trusted integration post customer messages
	// on behalf of a customer (I-12). Grantable by admins only, never implied.
	ScopeActAsCustomer = "tickets:act_as_customer"
)

var Scopes = []string{ScopeTicketsRead, ScopeTicketsWrite, ScopeDirectoryRead, ScopeDirectoryWrite, ScopeKBRead, ScopeKBWrite, ScopeAdmin, ScopeActAsCustomer}
