// SPDX-License-Identifier: AGPL-3.0-only

package authz

import (
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// permissions_spec §2 hard invariants, one test each, at the level of the
// pure rules. The SQL, router and socket halves of the same invariants are
// proven where they are enforced:
//   - I-1 router guards: internal/server, internal/app (route groups answer 404)
//   - I-2/I-3/I-4 SQL: internal/tickets TestVisibility_*, TestCanRead_AgreesWithVisibilityPredicate
//   - I-5: internal/authn TestInvariant_I5_* (sessions, PATs, sockets)
//   - I-6: TestInvariant_I6_ScopesNeverExceedRole (scopes_auth_test.go)
//   - I-7 sockets: internal/tickets TestWS_CustomerNeverReceivesInternal
//   - I-10 SQL: internal/kb TestCanSeeArticle_AgreesWithKBPredicate

var invCustomers = []*domain.Principal{tmReq, tmMgr}

// I-1: a customer is never authorised for staff or admin surfaces.
func TestInvariant_I1_CustomerNeverStaffOrAdmin(t *testing.T) {
	for _, c := range invCustomers {
		if a := AllowedAreas(c); len(a) != 1 || a[0] != "portal" {
			t.Errorf("%s areas %v", c.UserID, a)
		}
		for _, res := range AdminResources() {
			for _, op := range []AdminOp{OpRead, OpWrite} {
				if CanAdmin(c, res, op) == nil {
					t.Errorf("%s may %s %s", c.UserID, op, res)
				}
			}
		}
		if CanWriteTeamCanned(c, tmTeamA) == nil || CanEditStaffRoleTier(c, false) == nil {
			t.Errorf("%s passes a staff-only check", c.UserID)
		}
		for _, a := range []Action{ActNote, ActAssign, ActEscalate, ActDeescalate, ActTransfer, ActMerge, ActSplit, ActLink, ActDelete, ActEditFields} {
			if Can(c, a, tmTk(tmRequester(c.UserID, tmOrgX))) == nil {
				t.Errorf("%s may %s", c.UserID, a)
			}
		}
	}
}

// I-2: internal messages never reach customers: they can neither write nor
// edit one, and the note.created event is never customer-visible.
func TestInvariant_I2_CustomerNeverInternalMessages(t *testing.T) {
	for _, c := range invCustomers {
		own := tmTk(tmRequester(c.UserID, tmOrgX))
		if Can(c, ActNote, own) == nil {
			t.Errorf("%s may write an internal note", c.UserID)
		}
		if CanEditMessage(c, c.UserID, domain.MessageInternal, tmNow, tmNow) {
			t.Errorf("%s may edit an internal note", c.UserID)
		}
		if CanSeeEvent(c, tmSnap(c.UserID), events.NoteCreated, DefaultTicketPolicy()) {
			t.Errorf("%s receives note.created", c.UserID)
		}
	}
}

// I-3: a customer sees a ticket only as requester, as org_manager of its
// organization, or as a CC watcher; anything else is 404, never 403.
func TestInvariant_I3_CustomerVisibility(t *testing.T) {
	cases := []struct {
		name string
		p    *domain.Principal
		v    TicketView
		want bool
	}{
		{"requester own", tmReq, tmTk(), true},
		{"requester colleague", tmReq, tmTk(tmRequester(tmCustC2, tmOrgX)), false},
		{"requester as CC", tmReq, tmTk(tmRequester(tmCustY, tmOrgY), tmWatchers(tmCustC)), true},
		{"org_manager colleague", tmMgr, tmTk(tmRequester(tmCustC2, tmOrgX)), true},
		{"org_manager other org", tmMgr, tmTk(tmRequester(tmCustY, tmOrgY)), false},
		{"org_manager without org", &domain.Principal{UserID: "m0", Kind: domain.KindCustomer, Role: domain.RoleOrgManager}, tmTk(tmRequester(tmCustY, "")), false},
	}
	for _, c := range cases {
		if got := CanSeeTicket(c.p, c.v); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
		if !c.want {
			if err := Can(c.p, ActReply, c.v); invStatus(err) != 404 {
				t.Errorf("%s: invisible ticket answers %v, want 404", c.name, err)
			}
		}
	}
}

// I-4: an agent sees a ticket only as assignee, as a member of its team with
// tier ≥ ticket tier, or as a watcher; supervisors and admins see all.
func TestInvariant_I4_AgentVisibility(t *testing.T) {
	cases := []struct {
		name string
		p    *domain.Principal
		v    TicketView
		want bool
	}{
		{"L1 own team tier 1", tmL1, tmTk(), true},
		{"L1 own team tier 2", tmL1, tmTk(tmTier(2)), false},
		{"L2 own team tier 2", tmL2, tmTk(tmTier(2)), true},
		{"L2 other team", tmL2, tmTk(tmTeam(tmTeamB)), false},
		{"L1 assignee tier 3 other team", tmL1, tmTk(tmTier(3), tmTeam(tmTeamB), tmAssignee(tmL1.UserID)), true},
		{"L1 watcher tier 3", tmL1, tmTk(tmTier(3), tmWatchers(tmL1.UserID)), true},
		{"supervisor any", tmSup, tmTk(tmTier(3), tmTeam(tmTeamB)), true},
		{"admin any", tmAdm, tmTk(tmTier(3), tmTeam(tmTeamB)), true},
	}
	for _, c := range cases {
		if got := CanSeeTicket(c.p, c.v); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
		if !c.want && invStatus(Can(c.p, ActNote, c.v)) != 404 {
			t.Errorf("%s: invisible ticket must answer 404", c.name)
		}
	}
	// A visible ticket with a forbidden action answers 403.
	if invStatus(Can(tmL1, ActDelete, tmTk())) != 403 {
		t.Error("visible but forbidden must answer 403")
	}
}

// I-5: authn resolves a disabled user to no principal; every rule denies it.
func TestInvariant_I5_NoPrincipalNoAccess(t *testing.T) {
	if CanSeeTicket(nil, tmTk()) || Can(nil, ActReply, tmTk()) == nil || len(AllowedActions(nil, tmTk())) != 0 {
		t.Error("nil principal reaches a ticket")
	}
	if CanSeeEvent(nil, tmSnap(tmCustC), events.TicketCreated, DefaultTicketPolicy()) {
		t.Error("nil principal receives events")
	}
	if pred, _ := TicketPredicate(nil, DefaultTicketPolicy(), "t"); pred != "1=0" {
		t.Errorf("nil principal predicate %q", pred)
	}
	if CanCreateTicketFor(nil, tmCustC, tmOrgX) || CanAdmin(nil, ResReports, OpRead) == nil || HasAnyScope(nil) {
		t.Error("nil principal passes a check")
	}
	if CanSeeArticle(nil, ArticleView{Visibility: domain.KBCustomers, Status: domain.KBPublished}) {
		t.Error("nil principal reads beyond public articles")
	}
}

// I-7: internal event types never reach customers, whatever the ticket.
func TestInvariant_I7_InternalEventsNeverToCustomers(t *testing.T) {
	for _, c := range invCustomers {
		for _, typ := range events.All {
			got := CanSeeEvent(c, tmSnap(c.UserID), typ, DefaultTicketPolicy())
			if got != events.CustomerVisible(typ) {
				t.Errorf("%s / %s: CanSeeEvent=%v, CustomerVisible=%v", c.UserID, typ, got, events.CustomerVisible(typ))
			}
		}
		other := tmSnap(tmCustY)
		other.OrganizationID = tmOrgY
		if CanSeeEvent(c, other, events.MessageCreated, DefaultTicketPolicy()) {
			t.Errorf("%s receives events about another customer's ticket", c.UserID)
		}
	}
	for _, typ := range events.All {
		if !CanSeeEvent(tmL1, tmSnap(tmCustC), typ, DefaultTicketPolicy()) {
			t.Errorf("staff misses %s on a visible ticket", typ)
		}
	}
}

// I-8: the triage queue is visible to supervisors and admins, and to agents
// only while FD_TRIAGE_VISIBLE_TO_AGENTS is on (and within their tier).
func TestInvariant_I8_TriageVisibility(t *testing.T) {
	on, off := DefaultTicketPolicy(), DefaultTicketPolicy()
	off.TriageVisibleToAgents = false
	triage := func(pol TicketPolicy, tier int) TicketView {
		return tmTk(tmTeam(""), tmTier(tier), func(v *TicketView) { v.Policy = pol })
	}
	if !CanSeeTicket(tmL1, triage(on, 1)) || CanSeeTicket(tmL1, triage(on, 2)) || CanSeeTicket(tmL1, triage(off, 1)) {
		t.Error("agent triage visibility")
	}
	for _, p := range []*domain.Principal{tmSup, tmAdm} {
		if !CanSeeTicket(p, triage(off, 3)) {
			t.Errorf("%s must always see triage", p.Role)
		}
	}
	predOn, _ := TicketPredicate(tmL1, on, "t")
	predOff, _ := TicketPredicate(tmL1, off, "t")
	if !strings.Contains(predOn, "team_id IS NULL") || strings.Contains(predOff, "team_id IS NULL") {
		t.Errorf("SQL predicate ignores the triage flag: on=%q off=%q", predOn, predOff)
	}
}

// I-9: staff-only custom fields never reach customers, who write only
// customer_editable ones.
func TestInvariant_I9_CustomFieldVisibility(t *testing.T) {
	for _, r := range i9Rows() {
		if got := r.ok(); got != r.want {
			t.Errorf("%s: got %v want %v", r.name, got, r.want)
		}
	}
}

// I-10: KB visibility per audience; drafts are staff-only.
func TestInvariant_I10_KBVisibility(t *testing.T) {
	for _, r := range i10Rows() {
		if got := r.ok(); got != r.want {
			t.Errorf("%s: got %v want %v", r.name, got, r.want)
		}
	}
	if ArticleAudienceOf(nil) != ArticleAnonymous || ArticleAudienceOf(tmMgr) != ArticleCustomer || ArticleAudienceOf(tmSup) != ArticleStaff {
		t.Error("audience mapping")
	}
}

// invStatus is the HTTP status a Can error maps to (0 = allowed).
func invStatus(err error) int {
	if err == nil {
		return 0
	}
	return problems.From(err).Status()
}

// I-11 (D-20): an agent sees only tickets of the projects they are members
// of, combined with the team/tier/assignee/watcher rules; supervisors and
// admins see every project. Event snapshots written before D-20 carry no
// project and count as the Default project.
func TestInvariant_I11_AgentProjectVisibility(t *testing.T) {
	other := "proj-other"
	snap := tmSnap(tmCustC)
	for _, p := range []*domain.Principal{tmL1, tmL2, tmL3} {
		if !CanSeeEvent(p, snap, events.TicketCreated, DefaultTicketPolicy()) {
			t.Errorf("%s misses an event of its project (pre-D-20 snapshot = Default project)", p.UserID)
		}
		s := snap
		s.ProjectID = other
		s.AssigneeID = p.UserID
		s.Watchers = []string{p.UserID}
		if CanSeeEvent(p, s, events.TicketCreated, DefaultTicketPolicy()) {
			t.Errorf("%s receives an event of a project it is not a member of", p.UserID)
		}
		v := tmTk(func(v *TicketView) { v.ProjectID = other })
		if Can(p, ActReply, v) == nil || problems.From(Can(p, ActReply, v)).Slug != problems.SlugNotFound {
			t.Errorf("%s: a ticket outside its projects must be 404", p.UserID)
		}
		if pred, _ := TicketPredicate(&domain.Principal{UserID: "x", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Teams: p.Teams}, DefaultTicketPolicy(), "t"); pred != "1=0" {
			t.Errorf("an agent without projects must get the empty predicate, got %q", pred)
		}
		pred, args := TicketPredicate(p, DefaultTicketPolicy(), "t")
		if !strings.HasPrefix(pred, "((t.project_id || '') IN (") || args[0] != domain.DefaultProjectID {
			t.Errorf("%s: predicate must start with the project restriction: %s %v", p.UserID, pred, args)
		}
	}
	for _, p := range []*domain.Principal{tmSup, tmAdm} {
		s := snap
		s.ProjectID = other
		if !CanSeeEvent(p, s, events.TicketCreated, DefaultTicketPolicy()) {
			t.Errorf("%s must see every project", p.UserID)
		}
	}
	// The project admin role widens configuration rights, never visibility.
	padm := &domain.Principal{UserID: "pa", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 3, Teams: []string{tmTeamA},
		Projects: []string{domain.DefaultProjectID}, AdminProjects: []string{domain.DefaultProjectID}}
	if CanSeeTicket(padm, tmTk(func(v *TicketView) { v.TeamID = tmTeamB })) {
		t.Error("a project admin sees tickets of foreign teams only like any agent (I-4)")
	}
}
