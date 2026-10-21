// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// A custom field that does not apply to the ticket's category or project is
// neither required nor rejected for an empty value (the demo `order_number`:
// text, required, customer-editable, scoped to `billing`).

type cfScopeFix struct {
	e              *env
	billing, other string
	adm, agent     *domain.Principal
	cust           *domain.Principal
}

func newCFScopeFix(t *testing.T, db *store.DB) *cfScopeFix {
	e := newEnv(t, db)
	team := storetest.CreateTeam(t, db, "")
	f := &cfScopeFix{e: e, billing: storetest.CreateCategory(t, db, "Billing", team), other: storetest.CreateCategory(t, db, "Hardware", team)}
	e.svc.dir.Invalidate()
	f.adm = e.staff(domain.RoleAdmin, 1)
	f.agent = e.staff(domain.RoleAgent, 1, team)
	f.cust = e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
	cfCreate(t, e, f.adm, map[string]any{"key": "order_number", "label": map[string]string{"en": "Order"}, "type": "text",
		"required": true, "customer_visible": true, "customer_editable": true, "category_ids": []string{f.billing}})
	return f
}

func (f *cfScopeFix) portalCreate(t *testing.T, body map[string]any, code int) resp {
	t.Helper()
	body["subject"], body["body_md"] = "Invoice question", "Please check."
	return f.e.do(f.cust, "POST", "/api/v1/portal/tickets", body).expect(t, code)
}

// fieldCodes maps `field` → `code` of a 422 problem.
func fieldCodes(t *testing.T, r resp) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, x := range r.problem(t)["errors"].([]any) {
		fe := x.(map[string]any)
		out[fe["field"].(string)] = fe["code"].(string)
	}
	return out
}

func TestCF_ScopedRequiredField_NoCategoryEmptyValue201(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		for _, empty := range []any{"", "   ", nil} {
			r := f.portalCreate(t, map[string]any{"custom_fields": map[string]any{"order_number": empty}}, 201).m(t)
			if _, ok := r["custom_fields"].(map[string]any)["order_number"]; ok {
				t.Fatalf("empty %q stored: %v", empty, r["custom_fields"])
			}
		}
		r := f.portalCreate(t, map[string]any{"category_id": nil, "custom_fields": map[string]any{"order_number": ""}}, 201).m(t)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_field_values WHERE ticket_id = ?`, r["id"]); n != 0 {
			t.Fatalf("%d values written", n)
		}
	})
}

func TestCF_ScopedRequiredField_NoCategoryOmitted201(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		f.portalCreate(t, map[string]any{}, 201)
		f.portalCreate(t, map[string]any{"custom_fields": map[string]any{}}, 201)
	})
}

func TestCF_ScopedRequiredField_InCategoryMissing422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		for _, body := range []map[string]any{
			{"category_id": f.billing},
			{"category_id": f.billing, "custom_fields": map[string]any{"order_number": ""}},
			{"category_id": f.billing, "custom_fields": map[string]any{"order_number": nil}},
		} {
			if c := fieldCodes(t, f.portalCreate(t, body, 422)); c["cf.order_number"] != "required" {
				t.Fatalf("errors %v for %v", c, body)
			}
		}
		r := f.portalCreate(t, map[string]any{"category_id": f.billing, "custom_fields": map[string]any{"order_number": "INV-7"}}, 201).m(t)
		if r["custom_fields"].(map[string]any)["order_number"] != "INV-7" {
			t.Fatalf("custom_fields %v", r["custom_fields"])
		}
	})
}

func TestCF_ScopedRequiredField_OtherCategoryEmptyValue201(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		f.portalCreate(t, map[string]any{"category_id": f.other, "custom_fields": map[string]any{"order_number": ""}}, 201)
		f.portalCreate(t, map[string]any{"category_id": f.other}, 201)
		// A real value for a field that does not apply is still an error.
		if c := fieldCodes(t, f.portalCreate(t, map[string]any{"category_id": f.other, "custom_fields": map[string]any{"order_number": "INV-7"}}, 422)); c["cf.order_number"] != "not_applicable" {
			t.Fatalf("errors %v", c)
		}
	})
}

func TestCF_ScopedField_AgentCreate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		body := func(cat any, cf map[string]any) map[string]any {
			return map[string]any{"subject": "Phone call", "body_md": "Customer called", "requester_id": f.cust.UserID, "category_id": cat, "custom_fields": cf}
		}
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets", body(nil, map[string]any{"order_number": ""})).expect(t, 201)
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets", body(f.other, map[string]any{"order_number": nil})).expect(t, 201)
		f.e.do(f.agent, "POST", "/api/v1/agent/tickets", body(nil, nil)).expect(t, 201)
		if c := fieldCodes(t, f.e.do(f.agent, "POST", "/api/v1/agent/tickets", body(nil, map[string]any{"order_number": "INV-1"})).expect(t, 422)); c["cf.order_number"] != "not_applicable" {
			t.Fatalf("errors %v", c)
		}
		r := f.e.do(f.agent, "POST", "/api/v1/agent/tickets", body(f.billing, map[string]any{"order_number": "INV-2"})).expect(t, 201).m(t)
		if r["custom_fields"].(map[string]any)["order_number"] != "INV-2" {
			t.Fatalf("custom_fields %v", r["custom_fields"])
		}
	})
}

// TestCF_CategoryChangeKeepsUpdatesWorking: once the category changes, the
// stored value of a field that stopped applying neither blocks updates that
// resend it nor updates that send it empty.
func TestCF_CategoryChangeKeepsUpdatesWorking(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		id := f.portalCreate(t, map[string]any{"category_id": f.billing, "custom_fields": map[string]any{"order_number": "INV-7"}}, 201).m(t)["id"].(string)
		path := "/api/v1/agent/tickets/" + id
		// Category change and the old values in one request.
		f.e.do(f.agent, "PATCH", path, map[string]any{"category_id": f.other, "custom_fields": map[string]any{"order_number": "INV-7"}}).expect(t, 200)
		f.e.do(f.agent, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_number": ""}}).expect(t, 200)
		f.e.do(f.agent, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_number": nil}}).expect(t, 200)
		if c := fieldCodes(t, f.e.do(f.agent, "PATCH", path, map[string]any{"custom_fields": map[string]any{"order_number": "INV-8"}}).expect(t, 422)); c["cf.order_number"] != "not_applicable" {
			t.Fatalf("errors %v", c)
		}
		f.e.do(f.cust, "PATCH", "/api/v1/portal/tickets/"+id+"/fields", map[string]any{"custom_fields": map[string]any{"order_number": ""}}).expect(t, 200)
		// Nothing was written for the ignored values: the old one is kept.
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_field_values WHERE ticket_id = ? AND value_text = 'INV-7'`, id); n != 1 {
			t.Fatalf("stored value rows %d", n)
		}
		// Back in billing the field applies again and can be changed.
		r := f.e.do(f.agent, "PATCH", path, map[string]any{"category_id": f.billing, "custom_fields": map[string]any{"order_number": "INV-9"}}).expect(t, 200).m(t)
		if v := r["ticket"].(map[string]any)["custom_fields"].(map[string]any)["order_number"]; v != "INV-9" {
			t.Fatalf("order_number %v", v)
		}
	})
}

// TestCF_EmptyValueStillHidesStaffOnlyKeys: the empty-value pass for
// non-applicable fields runs after the I-9 checks, so it cannot probe
// staff-only keys.
func TestCF_EmptyValueStillHidesStaffOnlyKeys(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		cfCreate(t, f.e, f.adm, map[string]any{"key": "refund_amount", "label": map[string]string{"en": "Refund"}, "type": "number", "category_ids": []string{f.billing}})
		if c := fieldCodes(t, f.portalCreate(t, map[string]any{"custom_fields": map[string]any{"refund_amount": nil}}, 422)); c["cf.refund_amount"] != "unknown" {
			t.Fatalf("errors %v", c)
		}
	})
}

// TestCF_RequiredFieldNotEnforcedOnFollowupOrEmail: a reply to a closed
// ticket and an inbound e-mail cannot carry custom fields, so required
// customer fields never block them.
func TestCF_RequiredFieldNotEnforcedOnFollowupOrEmail(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		id := f.portalCreate(t, map[string]any{"category_id": f.billing, "custom_fields": map[string]any{"order_number": "INV-7"}}, 201).m(t)["id"].(string)
		f.e.exec(`UPDATE tickets SET status = 'closed' WHERE id = ?`, id)
		p := f.e.do(f.cust, "POST", "/api/v1/portal/tickets/"+id+"/messages", map[string]any{"body_md": "still wrong"}).expect(t, 409).problem(t)
		fu, err := lockFreeLoad(context.Background(), f.e.svc, p["detail"].(string))
		if err != nil || fu.CategoryID != f.billing || fu.FollowupOfID != id {
			t.Fatalf("follow-up %+v %v", fu, err)
		}
		// An unscoped required customer field applies to every ticket.
		cfCreate(t, f.e, f.adm, map[string]any{"key": "contract", "label": map[string]string{"en": "Contract"}, "type": "text",
			"required": true, "customer_visible": true, "customer_editable": true})
		if _, err := f.e.svc.CreateFromEmail(context.Background(), EmailTicket{InboundMessageID: "cf-mail-1", RequesterID: f.cust.UserID, Subject: "x", BodyMD: "y"}); err != nil {
			t.Fatalf("email create: %v", err)
		}
		if c := fieldCodes(t, f.portalCreate(t, map[string]any{}, 422)); c["cf.contract"] != "required" {
			t.Fatalf("portal errors %v", c)
		}
	})
}

// TestCF_EmptyScopeListAppliesEverywhere: `category_ids: []` means every
// category, like null (that is how the admin UI lists it).
func TestCF_EmptyScopeListAppliesEverywhere(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		cfCreate(t, f.e, f.adm, map[string]any{"key": "phone", "label": map[string]string{"en": "Phone"}, "type": "text",
			"customer_visible": true, "customer_editable": true, "category_ids": []string{}, "project_ids": []string{}})
		r := f.portalCreate(t, map[string]any{"custom_fields": map[string]any{"phone": "555"}}, 201).m(t)
		if r["custom_fields"].(map[string]any)["phone"] != "555" {
			t.Fatalf("custom_fields %v", r["custom_fields"])
		}
	})
}

// TestCFDef_PortalLookupKeepsCategoryScope: the portal form needs the scope to
// show a field only for its categories.
func TestCFDef_PortalLookupKeepsCategoryScope(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newCFScopeFix(t, db)
		var defs []map[string]any
		f.e.do(f.cust, "GET", "/api/v1/portal/custom-fields", nil).expect(t, 200).json(t, &defs)
		if len(defs) != 1 {
			t.Fatalf("defs %v", defs)
		}
		cats, _ := defs[0]["category_ids"].([]any)
		if len(cats) != 1 || cats[0] != f.billing || defs[0]["project_ids"] != nil {
			t.Fatalf("scope %v / %v", defs[0]["category_ids"], defs[0]["project_ids"])
		}
	})
}
