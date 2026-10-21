// SPDX-License-Identifier: AGPL-3.0-only

package directory_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func adminEnv(t *testing.T, db *store.DB) (*authntest.Env, authntest.User, *authntest.Client) {
	e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_SMTP_HOST": "smtp.example.test"}})
	a := e.StaffUser(domain.RoleAdmin, 3, true)
	return e, a, e.Login(a)
}

func TestAdminStaff_CreateListGetPatch(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		team := storetest.CreateTeam(t, e.DB, "")
		r, b := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "Agent.Two@corp.test", "name": "Agent Two", "role": "agent", "tier": 2,
			"team_ids": []string{team}, "password": "agent-two-pass-1"})
		if r.StatusCode != 201 {
			t.Fatalf("create: %d %s", r.StatusCode, b)
		}
		u := authntest.JSON(t, b)
		id := u["id"].(string)
		if u["email"] != "agent.two@corp.test" || u["tier"].(float64) != 2 || len(u["team_ids"].([]any)) != 1 {
			t.Fatalf("created: %v", u)
		}
		if r, _ := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "agent.two@corp.test", "name": "Dup", "role": "agent"}); r.StatusCode != 409 {
			t.Fatalf("duplicate: %d", r.StatusCode)
		}
		r, b = c.Do("GET", "/api/v1/admin/staff?q=two&role=agent&team_id="+team, nil)
		var page struct {
			Items      []map[string]any `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		}
		_ = json.Unmarshal(b, &page)
		if r.StatusCode != 200 || len(page.Items) != 1 || page.Items[0]["id"] != id {
			t.Fatalf("list: %d %s", r.StatusCode, b)
		}
		_, b = c.Do("GET", "/api/v1/admin/staff?limit=1", nil)
		_ = json.Unmarshal(b, &page)
		if len(page.Items) != 1 || page.NextCursor == nil {
			t.Fatalf("paging: %s", b)
		}
		r, _ = c.Do("GET", "/api/v1/admin/staff?limit=1&cursor="+*page.NextCursor, nil)
		if r.StatusCode != 200 {
			t.Fatalf("page 2: %d", r.StatusCode)
		}
		if r, _ := c.Do("GET", "/api/v1/admin/staff/"+id, nil); r.StatusCode != 200 {
			t.Fatal("get")
		}
		cust := e.Customer("", domain.RoleRequester)
		if r, _ := c.Do("GET", "/api/v1/admin/staff/"+cust.ID, nil); r.StatusCode != 404 {
			t.Fatal("customer visible through staff API")
		}
		r, b = c.Do("PATCH", "/api/v1/admin/staff/"+id, map[string]any{"role": "supervisor", "tier": 3, "team_ids": []string{}, "name": "Lead"})
		u = authntest.JSON(t, b)
		if r.StatusCode != 200 || u["role"] != "supervisor" || u["name"] != "Lead" || len(u["team_ids"].([]any)) != 0 {
			t.Fatalf("patch: %d %s", r.StatusCode, b)
		}
	})
}

func TestAdminStaff_TierOutOfRange422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		if r, _ := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "x@corp.test", "name": "X", "role": "agent", "tier": 4}); r.StatusCode != 422 {
			t.Fatalf("tier 4: %d", r.StatusCode)
		}
		id := storetest.CreateStaff(t, e.DB, domain.RoleAgent, 1)
		if r, _ := c.Do("PATCH", "/api/v1/admin/staff/"+id, map[string]any{"tier": 0}); r.StatusCode != 422 {
			t.Fatalf("tier 0: %d", r.StatusCode)
		}
		if r, _ := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "y@corp.test", "name": "Y", "role": "agent", "break_glass": true}); r.StatusCode != 422 {
			t.Fatalf("break_glass agent: %d", r.StatusCode)
		}
	})
}

func TestAdminStaff_SupervisorForbidden403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_AUTH_STAFF_LOCAL": "true"}})
		sup := e.Login(e.StaffUser(domain.RoleSupervisor, 3, false))
		if r, _ := sup.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 403 {
			t.Fatalf("list: %d", r.StatusCode)
		}
		if r, _ := sup.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "x@corp.test", "name": "X", "role": "admin"}); r.StatusCode != 403 {
			t.Fatalf("create: %d", r.StatusCode)
		}
	})
}

func TestAdminStaff_LastAdminProtected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, admin, c := adminEnv(t, db)
		for _, body := range []map[string]any{{"role": "agent"}, {"status": "disabled"}} {
			if r, b := c.Do("PATCH", "/api/v1/admin/staff/"+admin.ID, body); r.StatusCode != 409 {
				t.Fatalf("%v on the last admin: %d %s", body, r.StatusCode, b)
			}
		}
		other := e.StaffUser(domain.RoleAdmin, 3, false)
		if r, b := c.Do("PATCH", "/api/v1/admin/staff/"+other.ID, map[string]any{"role": "agent"}); r.StatusCode != 200 {
			t.Fatalf("demote a non-last admin: %d %s", r.StatusCode, b)
		}
		if bg := e.Str(`SELECT CAST(break_glass AS TEXT) FROM users WHERE id = ?`, other.ID); bg == "true" || bg == "1" {
			t.Fatal("break_glass kept on a non-admin")
		}
	})
}

func TestAdminStaff_IdpManagedRoleImmutable(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		id := storetest.CreateStaff(t, e.DB, domain.RoleAgent, 1)
		e.Exec(`UPDATE users SET idp_managed = ? WHERE id = ?`, true, id)
		for _, body := range []map[string]any{{"role": "admin"}, {"tier": 3}} {
			if r, _ := c.Do("PATCH", "/api/v1/admin/staff/"+id, body); r.StatusCode != 403 {
				t.Fatalf("%v on idp-managed: %d", body, r.StatusCode)
			}
		}
		if r, _ := c.Do("PATCH", "/api/v1/admin/staff/"+id, map[string]any{"name": "Renamed"}); r.StatusCode != 200 {
			t.Fatal("name change refused")
		}
	})
}

func TestAdminStaff_AuditWritten(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, admin, c := adminEnv(t, db)
		_, b := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "a@corp.test", "name": "A", "role": "agent", "password": "agent-a-pass-12"})
		id := authntest.JSON(t, b)["id"].(string)
		c.Do("PATCH", "/api/v1/admin/staff/"+id, map[string]any{"tier": 2})
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.staff_changed' AND target_id = ? AND actor_id = ?`, id, admin.ID); n != 2 {
			t.Fatalf("audit rows %d", n)
		}
	})
}

// TestAdminStaff_DisableRevokesEverything is I-5 through the admin API.
func TestAdminStaff_DisableRevokesEverything(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		victim := e.StaffUser(domain.RoleAdmin, 3, true)
		vc := e.Login(victim)
		pat := vc.PAT("tickets:read")
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		h := map[string][]string{"Authorization": {"Bearer " + pat.Bearer}}
		ws, _, err := websocket.Dial(t.Context(), "ws"+srv.URL[4:]+"/api/v1/ws", &websocket.DialOptions{Subprotocols: []string{"simpletickets.v1"}, HTTPHeader: h})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = ws.CloseNow() // safety net: the server usually closed it already
		}()
		if r, b := c.Do("PATCH", "/api/v1/admin/staff/"+victim.ID, map[string]any{"status": "disabled"}); r.StatusCode != 200 {
			t.Fatalf("disable: %d %s", r.StatusCode, b)
		}
		for _, cl := range []*authntest.Client{vc, pat} {
			if r, _ := cl.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
				t.Fatal("credential survived disable")
			}
		}
		ctx, cancel := contextWithTimeout(time.Second)
		defer cancel()
		_, _, err = ws.Read(ctx)
		if websocket.CloseStatus(err) != 4401 {
			t.Fatalf("socket close: %v", err)
		}
		if r, b := c.Do("PATCH", "/api/v1/admin/staff/"+victim.ID, map[string]any{"status": "active"}); r.StatusCode != 200 || authntest.JSON(t, b)["status"] != "active" {
			t.Fatalf("enable: %d %s", r.StatusCode, b)
		}
	})
}

func TestAdminStaff_InviteFlow(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		var got []authn.MailPayload
		authntest.CaptureStaffMail(e, func(p authn.MailPayload) { got = append(got, p) })
		r, b := c.Do("POST", "/api/v1/admin/staff", map[string]any{"email": "inv@corp.test", "name": "Inv", "role": "agent", "send_invite": true})
		if r.StatusCode != 201 || authntest.JSON(t, b)["status"] != "pending_verification" || len(got) != 1 || got[0].Template != authn.TplInvite {
			t.Fatalf("invite: %d %s %+v", r.StatusCode, b, got)
		}
		tok := authntest.TokenFromURL(got[0].Vars["url"])
		nc := e.Client()
		r, b = nc.Do("POST", "/api/v1/auth/invite/accept", map[string]any{"token": tok, "name": "Invited Agent", "password": "invited-agent-77"})
		me := authntest.JSON(t, b)
		if r.StatusCode != 200 || me["kind"] != "staff" || me["role"] != "agent" || me["status"] != "active" {
			t.Fatalf("accept: %d %s", r.StatusCode, b)
		}
	})
}
