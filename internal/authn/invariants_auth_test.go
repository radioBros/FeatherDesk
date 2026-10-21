// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// otherModules are the registered feature modules after authn and the staff
// admin, so the I-1 sweep covers every route actually mounted.
func otherModules() []app.Factory {
	if len(app.Registry) <= 2 {
		return nil
	}
	return app.Registry[2:]
}

// supportProbe mounts a route on the Support group (supervisor or admin).
type supportProbe struct{}

func (supportProbe) Name() string { return "support-probe" }
func (supportProbe) Mount(g server.Groups) {
	g.Support.Get("/probe-support", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
}

// TestGuard_CustomerOnAgentRoutes404 is I-1: every /agent/* and /admin/*
// operation (spec and router) answers 404 to a customer session and a
// customer PAT, before any handler runs.
func TestGuard_CustomerOnAgentRoutes404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Extra: otherModules()})
		c := e.Login(e.Customer(storetest.CreateOrg(t, e.DB, ""), domain.RoleOrgManager))
		pat := c.PAT("tickets:read", "tickets:write", "kb:read")
		ops, err := apispec.Operations()
		if err != nil {
			t.Fatal(err)
		}
		routes, err := apispec.RouterOperations(e.App.Server.Router())
		if err != nil {
			t.Fatal(err)
		}
		all := map[string]bool{}
		for op := range ops {
			all[op] = true
		}
		for _, op := range routes {
			all[op] = true
		}
		n := 0
		for op := range all {
			method, path, _ := strings.Cut(op, " ")
			if !strings.HasPrefix(path, "/api/v1/agent/") && !strings.HasPrefix(path, "/api/v1/admin/") {
				continue
			}
			path = strings.ReplaceAll(path, "{}", "0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f")
			for _, cl := range []*authntest.Client{c, pat} {
				r, b := cl.Do(method, path, map[string]any{})
				if r.StatusCode != 404 {
					t.Errorf("%s %s as customer (bearer=%v): %d %s", method, path, cl.Bearer != "", r.StatusCode, b)
				}
			}
			n++
		}
		if n < 50 {
			t.Fatalf("only %d agent/admin operations swept", n)
		}
	})
}

func TestGuard_StaffOnPortal404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Extra: otherModules()})
		admin := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		pat := admin.PAT("admin")
		ops, _ := apispec.Operations()
		for op := range ops {
			method, path, _ := strings.Cut(op, " ")
			if !strings.HasPrefix(path, "/api/v1/portal/") {
				continue
			}
			path = strings.ReplaceAll(path, "{}", "0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f")
			for _, cl := range []*authntest.Client{admin, pat} {
				if r, _ := cl.Do(method, path, map[string]any{}); r.StatusCode != 404 {
					t.Errorf("%s %s as staff: %d", method, path, r.StatusCode)
				}
			}
		}
	})
}

func TestGuard_SupervisorAdminSplit(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_AUTH_STAFF_LOCAL": "true"},
			Extra: []app.Factory{func(*app.Deps) (app.Module, error) { return supportProbe{}, nil }}})
		sup := e.Login(e.StaffUser(domain.RoleSupervisor, 3, false))
		agent := e.Login(e.StaffUser(domain.RoleAgent, 1, false))
		admin := e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
		for _, c := range []*authntest.Client{sup, agent, admin} {
			c.NoCheck = true // /admin/probe-support is a test-only route outside the spec
		}
		if r, _ := sup.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 403 {
			t.Fatalf("supervisor on admin route: %d", r.StatusCode)
		}
		if r, _ := sup.Do("GET", "/api/v1/admin/probe-support", nil); r.StatusCode != 204 {
			t.Fatalf("supervisor on support route: %d", r.StatusCode)
		}
		if r, _ := agent.Do("GET", "/api/v1/admin/probe-support", nil); r.StatusCode != 403 {
			t.Fatalf("agent on support route: %d", r.StatusCode)
		}
		if r, _ := admin.Do("GET", "/api/v1/admin/probe-support", nil); r.StatusCode != 204 {
			t.Fatalf("admin on support route: %d", r.StatusCode)
		}
		anon := e.Client()
		if r, _ := anon.Do("GET", "/api/v1/admin/staff", nil); r.StatusCode != 401 {
			t.Fatalf("anonymous: %d", r.StatusCode)
		}
	})
}

// ---- I-5 ----

func disable(t *testing.T, e *authntest.Env, id string) {
	t.Helper()
	err := e.DB.Tx(context.Background(), func(tx *store.Tx) error {
		return authn.DisableUser(context.Background(), tx, id, "", "test", "")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInvariant_I5_DisabledSessionRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("precondition")
		}
		disable(t, e, u.ID)
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("disabled user's session on the next request: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, u.ID); n != 0 {
			t.Fatal("sessions not deleted")
		}
		// Even a session row that survived (e.g. inserted later) is refused
		// because the user row is re-read per request.
		if r, _ := login(e.Client(), domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatal("disabled user can log in")
		}
	})
}

func TestInvariant_I5_DisabledPATRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		pat := e.Login(u).PAT("tickets:read")
		disable(t, e, u.ID)
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("disabled owner's PAT: %d", r.StatusCode)
		}
		// Re-enabling does not resurrect revoked tokens.
		e.Exec(`UPDATE users SET status = 'active' WHERE id = ?`, u.ID)
		if r, _ := pat.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("PAT valid after re-enable: %d", r.StatusCode)
		}
	})
}

// ---- WebSocket (AUTH-032) ----

func dialWS(t *testing.T, srv *httptest.Server, h http.Header, query string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws" + query
	return websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: []string{"simpletickets.v1"}, HTTPHeader: h})
}

func cookieHeader(e *authntest.Env, c *authntest.Client, origin string) http.Header {
	h := http.Header{}
	h.Set("Cookie", authn.CookieName+"="+c.Cookie)
	if origin != "" {
		h.Set("Origin", origin)
	}
	return h
}

// waitClose reads until the server closes and returns the close code.
func waitClose(t *testing.T, ws *websocket.Conn, within time.Duration) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	for {
		_, _, err := ws.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("socket not closed in time")
			}
			return websocket.CloseStatus(err)
		}
	}
}

func TestInvariant_I5_DisabledUserSocketClosed4401(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		ws, _, err := dialWS(t, srv, cookieHeader(e, c, e.Origin), "")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = ws.CloseNow() // safety net: the server usually closed it already
		}()
		disable(t, e, u.ID)
		if code := waitClose(t, ws, 15*time.Second); code != 4401 {
			t.Fatalf("close code %d", code)
		}
	})
}

func TestWS_LogoutClosesSocket4401(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		c := e.Login(e.Customer("", domain.RoleRequester))
		ws, _, err := dialWS(t, srv, cookieHeader(e, c, e.Origin), "")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = ws.CloseNow() // safety net: the server usually closed it already
		}()
		c.Do("POST", "/api/v1/auth/logout", nil)
		if code := waitClose(t, ws, 15*time.Second); code != 4401 {
			t.Fatalf("close code %d", code)
		}
	})
}

func TestWS_ForeignOriginCookie403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		c := e.Login(e.Customer("", domain.RoleRequester))
		_, resp, err := dialWS(t, srv, cookieHeader(e, c, "https://evil.example"), "")
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("foreign origin with cookie: err=%v resp=%v", err, resp)
		}
	})
}

func TestWS_MissingOriginCookie403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		c := e.Login(e.Customer("", domain.RoleRequester))
		_, resp, err := dialWS(t, srv, cookieHeader(e, c, ""), "")
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("missing origin with cookie: err=%v resp=%v", err, resp)
		}
	})
}

func TestWS_PATBearerAccepted(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		h := http.Header{"Authorization": {"Bearer " + pat.Bearer}}
		ws, _, err := dialWS(t, srv, h, "")
		if err != nil {
			t.Fatalf("PAT socket: %v", err)
		}
		if err := ws.Close(websocket.StatusNormalClosure, ""); err != nil {
			t.Fatalf("ws.Close: %v", err)
		}
	})
}

func TestWS_PATWithoutTicketsRead403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_AUTH_STAFF_LOCAL": "true"}})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		pat := e.Login(e.StaffUser(domain.RoleAgent, 1, false)).PAT("directory:read")
		_, resp, err := dialWS(t, srv, http.Header{"Authorization": {"Bearer " + pat.Bearer}}, "")
		if err == nil || resp == nil || resp.StatusCode != 403 {
			t.Fatalf("PAT without tickets:read: err=%v resp=%v", err, resp)
		}
	})
}

func wsTicket(t *testing.T, c *authntest.Client) string {
	t.Helper()
	r, b := c.Do("POST", "/api/v1/ws/ticket", nil)
	if r.StatusCode != 201 {
		t.Fatalf("ws ticket: %d %s", r.StatusCode, b)
	}
	m := authntest.JSON(t, b)
	if m["expires_in"].(float64) != 30 {
		t.Fatalf("expires_in: %v", m["expires_in"])
	}
	return m["ticket"].(string)
}

func TestWSTicket_SingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		tk := wsTicket(t, pat)
		ws, _, err := dialWS(t, srv, http.Header{}, "?ticket="+tk)
		if err != nil {
			t.Fatalf("first use: %v", err)
		}
		if err := ws.Close(websocket.StatusNormalClosure, ""); err != nil {
			t.Fatalf("ws.Close: %v", err)
		}
		if _, resp, err := dialWS(t, srv, http.Header{}, "?ticket="+tk); err == nil || resp.StatusCode != 401 {
			t.Fatalf("second use: err=%v resp=%v", err, resp)
		}
		if e.Auth.PendingWSTickets() != 0 {
			t.Fatal("ticket kept in memory after use")
		}
	})
}

func TestWSTicket_ExpiresAfter30s(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		tk := wsTicket(t, pat)
		e.Clock.Advance(31 * time.Second)
		if _, resp, err := dialWS(t, srv, http.Header{}, "?ticket="+tk); err == nil || resp.StatusCode != 401 {
			t.Fatalf("expired ticket: err=%v resp=%v", err, resp)
		}
	})
}

func TestWSTicket_NotLogged(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		tk := wsTicket(t, pat)
		ws, _, err := dialWS(t, srv, http.Header{}, "?ticket="+tk)
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.Close(websocket.StatusNormalClosure, ""); err != nil {
			t.Fatalf("ws.Close: %v", err)
		}
		if strings.Contains(e.Logs.String(), tk) {
			t.Fatal("ws ticket appears in the logs")
		}
	})
}

// TestWS_ReauthTimerCatchesExpiredSession: the hub's 60 s recheck goes
// through Recheck, which refuses an idle-expired session.
func TestWS_ReauthTimerCatchesExpiredSession(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Login(e.Customer("", domain.RoleRequester))
		p := &domain.Principal{SessionID: sha(c.Cookie)}
		if _, err := e.Auth.Recheck(context.Background(), p); err != nil {
			t.Fatalf("fresh session: %v", err)
		}
		e.Clock.Advance(31 * 24 * time.Hour)
		if _, err := e.Auth.Recheck(context.Background(), p); err == nil {
			t.Fatal("recheck accepted an expired session")
		}
	})
}
