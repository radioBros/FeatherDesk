// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"errors"

	"github.com/radioBros/FeatherDesk/internal/domain"
)

// AdminResource is a non-ticket resource from the staff administration
// matrix (permissions_spec §3). Ticket actions are not covered here.
type AdminResource string

const (
	ResCustomers         AdminResource = "customers"          // customers (directory)
	ResOrganizations     AdminResource = "organizations"      // organizations (directory)
	ResTeams             AdminResource = "teams"              // teams + team membership
	ResCategories        AdminResource = "categories"         // ticket categories
	ResCannedGlobal      AdminResource = "canned_global"      // global canned responses
	ResSLAPolicies       AdminResource = "sla_policies"       // SLA policies
	ResTags              AdminResource = "tags"               // tag administration
	ResCalendars         AdminResource = "business_calendars" // business calendars
	ResKBDrafts          AdminResource = "kb_drafts"          // write KB drafts
	ResKBPublish         AdminResource = "kb_publish"         // publish / archive KB articles
	ResKBCategories      AdminResource = "kb_categories"      // KB categories
	ResCustomFields      AdminResource = "custom_fields"      // custom field definitions
	ResInboundMailboxes  AdminResource = "inbound_mailboxes"  // inbound mailboxes
	ResStorage           AdminResource = "storage"            // storage status
	ResStaffUsers        AdminResource = "staff_users"        // staff create / role / tier / disable
	ResSettings          AdminResource = "settings"           // instance settings
	ResBranding          AdminResource = "branding"           // branding
	ResTierLabels        AdminResource = "tier_labels"        // tier labels
	ResWebhooks          AdminResource = "webhooks"           // webhooks
	ResAllTokens         AdminResource = "all_api_tokens"     // every user's API tokens
	ResAuditLog          AdminResource = "audit_log"          // audit log
	ResDeadJobs          AdminResource = "dead_jobs"          // dead deferred jobs
	ResReports           AdminResource = "reports"            // team / global reports
	ResOwnStats          AdminResource = "own_stats"          // the caller's own stats
	ResCustomerInvite    AdminResource = "customer_invite"    // create customer / invite
	ResStaffDirectory    AdminResource = "staff_directory"    // staff lookups for pickers
	ResCustomFieldLookup AdminResource = "custom_field_lookup"
	// ResProjects: create, delete and membership of projects (D-20).
	// Per-project configuration is decided by Principal.AdminsProject.
	ResProjects AdminResource = "projects"
)

// AdminOp is read or write on an AdminResource.
type AdminOp string

const (
	OpRead  AdminOp = "read"
	OpWrite AdminOp = "write"
)

// ErrAdminDenied is returned by CanAdmin; handlers map it to 403 (the caller
// can see the area but may not act) — customers never reach staff routes (I-1).
var ErrAdminDenied = errors.New("authz: administration action not allowed")

// minRole: the lowest staff role allowed per resource and op ("" = nobody).
type adminRule struct {
	read, write domain.Role
	// scope a PAT needs (in addition to the role); read and write.
	readScope, writeScope string
}

var adminMatrix = map[AdminResource]adminRule{
	ResCustomers:         {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeDirectoryRead, domain.ScopeDirectoryWrite},
	ResOrganizations:     {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeDirectoryRead, domain.ScopeDirectoryWrite},
	ResCustomerInvite:    {domain.RoleAgent, domain.RoleAgent, domain.ScopeDirectoryRead, domain.ScopeDirectoryWrite},
	ResStaffDirectory:    {domain.RoleAgent, "", domain.ScopeDirectoryRead, ""},
	ResTeams:             {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeDirectoryRead, domain.ScopeAdmin},
	ResCategories:        {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeDirectoryRead, domain.ScopeAdmin},
	ResCannedGlobal:      {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeTicketsRead, domain.ScopeAdmin},
	ResSLAPolicies:       {domain.RoleSupervisor, domain.RoleSupervisor, domain.ScopeAdmin, domain.ScopeAdmin},
	ResTags:              {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeTicketsRead, domain.ScopeAdmin},
	ResCalendars:         {domain.RoleSupervisor, domain.RoleSupervisor, domain.ScopeAdmin, domain.ScopeAdmin},
	ResKBDrafts:          {domain.RoleAgent, domain.RoleAgent, domain.ScopeKBRead, domain.ScopeKBWrite},
	ResKBPublish:         {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeKBRead, domain.ScopeKBWrite},
	ResKBCategories:      {domain.RoleAgent, domain.RoleSupervisor, domain.ScopeKBRead, domain.ScopeAdmin},
	ResCustomFields:      {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResCustomFieldLookup: {domain.RoleAgent, "", domain.ScopeTicketsRead, ""},
	ResInboundMailboxes:  {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResStorage:           {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResStaffUsers:        {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResSettings:          {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResBranding:          {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResTierLabels:        {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResWebhooks:          {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResAllTokens:         {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResAuditLog:          {domain.RoleAdmin, "", domain.ScopeAdmin, ""},
	ResDeadJobs:          {domain.RoleAdmin, domain.RoleAdmin, domain.ScopeAdmin, domain.ScopeAdmin},
	ResReports:           {domain.RoleSupervisor, "", domain.ScopeTicketsRead, ""},
	ResOwnStats:          {domain.RoleAgent, "", domain.ScopeTicketsRead, ""},
	ResProjects:          {domain.RoleSupervisor, domain.RoleSupervisor, domain.ScopeAdmin, domain.ScopeAdmin},
}

// AdminResources lists every resource of the matrix (tests iterate it).
func AdminResources() []AdminResource {
	out := make([]AdminResource, 0, len(adminMatrix))
	for r := range adminMatrix {
		out = append(out, r)
	}
	return out
}

// CanAdmin decides a non-ticket administration action. Customers, unknown
// resources and roles below the matrix minimum get ErrAdminDenied; a PAT
// additionally needs the matching scope (I-6: role ∩ scopes).
func CanAdmin(p *domain.Principal, res AdminResource, op AdminOp) error {
	if !p.IsStaff() {
		return ErrAdminDenied
	}
	rule, ok := adminMatrix[res]
	if !ok {
		return ErrAdminDenied
	}
	need, scope := rule.read, rule.readScope
	if op == OpWrite {
		need, scope = rule.write, rule.writeScope
	}
	if need == "" || domain.RoleRank(p.Role) < domain.RoleRank(need) {
		return ErrAdminDenied
	}
	if !HasAnyScope(p, scope) {
		return ErrAdminDenied
	}
	return nil
}

// CanWriteTeamCanned: agents may manage canned responses of their own teams;
// supervisors and admins any team (permissions_spec §3 "canned: own team").
func CanWriteTeamCanned(p *domain.Principal, teamID string) error {
	if !p.IsStaff() || !HasAnyScope(p, domain.ScopeAdmin, domain.ScopeTicketsWrite) {
		return ErrAdminDenied
	}
	if p.IsSupervisorOrAdmin() || (teamID != "" && p.InTeam(teamID)) {
		return nil
	}
	return ErrAdminDenied
}

// CanEditStaffRoleTier: role and tier of an IdP-managed staff user are owned
// by the identity provider (D-04 §4) and read-only for everyone.
func CanEditStaffRoleTier(p *domain.Principal, targetIDPManaged bool) error {
	if err := CanAdmin(p, ResStaffUsers, OpWrite); err != nil {
		return err
	}
	if targetIDPManaged {
		return ErrAdminDenied
	}
	return nil
}

// AllowedAreas lists the SPA areas a principal may enter (GET /me).
func AllowedAreas(p *domain.Principal) []string {
	switch {
	case p.IsCustomer():
		return []string{"portal"}
	case p.IsAdmin():
		return []string{"agent", "admin"}
	case p.IsSupervisorOrAdmin():
		return []string{"agent", "support_admin"}
	case p.IsStaff() && len(p.AdminProjects) > 0:
		// D-20: the configuration pages of the projects they administer.
		return []string{"agent", "project_admin"}
	case p.IsStaff():
		return []string{"agent"}
	}
	return []string{}
}

// ProjectConfig is a project-scoped configuration resource (D-20,
// permissions_spec §3 "Project administration matrix").
type ProjectConfig string

const (
	PCCategories   ProjectConfig = "categories"
	PCTeamMapping  ProjectConfig = "team_mapping"
	PCSLA          ProjectConfig = "sla"
	PCKBCategories ProjectConfig = "kb_categories"
	PCAutomation   ProjectConfig = "automation"
	PCMacros       ProjectConfig = "macros"
	PCCanned       ProjectConfig = "canned"
	PCPortal       ProjectConfig = "portal_settings"
	PCMembers      ProjectConfig = "members"
	PCLifecycle    ProjectConfig = "create_delete"
)

// ProjectConfigs lists every project configuration resource (tests iterate it).
var ProjectConfigs = []ProjectConfig{PCCategories, PCTeamMapping, PCSLA, PCKBCategories, PCAutomation, PCMacros, PCCanned,
	PCPortal, PCMembers, PCLifecycle}

// CanProjectConfig decides a change of res in project projectID ("" =
// global configuration). Supervisors and admins may change anything; a
// project admin only the configuration of the projects they administer, and
// never the membership, creation or deletion of projects or anything global.
// A PAT additionally needs the admin scope (the routes' x-scopes).
func CanProjectConfig(p *domain.Principal, res ProjectConfig, projectID string) error {
	if !p.IsStaff() {
		return ErrAdminDenied
	}
	if !HasAnyScope(p, domain.ScopeAdmin) {
		return ErrAdminDenied
	}
	if p.IsSupervisorOrAdmin() {
		return nil
	}
	switch res {
	case PCMembers, PCLifecycle:
		return ErrAdminDenied
	}
	if projectID == "" || !p.AdminsProject(projectID) {
		return ErrAdminDenied
	}
	return nil
}
