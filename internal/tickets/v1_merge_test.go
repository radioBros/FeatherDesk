// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// mergeFix: two tickets in team A of the same organization.
type mergeFix struct {
	e          *env
	team, org  string
	agent, sup *domain.Principal
	ca, cb     *domain.Principal
	a, b       string
}

func newMergeFix(t *testing.T, db *store.DB, sameRequester bool) *mergeFix {
	e := newEnv(t, db)
	f := &mergeFix{e: e, team: storetest.CreateTeam(t, db, ""), org: storetest.CreateOrg(t, db, "")}
	f.agent = e.staff(domain.RoleAgent, 1, f.team)
	f.sup = e.staff(domain.RoleSupervisor, 1)
	f.ca = e.customer(f.org, domain.RoleRequester)
	f.cb = f.ca
	if !sameRequester {
		f.cb = e.customer(f.org, domain.RoleRequester)
	}
	f.a = e.createTicket(f.ca, CreateInput{Subject: "Source", BodyMD: "source body"})
	f.b = e.createTicket(f.cb, CreateInput{Subject: "Target", BodyMD: "target body"})
	for _, id := range []string{f.a, f.b} {
		e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"team_id": f.team}).expect(t, 200)
	}
	return f
}

func (f *mergeFix) merge(p *domain.Principal) resp {
	return f.e.do(p, "POST", "/api/v1/agent/tickets/"+f.a+"/merge", map[string]any{"into_ticket_id": f.b})
}

func TestMerge_MovesMessagesAttachmentsWatchers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		att := f.e.uploadID(f.agent, "log.txt", []byte("log"))
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/messages", map[string]any{"kind": "public", "body_md": "with file", "attachment_ids": []string{att}}).expect(t, 201)
		w := f.e.staff(domain.RoleAgent, 2, f.team)
		f.e.do(f.sup, "POST", "/api/v1/agent/tickets/"+f.a+"/watchers", map[string]any{"user_id": w.UserID}).expect(t, 200)
		var msgID string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT id FROM ticket_messages WHERE ticket_id = ? AND body_md = 'source body'`, f.a).Scan(&msgID)
		f.merge(f.sup).expect(t, 200)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE id = ? AND ticket_id = ? AND moved_from_ticket_id = ?`, msgID, f.b, f.a); n != 1 {
			t.Fatal("message not moved with its id")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM attachments WHERE id = ? AND ticket_id = ?`, att, f.b); n != 1 {
			t.Fatal("attachment not moved")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?`, f.b, w.UserID); n != 1 {
			t.Fatal("watcher not moved")
		}
		if slices.Index(f.e.eventsOf(f.a), events.TicketMerged) < 0 || slices.Index(f.e.eventsOf(f.b), events.TicketMerged) < 0 {
			t.Fatal("ticket.merged missing")
		}
	})
}

func TestMerge_SourceClosedAsDuplicate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		f.merge(f.sup).expect(t, 200)
		r, _ := lockFreeLoad(context.Background(), f.e.svc, f.a)
		if r.Status != domain.StatusClosed {
			t.Fatalf("source %s", r.Status)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_links WHERE from_ticket_id = ? AND to_ticket_id = ? AND type = 'duplicate_of'`, f.a, f.b); n != 1 {
			t.Fatal("no duplicate_of link")
		}
	})
}

func TestMerge_IntoClosed409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.b, map[string]any{"status": "resolved"}).expect(t, 200)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.b, map[string]any{"status": "closed"}).expect(t, 200)
		f.merge(f.sup).expect(t, 409)
		f.e.do(f.sup, "POST", "/api/v1/agent/tickets/"+f.a+"/merge", map[string]any{"into_ticket_id": f.a}).expect(t, 422)
	})
}

func TestMerge_CrossRequesterDifferentOrg409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		other := f.e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		c := f.e.createTicket(other, CreateInput{})
		p := f.e.do(f.sup, "POST", "/api/v1/agent/tickets/"+f.a+"/merge", map[string]any{"into_ticket_id": c}).expect(t, 409).problem(t)
		if p["type"] != problems.BaseURI+problems.SlugMergeCrossRequester {
			t.Fatalf("type %v", p["type"])
		}
	})
}

func TestMerge_SameOrgRequesterBecomesCC(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, false)
		f.merge(f.sup).expect(t, 200)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?`, f.b, f.ca.UserID); n != 1 {
			t.Fatal("source requester not CC of the target")
		}
		f.e.do(f.ca, "GET", "/api/v1/portal/tickets/"+f.b, nil).expect(t, 200)
	})
}

func TestMerge_AgentNotSourceAssignee403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		f.merge(f.agent).expect(t, 403)
		f.e.do(f.agent, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"assignee_id": f.agent.UserID}).expect(t, 200)
		f.merge(f.agent).expect(t, 200)
	})
}

// TestMerge_AgentNeedsBothVisibleAndSourceAssignee (AUTH-011, permissions_spec
// §3 merge ◐): an agent merges only when it sees both tickets and is the
// assignee of the source. An invisible target answers 404 (existence is never
// disclosed), a visible source it does not own answers 403; supervisors merge
// without owning the source.
func TestMerge_AgentNeedsBothVisibleAndSourceAssignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		// Assignee of the source, but the target is escalated past its tier.
		f.e.do(f.agent, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"assignee_id": f.agent.UserID}).expect(t, 200)
		f.e.exec(`UPDATE tickets SET tier = 2 WHERE id = ?`, f.b)
		f.merge(f.agent).expect(t, 404)
		// Target visible again but the source is owned by someone else: 403.
		f.e.exec(`UPDATE tickets SET tier = 1 WHERE id = ?`, f.b)
		other := f.e.staff(domain.RoleAgent, 1, f.team)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"assignee_id": other.UserID}).expect(t, 200)
		f.merge(f.agent).expect(t, 403)
		// Both visible and source assignee: allowed.
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"assignee_id": f.agent.UserID}).expect(t, 200)
		f.merge(f.agent).expect(t, 200)
	})
}

func TestMerge_CustomerSeesMergedNotice(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		f.merge(f.sup).expect(t, 200)
		var n int64
		_ = db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, f.b).Scan(&n)
		r := f.e.do(f.ca, "GET", "/api/v1/portal/tickets/"+f.a, nil).expect(t, 200)
		if !strings.Contains(string(r.body), "merged into #") || f.e.do(f.ca, "GET", "/api/v1/portal/tickets/"+f.b, nil).StatusCode != 200 {
			t.Fatalf("no merged notice: %s", r.body)
		}
	})
}

func TestSplit_CopiesSelectedMessagesOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		m1 := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/messages", map[string]any{"kind": "public", "body_md": "second issue"}).expect(t, 201).m(t)["id"].(string)
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/messages", map[string]any{"kind": "public", "body_md": "stays"}).expect(t, 201)
		child := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/split", map[string]any{"message_ids": []string{m1}, "subject": "Second issue"}).expect(t, 201).m(t)
		cid := child["id"].(string)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'second issue'`, cid); n != 1 {
			t.Fatal("selected message not copied")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'stays'`, cid); n != 0 {
			t.Fatal("unselected message copied")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE id = ? AND ticket_id = ? AND split_to_ticket_id = ?`, m1, f.a, cid); n != 1 {
			t.Fatal("original not kept/marked")
		}
		r, _ := lockFreeLoad(context.Background(), f.e.svc, cid)
		if r.RequesterID != f.ca.UserID || r.TeamID != f.team || r.Tier != 1 {
			t.Fatalf("child %+v", r)
		}
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/split", map[string]any{"message_ids": []string{store.NewID()}, "subject": "x"}).expect(t, 422)
		if n := len(slices.DeleteFunc(f.e.eventsOf(f.a), func(s string) bool { return s != events.TicketSplit })); n != 1 {
			t.Fatalf("%d split events", n)
		}
	})
}

func TestSplit_ChildHasParentLink(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		m1 := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/messages", map[string]any{"kind": "public", "body_md": "x"}).expect(t, 201).m(t)["id"].(string)
		child := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/split", map[string]any{"message_ids": []string{m1}, "subject": "Child"}).expect(t, 201).m(t)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_links WHERE from_ticket_id = ? AND to_ticket_id = ? AND type = 'parent_of'`, f.a, child["id"]); n != 1 {
			t.Fatal("no parent_of link")
		}
	})
}

func TestSplit_InternalNoteNotExposedToCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		note := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/messages", map[string]any{"kind": "internal", "body_md": "SPLIT-SECRET"}).expect(t, 201).m(t)["id"].(string)
		child := f.e.do(f.agent, "POST", "/api/v1/agent/tickets/"+f.a+"/split", map[string]any{"message_ids": []string{note}, "subject": "Child"}).expect(t, 201).m(t)
		r := f.e.do(f.ca, "GET", "/api/v1/portal/tickets/"+child["id"].(string), nil).expect(t, 200)
		if strings.Contains(string(r.body), "SPLIT-SECRET") {
			t.Fatal("split internal note visible on the portal")
		}
	})
}

// ---- TKT-038 links ----

func linkTickets(t *testing.T, e *env, p *domain.Principal, from, to, typ string) resp {
	return e.do(p, "POST", "/api/v1/agent/tickets/"+from+"/links", map[string]any{"to_ticket_id": to, "type": typ})
}

func TestLinks_OneParentOnly409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		c := f.e.createTicket(f.ca, CreateInput{})
		linkTickets(t, f.e, f.sup, f.a, c, "parent_of").expect(t, 201)
		linkTickets(t, f.e, f.sup, f.b, c, "parent_of").expect(t, 409)
		linkTickets(t, f.e, f.sup, f.a, c, "parent_of").expect(t, 409)
	})
}

func TestLinks_CycleRefused409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		c := f.e.createTicket(f.ca, CreateInput{})
		linkTickets(t, f.e, f.sup, f.a, f.b, "parent_of").expect(t, 201)
		linkTickets(t, f.e, f.sup, f.b, c, "parent_of").expect(t, 201)
		p := linkTickets(t, f.e, f.sup, c, f.a, "parent_of").expect(t, 409).problem(t)
		if p["type"] != problems.BaseURI+problems.SlugLinkCycle {
			t.Fatalf("type %v", p["type"])
		}
	})
}

func TestLinks_SelfLink422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		linkTickets(t, f.e, f.sup, f.a, f.a, "related_to").expect(t, 422)
	})
}

func TestLinks_TargetNotVisible404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		hidden := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.ca.UserID, TeamID: storetest.CreateTeam(t, db, "")})
		linkTickets(t, f.e, f.agent, f.a, hidden, "related_to").expect(t, 404)
		r := linkTickets(t, f.e, f.agent, f.a, f.b, "related_to").expect(t, 201).m(t)
		var links []map[string]any
		f.e.do(f.agent, "GET", "/api/v1/agent/tickets/"+f.b+"/links", nil).expect(t, 200).json(t, &links)
		if len(links) != 1 || links[0]["id"] != r["id"] {
			t.Fatalf("related_to not shown on both sides: %v", links)
		}
		f.e.do(f.agent, "DELETE", "/api/v1/agent/tickets/"+f.b+"/links/"+r["id"].(string), nil).expect(t, 204)
		if slices.Index(f.e.eventsOf(f.a), events.TicketUnlinked) < 0 || slices.Index(f.e.eventsOf(f.b), events.TicketUnlinked) < 0 {
			t.Fatal("unlinked events missing")
		}
	})
}

func TestLinks_CustomerSeesOnlyVisibleLinks(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, false)
		linkTickets(t, f.e, f.sup, f.a, f.b, "related_to").expect(t, 201)
		r := f.e.do(f.ca, "GET", "/api/v1/portal/tickets/"+f.a, nil).expect(t, 200)
		if strings.Contains(string(r.body), f.b) || strings.Contains(string(r.body), "Target") {
			t.Fatal("customer learns about a ticket they cannot see")
		}
	})
}

func TestLinks_DuplicateOfRequiresClosedSource(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		linkTickets(t, f.e, f.sup, f.a, f.b, "duplicate_of").expect(t, 409)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"status": "resolved"}).expect(t, 200)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"status": "closed"}).expect(t, 200)
		linkTickets(t, f.e, f.sup, f.a, f.b, "duplicate_of").expect(t, 201)
	})
}

func TestParent_ResolveWithOpenChildrenWarns(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		linkTickets(t, f.e, f.sup, f.a, f.b, "parent_of").expect(t, 201)
		r := f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.a, map[string]any{"status": "resolved"}).expect(t, 200).m(t)
		ws, _ := r["warnings"].([]any)
		if len(ws) != 1 || ws[0] != "open_children" {
			t.Fatalf("warnings %v", r["warnings"])
		}
	})
}

func TestParent_LastChildResolvedNotifiesAssignee(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newMergeFix(t, db, true)
		c := f.e.createTicket(f.ca, CreateInput{})
		linkTickets(t, f.e, f.sup, f.a, f.b, "parent_of").expect(t, 201)
		linkTickets(t, f.e, f.sup, f.a, c, "parent_of").expect(t, 201)
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+f.b, map[string]any{"status": "resolved"}).expect(t, 200)
		if slices.Contains(f.e.eventsOf(f.a), events.TicketChildrenResolved) {
			t.Fatal("children_resolved before the last child")
		}
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+c, map[string]any{"status": "resolved"}).expect(t, 200)
		if !slices.Contains(f.e.eventsOf(f.a), events.TicketChildrenResolved) {
			t.Fatal("no ticket.children_resolved on the parent")
		}
	})
}

// ---- TKT-040 custom field values ----

type cfFix struct {
	*lifecycle
	adm *domain.Principal
}

func newCFFix(t *testing.T, db *store.DB) *cfFix {
	l := newLifecycle(t, db)
	f := &cfFix{lifecycle: l, adm: l.e.staff(domain.RoleAdmin, 1)}
	opts := []map[string]any{{"value": "eu", "label": map[string]string{"en": "EU"}}, {"value": "us", "label": map[string]string{"en": "US"}}}
	for _, d := range []map[string]any{
		{"key": "order_id", "label": map[string]string{"en": "Order"}, "type": "text", "customer_visible": true, "customer_editable": true},
		{"key": "qty", "label": map[string]string{"en": "Qty"}, "type": "number", "customer_visible": true},
		{"key": "due", "label": map[string]string{"en": "Due"}, "type": "date"},
		{"key": "region", "label": map[string]string{"en": "Region"}, "type": "select", "options": opts},
		{"key": "regions", "label": map[string]string{"en": "Regions"}, "type": "multiselect", "options": opts},
		{"key": "vip", "label": map[string]string{"en": "VIP"}, "type": "checkbox"},
		{"key": "root_cause", "label": map[string]string{"en": "Root cause"}, "type": "textarea"},
	} {
		cfCreate(t, l.e, f.adm, d)
	}
	return f
}

func TestCF_TypedValidation422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		bad := map[string]any{"qty": "many", "due": "2026-13-40", "region": "mars", "regions": []string{"eu", "xx"}, "vip": "yes", "order_id": strings.Repeat("x", 1001)}
		p := f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": bad}).expect(t, 422).problem(t)
		if n := len(p["errors"].([]any)); n != 6 {
			t.Fatalf("errors %v", p["errors"])
		}
		good := map[string]any{"qty": 3, "due": "2026-12-01", "region": "eu", "regions": []string{"us", "eu"}, "vip": true, "order_id": "A-1"}
		r := f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": good}).expect(t, 200).m(t)
		cf := r["ticket"].(map[string]any)["custom_fields"].(map[string]any)
		if cf["qty"] != float64(3) || cf["due"] != "2026-12-01" || cf["vip"] != true || len(cf["regions"].([]any)) != 2 {
			t.Fatalf("stored %v", cf)
		}
		if ev := f.e.eventsOf(f.ticketID); ev[len(ev)-1] != events.TicketFieldsChanged {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestCF_RequiredCustomerFieldAtCreate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		cfCreate(t, e, adm, map[string]any{"key": "serial", "label": map[string]string{"en": "Serial"}, "type": "text", "required": true, "customer_visible": true, "customer_editable": true})
		cust := e.customer("", domain.RoleRequester)
		e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y"}).expect(t, 422)
		r := e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "custom_fields": map[string]any{"serial": "SN1"}}).expect(t, 201).m(t)
		if r["custom_fields"].(map[string]any)["serial"] != "SN1" {
			t.Fatal("value not stored")
		}
	})
}

func TestCF_RequiredStaffFieldBlocksResolve409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		f.e.do(f.adm, "PATCH", "/api/v1/admin/custom-fields/"+mustFieldID(t, f.e, "root_cause"), map[string]any{"required": true}).expect(t, 200)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"status": "resolved"}).expect(t, 409)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"status": "resolved", "custom_fields": map[string]any{"root_cause": "toner"}}).expect(t, 200)
	})
}

func mustFieldID(t *testing.T, e *env, key string) string {
	d, ok := e.svc.dir.FieldByKey(context.Background(), key)
	if !ok {
		t.Fatalf("no field %s", key)
	}
	return d.ID
}

func TestCF_CustomerCannotWriteNonEditable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		f.e.do(f.cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "custom_fields": map[string]any{"qty": 2}}).expect(t, 422)
		f.e.do(f.cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "custom_fields": map[string]any{"vip": true}}).expect(t, 422)
		f.e.do(f.cust, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "custom_fields": map[string]any{"order_id": "B-2"}}).expect(t, 201)
	})
}

// TestCF_PortalFieldsPatch: PATCH /portal/tickets/{id}/fields edits
// customer-editable fields only, emits ticket.fields_changed, and is 409 once
// the ticket is closed (TKT-040).
func TestCF_PortalFieldsPatch(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		path := "/api/v1/portal/tickets/" + f.ticketID + "/fields"
		r := f.e.do(f.cust, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_id": "C-3"}}).expect(t, 200).m(t)
		if cf := r["custom_fields"].(map[string]any); cf["order_id"] != "C-3" {
			t.Fatalf("custom_fields %v", cf)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.fields_changed'`, f.ticketID); n != 1 {
			t.Fatalf("fields_changed events: %d", n)
		}
		p := f.e.do(f.cust, "PATCH", path, map[string]any{"custom_fields": map[string]any{"qty": 2, "vip": true}}).expect(t, 422).problem(t)
		codes := map[string]string{}
		for _, e := range p["errors"].([]any) {
			fe := e.(map[string]any)
			codes[fe["field"].(string)] = fe["code"].(string)
		}
		if codes["cf.qty"] != "read_only" || codes["cf.vip"] != "unknown" {
			t.Fatalf("errors %v", codes)
		}
		f.e.do(f.cust, "PATCH", path, map[string]any{"custom_fields": map[string]any{}}).expect(t, 422)
		f.e.do(f.agent, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_id": "X"}}).expect(t, 404)
		f.e.exec(`UPDATE tickets SET status = 'closed' WHERE id = ?`, f.ticketID)
		f.e.do(f.cust, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_id": "D-4"}}).expect(t, 409)
	})
}

func TestCF_FilterEqualityAndRange(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		second := f.e.createTicket(f.cust, CreateInput{})
		f.e.do(f.sup, "PATCH", "/api/v1/agent/tickets/"+second, map[string]any{"team_id": f.team}).expect(t, 200)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": map[string]any{"qty": 3, "region": "eu", "regions": []string{"eu"}, "due": "2026-11-01", "vip": true}}).expect(t, 200)
		f.e.do(f.agent, "PATCH", "/api/v1/agent/tickets/"+second, map[string]any{"custom_fields": map[string]any{"qty": 10, "region": "us", "regions": []string{"us", "eu"}, "due": "2026-12-15"}}).expect(t, 200)
		cases := map[string][]string{
			"cf.region=eu":              {f.ticketID},
			"cf.qty=gte:5":              {second},
			"cf.qty=lte:5":              {f.ticketID},
			"cf.regions=us":             {second},
			"cf.regions=eu":             {f.ticketID, second},
			"cf.due=gte:2026-12-01":     {second},
			"cf.vip=true":               {f.ticketID},
			"cf.qty=gte:1&cf.region=us": {second},
		}
		for q, want := range cases {
			ids, _ := listIDs(t, f.e, f.agent, "/api/v1/agent/tickets?sort=number&"+q)
			if !slices.Equal(ids, want) {
				t.Errorf("%s: got %v want %v", q, ids, want)
			}
		}
		f.e.do(f.agent, "GET", "/api/v1/agent/tickets?cf.qty=abc", nil).expect(t, 422)
	})
}

func TestCF_StaffOnlyHiddenInPortalREST(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": map[string]any{"root_cause": "STAFF-ONLY-CAUSE", "qty": 7}}).expect(t, 200)
		for _, p := range []string{"/api/v1/portal/tickets/" + f.ticketID, "/api/v1/portal/tickets"} {
			r := f.e.do(f.cust, "GET", p, nil).expect(t, 200)
			if strings.Contains(string(r.body), "STAFF-ONLY-CAUSE") || strings.Contains(string(r.body), "root_cause") {
				t.Fatalf("%s leaks a staff-only field (I-9)", p)
			}
			if !strings.Contains(string(r.body), `"qty":7`) {
				t.Fatalf("%s lacks the customer-visible field", p)
			}
		}
	})
}

// TestCF_StaffOnlyHiddenInCustomerWSFrames: fields_changed frames for
// customers carry only customer-visible keys (I-9 on WebSocket).
func TestCF_StaffOnlyHiddenInCustomerWSFrames(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		cursor := ""
		f.e.dispatch(&cursor)
		c := f.e.wsDial(f.cust)
		c.subscribe("s", "ticket:"+f.ticketID)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": map[string]any{"root_cause": "WS-STAFF-ONLY", "qty": 4}}).expect(t, 200)
		f.e.dispatch(&cursor)
		fr := c.waitFor(3*time.Second, func(m map[string]any) bool { return m["event"] == events.TicketFieldsChanged })
		if fr == nil {
			t.Fatal("no fields_changed frame")
		}
		raw := c.rawJoined()
		if strings.Contains(raw, "root_cause") || strings.Contains(raw, "WS-STAFF-ONLY") {
			t.Fatalf("staff-only field in a customer frame: %s", raw)
		}
		keys := fr["data"].(map[string]any)["changes"].(map[string]any)["keys"].([]any)
		if len(keys) != 1 || keys[0] != "qty" {
			t.Fatalf("keys %v", keys)
		}
	})
}

func TestSearch_CustomerCannotMatchStaffOnlyFieldText(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFFix(t, db)
		f.e.do(f.agent, "PATCH", f.path(""), map[string]any{"custom_fields": map[string]any{"root_cause": "flibbertigibbet", "order_id": "ordnumxyz"}}).expect(t, 200)
		f.e.index(f.ticketID)
		if ids := search(t, f.e, f.cust, "flibbertigibbet"); len(ids) != 0 {
			t.Fatal("customer matched a staff-only field value")
		}
		if ids := search(t, f.e, f.cust, "ordnumxyz"); len(ids) != 1 {
			t.Fatal("customer cannot find a customer-visible field value")
		}
		if ids := search(t, f.e, f.agent, "flibbertigibbet"); len(ids) != 1 {
			t.Fatal("staff cannot find a staff field value")
		}
	})
}

// ---- TKT-041 inbound e-mail ----

func TestEmailCreate_ChannelEmailAndMailboxTeam(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		cust := e.customer("", domain.RoleRequester)
		id, err := e.svc.CreateFromEmail(context.Background(), EmailTicket{InboundMessageID: "in-1", TeamID: team, RequesterID: cust.UserID, Subject: "", BodyMD: "Help"})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := lockFreeLoad(context.Background(), e.svc, id)
		if r.Channel != domain.ChannelEmail || r.TeamID != team || r.Subject != "(no subject)" {
			t.Fatalf("ticket %+v", r)
		}
		if n := e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND inbound_message_id = 'in-1' AND channel = 'email'`, id); n != 1 {
			t.Fatal("inbound id not recorded")
		}
	})
}

func TestEmailCreate_SelfRegistrationOffRefused(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		setSupport(t, e, func(s *settings.Support) { s.SelfRegistration = false })
		// mailin only creates customers when self-registration is on; an
		// unknown or non-customer requester is refused here as well.
		staff := e.staff(domain.RoleAgent, 1)
		if _, err := e.svc.CreateFromEmail(context.Background(), EmailTicket{RequesterID: staff.UserID, Subject: "x", BodyMD: "y"}); err == nil {
			t.Fatal("staff requester accepted")
		}
		if _, err := e.svc.CreateFromEmail(context.Background(), EmailTicket{RequesterID: store.NewID(), Subject: "x", BodyMD: "y"}); err == nil {
			t.Fatal("unknown requester accepted")
		}
	})
}

func TestEmailReply_PendingReopens(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_md": "?", "set_status": "pending"}).expect(t, 201)
		if _, err := l.e.svc.ReplyFromEmail(context.Background(), EmailReply{InboundMessageID: "r-1", TicketID: l.ticketID, AuthorID: l.cust.UserID, BodyMD: "answer"}); err != nil {
			t.Fatal(err)
		}
		if r := l.row(t); r.Status != domain.StatusOpen {
			t.Fatalf("status %s", r.Status)
		}
	})
}

func TestEmailReply_StaffInternalMarker(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		if _, err := l.e.svc.ReplyFromEmail(context.Background(), EmailReply{InboundMessageID: "r-2", TicketID: l.ticketID, AuthorID: l.agent.UserID, Internal: true, BodyMD: "note by mail"}); err != nil {
			t.Fatal(err)
		}
		if n := l.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND kind = 'internal' AND channel = 'email'`, l.ticketID); n != 1 {
			t.Fatal("no internal note")
		}
	})
}

func TestEmailReply_NonParticipantRefused(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		stranger := l.e.customer("", domain.RoleRequester)
		outsider := l.e.staff(domain.RoleAgent, 1, storetest.CreateTeam(t, db, ""))
		for _, a := range []string{stranger.UserID, outsider.UserID, store.NewID()} {
			_, err := l.e.svc.ReplyFromEmail(context.Background(), EmailReply{InboundMessageID: "r-" + a, TicketID: l.ticketID, AuthorID: a, BodyMD: "x"})
			if !errors.Is(err, ErrNotParticipant) {
				t.Fatalf("author %s: %v", a, err)
			}
		}
	})
}

func TestEmailReply_OnClosedCreatesFollowup(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "resolved"}).expect(t, 200)
		l.e.do(l.agent, "PATCH", l.path(""), map[string]any{"status": "closed"}).expect(t, 200)
		id, err := l.e.svc.ReplyFromEmail(context.Background(), EmailReply{InboundMessageID: "r-3", TicketID: l.ticketID, AuthorID: l.cust.UserID, BodyMD: "again"})
		if err != nil || id == l.ticketID {
			t.Fatalf("follow-up %s %v", id, err)
		}
		r, _ := lockFreeLoad(context.Background(), l.e.svc, id)
		if r.FollowupOfID != l.ticketID || r.Channel != domain.ChannelEmail {
			t.Fatalf("follow-up %+v", r)
		}
	})
}

func TestEmailCreate_DuplicateMessageIdNoop(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		in := EmailTicket{InboundMessageID: "dup-1", RequesterID: cust.UserID, Subject: "x", BodyMD: "y"}
		a, err := e.svc.CreateFromEmail(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		b, err := e.svc.CreateFromEmail(context.Background(), in)
		if err != nil || a != b || e.count(`SELECT COUNT(*) FROM tickets`) != 1 {
			t.Fatalf("duplicate created: %s %s %v", a, b, err)
		}
	})
}

func TestEmailCreate_AttachmentLimitsApplied(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db) // 1 MiB limit
		cust := e.customer("", domain.RoleRequester)
		var atts []EmailAttachment
		atts = append(atts, EmailAttachment{Filename: "huge.bin", Data: make([]byte, 2<<20)})
		for i := 0; i < 12; i++ {
			atts = append(atts, EmailAttachment{Filename: "f.txt", Data: []byte("hello")})
		}
		atts = append(atts, EmailAttachment{Filename: "original.eml", Data: []byte("From: a@b\r\n\r\nbody")})
		id, err := e.svc.CreateFromEmail(context.Background(), EmailTicket{InboundMessageID: "att-1", RequesterID: cust.UserID, Subject: "x", BodyMD: "y", Attachments: atts})
		if err != nil {
			t.Fatal(err)
		}
		if n := e.count(`SELECT COUNT(*) FROM attachments WHERE ticket_id = ?`, id); n != MaxAttachmentsPerMessage {
			t.Fatalf("%d attachments stored", n)
		}
		if n := e.count(`SELECT COUNT(*) FROM attachments WHERE size_bytes > ?`, 1<<20); n != 0 {
			t.Fatal("oversized attachment stored")
		}
		if n := e.count(`SELECT COUNT(*) FROM attachments WHERE content_type LIKE 'text/plain%' AND ticket_id = ?`, id); n == 0 {
			t.Fatal("content type not sniffed")
		}
	})
}
