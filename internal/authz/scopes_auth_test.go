// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"slices"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

func TestGrantableScopes_Matrix(t *testing.T) {
	cust := GrantableScopes(domain.KindCustomer, domain.RoleRequester)
	if !slices.Equal(cust, []string{domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeKBRead}) {
		t.Fatalf("customer: %v", cust)
	}
	if slices.Contains(GrantableScopes(domain.KindStaff, domain.RoleSupervisor), domain.ScopeAdmin) {
		t.Fatal("supervisor may hold admin")
	}
	if !slices.Contains(GrantableScopes(domain.KindStaff, domain.RoleAdmin), domain.ScopeAdmin) {
		t.Fatal("admin may not hold admin")
	}
	if len(GrantableScopes(domain.KindCustomer, domain.RoleAdmin)) != 0 || len(GrantableScopes("bogus", domain.RoleAdmin)) != 0 {
		t.Fatal("invalid kind/role combos must grant nothing")
	}
}

// TestInvariant_I6_ScopesNeverExceedRole: for every kind × role, the
// effective scopes of any token are a subset of what the role may hold, and
// non-nil (a token never turns into a full session principal).
func TestInvariant_I6_ScopesNeverExceedRole(t *testing.T) {
	all := append(slices.Clone(domain.Scopes), "bogus:scope")
	for _, k := range []domain.UserKind{domain.KindStaff, domain.KindCustomer} {
		for _, r := range []domain.Role{domain.RoleAdmin, domain.RoleSupervisor, domain.RoleAgent, domain.RoleRequester, domain.RoleOrgManager} {
			eff := EffectiveScopes(all, k, r)
			if eff == nil {
				t.Fatal("nil effective scopes")
			}
			for _, s := range eff {
				if !ScopeGrantable(k, r, s) {
					t.Errorf("%s/%s effective %s", k, r, s)
				}
			}
			p := &domain.Principal{Kind: k, Role: r, TokenID: "t", Scopes: EffectiveScopes(nil, k, r)}
			if HasAnyScope(p, domain.ScopeTicketsRead) {
				t.Errorf("empty token grants tickets:read for %s/%s", k, r)
			}
		}
	}
	// Demotion: an admin token held by a now-agent loses admin.
	if slices.Contains(EffectiveScopes([]string{domain.ScopeAdmin}, domain.KindStaff, domain.RoleAgent), domain.ScopeAdmin) {
		t.Fatal("demoted owner keeps admin scope")
	}
}

func TestInvalidScopes_CustomerDirectory(t *testing.T) {
	bad := InvalidScopes(domain.KindCustomer, domain.RoleOrgManager, []string{domain.ScopeTicketsRead, domain.ScopeDirectoryRead, domain.ScopeAdmin})
	if !slices.Equal(bad, []string{domain.ScopeDirectoryRead, domain.ScopeAdmin}) {
		t.Fatalf("bad = %v", bad)
	}
}

func TestHasAnyScope(t *testing.T) {
	sess := &domain.Principal{Kind: domain.KindStaff, SessionID: "s"}
	if !HasAnyScope(sess, domain.ScopeAdmin) || !ViaSession(sess) || ViaToken(sess) {
		t.Fatal("session principal")
	}
	tok := &domain.Principal{TokenID: "t", Scopes: []string{domain.ScopeTicketsRead}}
	if !HasAnyScope(tok, domain.ScopeKBRead) || HasAnyScope(tok, domain.ScopeTicketsWrite) || HasAnyScope(nil) {
		t.Fatal("token principal")
	}
}
