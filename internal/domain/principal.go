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
	Locale         string
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

// HasScope reports whether a PAT scope allows the action; session auth
// (nil scopes) always passes. tickets:read implies kb:read.
func (p *Principal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	if p.Scopes == nil {
		return true
	}
	if slices.Contains(p.Scopes, "admin") || slices.Contains(p.Scopes, scope) {
		return true
	}
	return scope == ScopeKBRead && slices.Contains(p.Scopes, ScopeTicketsRead)
}

const (
	ScopeTicketsRead    = "tickets:read"
	ScopeTicketsWrite   = "tickets:write"
	ScopeDirectoryRead  = "directory:read"
	ScopeDirectoryWrite = "directory:write"
	ScopeKBRead         = "kb:read"
	ScopeKBWrite        = "kb:write"
	ScopeAdmin          = "admin"
)

var Scopes = []string{ScopeTicketsRead, ScopeTicketsWrite, ScopeDirectoryRead, ScopeDirectoryWrite, ScopeKBRead, ScopeKBWrite, ScopeAdmin}
