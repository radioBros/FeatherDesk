// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// ---- TKT-015 customers ----

func TestCustomers_AgentCanCreate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		u := e.do(agent, "POST", "/api/v1/agent/customers", map[string]any{"email": "Bob@Example.org", "name": "Bob"}).expect(t, 201).m(t)
		if u["kind"] != "customer" || u["email"] != "bob@example.org" || u["role"] != "requester" || u["has_password"] != false {
			t.Fatalf("created %v", u)
		}
		e.do(agent, "GET", "/api/v1/agent/customers/"+u["id"].(string), nil).expect(t, 200)
		page := e.do(agent, "GET", "/api/v1/agent/customers?q=bo", nil).expect(t, 200).m(t)
		if len(page["items"].([]any)) != 1 {
			t.Fatalf("search %v", page)
		}
		// Agents read and create; updates need a supervisor (permissions §3).
		e.do(agent, "PATCH", "/api/v1/agent/customers/"+u["id"].(string), map[string]any{"name": "Robert"}).expect(t, 403)
		sup := e.staff(domain.RoleSupervisor, 1)
		r := e.do(sup, "PATCH", "/api/v1/agent/customers/"+u["id"].(string), map[string]any{"name": "Robert"}).expect(t, 200).m(t)
		if r["name"] != "Robert" {
			t.Fatal("not updated")
		}
	})
}

func TestCustomers_CannotCreateStaffKind(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		var email string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, agent.UserID).Scan(&email)
		e.do(agent, "POST", "/api/v1/agent/customers", map[string]any{"email": email, "name": "Clash"}).expect(t, 409)
		u := e.do(agent, "POST", "/api/v1/agent/customers", map[string]any{"email": "k@example.org", "name": "K", "kind": "staff", "role": "admin"}).expect(t, 422)
		_ = u
		u2 := e.do(agent, "POST", "/api/v1/agent/customers", map[string]any{"email": "k@example.org", "name": "K", "kind": "staff"}).expect(t, 201).m(t)
		if u2["kind"] != "customer" {
			t.Fatal("kind taken from input")
		}
	})
}

func TestCustomers_DisableRevokes(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		cust := e.customer("", domain.RoleRequester)
		now := store.Now()
		e.exec(`INSERT INTO sessions (id_hash, user_id, audience, csrf_token, expires_at) VALUES ('h1', ?, 'customer', 'c', ?)`, cust.UserID, now.Add(3600e9))
		e.exec(`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, created_at) VALUES (?, ?, 't', 'st_pat_x', 'th', ?)`, store.NewID(), cust.UserID, now)
		r := e.do(sup, "PATCH", "/api/v1/agent/customers/"+cust.UserID, map[string]any{"status": "disabled"}).expect(t, 200).m(t)
		if r["status"] != "disabled" {
			t.Fatalf("status %v", r["status"])
		}
		if e.count(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, cust.UserID) != 0 ||
			e.count(`SELECT COUNT(*) FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL`, cust.UserID) != 0 {
			t.Fatal("sessions or tokens survived (I-5)")
		}
		if e.count(`SELECT COUNT(*) FROM audit_log WHERE target_id = ?`, cust.UserID) == 0 {
			t.Fatal("no audit entry")
		}
	})
}

// ---- TKT-016 organizations ----

func TestOrg_DomainUnique409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		o := e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Acme", "email_domains": []string{"ACME.test"}}).expect(t, 201).m(t)
		if o["email_domains"].([]any)[0] != "acme.test" {
			t.Fatalf("domains %v", o["email_domains"])
		}
		e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Acme 2", "email_domains": []string{"acme.test"}}).expect(t, 409)
		e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "acme"}).expect(t, 409)
		e.do(e.staff(domain.RoleAgent, 1), "POST", "/api/v1/agent/organizations", map[string]any{"name": "Other"}).expect(t, 403)
	})
}

func TestOrg_WebmailDomainRejected422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Gmail users", "email_domains": []string{"gmail.com"}}).expect(t, 422)
		e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Bad", "email_domains": []string{"not a domain"}}).expect(t, 422)
	})
}

func TestOrg_ArchivedNotAutoJoined(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		o := e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Old Corp", "email_domains": []string{"old.test"}}).expect(t, 201).m(t)
		id := o["id"].(string)
		e.do(sup, "PATCH", "/api/v1/agent/organizations/"+id, map[string]any{"status": "archived"}).expect(t, 200)
		u := e.do(sup, "POST", "/api/v1/agent/customers", map[string]any{"email": "x@old.test", "name": "X"}).expect(t, 201).m(t)
		if u["organization_id"] != nil {
			t.Fatal("joined an archived organization")
		}
		// The archived org's domains are free again.
		e.do(sup, "POST", "/api/v1/agent/organizations", map[string]any{"name": "New Corp", "email_domains": []string{"old.test"}}).expect(t, 201)
		u2 := e.do(sup, "POST", "/api/v1/agent/customers", map[string]any{"email": "y@old.test", "name": "Y"}).expect(t, 201).m(t)
		if u2["organization_id"] == nil || u2["organization_id"] == id {
			t.Fatal("not joined to the active organization")
		}
		g := e.do(sup, "GET", "/api/v1/agent/organizations/"+id, nil).expect(t, 200).m(t)
		if g["status"] != "archived" {
			t.Fatal("not archived")
		}
	})
}

// ---- TKT-017 teams ----

func TestTeam_CustomerMemberRefused(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Network"}).expect(t, 201).m(t)
		cust := e.customer("", domain.RoleRequester)
		e.do(sup, "POST", "/api/v1/admin/teams/"+team["id"].(string)+"/members", map[string]any{"user_id": cust.UserID}).expect(t, 422)
		agent := e.staff(domain.RoleAgent, 2)
		m := e.do(sup, "POST", "/api/v1/admin/teams/"+team["id"].(string)+"/members", map[string]any{"user_id": agent.UserID}).expect(t, 201).m(t)
		if m["tier"] != float64(2) || m["source"] != "local" {
			t.Fatalf("member %v", m)
		}
		e.do(sup, "POST", "/api/v1/admin/teams/"+team["id"].(string)+"/members", map[string]any{"user_id": agent.UserID}).expect(t, 409)
		var ms []map[string]any
		e.do(sup, "GET", "/api/v1/admin/teams/"+team["id"].(string)+"/members", nil).expect(t, 200).json(t, &ms)
		if len(ms) != 1 {
			t.Fatalf("members %v", ms)
		}
		e.do(agent, "POST", "/api/v1/admin/teams", map[string]any{"name": "Nope"}).expect(t, 403)
	})
}

func TestTeam_RemoveMemberUnassignsInvalidTickets(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		sup := f.e.staff(domain.RoleSupervisor, 1)
		f.e.do(f.l1, "PATCH", f.path(""), map[string]any{"assignee_id": f.l1.UserID}).expect(t, 200)
		f.e.do(sup, "DELETE", "/api/v1/admin/teams/"+f.teamA+"/members/"+f.l1.UserID, nil).expect(t, 204)
		r, _ := lockFreeLoad(context.Background(), f.e.svc, f.id)
		if r.AssigneeID != "" {
			t.Fatal("ticket kept an assignee outside the team (M-4)")
		}
		ev := f.e.eventsOf(f.id)
		if ev[len(ev)-1] != "ticket.assigned" {
			t.Fatalf("events %v", ev)
		}
	})
}

func TestTeam_DeleteWithTickets409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Billing"}).expect(t, 201).m(t)["id"].(string)
		cat := e.do(sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "Invoices", "default_team_id": team}).expect(t, 201).m(t)["id"].(string)
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: e.customer("", domain.RoleRequester).UserID, TeamID: team})
		e.do(sup, "DELETE", "/api/v1/admin/teams/"+team, nil).expect(t, 409)
		e.do(sup, "PATCH", "/api/v1/agent/tickets/"+id, map[string]any{"team_id": nil}).expect(t, 200)
		e.do(sup, "DELETE", "/api/v1/admin/teams/"+team, nil).expect(t, 204)
		var cats []map[string]any
		e.do(sup, "GET", "/api/v1/admin/categories", nil).expect(t, 200).json(t, &cats)
		if cats[0]["id"] != cat || cats[0]["default_team_id"] != nil {
			t.Fatalf("category routing not reset: %v", cats)
		}
	})
}

func TestTeam_MailboxModeRequiresEmail422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "A", "notify_mode": "mailbox"}).expect(t, 422)
		e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "A", "notify_mode": "both", "notification_email": nil}).expect(t, 422)
		r := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "A", "notify_mode": "both", "notification_email": "team-a@example.org"}).expect(t, 201).m(t)
		if r["notify_mode"] != "both" || r["notification_email"] != "team-a@example.org" {
			t.Fatalf("team %v", r)
		}
	})
}

func TestTeam_InvalidNotificationEmail422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "A", "notification_email": "not-an-email"}).expect(t, 422)
	})
}

// ---- TKT-018 categories ----

func TestCategories_InactiveHiddenFromPortal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		e.do(sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "B visible", "sort": 2}).expect(t, 201)
		e.do(sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "A visible", "sort": 1}).expect(t, 201)
		e.do(sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "Hidden", "active": false}).expect(t, 201)
		var cats []map[string]any
		e.do(e.customer("", domain.RoleRequester), "GET", "/api/v1/portal/categories", nil).expect(t, 200).json(t, &cats)
		if len(cats) != 2 || cats[0]["name"] != "A visible" || cats[1]["name"] != "B visible" {
			t.Fatalf("portal categories %v", cats)
		}
		e.do(e.staff(domain.RoleAgent, 1), "GET", "/api/v1/agent/categories", nil).expect(t, 200).json(t, &cats)
		if len(cats) != 3 {
			t.Fatalf("agent categories %d", len(cats))
		}
	})
}

func TestCategories_SupervisorCanEditAgent403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup, agent := e.staff(domain.RoleSupervisor, 1), e.staff(domain.RoleAgent, 3)
		id := e.do(sup, "POST", "/api/v1/admin/categories", map[string]any{"name": "Hardware"}).expect(t, 201).m(t)["id"].(string)
		e.do(sup, "PATCH", "/api/v1/admin/categories/"+id, map[string]any{"name": "Hardware & devices", "default_priority": "high"}).expect(t, 200)
		e.do(agent, "PATCH", "/api/v1/admin/categories/"+id, map[string]any{"name": "x"}).expect(t, 403)
		e.do(e.customer("", domain.RoleRequester), "PATCH", "/api/v1/admin/categories/"+id, map[string]any{"name": "x"}).expect(t, 404)
	})
}

// ---- TKT-019 settings ----

func TestSettings_ValidationPerField(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		p := e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"auto_close_days": 0, "reopen_window_days": 91, "default_locale": "xx", "tier_labels": map[string]string{"1": ""}}).expect(t, 422).m(t)
		if n := len(p["errors"].([]any)); n != 4 {
			t.Fatalf("errors %v", p["errors"])
		}
		r := e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"auto_close_days": 14, "tier_labels": map[string]string{"2": "Second line"}}).expect(t, 200).m(t)
		if r["auto_close_days"] != float64(14) || r["tier_labels"].(map[string]any)["2"] != "Second line" || r["tier_labels"].(map[string]any)["1"] != "L1" {
			t.Fatalf("settings %v", r)
		}
		e.do(e.staff(domain.RoleSupervisor, 1), "GET", "/api/v1/admin/settings", nil).expect(t, 403)
	})
}

func TestSettings_PortalEnabledToggle(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		if r := e.do(adm, "GET", "/api/v1/admin/settings", nil).expect(t, 200).m(t); r["portal_enabled"] != true {
			t.Fatalf("default portal_enabled: %v", r["portal_enabled"])
		}
		e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": "no"}).expect(t, 422)
		if r := e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": false}).expect(t, 200).m(t); r["portal_enabled"] != false || r["kb_enabled"] != true {
			t.Fatalf("after off: %v", r)
		}
		if r := e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"auto_close_days": 10}).expect(t, 200).m(t); r["portal_enabled"] != false {
			t.Fatalf("unrelated patch reset the switch: %v", r)
		}
	})
}

func TestSettings_CacheInvalidatedOnWrite(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		e.do(adm, "GET", "/api/v1/admin/settings", nil).expect(t, 200)
		// Change the row behind the cache's back: the cached value is served.
		e.exec(`UPDATE settings SET value = ? WHERE key = 'support'`, `{"auto_close_days": 33}`)
		if r := e.do(adm, "GET", "/api/v1/admin/settings", nil).m(t); r["auto_close_days"] != float64(7) {
			t.Fatalf("not served from memory: %v", r["auto_close_days"])
		}
		e.do(adm, "PATCH", "/api/v1/admin/settings", map[string]any{"auto_close_days": 9}).expect(t, 200)
		if r := e.do(adm, "GET", "/api/v1/admin/settings", nil).m(t); r["auto_close_days"] != float64(9) {
			t.Fatal("write did not refresh the cache")
		}
	})
}

func TestSettings_PublicSubsetOnly(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	e := newEnv(t, db)
	pub := publicSupportKeys(e)
	allowed := map[string]bool{"tier_labels": true, "self_registration": true, "customer_urgent_allowed": true, "kb_enabled": true, "default_locale": true, "locales": true}
	for k := range pub {
		if !allowed[k] {
			t.Errorf("public key %q not in the allowlist", k)
		}
	}
	for _, secret := range []string{"auto_close_days", "urgent_requires_supervisor", "triage_visible_to_agents", "password", "secret"} {
		if _, ok := pub[secret]; ok {
			t.Errorf("%s is public", secret)
		}
	}
}

// ---- TKT-037 lookups ----

func TestLookups_CustomerOnAgentRoutes404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		for _, p := range []string{"/api/v1/agent/teams", "/api/v1/agent/categories", "/api/v1/agent/staff", "/api/v1/agent/custom-fields", "/api/v1/agent/customers", "/api/v1/admin/teams"} {
			e.do(cust, "GET", p, nil).expect(t, 404)
		}
	})
}

func TestStaffLookup_ExcludesDisabledAndCustomers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 2, team)
		gone := e.staff(domain.RoleAgent, 1)
		e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, gone.UserID)
		e.customer("", domain.RoleRequester)
		var list []map[string]any
		e.do(agent, "GET", "/api/v1/agent/staff", nil).expect(t, 200).json(t, &list)
		if len(list) != 1 || list[0]["id"] != agent.UserID || list[0]["team_ids"].([]any)[0] != team {
			t.Fatalf("staff lookup %v", list)
		}
		e.do(agent, "GET", "/api/v1/agent/staff?q=zzz", nil).expect(t, 200).json(t, &list)
		if len(list) != 0 {
			t.Fatal("prefix search")
		}
	})
}

func TestLookups_ZeroDBReadsWhenCached(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		storetest.CreateTeam(t, db, "")
		e.svc.dir.Invalidate()
		e.do(agent, "GET", "/api/v1/agent/teams", nil).expect(t, 200)
		loads := e.svc.dir.SnapshotLoads()
		for i := 0; i < 20; i++ {
			e.do(agent, "GET", "/api/v1/agent/teams", nil).expect(t, 200)
			e.do(agent, "GET", "/api/v1/agent/categories", nil).expect(t, 200)
			e.do(agent, "GET", "/api/v1/agent/custom-fields", nil).expect(t, 200)
		}
		if e.svc.dir.SnapshotLoads() != loads {
			t.Fatal("lookups read the database while cached")
		}
	})
}

// ---- TKT-039 custom field definitions ----

func cfCreate(t *testing.T, e *env, p *domain.Principal, body map[string]any) map[string]any {
	t.Helper()
	return e.do(p, "POST", "/api/v1/admin/custom-fields", body).expect(t, 201).m(t)
}

func TestCFDef_AdminOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		body := map[string]any{"key": "order_id", "label": map[string]string{"en": "Order"}, "type": "text"}
		e.do(e.staff(domain.RoleSupervisor, 1), "POST", "/api/v1/admin/custom-fields", body).expect(t, 403)
		e.do(e.customer("", domain.RoleRequester), "POST", "/api/v1/admin/custom-fields", body).expect(t, 404)
		cfCreate(t, e, e.staff(domain.RoleAdmin, 1), body)
	})
}

func TestCFDef_KeyImmutable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		f := cfCreate(t, e, adm, map[string]any{"key": "order_id", "label": map[string]string{"en": "Order"}, "type": "text"})
		e.do(adm, "PATCH", "/api/v1/admin/custom-fields/"+f["id"].(string), map[string]any{"key": "other"}).expect(t, 422)
		e.do(adm, "POST", "/api/v1/admin/custom-fields", map[string]any{"key": "order_id", "label": map[string]string{"en": "Dup"}, "type": "text"}).expect(t, 409)
	})
}

func TestCFDef_TypeChangeWithValues409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		f := cfCreate(t, e, adm, map[string]any{"key": "qty", "label": map[string]string{"en": "Qty"}, "type": "text"})
		cust := e.customer("", domain.RoleRequester)
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust.UserID})
		e.exec(`INSERT INTO ticket_field_values (ticket_id, field_id, value_text, updated_at) VALUES (?, ?, '3', ?)`, id, f["id"], store.Now())
		e.do(adm, "PATCH", "/api/v1/admin/custom-fields/"+f["id"].(string), map[string]any{"type": "number"}).expect(t, 409)
	})
}

func TestCFDef_OptionInUseRemoval409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		opts := []map[string]any{{"value": "eu", "label": map[string]string{"en": "EU"}}, {"value": "us", "label": map[string]string{"en": "US"}}}
		f := cfCreate(t, e, adm, map[string]any{"key": "region", "label": map[string]string{"en": "Region"}, "type": "select", "options": opts})
		id := storetest.CreateTicket(t, db, storetest.Ticket{Requester: e.customer("", domain.RoleRequester).UserID})
		e.exec(`INSERT INTO ticket_field_values (ticket_id, field_id, value_text, updated_at) VALUES (?, ?, 'eu', ?)`, id, f["id"], store.Now())
		e.do(adm, "PATCH", "/api/v1/admin/custom-fields/"+f["id"].(string), map[string]any{"options": opts[1:]}).expect(t, 409)
		e.do(adm, "PATCH", "/api/v1/admin/custom-fields/"+f["id"].(string), map[string]any{"options": opts[:1]}).expect(t, 200)
		r := e.do(adm, "PATCH", "/api/v1/admin/custom-fields/"+f["id"].(string), map[string]any{"active": false}).expect(t, 200).m(t)
		if r["active"] != false {
			t.Fatal("not archived")
		}
	})
}

func TestCFDef_EditableImpliesVisible(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		e.do(adm, "POST", "/api/v1/admin/custom-fields", map[string]any{"key": "x", "label": map[string]string{"en": "X"}, "type": "text", "customer_editable": true}).expect(t, 422)
		cfCreate(t, e, adm, map[string]any{"key": "x", "label": map[string]string{"en": "X"}, "type": "text", "customer_editable": true, "customer_visible": true})
	})
}

func TestCFDef_PortalExposesOnlyCustomerVisible(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		cfCreate(t, e, adm, map[string]any{"key": "internal_code", "label": map[string]string{"en": "Code"}, "type": "text"})
		cfCreate(t, e, adm, map[string]any{"key": "phone", "label": map[string]string{"en": "Phone"}, "type": "text", "customer_visible": true, "customer_editable": true})
		var defs []map[string]any
		e.do(e.customer("", domain.RoleRequester), "GET", "/api/v1/portal/custom-fields", nil).expect(t, 200).json(t, &defs)
		if len(defs) != 1 || defs[0]["key"] != "phone" {
			t.Fatalf("portal defs %v", defs)
		}
		e.do(e.staff(domain.RoleAgent, 1), "GET", "/api/v1/agent/custom-fields", nil).expect(t, 200).json(t, &defs)
		if len(defs) != 2 {
			t.Fatalf("agent defs %d", len(defs))
		}
	})
}

func TestCFDef_SnapshotZeroDBReads(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		cfCreate(t, e, adm, map[string]any{"key": "a", "label": map[string]string{"en": "A"}, "type": "checkbox"})
		loads := e.svc.dir.SnapshotLoads()
		cust := e.customer("", domain.RoleRequester)
		for i := 0; i < 10; i++ {
			e.do(cust, "GET", "/api/v1/portal/custom-fields", nil).expect(t, 200)
			e.do(adm, "GET", "/api/v1/admin/custom-fields", nil).expect(t, 200)
		}
		if e.svc.dir.SnapshotLoads() != loads {
			t.Fatal("definitions read from the DB on the request path")
		}
	})
}

// ---- TKT-035 calendars over HTTP ----

func TestCalendar_DeleteReferenced409(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		c := e.do(sup, "POST", "/api/v1/admin/calendars", map[string]any{"name": "Rome office", "timezone": "Europe/Rome",
			"week": map[string]any{"mon": []map[string]string{{"start": "09:00", "end": "18:00"}}}, "holidays": []string{"2026-12-25"}}).expect(t, 201).m(t)
		cid := c["id"].(string)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "Rome", "calendar_id": cid}).expect(t, 201).m(t)
		e.do(sup, "DELETE", "/api/v1/admin/calendars/"+cid, nil).expect(t, 409)
		e.do(sup, "PATCH", "/api/v1/admin/teams/"+team["id"].(string), map[string]any{"name": "Rome", "calendar_id": nil}).expect(t, 200)
		e.do(sup, "DELETE", "/api/v1/admin/calendars/"+cid, nil).expect(t, 204)
		e.do(sup, "POST", "/api/v1/admin/calendars", map[string]any{"name": "Bad", "timezone": "Europe/Rome",
			"week": map[string]any{"mon": []map[string]string{{"start": "18:00", "end": "09:00"}}}}).expect(t, 422)
		e.do(e.staff(domain.RoleAgent, 3), "GET", "/api/v1/admin/calendars", nil).expect(t, 403)
	})
}

func publicSupportKeys(e *env) map[string]any {
	return directory.PublicSupport(e.svc.dir.Support(context.Background()))
}

func TestTeam_NotifyTargets(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 1)
		team := e.do(sup, "POST", "/api/v1/admin/teams", map[string]any{"name": "N", "notify_mode": "both", "notification_email": "n@example.org"}).expect(t, 201).m(t)["id"].(string)
		l1, l2 := e.staff(domain.RoleAgent, 1, team), e.staff(domain.RoleAgent, 2, team)
		e.svc.dir.Invalidate()
		nt, err := e.svc.dir.TeamNotifyTargets(context.Background(), team, 2)
		if err != nil || nt.Mode != "both" || nt.Mailbox != "n@example.org" || len(nt.MemberIDs) != 1 || nt.MemberIDs[0] != l2.UserID {
			t.Fatalf("targets %+v %v (l1 %s)", nt, err, l1.UserID)
		}
	})
}

// TestStaffTierChange_RevalidatesM4: the staff admin path (tier lowered,
// via directory.MembershipChanged) unassigns tickets the member no longer
// qualifies for; the team-cache hook fires on membership changes.
func TestStaffTierChange_RevalidatesM4(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newEscFix(t, db)
		f.e.do(f.l1, "POST", f.path("/escalate"), map[string]any{"reason": "x"}).expect(t, 200)
		f.e.do(f.l2, "PATCH", f.path(""), map[string]any{"assignee_id": f.l2.UserID}).expect(t, 200)
		ctx := context.Background()
		err := db.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET tier = 1 WHERE id = ?`, f.l2.UserID); err != nil {
				return err
			}
			return directory.MembershipChanged(ctx, tx, "", "", []string{f.l2.UserID})
		})
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := lockFreeLoad(ctx, f.e.svc, f.id); r.AssigneeID != "" {
			t.Fatal("demoted assignee kept a tier-2 ticket (M-4)")
		}
		var changed []string
		f.e.svc.dir.OnTeamsChanged(func(ids []string) { changed = append(changed, ids...) })
		sup := f.e.staff(domain.RoleSupervisor, 1)
		f.e.do(sup, "DELETE", "/api/v1/admin/teams/"+f.teamA+"/members/"+f.l3.UserID, nil).expect(t, 204)
		if len(changed) != 1 || changed[0] != f.l3.UserID {
			t.Fatalf("team cache hook got %v", changed)
		}
	})
}
