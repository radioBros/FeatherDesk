// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func applyPath(ticket, macro string) string {
	return "/api/v1/agent/tickets/" + ticket + "/macros/" + macro + "/apply"
}

// TestMacro_ApplyRespectsAllowedActions: a macro applies as the agent; one
// part the agent may not do (urgent priority without supervisor rights)
// rejects the whole macro and nothing changes, while a supervisor applies it.
func TestMacro_ApplyRespectsAllowedActions(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		setSupport(t, e, func(s *settings.Support) { s.UrgentRequiresSupervisor = true })
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 3, team)
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{CategoryID: sp(storetest.CreateCategory(t, db, "", team))})
		mac := e.do(sup, "POST", "/api/v1/admin/macros", map[string]any{"name": "Escalate urgently", "reply_md": "Hi {{customer.name}}, on it.",
			"priority": "urgent", "add_tags": []string{"Urgent-Fix"}}).expect(t, 201).m(t)
		if mac["shared"] != true || mac["owner_id"] != nil || mac["reply_kind"] != "public" || !slices.Equal(anyList(mac["add_tags"]), []string{"urgent-fix"}) {
			t.Fatalf("created: %v", mac)
		}
		mid := mac["id"].(string)
		before := len(e.eventsOf(id))
		e.do(agent, "POST", applyPath(id, mid), nil).expect(t, 403)
		if len(e.eventsOf(id)) != before || e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ?`, id) != 1 || len(e.tagsOf(id)) != 0 {
			t.Fatal("a rejected macro changed the ticket")
		}
		res := e.do(sup, "POST", applyPath(id, mid), nil, "Idempotency-Key", "apply-1").expect(t, 200).m(t)
		tk := res["ticket"].(map[string]any)
		if res["visible"] != true || tk["priority"] != "urgent" || tk["status"] != "open" {
			t.Fatalf("applied: %v", res)
		}
		var body string
		_ = db.Read().QueryRowContext(t.Context(), `SELECT body_md FROM ticket_messages WHERE ticket_id = ? AND author_id = ?`, id, sup.UserID).Scan(&body)
		if !strings.HasPrefix(body, "Hi Customer") || strings.Contains(body, "{{") {
			t.Fatalf("reply %q", body)
		}
		// A note-only macro needs only the note permission.
		note := e.do(agent, "POST", "/api/v1/agent/macros", map[string]any{"name": "Note", "reply_kind": "internal", "reply_md": "checked by {{agent.name}}"}).expect(t, 201).m(t)
		e.do(agent, "POST", applyPath(id, note["id"].(string)), nil).expect(t, 200)
		// Stale If-Match.
		e.do(agent, "POST", applyPath(id, note["id"].(string)), nil, "If-Match", `W/"1"`).expect(t, 412)
	})
}

func anyList(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// TestMacro_PersonalAndShared: personal macros are the owner's only, shared
// ones are offered to all agents and managed by supervisors; a macro applies
// reply, tags, assignee and status in one change.
func TestMacro_PersonalAndShared(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		team := storetest.CreateTeam(t, db, "")
		sup := e.staff(domain.RoleSupervisor, 3, team)
		a, b := e.staff(domain.RoleAgent, 1, team), e.staff(domain.RoleAgent, 1, team)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{CategoryID: sp(storetest.CreateCategory(t, db, "", team))})
		if _, err := e.svc.Patch(t.Context(), sup, id, PatchInput{Tags: &[]string{"waiting", "keep"}}, ""); err != nil {
			t.Fatal(err)
		}

		mine := e.do(a, "POST", "/api/v1/agent/macros", map[string]any{"name": "Take and wait", "reply_md": "Looking into it.",
			"status": "pending", "assignee": "me", "add_tags": []string{"triaged"}, "remove_tags": []string{"waiting"}}).expect(t, 201).m(t)
		if mine["shared"] != false || mine["owner_id"] != a.UserID {
			t.Fatalf("personal: %v", mine)
		}
		shared := e.do(sup, "POST", "/api/v1/admin/macros", map[string]any{"name": "Shared close", "status": "resolved"}).expect(t, 201).m(t)
		e.do(a, "POST", "/api/v1/admin/macros", map[string]any{"name": "x", "status": "open"}).expect(t, 403)
		e.do(cust, "GET", "/api/v1/agent/macros", nil).expect(t, 404)

		var listA, listB []map[string]any
		e.do(a, "GET", "/api/v1/agent/macros", nil).expect(t, 200).json(t, &listA)
		e.do(b, "GET", "/api/v1/agent/macros", nil).expect(t, 200).json(t, &listB)
		if len(listA) != 2 || len(listB) != 1 || listB[0]["id"] != shared["id"] {
			t.Fatalf("lists: a=%v b=%v", listA, listB)
		}
		var adminList []map[string]any
		e.do(sup, "GET", "/api/v1/admin/macros", nil).expect(t, 200).json(t, &adminList)
		if len(adminList) != 1 {
			t.Fatalf("admin list shows personal macros: %v", adminList)
		}
		// Another agent's personal macro is invisible; a shared one is read-only on /agent.
		e.do(b, "POST", applyPath(id, mine["id"].(string)), nil).expect(t, 404)
		e.do(b, "PATCH", "/api/v1/agent/macros/"+mine["id"].(string), map[string]any{"name": "hijack", "status": "open"}).expect(t, 404)
		e.do(a, "PATCH", "/api/v1/agent/macros/"+shared["id"].(string), map[string]any{"name": "mine now", "status": "open"}).expect(t, 403)
		e.do(sup, "PATCH", "/api/v1/admin/macros/"+mine["id"].(string), map[string]any{"name": "x", "status": "open"}).expect(t, 404)
		// Validation.
		e.do(a, "POST", "/api/v1/agent/macros", map[string]any{"name": "empty"}).expect(t, 422)
		e.do(a, "POST", "/api/v1/agent/macros", map[string]any{"name": "bad tag", "add_tags": []string{"Not ok!"}}).expect(t, 422)
		e.do(a, "POST", "/api/v1/agent/macros", map[string]any{"name": "bad assignee", "assignee": cust.UserID}).expect(t, 422)

		res := e.do(a, "POST", applyPath(id, mine["id"].(string)), nil).expect(t, 200).m(t)
		tk := res["ticket"].(map[string]any)
		if tk["status"] != "pending" || tk["assignee"].(map[string]any)["id"] != a.UserID {
			t.Fatalf("applied: %v", tk)
		}
		if tags := e.tagsOf(id); !slices.Equal(tags, []string{"keep", "triaged"}) {
			t.Fatalf("tags %v", tags)
		}
		if e.ticketField(id, "first_responded_at") == "" {
			t.Fatal("the macro reply did not count as a staff reply")
		}
		e.do(a, "PATCH", "/api/v1/agent/macros/"+mine["id"].(string), map[string]any{"name": "Renamed", "assignee": "none"}).expect(t, 200)
		e.do(a, "DELETE", "/api/v1/agent/macros/"+mine["id"].(string), nil).expect(t, 204)
		e.do(sup, "PATCH", "/api/v1/admin/macros/"+shared["id"].(string), map[string]any{"name": "Shared resolve", "status": "resolved"}).expect(t, 200)
		e.do(sup, "DELETE", "/api/v1/admin/macros/"+shared["id"].(string), nil).expect(t, 204)
		e.do(a, "POST", applyPath(id, shared["id"].(string)), nil).expect(t, 404)
	})
}
