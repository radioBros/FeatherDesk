// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"slices"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

var (
	amAgent = &domain.Principal{UserID: "a", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Teams: []string{"t1"}}
	amSup   = &domain.Principal{UserID: "s", Kind: domain.KindStaff, Role: domain.RoleSupervisor, Tier: 1}
	amAdmin = &domain.Principal{UserID: "x", Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 1}
	amCust  = &domain.Principal{UserID: "c", Kind: domain.KindCustomer, Role: domain.RoleOrgManager, OrganizationID: "o"}
)

// amRow is one cell group of the staff administration matrix
// (permissions_spec §3): allowed for agent / supervisor / admin.
type amRow struct {
	res            AdminResource
	op             AdminOp
	agent, sup, ad bool
}

var amMatrix = []amRow{
	{ResCustomers, OpRead, true, true, true}, {ResCustomers, OpWrite, false, true, true},
	{ResOrganizations, OpRead, true, true, true}, {ResOrganizations, OpWrite, false, true, true},
	{ResCustomerInvite, OpWrite, true, true, true},
	{ResTeams, OpWrite, false, true, true},
	{ResCategories, OpWrite, false, true, true}, {ResCannedGlobal, OpWrite, false, true, true},
	{ResSLAPolicies, OpWrite, false, true, true}, {ResTags, OpWrite, false, true, true}, {ResCalendars, OpWrite, false, true, true},
	{ResKBDrafts, OpWrite, true, true, true}, {ResKBPublish, OpWrite, false, true, true}, {ResKBCategories, OpWrite, false, true, true},
	{ResCustomFields, OpWrite, false, false, true}, {ResInboundMailboxes, OpWrite, false, false, true}, {ResStorage, OpRead, false, false, true},
	{ResStaffUsers, OpRead, false, false, true}, {ResStaffUsers, OpWrite, false, false, true},
	{ResSettings, OpWrite, false, false, true}, {ResBranding, OpWrite, false, false, true}, {ResTierLabels, OpWrite, false, false, true},
	{ResWebhooks, OpWrite, false, false, true}, {ResAllTokens, OpRead, false, false, true}, {ResAuditLog, OpRead, false, false, true},
	{ResDeadJobs, OpWrite, false, false, true},
	{ResReports, OpRead, false, true, true}, {ResOwnStats, OpRead, true, true, true},
	{ResProjects, OpRead, false, true, true}, {ResProjects, OpWrite, false, true, true},
}

func TestAdminMatrix_StaffAdministration(t *testing.T) {
	for _, r := range amMatrix {
		for _, c := range []struct {
			p    *domain.Principal
			want bool
		}{{amAgent, r.agent}, {amSup, r.sup}, {amAdmin, r.ad}} {
			if got := CanAdmin(c.p, r.res, r.op) == nil; got != c.want {
				t.Errorf("%s %s %s: got %v want %v", c.p.Role, r.op, r.res, got, c.want)
			}
		}
	}
	covered := map[AdminResource]bool{}
	for _, r := range amMatrix {
		covered[r.res] = true
	}
	for _, res := range AdminResources() {
		if !covered[res] && res != ResStaffDirectory && res != ResCustomFieldLookup {
			t.Errorf("resource %s has no matrix row", res)
		}
	}
}

func TestAdminMatrix_CustomerAlwaysDenied(t *testing.T) {
	for _, res := range AdminResources() {
		for _, op := range []AdminOp{OpRead, OpWrite} {
			if CanAdmin(amCust, res, op) == nil || CanAdmin(nil, res, op) == nil {
				t.Errorf("customer/anonymous allowed %s %s", op, res)
			}
		}
	}
	if CanAdmin(amAdmin, "unknown", OpRead) == nil {
		t.Fatal("unknown resource allowed")
	}
}

func TestAdminOnly_CustomFieldsInboundStorage(t *testing.T) {
	for _, res := range []AdminResource{ResCustomFields, ResInboundMailboxes, ResStorage} {
		if CanAdmin(amSup, res, OpWrite) == nil {
			t.Errorf("supervisor may write %s", res)
		}
		if CanAdmin(amAdmin, res, OpWrite) != nil {
			t.Errorf("admin denied %s", res)
		}
	}
}

func TestCalendars_SupervisorAllowed(t *testing.T) {
	if CanAdmin(amSup, ResCalendars, OpWrite) != nil || CanAdmin(amAgent, ResCalendars, OpWrite) == nil {
		t.Fatal("calendars: supervisor+ only")
	}
}

func TestAdminMatrix_PATNeedsScope(t *testing.T) {
	tok := func(p *domain.Principal, scopes ...string) *domain.Principal {
		c := *p
		c.TokenID, c.Scopes = "t", EffectiveScopes(scopes, p.Kind, p.Role)
		return &c
	}
	if CanAdmin(tok(amAdmin, domain.ScopeTicketsRead), ResStaffUsers, OpRead) == nil {
		t.Fatal("tickets:read token administers staff")
	}
	if CanAdmin(tok(amAdmin, domain.ScopeAdmin), ResStaffUsers, OpWrite) != nil {
		t.Fatal("admin token of an admin denied")
	}
	if CanAdmin(tok(amSup, domain.ScopeDirectoryRead), ResCustomers, OpWrite) == nil {
		t.Fatal("directory:read token writes customers")
	}
	if CanAdmin(tok(amSup, domain.ScopeDirectoryWrite), ResCustomers, OpWrite) != nil {
		t.Fatal("directory:write token of a supervisor denied")
	}
}

func TestCanEditStaffRoleTier_IdpManaged(t *testing.T) {
	if CanEditStaffRoleTier(amAdmin, true) == nil {
		t.Fatal("IdP-managed role editable")
	}
	if CanEditStaffRoleTier(amAdmin, false) != nil || CanEditStaffRoleTier(amSup, false) == nil {
		t.Fatal("role edit rules")
	}
}

func TestCanWriteTeamCanned_OwnTeam(t *testing.T) {
	if CanWriteTeamCanned(amAgent, "t1") != nil || CanWriteTeamCanned(amAgent, "t2") == nil || CanWriteTeamCanned(amSup, "t2") != nil {
		t.Fatal("team canned rules")
	}
}

func TestAllowedAreas(t *testing.T) {
	if !slices.Equal(AllowedAreas(amCust), []string{"portal"}) || !slices.Contains(AllowedAreas(amAdmin), "admin") || slices.Contains(AllowedAreas(amAgent), "admin") {
		t.Fatal("areas")
	}
}
