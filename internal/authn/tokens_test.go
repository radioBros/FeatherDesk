// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func TestPAT_HashOnlyStored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		r, b := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "ci", "scopes": []string{"tickets:read"}})
		if r.StatusCode != 201 {
			t.Fatalf("create: %d %s", r.StatusCode, b)
		}
		out := authntest.JSON(t, b)
		tok := out["token"].(string)
		if !strings.HasPrefix(tok, "st_pat_") || !strings.HasPrefix(tok[7:], out["prefix"].(string)) {
			t.Fatalf("token format: %s prefix %v", tok, out["prefix"])
		}
		if n := e.Int(`SELECT COUNT(*) FROM api_tokens WHERE token_hash = ?`, sha(tok)); n != 1 {
			t.Fatal("hash not stored")
		}
		if n := e.Int(`SELECT COUNT(*) FROM api_tokens WHERE token_hash = ? OR prefix = ? OR name = ?`, tok, tok, tok); n != 0 {
			t.Fatal("raw token stored")
		}
		// Shown once: the list never contains it.
		_, b = c.Do("GET", "/api/v1/me/tokens", nil)
		if strings.Contains(string(b), tok) || strings.Contains(string(b), `"token"`) {
			t.Fatalf("token listed again: %s", b)
		}
		pat := &authntest.Client{E: e, Bearer: tok, IP: "192.0.2.3"}
		if r, b := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 || authntest.JSON(t, b)["scopes"] == nil {
			t.Fatalf("PAT /me: %d %s", r.StatusCode, b)
		}
	})
}

// I-6: an admin token stops working on admin routes the moment its owner is demoted.
func TestPAT_ScopeIntersection_DemotedOwnerLosesAdmin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		e.StaffUser(domain.RoleAdmin, 3, true) // keep another admin
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		pat := e.Login(admin).PAT("admin")
		if r, b := pat.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 200 {
			t.Fatalf("admin PAT: %d %s", r.StatusCode, b)
		}
		e.Exec(`UPDATE users SET role = 'agent', break_glass = ? WHERE id = ?`, false, admin.ID)
		if r, _ := pat.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 403 {
			t.Fatalf("demoted owner's admin PAT on admin route: %d", r.StatusCode)
		}
		_, b := pat.Do("GET", "/api/v1/me", nil)
		var me struct{ Scopes []string }
		_ = json.Unmarshal(b, &me)
		if len(me.Scopes) != 0 {
			t.Fatalf("effective scopes after demotion: %v (want none: admin ∩ agent = ∅)", me.Scopes)
		}
	})
}

// I-6 per operation: the x-scopes of the spec are enforced for PATs.
func TestPAT_ScopeEnforcedPerOperation(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		admin := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		read := admin.PAT("tickets:read")
		if r, _ := read.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 403 {
			t.Fatalf("tickets:read PAT of an admin on /admin/staff: %d", r.StatusCode)
		}
		if r, _ := read.Do("GET", "/api/v1/admin/tokens", nil); r.StatusCode != 403 {
			t.Fatalf("tickets:read PAT on /admin/tokens: %d", r.StatusCode)
		}
		// Token management needs a browser session, never a PAT.
		if r, _ := admin.PAT("admin").Do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{"admin"}}); r.StatusCode != 403 {
			t.Fatalf("PAT minting a PAT: %d", r.StatusCode)
		}
		if r, _ := read.Do("POST", "/api/v1/ws/ticket", nil); r.StatusCode != 201 {
			t.Fatalf("tickets:read PAT ws ticket: %d", r.StatusCode)
		}
	})
}

func TestPAT_CustomerCannotMintDirectoryScope(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		for _, s := range []string{"directory:read", "directory:write", "admin", "kb:write"} {
			if r, _ := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{s}}); r.StatusCode != 403 {
				t.Fatalf("customer minting %s: %d", s, r.StatusCode)
			}
		}
		if r, _ := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{"tickets:read", "tickets:write", "kb:read"}}); r.StatusCode != 201 {
			t.Fatalf("customer allowed scopes: %d", r.StatusCode)
		}
	})
}

func TestPAT_AdminScopeOnlyForAdmins(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_AUTH_STAFF_LOCAL": "true"}})
		sup := e.Login(e.StaffUser(domain.RoleSupervisor, 3, false))
		if r, _ := sup.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{"admin"}}); r.StatusCode != 403 {
			t.Fatalf("supervisor minting admin: %d", r.StatusCode)
		}
		if r, _ := sup.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "x", "scopes": []string{"directory:write"}}); r.StatusCode != 201 {
			t.Fatalf("supervisor directory:write: %d", r.StatusCode)
		}
	})
}

func TestPAT_RevokedRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		pat := c.PAT("tickets:read")
		id := e.Str(`SELECT id FROM api_tokens WHERE token_hash = ?`, sha(pat.Bearer))
		if r, _ := c.Do("DELETE", "/api/v1/me/tokens/"+id, nil); r.StatusCode != 204 {
			t.Fatalf("revoke: %d", r.StatusCode)
		}
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("revoked PAT: %d", r.StatusCode)
		}
		other := e.Login(e.Customer("", domain.RoleRequester))
		if r, _ := other.Do("DELETE", "/api/v1/me/tokens/"+id, nil); r.StatusCode != 404 {
			t.Fatalf("revoking someone else's token: %d", r.StatusCode)
		}
	})
}

func TestPAT_ExpiredRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		exp := e.Clock.Now().Add(time.Hour).Format(time.RFC3339)
		r, b := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "short", "scopes": []string{"tickets:read"}, "expires_at": exp})
		if r.StatusCode != 201 {
			t.Fatalf("create: %d %s", r.StatusCode, b)
		}
		pat := &authntest.Client{E: e, Bearer: authntest.JSON(t, b)["token"].(string), IP: "192.0.2.4"}
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("fresh token rejected")
		}
		e.Clock.Advance(2 * time.Hour)
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("expired PAT: %d", r.StatusCode)
		}
	})
}

func TestPAT_LastUsedNoPerRequestWrite(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		id := e.Str(`SELECT id FROM api_tokens WHERE token_hash = ?`, sha(pat.Bearer))
		before := e.App.Deps.Queue.WriteBehindFlushes()
		for i := 0; i < 50; i++ {
			pat.Do("GET", "/api/v1/me", nil)
		}
		if e.Str(`SELECT CAST(last_used_at AS TEXT) FROM api_tokens WHERE id = ?`, id) != "" {
			t.Fatal("last_used_at written per request")
		}
		if err := e.App.Deps.Queue.FlushWriteBehind(t.Context()); err != nil {
			t.Fatal(err)
		}
		if e.App.Deps.Queue.WriteBehindFlushes() != before+1 {
			t.Fatal("expected one write-behind flush")
		}
		if e.Str(`SELECT CAST(last_used_at AS TEXT) FROM api_tokens WHERE id = ?`, id) == "" {
			t.Fatal("last_used_at not flushed")
		}
	})
}

func TestPAT_MaxActive(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		for i := 0; i < 20; i++ {
			c.PAT("tickets:read")
		}
		if r, _ := c.Do("POST", "/api/v1/me/tokens", map[string]any{"name": "21st", "scopes": []string{"tickets:read"}}); r.StatusCode != 422 {
			t.Fatalf("21st token: %d", r.StatusCode)
		}
	})
}

func TestAdminTokens_ListAndRevoke(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		cust := e.Customer("", domain.RoleRequester)
		pat := e.Login(cust).PAT("tickets:read")
		admin := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		r, b := admin.Do("GET", "/api/v1/admin/tokens?user_id="+cust.ID+"&limit=1", nil)
		page := authntest.JSON(t, b)
		items := page["items"].([]any)
		if r.StatusCode != 200 || len(items) != 1 {
			t.Fatalf("list: %d %s", r.StatusCode, b)
		}
		id := items[0].(map[string]any)["id"].(string)
		if r, _ := admin.Do("DELETE", "/api/v1/admin/tokens/"+id, nil); r.StatusCode != 204 {
			t.Fatalf("admin revoke: %d", r.StatusCode)
		}
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatal("token still valid")
		}
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.token_revoked' AND target_id = ?`, id); n != 1 {
			t.Fatal("revocation not audited")
		}
	})
}

func TestSecurity_SessionCookieNotAcceptedAsBearer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		b := &authntest.Client{E: e, Bearer: c.Cookie, IP: "192.0.2.7"}
		if r, _ := b.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("session token as bearer: %d", r.StatusCode)
		}
	})
}

func TestSecurity_PATInQueryStringIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		anon := e.Client()
		for _, q := range []string{"access_token", "token", "bearer"} {
			if r, _ := anon.Do("GET", "/api/v1/me?"+q+"="+pat.Bearer, nil); r.StatusCode != 401 {
				t.Fatalf("PAT in ?%s accepted: %d", q, r.StatusCode)
			}
		}
	})
}
