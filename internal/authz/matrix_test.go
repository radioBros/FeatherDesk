// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"sort"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
)

// CON-007 / AUTH-011: the permission matrix of permissions_spec as data.
//
// specCells lists every ✅/❌/◐ cell of the spec, reviewed against it:
//   - §3 staff action matrix: 21 rows × 5 columns (L1, L2, L3, supervisor, admin)
//   - §3 staff administration matrix: 11 rows × 3 columns (agent, supervisor, admin)
//   - §3 project administration matrix (D-20): 10 rows × 5 columns (member,
//     project_admin, other project's admin, supervisor, admin)
//   - I-11 project visibility (D-20): 5 rows × 3 columns (agent, supervisor, admin)
//   - §4 customer action matrix: 11 rows × 2 columns (requester, org_manager)
//   - I-9 custom fields: see/edit × staff/customer
//   - I-10 KB: 3 audiences × 3 visibilities × published/draft
//
// Every row of TestPermissionMatrix names the cell it proves; every cell has
// at least one row, and ◐ cells have rows for both outcomes of their
// condition. matrixRows is the reviewed row count: changing the spec means
// changing specCells, the rows and this constant together.
const matrixRows = 480 // +2: tickets:act_as_customer is never grantable to customers (I-12)

var staffActionRows = []string{
	"read", "reply", "note", "set_status", "set_priority", "category_tags", "assign_self", "assign_other", "unassign",
	"transfer", "escalate", "deescalate", "close", "reopen_closed", "edit_own_message", "create_for_customer", "delete",
	"link", "merge", "split", "edit_staff_fields",
}

var adminRows = []string{
	"customers_orgs", "create_customer", "teams", "support_config", "kb", "custom_fields_inbound_storage",
	"staff_users", "instance_settings", "webhooks_tokens_audit_jobs", "reports", "projects",
}

// projectAdminCols are the columns of the project administration matrix.
var projectAdminCols = []string{"member", "project_admin", "other_project_admin", "supervisor", "admin"}

// i11Rows are the rows of the I-11 table: an agent outside the ticket's
// project sees nothing, whatever the other rules say.
var i11RowNames = []string{"in_project_team", "outside_project_team", "outside_project_assignee", "outside_project_watcher", "outside_project_triage"}

var customerActionRows = []string{
	"create", "list", "read_public", "reply", "priority_on_create", "resolve", "reopen_resolved", "reply_closed",
	"manage_members", "csat", "api_tokens",
}

// specCells returns every cell of the spec tables (see the comment above).
func specCells() []string {
	var out []string
	for _, r := range staffActionRows {
		for _, k := range tmOrder {
			out = append(out, staffCell(r, k))
		}
	}
	for _, r := range adminRows {
		for _, k := range []string{"agent", "supervisor", "admin"} {
			out = append(out, "staff-admin/"+r+"/"+k)
		}
	}
	for _, r := range customerActionRows {
		for _, k := range []domain.Role{domain.RoleRequester, domain.RoleOrgManager} {
			out = append(out, "customer-action/"+r+"/"+string(k))
		}
	}
	for _, r := range authzProjectConfigs() {
		for _, k := range projectAdminCols {
			out = append(out, "project-admin/"+r+"/"+k)
		}
	}
	for _, r := range i11RowNames {
		for _, k := range []string{"agent", "supervisor", "admin"} {
			out = append(out, "I-11/"+r+"/"+k)
		}
	}
	for _, op := range []string{"see", "edit"} {
		for _, k := range []string{"staff", "customer"} {
			out = append(out, "I-9/"+op+"/"+k)
		}
	}
	for _, aud := range []string{"anonymous", "customer", "staff"} {
		for _, v := range []domain.KBVisibility{domain.KBPublic, domain.KBCustomers, domain.KBInternal} {
			for _, st := range []domain.KBStatus{domain.KBPublished, domain.KBDraft} {
				out = append(out, "I-10/"+aud+"/"+string(v)+"/"+string(st))
			}
		}
	}
	return out
}

// adminCellOf maps an administration resource to its §3 matrix row.
var adminCellOf = map[AdminResource]string{
	ResCustomers: "customers_orgs", ResOrganizations: "customers_orgs", ResCustomerInvite: "create_customer",
	ResTeams: "teams", ResCategories: "support_config", ResCannedGlobal: "support_config", ResSLAPolicies: "support_config",
	ResTags: "support_config", ResCalendars: "support_config",
	ResKBDrafts: "kb", ResKBPublish: "kb", ResKBCategories: "kb",
	ResCustomFields: "custom_fields_inbound_storage", ResInboundMailboxes: "custom_fields_inbound_storage", ResStorage: "custom_fields_inbound_storage",
	ResStaffUsers: "staff_users", ResSettings: "instance_settings", ResBranding: "instance_settings", ResTierLabels: "instance_settings",
	ResWebhooks: "webhooks_tokens_audit_jobs", ResAllTokens: "webhooks_tokens_audit_jobs", ResAuditLog: "webhooks_tokens_audit_jobs",
	ResDeadJobs: "webhooks_tokens_audit_jobs", ResReports: "reports", ResOwnStats: "reports", ResProjects: "projects",
}

func authzProjectConfigs() []string {
	out := make([]string, len(ProjectConfigs))
	for i, r := range ProjectConfigs {
		out[i] = string(r)
	}
	return out
}

// projectAdminRows: the project administration matrix (D-20). Each resource
// is checked in project P (the project admin's) and, for supervisors and
// admins, globally too; a project admin is also checked on the global
// configuration and on another project (both denied).
func projectAdminRows() []tmRow {
	const pP, pQ = "proj-p", "proj-q"
	member := &domain.Principal{UserID: "pm", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Projects: []string{pP}}
	padm := &domain.Principal{UserID: "pa", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1, Projects: []string{pP}, AdminProjects: []string{pP}}
	other := &domain.Principal{UserID: "po", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Projects: []string{pQ}, AdminProjects: []string{pQ}}
	padmTok := &domain.Principal{UserID: "pt", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1, Projects: []string{pP}, AdminProjects: []string{pP},
		TokenID: "t", Scopes: []string{domain.ScopeTicketsWrite}}
	var rows []tmRow
	for _, res := range ProjectConfigs {
		res := res
		lead := res != PCMembers && res != PCLifecycle
		add := func(col, name string, p *domain.Principal, project string, want bool) {
			rows = append(rows, tmRow{cell: "project-admin/" + string(res) + "/" + col, name: "project-admin/" + string(res) + "/" + name,
				who: p, ok: func() bool { return CanProjectConfig(p, res, project) == nil }, want: want})
		}
		add("member", "member", member, pP, false)
		add("project_admin", "project_admin", padm, pP, lead)
		add("project_admin", "project_admin-global", padm, "", false)
		add("project_admin", "project_admin-token-without-admin", padmTok, pP, false)
		add("other_project_admin", "other_project_admin", other, pP, false)
		add("supervisor", "supervisor", tmSup, pP, true)
		add("supervisor", "supervisor-global", tmSup, "", true)
		add("admin", "admin", tmAdm, pP, true)
		add("admin", "admin-global", tmAdm, "", true)
	}
	return rows
}

// i11Rows: I-11 (D-20), agents see only tickets of their projects; admins
// and supervisors see every project. The SQL predicate is checked against
// CanSeeTicket by TestCanRead_AgreesWithVisibilityPredicate.
func i11Rows() []tmRow {
	const pOther = "proj-other"
	in := tmTk()                                              // team A, tier 1, Default project
	out := tmTk(func(v *TicketView) { v.ProjectID = pOther }) // same, another project
	asg := tmTk(func(v *TicketView) { v.ProjectID, v.TeamID, v.AssigneeID = pOther, tmTeamB, tmL3.UserID })
	wat := tmTk(func(v *TicketView) { v.ProjectID, v.TeamID, v.Watchers = pOther, tmTeamB, []string{tmL3.UserID} })
	tri := tmTk(func(v *TicketView) { v.ProjectID, v.TeamID = pOther, "" })
	cases := []struct {
		row  string
		v    TicketView
		want bool
	}{{"in_project_team", in, true}, {"outside_project_team", out, false}, {"outside_project_assignee", asg, false},
		{"outside_project_watcher", wat, false}, {"outside_project_triage", tri, false}}
	var rows []tmRow
	for _, c := range cases {
		v := c.v
		for _, w := range []struct {
			col  string
			p    *domain.Principal
			want bool
		}{{"agent", tmL3, c.want}, {"supervisor", tmSup, true}, {"admin", tmAdm, true}} {
			p := w.p
			rows = append(rows, tmRow{cell: "I-11/" + c.row + "/" + w.col, name: "I-11/" + c.row + "/" + w.col,
				who: p, ok: func() bool { return CanSeeTicket(p, v) }, want: w.want})
		}
	}
	return rows
}

// adminMatrixRows: the §3 staff administration matrix (amMatrix in
// admin_matrix_test.go) plus the "canned: own team ✅" note for agents.
func adminMatrixRows() []tmRow {
	var rows []tmRow
	for _, r := range amMatrix {
		for _, c := range []struct {
			k    string
			p    *domain.Principal
			want bool
		}{{"agent", amAgent, r.agent}, {"supervisor", amSup, r.sup}, {"admin", amAdmin, r.ad}} {
			res, op, p := r.res, r.op, c.p
			rows = append(rows, tmRow{
				cell: "staff-admin/" + adminCellOf[res] + "/" + c.k, name: "admin/" + string(op) + "/" + string(res) + "/" + c.k,
				who: p, ok: func() bool { return CanAdmin(p, res, op) == nil }, want: c.want,
			})
		}
	}
	rows = append(rows,
		tmRow{"staff-admin/support_config/agent", "admin/canned-own-team/agent", amAgent, func() bool { return CanWriteTeamCanned(amAgent, "t1") == nil }, true},
		tmRow{"staff-admin/support_config/agent", "admin/canned-other-team/agent", amAgent, func() bool { return CanWriteTeamCanned(amAgent, "t2") == nil }, false},
		tmRow{"staff-admin/staff_users/admin", "admin/staff-role-idp-managed/admin", amAdmin, func() bool { return CanEditStaffRoleTier(amAdmin, true) == nil }, false},
	)
	return rows
}

// i9Rows: I-9 (custom field visibility and editability).
func i9Rows() []tmRow {
	var rows []tmRow
	fields := map[string]FieldAccess{
		"hidden": {}, "visible": {CustomerVisible: true}, "editable": {CustomerVisible: true, CustomerEditable: true},
		// customer_editable without customer_visible is not editable by customers.
		"editable-hidden": {CustomerEditable: true},
	}
	for name, f := range fields {
		for _, p := range []*domain.Principal{tmL1, tmAdm, tmReq, tmMgr} {
			k := "staff"
			if p.IsCustomer() {
				k = "customer"
			}
			wantSee, wantEdit := true, true
			if p.IsCustomer() {
				wantSee = f.CustomerVisible
				wantEdit = f.CustomerVisible && f.CustomerEditable
			}
			rows = append(rows,
				tmRow{"I-9/see/" + k, "I-9/see/" + name + "/" + p.UserID, p, func() bool { return CanSeeField(p, f) }, wantSee},
				tmRow{"I-9/edit/" + k, "I-9/edit/" + name + "/" + p.UserID, p, func() bool { return CanEditField(p, f) }, wantEdit})
		}
	}
	return rows
}

// i10Principals are the four principals of TestCanSeeArticle_Matrix.
var i10Principals = []struct {
	name, aud string
	p         *domain.Principal
}{
	{"anonymous", "anonymous", nil},
	{"customer", "customer", tmReq},
	{"agent", "staff", tmL1},
	{"admin", "staff", tmAdm},
}

// i10Rows: I-10, 4 principals × 3 visibilities × published/draft.
func i10Rows() []tmRow {
	var rows []tmRow
	for _, pr := range i10Principals {
		for _, v := range []domain.KBVisibility{domain.KBPublic, domain.KBCustomers, domain.KBInternal} {
			for _, st := range []domain.KBStatus{domain.KBPublished, domain.KBDraft} {
				want := false
				switch pr.aud {
				case "staff":
					want = true
				case "customer":
					want = st == domain.KBPublished && v != domain.KBInternal
				default:
					want = st == domain.KBPublished && v == domain.KBPublic
				}
				p, view := pr.p, ArticleView{Visibility: v, Status: st}
				rows = append(rows, tmRow{
					cell: "I-10/" + pr.aud + "/" + string(v) + "/" + string(st),
					name: "I-10/" + pr.name + "/" + string(v) + "/" + string(st),
					who:  p, ok: func() bool { return CanSeeArticle(p, view) }, want: want,
				})
			}
		}
	}
	return rows
}

func allMatrixRows() []tmRow {
	var rows []tmRow
	rows = append(rows, tmStaffMatrix()...)
	rows = append(rows, tmCustomerMatrix()...)
	rows = append(rows, adminMatrixRows()...)
	rows = append(rows, projectAdminRows()...)
	rows = append(rows, i11Rows()...)
	rows = append(rows, i9Rows()...)
	rows = append(rows, i10Rows()...)
	return rows
}

// TestPermissionMatrix (CON-007): every row passes, the row count equals the
// reviewed constant, every row proves a spec cell and every cell is proven.
func TestPermissionMatrix(t *testing.T) {
	rows := allMatrixRows()
	if len(rows) != matrixRows {
		t.Fatalf("matrix has %d rows, the reviewed constant says %d: update both together with permissions_spec", len(rows), matrixRows)
	}
	cells := map[string]int{}
	for _, c := range specCells() {
		cells[c] = 0
	}
	if len(cells) != 21*5+11*3+10*5+5*3+11*2+4+18 {
		t.Fatalf("specCells lists %d cells", len(cells))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.name] {
			t.Errorf("duplicate row %q", r.name)
		}
		seen[r.name] = true
		if _, ok := cells[r.cell]; !ok {
			t.Errorf("row %q proves %q, which is not a spec cell", r.name, r.cell)
			continue
		}
		cells[r.cell]++
		if got := r.ok(); got != r.want {
			t.Errorf("%s (%s): got %v, want %v", r.name, r.cell, got, r.want)
		}
	}
	var missing []string
	for c, n := range cells {
		if n == 0 {
			missing = append(missing, c)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("spec cells without a row:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestPermissionMatrix_ConditionalCellsBothWays: every ◐ cell of the spec has
// a row that allows and a row that denies, so the condition is exercised.
func TestPermissionMatrix_ConditionalCellsBothWays(t *testing.T) {
	conditional := map[string][]string{
		"staff-action/set_priority":     {"L1", "L2", "L3"},
		"staff-action/assign_self":      {"L1", "L2", "L3"},
		"staff-action/assign_other":     {"L1", "L2", "L3"},
		"staff-action/unassign":         {"L1", "L2", "L3"},
		"staff-action/deescalate":       {"L2", "L3"},
		"staff-action/edit_own_message": {"L1", "L2", "L3"},
		"staff-action/merge":            {"L1", "L2", "L3"},
		"customer-action/priority_on_create": {
			string(domain.RoleRequester), string(domain.RoleOrgManager)},
		"customer-action/reopen_resolved": {string(domain.RoleRequester), string(domain.RoleOrgManager)},
		"customer-action/create":          {string(domain.RoleOrgManager)},
		"staff-admin/support_config":      {"agent"},
	}
	outcomes := map[string]map[bool]bool{}
	for _, r := range allMatrixRows() {
		if outcomes[r.cell] == nil {
			outcomes[r.cell] = map[bool]bool{}
		}
		outcomes[r.cell][r.want] = true
	}
	for row, cols := range conditional {
		for _, k := range cols {
			c := row + "/" + k
			if !outcomes[c][true] || !outcomes[c][false] {
				t.Errorf("◐ cell %s lacks an allowed or a denied row: %v", c, outcomes[c])
			}
		}
	}
}

// TestCanSeeField_CustomerHiddenStaffOnly (I-9).
func TestCanSeeField_CustomerHiddenStaffOnly(t *testing.T) {
	hidden := FieldAccess{CustomerVisible: false, CustomerEditable: true}
	for _, p := range []*domain.Principal{tmL1, tmL2, tmL3, tmSup, tmAdm} {
		if !CanSeeField(p, hidden) || !CanEditField(p, hidden) {
			t.Errorf("staff %s must see and edit a staff-only field", p.UserID)
		}
	}
	for _, p := range []*domain.Principal{tmReq, tmMgr} {
		if CanSeeField(p, hidden) || CanEditField(p, hidden) {
			t.Errorf("customer %s sees or edits a customer_visible=false field", p.UserID)
		}
		if !CanSeeField(p, FieldAccess{CustomerVisible: true}) || CanEditField(p, FieldAccess{CustomerVisible: true}) {
			t.Errorf("customer %s: visible but not editable field", p.UserID)
		}
	}
}

// TestCanSeeArticle_Matrix (I-10): 4 principals × 3 visibilities ×
// draft/published, plus archived, category narrowing and PAT scopes.
func TestCanSeeArticle_Matrix(t *testing.T) {
	n := 0
	for _, r := range i10Rows() {
		if got := r.ok(); got != r.want {
			t.Errorf("%s: got %v want %v", r.name, got, r.want)
		}
		n++
	}
	if n != 4*3*2 {
		t.Fatalf("%d rows", n)
	}
	archived := ArticleView{Visibility: domain.KBPublic, Status: domain.KBArchived}
	if CanSeeArticle(nil, archived) || CanSeeArticle(tmReq, archived) || !CanSeeArticle(tmL1, archived) {
		t.Error("archived articles are staff-only")
	}
	narrowed := ArticleView{Visibility: domain.KBPublic, CategoryVisibility: domain.KBInternal, Status: domain.KBPublished}
	if CanSeeArticle(nil, narrowed) || CanSeeArticle(tmReq, narrowed) || !CanSeeArticle(tmAdm, narrowed) {
		t.Error("an internal category hides its public articles from non-staff")
	}
	if CanSeeArticle(tmAdm, ArticleView{Visibility: "bogus", Status: domain.KBPublished}) {
		t.Error("unknown visibility must be denied")
	}
	custOnly := ArticleView{Visibility: domain.KBCustomers, Status: domain.KBPublished}
	tok := func(p *domain.Principal, scopes ...string) *domain.Principal {
		c := *p
		c.TokenID, c.Scopes = "t", scopes
		return &c
	}
	if !CanSeeArticle(tok(tmReq, domain.ScopeTicketsRead), custOnly) {
		t.Error("tickets:read implies kb:read")
	}
	if CanSeeArticle(tok(tmReq, domain.ScopeTicketsWrite), custOnly) {
		t.Error("a token without kb:read reads like an anonymous visitor")
	}
	if !CanSeeArticle(tok(tmReq, domain.ScopeTicketsWrite), ArticleView{Visibility: domain.KBPublic, Status: domain.KBPublished}) {
		t.Error("public articles are visible to everyone")
	}
	internal := ArticleView{Visibility: domain.KBInternal, Status: domain.KBDraft}
	if CanSeeArticle(tok(tmL1, domain.ScopeDirectoryRead), internal) || !CanSeeArticle(tok(tmL1, domain.ScopeKBRead), internal) {
		t.Error("staff tokens need kb:read for internal drafts")
	}
}
