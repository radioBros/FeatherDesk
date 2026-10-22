// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestE2E_Projects (D-20) on the full app, both engines, every response
// validated against api/openapi.yaml: project administration, staff
// memberships through /admin/staff, the switcher on /me, organization
// limits in the portal, /auth/config?project=, KEY-123 email subjects,
// webhooks filtered by project, mailboxes and KB categories in a project,
// and MCP by reference with I-11.
func TestE2E_Projects(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		other := storetest.CreateTeam(t, db, "Other")
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()
		admin, _ := s.staff(domain.RoleAdmin, 3)

		// ---- administration ----
		mob := admin.must(201, "POST", "/api/v1/admin/projects", map[string]any{"key": "MOB", "name": "Mobile", "team_ids": []string{team},
			"default_team_id": team, "portal_title": "Mobile help", "color": "#00AA88"})
		mobID := mob["id"].(string)
		it := admin.must(201, "POST", "/api/v1/admin/projects", map[string]any{"key": "IT", "name": "Internal IT", "portal_visible": false})
		admin.must(200, "PUT", "/api/v1/admin/projects/"+it["id"].(string)+"/teams", map[string]any{"team_ids": []string{team}})

		// Staff created with explicit memberships, and without (default project).
		mk := func(email string, projects any) (*client, string) {
			body := map[string]any{"email": email, "name": email, "role": "agent", "tier": 2, "team_ids": []string{team}, "password": "a-long-password-1"}
			if projects != nil {
				body["projects"] = projects
			}
			u := admin.must(201, "POST", "/api/v1/admin/staff", body)
			c := s.client()
			c.must(200, "POST", "/api/v1/auth/login", map[string]any{"audience": "staff", "email": email, "password": "a-long-password-1"})
			return c, u["id"].(string)
		}
		mobAgent, mobAgentID := mk("mob@example.test", []map[string]any{{"project_id": "MOB", "role": "project_admin"}})
		hdAgent, _ := mk("hd@example.test", nil)
		me := mobAgent.must(200, "GET", "/api/v1/me", nil)
		if ps := me["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["project_id"] != mobID {
			t.Fatalf("memberships on /me: %v", me["projects"])
		}
		if areas := fmt.Sprint(me["allowed_areas"]); !strings.Contains(areas, "project_admin") {
			t.Fatalf("project admin area missing: %v", areas)
		}
		if ps := hdAgent.must(200, "GET", "/api/v1/me", nil)["projects"].([]any); len(ps) != 1 || ps[0].(map[string]any)["project_id"] != domain.DefaultProjectID {
			t.Fatalf("default membership: %v", ps)
		}
		mobAgent.must(200, "PATCH", "/api/v1/me", map[string]any{"current_project_id": mobID})
		if got := mobAgent.must(200, "GET", "/api/v1/me", nil)["current_project_id"]; got != mobID {
			t.Fatalf("switcher not persisted: %v", got)
		}
		mobAgent.must(422, "PATCH", "/api/v1/me", map[string]any{"current_project_id": domain.DefaultProjectID})
		mobAgent.must(200, "PATCH", "/api/v1/me", map[string]any{"current_project_id": nil})
		if code, _ := mobAgent.do("GET", "/api/v1/admin/projects/"+domain.DefaultProjectID, nil); code != 404 {
			t.Fatalf("project admin reads a foreign project: %d", code)
		}

		// ---- customers: organization limit, portal list, /auth/config ----
		org := admin.must(201, "POST", "/api/v1/agent/organizations", map[string]any{"name": "Acme", "project_ids": []string{"MOB"}})
		if ps := org["project_ids"].([]any); len(ps) != 1 || ps[0] != mobID {
			t.Fatalf("org projects %v", org)
		}
		custID := storetest.CreateCustomer(t, db, org["id"].(string))
		cust := s.loginFixture(custID, domain.KindCustomer)
		var plist []map[string]any
		_, body := cust.do("GET", "/api/v1/portal/projects", nil)
		_ = json.Unmarshal(body, &plist)
		if len(plist) != 1 || plist[0]["key"] != "MOB" || plist[0]["portal_title"] != "Mobile help" {
			t.Fatalf("portal projects %s", body)
		}
		cust.must(422, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "project_id": "HD"})
		tk := cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "App crashes", "body_md": "On start."})
		if tk["ref"] != "MOB-1" || tk["project"].(map[string]any)["key"] != "MOB" {
			t.Fatalf("customer ticket %v", tk)
		}
		anon := s.client()
		cfg := anon.must(200, "GET", "/api/v1/auth/config?project=mob", nil)
		if p, _ := cfg["project"].(map[string]any); p == nil || p["key"] != "MOB" || p["portal_title"] != "Mobile help" {
			t.Fatalf("auth config project %v", cfg["project"])
		}
		for _, k := range []string{"IT", "NOPE"} {
			if c := anon.must(200, "GET", "/api/v1/auth/config?project="+k, nil); c["project"] != nil {
				t.Fatalf("hidden/unknown project %s exposed: %v", k, c["project"])
			}
		}
		if _, ok := anon.must(200, "GET", "/api/v1/auth/config", nil)["project"]; ok {
			t.Fatal("project key present without ?project=")
		}

		// ---- email subject carries the ref ----
		// The MOB team member hears about it with the ref in the subject;
		// the HD-only agent of the same team does not (I-11 in mail).
		m := s.waitMail("mob@example.test", 10*time.Second, func(m sentMail) bool { return strings.Contains(m.Subject, "App crashes") })
		if len(s.mailsTo("hd@example.test")) != 0 {
			t.Fatal("I-11: a non-member was mailed about a MOB ticket")
		}
		if !strings.HasPrefix(m.Subject, "[MOB-1] ") {
			t.Fatalf("subject %q", m.Subject)
		}

		// ---- webhooks filtered by project ----
		rcv := newReceiver(t)
		wh := admin.must(201, "POST", "/api/v1/admin/webhooks", map[string]any{"url": rcv.srv.URL + "/hook", "events": []string{"ticket.created"},
			"project_ids": []string{"MOB"}})
		if ps := wh["project_ids"].([]any); len(ps) != 1 || ps[0] != mobID {
			t.Fatalf("webhook projects %v", wh)
		}
		hdT := hdAgent.must(201, "POST", "/api/v1/agent/tickets", map[string]any{"subject": "Web down", "body_md": "x", "requester_id": custID})
		mobT := mobAgent.must(201, "POST", "/api/v1/agent/tickets", map[string]any{"subject": "App slow", "body_md": "x", "requester_id": custID, "project_id": "MOB"})
		d := rcv.waitEvent(t, "ticket.created", 10*time.Second, nil)
		var env map[string]any
		_ = json.Unmarshal(d.Body, &env)
		if tkt := env["data"].(map[string]any)["ticket"].(map[string]any); tkt["id"] != mobT["id"] || tkt["ref"] != "MOB-2" || tkt["project_id"] != mobID {
			t.Fatalf("webhook payload %v", tkt)
		}
		time.Sleep(300 * time.Millisecond)
		for _, x := range rcv.deliveries() {
			if strings.Contains(string(x.Body), hdT["id"].(string)) {
				t.Fatal("webhook filtered on MOB received an HD ticket")
			}
		}

		// ---- mailboxes and KB categories in a project ----
		mb := admin.must(201, "POST", "/api/v1/admin/inbound/mailboxes", map[string]any{"name": "Mobile mail", "kind": "webhook", "address": "app@example.test",
			"project_id": "MOB", "team_id": team})
		if mb["project_id"] != mobID {
			t.Fatalf("mailbox project %v", mb)
		}
		admin.must(422, "POST", "/api/v1/admin/inbound/mailboxes", map[string]any{"name": "Bad", "kind": "webhook", "address": "bad@example.test",
			"project_id": "MOB", "team_id": other})
		kbc := mobAgent.must(201, "POST", "/api/v1/admin/kb/categories", map[string]any{"slug": "app", "title": map[string]string{"en": "App"},
			"visibility": "public", "project_id": "MOB"})
		if kbc["project_id"] != mobID {
			t.Fatalf("kb category %v", kbc)
		}
		mobAgent.must(403, "POST", "/api/v1/admin/kb/categories", map[string]any{"slug": "global", "title": map[string]string{"en": "G"}, "visibility": "public"})
		admin.must(201, "POST", "/api/v1/admin/kb/categories", map[string]any{"slug": "web", "title": map[string]string{"en": "Web"}, "visibility": "public",
			"project_id": "HD"})
		_, kb := anon.do("GET", "/api/v1/kb/categories?project=MOB", nil)
		if !strings.Contains(string(kb), `"slug":"app"`) || strings.Contains(string(kb), `"slug":"web"`) {
			t.Fatalf("public KB categories by project: %s", kb)
		}

		// ---- MCP by reference, I-11 included ----
		admin.must(200, "PATCH", "/api/v1/admin/integrations", map[string]any{"mcp_enabled": true})
		mc := s.mcp(s.pat(mobAgent, "tickets:read", "tickets:write"), "2025-06-18")
		if got := mc.ok("get_ticket", map[string]any{"ticket": "mob-1"}); got["ref"] != "MOB-1" {
			t.Fatalf("get_ticket by ref %v", got)
		}
		mc.fail("get_ticket", map[string]any{"ticket": hdT["ref"]}, "not-found")
		if _, e := mc.rpc("resources/read", map[string]any{"uri": "ticket://" + hdT["ref"].(string)}); e == nil {
			t.Fatal("I-11: resources/read of a foreign project")
		}
		if res, e := mc.rpc("resources/read", map[string]any{"uri": "ticket://MOB-2"}); e != nil || !strings.Contains(fmt.Sprint(res), "App slow") {
			t.Fatalf("resources/read ticket://MOB-2: %v %v", res, e)
		}
		list := mc.ok("search_tickets", map[string]any{"project": "MOB"})
		if items := list["items"].([]any); len(items) != 2 {
			t.Fatalf("search_tickets project MOB: %v", list)
		}

		// ---- removing the membership closes the project at once (I-11) ----
		admin.must(200, "PUT", "/api/v1/admin/projects/"+mobID+"/members", map[string]any{"members": []any{}})
		if code, _ := mobAgent.do("GET", "/api/v1/agent/tickets/by-ref/MOB-1", nil); code != 404 {
			t.Fatalf("after leaving the project: %d", code)
		}
		_ = mobAgentID
	})
}
