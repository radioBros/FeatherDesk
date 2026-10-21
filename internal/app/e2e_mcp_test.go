// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// mcpClient speaks MCP to /mcp of a stack in-process; every response is
// validated against api/openapi.yaml.
type mcpClient struct {
	s       *stack
	token   string
	version string
	sid     string
	seq     int
	origin  string
}

func (s *stack) pat(c *client, scopes ...string) string {
	s.t.Helper()
	tok := c.must(201, "POST", "/api/v1/me/tokens", map[string]any{"name": "mcp " + strings.Join(scopes, " "), "scopes": scopes})
	v, _ := tok["token"].(string)
	if !strings.HasPrefix(v, "st_pat_") {
		s.t.Fatalf("token: %v", tok)
	}
	return v
}

func (m *mcpClient) raw(method, body string, hdr ...string) (int, http.Header, []byte) {
	t := m.s.t
	t.Helper()
	req := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.80:40000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	if m.sid != "" {
		req.Header.Set("Mcp-Session-Id", m.sid)
		req.Header.Set("MCP-Protocol-Version", m.version)
	}
	if m.origin != "" {
		req.Header.Set("Origin", m.origin)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	m.s.a.Server.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	data := rec.Body.Bytes()
	vreq := httptest.NewRequest(method, "/mcp", bytes.NewReader([]byte(body)))
	vreq.Header = req.Header.Clone()
	apispec.ValidateResponse(t, vreq, resp)
	return resp.StatusCode, resp.Header, data
}

// rpc sends one request and returns the decoded JSON-RPC response.
func (m *mcpClient) rpc(method string, params any) (result map[string]any, rpcErr map[string]any) {
	t := m.s.t
	t.Helper()
	m.seq++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.seq, "method": method, "params": params})
	code, _, data := m.raw("POST", string(b))
	if code != 200 {
		t.Fatalf("%s: HTTP %d %s", method, code, data)
	}
	var out struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v %s", method, err, data)
	}
	return out.Result, out.Error
}

func (s *stack) mcp(token, version string) *mcpClient {
	s.t.Helper()
	m := &mcpClient{s: s, token: token, version: version}
	code, hdr, data := m.raw("POST", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}`)
	if code != 200 || hdr.Get("Mcp-Session-Id") == "" {
		s.t.Fatalf("initialize: %d %s", code, data)
	}
	m.sid = hdr.Get("Mcp-Session-Id")
	if code, _, data := m.raw("POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		s.t.Fatalf("initialized: %d %s", code, data)
	}
	return m
}

func (m *mcpClient) tools() []string {
	res, e := m.rpc("tools/list", map[string]any{})
	if e != nil {
		m.s.t.Fatalf("tools/list: %v", e)
	}
	var names []string
	for _, x := range res["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

// call runs a tool and returns (structuredContent, isError, raw text).
func (m *mcpClient) call(name string, args map[string]any) (map[string]any, bool, string) {
	t := m.s.t
	t.Helper()
	res, e := m.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	if e != nil {
		t.Fatalf("%s: protocol error %v", name, e)
	}
	sc, _ := res["structuredContent"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	return sc, res["isError"] == true, text
}

func (m *mcpClient) ok(name string, args map[string]any) map[string]any {
	m.s.t.Helper()
	sc, isErr, text := m.call(name, args)
	if isErr {
		m.s.t.Fatalf("%s(%v) failed: %s", name, args, text)
	}
	return sc
}

func (m *mcpClient) fail(name string, args map[string]any, slug string) map[string]any {
	m.s.t.Helper()
	sc, isErr, text := m.call(name, args)
	e, _ := sc["error"].(map[string]any)
	if !isErr || e["slug"] != slug {
		m.s.t.Fatalf("%s(%v): want tool error %s, got %s", name, args, slug, text)
	}
	return e
}

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

// TestE2E_MCP drives the MCP endpoint on the full app (both engines): the
// setting, Origin, every tool as staff, scope enforcement, the customer view
// (no internal notes, no staff-only tools or fields), resources, the
// 2026-07-28 stateless path and the audit trail.
func TestE2E_MCP(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "Hardware", team)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run() // the queue keeps the search index current

		admin, adminID := s.staff(domain.RoleAdmin, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1, team)
		custID := storetest.CreateCustomer(t, db, "")
		var custEmail string
		if err := s.db.Read().QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, custID).Scan(&custEmail); err != nil {
			t.Fatal(err)
		}
		cust := s.loginFixture(custID, domain.KindCustomer)
		staffTok := s.pat(agent, "tickets:read", "tickets:write", "directory:read")
		roTok := s.pat(agent, "tickets:read")
		adminTok := s.pat(admin, "tickets:read", "tickets:write")
		custTok := s.pat(cust, "tickets:read", "tickets:write")

		// KB: one public and one internal article.
		kbCat := admin.must(201, "POST", "/api/v1/admin/kb/categories", map[string]any{"slug": "how-to", "title": map[string]string{"en": "How-to"}, "visibility": "public"})
		for _, a := range []struct{ slug, title, vis string }{{"reset-password", "Reset your password", "public"}, {"staff-runbook", "Password runbook", "internal"}} {
			art := admin.must(201, "POST", "/api/v1/agent/kb/articles", map[string]any{"category_id": kbCat["id"], "slug": a.slug, "locale": "en",
				"title": a.title, "body_md": "Open **Profile** and choose a new password.", "visibility": a.vis})
			admin.must(200, "PATCH", fmt.Sprintf("/api/v1/agent/kb/articles/%s", art["id"]), map[string]any{"status": "published"})
		}

		// ---- the setting: off by default, every method 404 ----
		off := &mcpClient{s: s, token: staffTok}
		if code, _, data := off.raw("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); code != 404 {
			t.Fatalf("disabled POST: %d %s", code, data)
		}
		if code, _, _ := off.raw("GET", ""); code != 404 {
			t.Fatalf("disabled GET: %d", code)
		}
		if got := admin.must(200, "GET", "/api/v1/admin/integrations", nil); got["mcp_enabled"] != false {
			t.Fatalf("default: %v", got)
		}
		if info := cust.must(200, "GET", "/api/v1/integrations/mcp", nil); info["enabled"] != false || info["endpoint"] != e2ePublicURL+"/mcp" {
			t.Fatalf("info: %v", info)
		}
		agent.must(403, "PATCH", "/api/v1/admin/integrations", map[string]any{"mcp_enabled": true})
		admin.must(422, "PATCH", "/api/v1/admin/integrations", map[string]any{"mcp_enabled": "yes"})
		admin.must(200, "PATCH", "/api/v1/admin/integrations", map[string]any{"mcp_enabled": true})
		var audits int
		if err := s.db.Read().QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE action = 'integrations.updated' AND actor_id = ?`, adminID).Scan(&audits); err != nil || audits != 1 {
			t.Fatalf("audit rows: %d %v", audits, err)
		}
		if code, hdr, _ := off.raw("GET", ""); code != 405 || hdr.Get("Allow") == "" {
			t.Fatalf("enabled GET: %d", code)
		}

		// ---- Origin and auth ----
		evil := &mcpClient{s: s, token: staffTok, origin: "https://evil.example"}
		if code, _, _ := evil.raw("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); code != 403 {
			t.Fatalf("foreign Origin: %d", code)
		}
		anon := &mcpClient{s: s}
		if code, hdr, _ := anon.raw("POST", `{"jsonrpc":"2.0","id":1,"method":"ping"}`); code != 401 || hdr.Get("WWW-Authenticate") == "" {
			t.Fatalf("no token: %d", code)
		}
		same := &mcpClient{s: s, token: staffTok, origin: e2ePublicURL}
		if code, _, data := same.raw("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); code != 200 {
			t.Fatalf("own Origin: %d %s", code, data)
		}
		if code, _, data := same.raw("POST", `{"jsonrpc":"2.0","id":1,"method":`); code != 400 || !strings.Contains(string(data), "-32700") {
			t.Fatalf("parse error: %d %s", code, data)
		}

		// ---- staff: every tool ----
		st := s.mcp(staffTok, "2025-06-18")
		tools := st.tools()
		for _, n := range []string{"search_tickets", "list_my_tickets", "get_ticket", "create_ticket", "reply_to_ticket", "add_internal_note",
			"update_ticket", "escalate_ticket", "deescalate_ticket", "apply_macro", "search_kb", "get_kb_article", "list_teams", "list_categories"} {
			if !slices.Contains(tools, n) {
				t.Fatalf("staff tools lack %s: %v", n, tools)
			}
		}
		teams := st.ok("list_teams", nil)["teams"].([]any)
		if len(teams) != 1 || teams[0].(map[string]any)["id"] != team {
			t.Fatalf("list_teams: %v", teams)
		}
		cats := st.ok("list_categories", nil)["categories"].([]any)
		if len(cats) != 1 || cats[0].(map[string]any)["id"] != category {
			t.Fatalf("list_categories: %v", cats)
		}
		created := st.ok("create_ticket", map[string]any{"subject": "Printer on fire", "body_md": "Smoke from tray 2.", "requester_email": custEmail,
			"team_id": team, "category_id": category, "assignee_id": "me", "priority": "high"})
		n, id := num(created["number"]), created["id"].(string)
		if created["channel"] != "api" || n == 0 {
			t.Fatalf("create_ticket: %v", created)
		}
		var actor, channel string
		if err := s.db.Read().QueryRowContext(ctx, `SELECT e.actor_id, t.channel FROM ticket_events e JOIN tickets t ON t.id = e.ticket_id
			WHERE e.ticket_id = ? AND e.type = 'ticket.created'`, id).Scan(&actor, &channel); err != nil || actor != agentID || channel != "api" {
			t.Fatalf("created event actor %q channel %q (%v)", actor, channel, err)
		}
		note := st.ok("add_internal_note", map[string]any{"ticket": fmt.Sprintf("#%d", n), "body_md": "SECRET-NOTE-7731 check the fuser"})
		if note["kind"] != "internal" {
			t.Fatalf("note: %v", note)
		}
		reply := st.ok("reply_to_ticket", map[string]any{"ticket": n, "body_md": "We are on it.", "set_status": "pending"})
		if reply["kind"] != "public" || reply["channel"] != "api" {
			t.Fatalf("reply: %v", reply)
		}
		got := st.ok("get_ticket", map[string]any{"ticket": n})
		if got["status"] != "pending" || num(got["messages_total"]) != 3 {
			t.Fatalf("get_ticket: status %v, %v messages", got["status"], got["messages_total"])
		}
		kinds := []string{}
		for _, msg := range got["messages"].([]any) {
			kinds = append(kinds, msg.(map[string]any)["kind"].(string))
		}
		if !slices.Contains(kinds, "internal") {
			t.Fatalf("staff must see the internal note: %v", kinds)
		}
		upd := st.ok("update_ticket", map[string]any{"ticket": id, "priority": "urgent", "tags": []string{"vip"}})
		if tk := upd["ticket"].(map[string]any); tk["priority"] != "urgent" || upd["visible"] != true {
			t.Fatalf("update_ticket: %v", upd)
		}
		e := st.fail("update_ticket", map[string]any{"ticket": n, "status": "new"}, "invalid-transition")
		if e["status"] != float64(409) {
			t.Fatalf("invalid transition: %v", e)
		}
		st.fail("update_ticket", map[string]any{"ticket": n}, "validation")
		st.fail("get_ticket", map[string]any{"ticket": 999999}, "not-found")
		agent.must(201, "POST", "/api/v1/agent/macros", map[string]any{"name": "Ask for logs", "reply_kind": "public", "reply_md": "Please send the logs.", "status": "pending"})
		mac := st.ok("apply_macro", map[string]any{"ticket": n, "macro": "ask FOR logs"})
		if mac["ticket"].(map[string]any)["status"] != "pending" {
			t.Fatalf("apply_macro: %v", mac)
		}
		st.fail("apply_macro", map[string]any{"ticket": n, "macro": "nope"}, "validation")
		mine := st.ok("list_my_tickets", nil)["items"].([]any)
		if len(mine) != 1 || num(mine[0].(map[string]any)["number"]) != n {
			t.Fatalf("list_my_tickets: %v", mine)
		}
		var found []any
		for deadline := time.Now().Add(10 * time.Second); len(found) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			found = st.ok("search_tickets", map[string]any{"query": "fuser"})["items"].([]any)
		}
		if len(found) != 1 {
			t.Fatalf("staff search must match the internal note: %v", found)
		}
		kbHits := st.ok("search_kb", map[string]any{"query": "password"})["items"].([]any)
		if len(kbHits) != 2 {
			t.Fatalf("staff KB search: %v", kbHits)
		}
		if art := st.ok("get_kb_article", map[string]any{"slug": "reset-password"}); art["title"] != "Reset your password" || art["body_html"] != nil {
			t.Fatalf("get_kb_article: %v", art)
		}
		// Escalation: the L1 agent loses sight; an admin brings it back.
		esc := st.ok("escalate_ticket", map[string]any{"ticket": n, "reason": "needs L2"})
		if esc["visible"] != false {
			t.Fatalf("escalate visible: %v", esc)
		}
		st.fail("get_ticket", map[string]any{"ticket": n}, "not-found")
		adm := s.mcp(adminTok, "2025-11-25")
		de := adm.ok("deescalate_ticket", map[string]any{"ticket": n, "reason": "L1 can finish"})
		if num(de["ticket"].(map[string]any)["tier"]) != 1 {
			t.Fatalf("deescalate: %v", de)
		}

		// ---- scopes: a tickets:read token cannot write ----
		ro := s.mcp(roTok, "2025-06-18")
		if rt := ro.tools(); slices.Contains(rt, "create_ticket") || slices.Contains(rt, "list_teams") || !slices.Contains(rt, "get_ticket") {
			t.Fatalf("read-only tools: %v", rt)
		}
		var before, after int
		_ = s.db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tickets`).Scan(&before)
		ro.fail("create_ticket", map[string]any{"subject": "x", "body_md": "y", "requester_email": custEmail}, "forbidden")
		ro.fail("add_internal_note", map[string]any{"ticket": n, "body_md": "x"}, "forbidden")
		_ = s.db.Read().QueryRowContext(ctx, `SELECT count(*) FROM tickets`).Scan(&after)
		if before != after {
			t.Fatal("a tickets:read token created a ticket")
		}

		// The portal by-number route behind the customer tools: the portal view, 404 for staff and strangers.
		if pt := cust.must(200, "GET", fmt.Sprintf("/api/v1/portal/tickets/by-number/%d", n), nil); pt["id"] != id {
			t.Fatalf("portal by-number: %v", pt)
		}
		agent.must(404, "GET", fmt.Sprintf("/api/v1/portal/tickets/by-number/%d", n), nil)
		other := s.loginFixture(storetest.CreateCustomer(t, db, ""), domain.KindCustomer)
		other.must(404, "GET", fmt.Sprintf("/api/v1/portal/tickets/by-number/%d", n), nil)

		// ---- customer: customer tools only, public messages only ----
		cu := s.mcp(custTok, "2025-11-25")
		ctools := cu.tools()
		for _, n := range []string{"add_internal_note", "escalate_ticket", "deescalate_ticket", "apply_macro", "list_teams"} {
			if slices.Contains(ctools, n) {
				t.Fatalf("customer sees staff-only tool %s", n)
			}
		}
		if _, e := cu.rpc("tools/call", map[string]any{"name": "add_internal_note", "arguments": map[string]any{"ticket": n, "body_md": "x"}}); e == nil || e["code"] != float64(-32602) {
			t.Fatalf("customer calling a staff tool: %v", e)
		}
		res, _ := cu.rpc("tools/call", map[string]any{"name": "get_ticket", "arguments": map[string]any{"ticket": n}})
		raw, _ := json.Marshal(res)
		if strings.Contains(string(raw), "SECRET-NOTE-7731") || strings.Contains(string(raw), `"internal"`) {
			t.Fatalf("I-2: the customer saw the internal note: %s", raw)
		}
		ct := res["structuredContent"].(map[string]any)
		for _, k := range []string{"tier", "team_id", "assignee", "tags", "watchers", "sla_breached"} {
			if _, ok := ct[k]; ok {
				t.Fatalf("I-9: staff-only field %s in the customer view", k)
			}
		}
		if ct["status"] != "awaiting_reply" {
			t.Fatalf("customer status mapping: %v", ct["status"])
		}
		if hits := cu.ok("search_tickets", map[string]any{"query": "fuser"})["items"].([]any); len(hits) != 0 {
			t.Fatalf("I-2: internal text is searchable for the customer: %v", hits)
		}
		if hits := cu.ok("search_kb", map[string]any{"query": "password"})["items"].([]any); len(hits) != 1 {
			t.Fatalf("customer KB search must not include internal articles: %v", hits)
		}
		cu.fail("get_kb_article", map[string]any{"slug": "staff-runbook"}, "not-found")
		if r := cu.ok("reply_to_ticket", map[string]any{"ticket": n, "body_md": "Logs attached."}); r["kind"] != "public" {
			t.Fatalf("customer reply: %v", r)
		}
		mineC := cu.ok("list_my_tickets", nil)["items"].([]any)
		if len(mineC) != 1 {
			t.Fatalf("customer list: %v", mineC)
		}
		newT := cu.ok("create_ticket", map[string]any{"subject": "Second issue", "body_md": "Also the scanner."})
		if newT["channel"] != "api" {
			t.Fatalf("customer create: %v", newT)
		}
		if r := cu.ok("update_ticket", map[string]any{"ticket": num(newT["number"]), "status": "resolved"}); r["status"] != "resolved" {
			t.Fatalf("customer resolve: %v", r)
		}
		cu.ok("list_categories", nil)

		// Resources respect the same visibility.
		rl, _ := cu.rpc("resources/list", map[string]any{})
		rraw, _ := json.Marshal(rl)
		// D-20: resources are ticket://KEY-123; the legacy ticket://<number> still reads.
		if !strings.Contains(string(rraw), fmt.Sprintf("ticket://HD-%d", n)) || strings.Contains(string(rraw), "kb://staff-runbook") {
			t.Fatalf("customer resources/list: %s", rraw)
		}
		for _, uri := range []string{fmt.Sprintf("ticket://HD-%d", n), fmt.Sprintf("ticket://%d", n)} {
			rr, _ := cu.rpc("resources/read", map[string]any{"uri": uri})
			if b, _ := json.Marshal(rr); strings.Contains(string(b), "SECRET-NOTE-7731") || !strings.Contains(string(b), "Logs attached") {
				t.Fatalf("I-2 via resources/read %s: %s", uri, b)
			}
		}
		if _, e := cu.rpc("resources/read", map[string]any{"uri": "kb://staff-runbook"}); e == nil || e["code"] != float64(-32002) {
			t.Fatalf("hidden KB resource: %v", e)
		}
		kr, _ := st.rpc("resources/read", map[string]any{"uri": "kb://reset-password"})
		if b, _ := json.Marshal(kr); !strings.Contains(string(b), "# Reset your password") {
			t.Fatalf("kb resource: %s", b)
		}

		// ---- 2026-07-28: stateless, headers mirror the body ----
		modern := &mcpClient{s: s, token: staffTok}
		body := `{"jsonrpc":"2.0","id":"m1","method":"tools/call","params":{"name":"search_tickets","arguments":{"query":"printer"},` +
			`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
		code, _, data := modern.raw("POST", body, "MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "search_tickets")
		if code != 200 || !strings.Contains(string(data), `"resultType":"complete"`) || !strings.Contains(string(data), "Printer on fire") {
			t.Fatalf("modern tools/call: %d %s", code, data)
		}
		if code, _, data := modern.raw("POST", body, "MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "get_ticket"); code != 400 || !strings.Contains(string(data), "-32020") {
			t.Fatalf("modern header mismatch: %d %s", code, data)
		}

		// With the customer portal switched off, customer tokens stop working here too.
		admin.must(200, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": false})
		if code, _, _ := cu.raw("POST", `{"jsonrpc":"2.0","id":9,"method":"ping"}`); code != 401 {
			t.Fatalf("customer token with the portal off: %d", code)
		}
		if _, e := st.rpc("ping", map[string]any{}); e != nil {
			t.Fatalf("staff with the portal off: %v", e)
		}
		admin.must(200, "PATCH", "/api/v1/admin/settings", map[string]any{"portal_enabled": true})

		// Switching MCP off closes the door for existing sessions too.
		admin.must(200, "PATCH", "/api/v1/admin/integrations", map[string]any{"mcp_enabled": false})
		if code, _, _ := st.raw("POST", `{"jsonrpc":"2.0","id":9,"method":"ping"}`); code != 404 {
			t.Fatalf("disabled with a live session: %d", code)
		}
	})
}
