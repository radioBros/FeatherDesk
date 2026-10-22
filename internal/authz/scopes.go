// SPDX-License-Identifier: AGPL-3.0-only

// Package authz holds the pure authorisation rules (permissions_spec).
//
// This file and admin_matrix.go are owned by the auth module: principal and
// API-token scope helpers (I-6) and the staff administration matrix
// (permissions_spec §3 "Staff administration matrix"). Ticket visibility and
// ticket actions live in tickets*.go (tickets module). Everything here depends
// only on internal/domain so any package can import it.
package authz

import (
	"slices"

	"github.com/radioBros/FeatherDesk/internal/domain"
)

// GrantableScopes returns the PAT scopes a user of this kind and role may hold
// (permissions_spec §5): customers get tickets:read/write and kb:read; staff
// get everything except admin; admin and tickets:act_as_customer (I-12) only
// for admins.
func GrantableScopes(kind domain.UserKind, role domain.Role) []string {
	switch kind {
	case domain.KindCustomer:
		if !role.ValidFor(domain.KindCustomer) {
			return []string{}
		}
		return []string{domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeKBRead}
	case domain.KindStaff:
		switch role {
		case domain.RoleAdmin:
			return slices.Clone(domain.Scopes)
		case domain.RoleSupervisor, domain.RoleAgent:
			return []string{domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeDirectoryRead,
				domain.ScopeDirectoryWrite, domain.ScopeKBRead, domain.ScopeKBWrite}
		}
	}
	return []string{}
}

// ScopeGrantable reports whether scope may be held by a user of kind/role.
func ScopeGrantable(kind domain.UserKind, role domain.Role, scope string) bool {
	return slices.Contains(GrantableScopes(kind, role), scope)
}

// EffectiveScopes is I-6 evaluated per request: the token's scopes
// intersected with what the owner's *current* role may hold. The result is
// never nil (nil on a Principal means "session, full user permissions").
func EffectiveScopes(tokenScopes []string, kind domain.UserKind, role domain.Role) []string {
	out := make([]string, 0, len(tokenScopes))
	for _, s := range tokenScopes {
		if ScopeGrantable(kind, role, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// InvalidScopes returns the requested scopes the owner may not mint (unknown
// scopes included). Empty means the request is acceptable.
func InvalidScopes(kind domain.UserKind, role domain.Role, requested []string) []string {
	var bad []string
	for _, s := range requested {
		if !ScopeGrantable(kind, role, s) {
			bad = append(bad, s)
		}
	}
	return bad
}

// HasAnyScope reports whether p's token grants at least one of scopes. Session
// principals (nil Scopes) always pass; an empty list passes.
func HasAnyScope(p *domain.Principal, scopes ...string) bool {
	if p == nil {
		return false
	}
	if p.Scopes == nil || len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if p.HasScope(s) {
			return true
		}
	}
	return false
}

// ViaToken reports whether p authenticated with a personal access token.
func ViaToken(p *domain.Principal) bool { return p != nil && p.TokenID != "" }

// ViaSession reports whether p authenticated with a browser session cookie.
func ViaSession(p *domain.Principal) bool { return p != nil && p.SessionID != "" && p.TokenID == "" }
