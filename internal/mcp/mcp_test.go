// SPDX-License-Identifier: AGPL-3.0-only

package mcp

// Protocol tests against a fake REST API: the handshake and version
// negotiation of every revision, session headers, the 2026-07-28 header
// rules, malformed JSON-RPC, batches, Origin, auth and the setting. The
// tools against the real REST API (both engines) live in
// internal/app/e2e_mcp_test.go.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
)

const (
	tokStaff = "fd_pat_staff"
	tokRO    = "fd_pat_readonly"
	tokCust  = "fd_pat_customer"
	origin   = "https://help.example.test"
)

// fakeAPI stands in for the REST router.
type fakeAPI struct {
	mu    sync.Mutex
	calls []string
	auth  []string
}

var fakeMe = map[string]map[string]any{
	tokStaff: {"id": "u-staff", "kind": "staff", "role": "agent", "name": "Sam", "scopes": []string{"tickets:read", "tickets:write", "directory:read"}},
	tokRO:    {"id": "u-ro", "kind": "staff", "role": "agent", "name": "Rae", "scopes": []string{"tickets:read"}},
	tokCust:  {"id": "u-cust", "kind": "customer", "role": "requester", "name": "Cleo", "scopes": []string{"tickets:read", "tickets:write"}},
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if server.PrincipalFrom(r.Context()) != nil {
		problems.Write(w, r, problems.Internal(nil)) // must never happen: scopes would be skipped
		return
	}
	me, ok := fakeMe[bearer(r)]
	if !ok {
		problems.Write(w, r, problems.Unauthenticated())
		return
	}
	scopes, _ := me["scopes"].([]string)
	writable := slices.Contains(scopes, "tickets:write")
	js := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/me":
		js(200, me)
	case "GET /api/v1/agent/tickets", "GET /api/v1/portal/tickets":
		js(200, map[string]any{"items": []any{map[string]any{"number": 7, "subject": "Printer", "status": "open", "query": r.URL.RawQuery}}, "next_cursor": nil})
	case "GET /api/v1/agent/tickets/by-number/7":
		js(200, map[string]any{"id": "t-7", "number": 7, "subject": "Printer", "messages": []any{
			map[string]any{"id": "m1", "kind": "public"}, map[string]any{"id": "m2", "kind": "internal"}, map[string]any{"id": "m3", "kind": "public"}}})
	case "POST /api/v1/agent/tickets":
		if !writable {
			problems.Write(w, r, problems.Forbidden("token scope does not allow this operation"))
			return
		}
		js(201, map[string]any{"id": "t-8", "number": 8})
	case "PATCH /api/v1/agent/tickets/t-7":
		problems.Write(w, r, problems.InvalidTransition("closed", []string{"open"}))
	case "GET /api/v1/kb/articles":
		js(200, map[string]any{"items": []any{map[string]any{"slug": "reset", "title": "Reset"}}})
	default:
		problems.Write(w, r, problems.NotFound())
	}
}

type harness struct {
	t       *testing.T
	h       *Handler
	api     *fakeAPI
	enabled bool
	now     time.Time
}

func newHarness(t *testing.T) *harness {
	hs := &harness{t: t, api: &fakeAPI{}, enabled: true, now: time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)}
	hs.h = New(Options{
		API:         hs.api,
		Enabled:     func(context.Context) (bool, error) { return hs.enabled, nil },
		Origins:     []string{origin, "http://localhost:3200"},
		Version:     "test",
		Now:         func() time.Time { return hs.now },
		MaxSessions: 3,
		SessionTTL:  time.Hour,
	})
	return hs
}

type reply struct {
	code int
	hdr  http.Header
	body []byte
	rpc  struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  map[string]any  `json:"result"`
		Error   *rpcError       `json:"error"`
	}
}

// do sends one HTTP request to the handler (method POST unless overridden).
func (hs *harness) do(method, token, body string, hdr ...string) *reply {
	hs.t.Helper()
	req := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.7:4000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	switch method {
	case http.MethodGet:
		hs.h.ServeGet(rec, req)
	case http.MethodDelete:
		hs.h.ServeDelete(rec, req)
	default:
		hs.h.ServePost(rec, req)
	}
	out := &reply{code: rec.Code, hdr: rec.Header(), body: rec.Body.Bytes()}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") && len(out.body) > 0 && out.body[0] == '{' {
		if err := json.Unmarshal(out.body, &out.rpc); err != nil {
			hs.t.Fatalf("decode %s: %v", out.body, err)
		}
	}
	return out
}

func (hs *harness) post(token, body string, hdr ...string) *reply {
	return hs.do(http.MethodPost, token, body, hdr...)
}

func (r *reply) want(t *testing.T, code int) *reply {
	t.Helper()
	if r.code != code {
		t.Fatalf("status %d, want %d: %s", r.code, code, r.body)
	}
	return r
}

func (r *reply) wantError(t *testing.T, code, rpcCode int) *reply {
	t.Helper()
	r.want(t, code)
	if r.rpc.Error == nil || r.rpc.Error.Code != rpcCode {
		t.Fatalf("want JSON-RPC error %d, got %s", rpcCode, r.body)
	}
	return r
}

// initialize opens a legacy session and returns its id.
func (hs *harness) initialize(token, version string) string {
	hs.t.Helper()
	r := hs.post(token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`).want(hs.t, 200)
	sid := r.hdr.Get("Mcp-Session-Id")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(sid) {
		hs.t.Fatalf("session id %q", sid)
	}
	hs.post(token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "Mcp-Session-Id", sid, "MCP-Protocol-Version", version).want(hs.t, 202)
	return sid
}

func modernBody(id int, method, params string) string {
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"t","version":"1"}}`
	if params == "" {
		params = "{" + meta + "}"
	} else {
		params = strings.TrimSuffix(params, "}") + "," + meta + "}"
	}
	b, _ := json.Marshal(id)
	return `{"jsonrpc":"2.0","id":` + string(b) + `,"method":"` + method + `","params":` + params + `}`
}

func modernHeaders(method, name string) []string {
	h := []string{"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", method}
	if name != "" {
		h = append(h, "Mcp-Name", name)
	}
	return h
}

// ---- handshake and negotiation ----

func TestMCP_InitializeNegotiation(t *testing.T) {
	hs := newHarness(t)
	for requested, want := range map[string]string{
		"2025-11-25": "2025-11-25", "2025-06-18": "2025-06-18", "2025-03-26": "2025-03-26",
		"2024-11-05": "2025-11-25", "2026-07-28": "2025-11-25", "1999-01-01": "2025-11-25",
	} {
		r := hs.post(tokStaff, `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"`+requested+`","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`).want(t, 200)
		if got := r.rpc.Result["protocolVersion"]; got != want {
			t.Errorf("requested %s: negotiated %v, want %s", requested, got, want)
		}
		if string(r.rpc.ID) != `"init"` {
			t.Errorf("id not echoed: %s", r.rpc.ID)
		}
		info, _ := r.rpc.Result["serverInfo"].(map[string]any)
		caps, _ := r.rpc.Result["capabilities"].(map[string]any)
		if info["name"] != "featherdesk" || caps["tools"] == nil || caps["resources"] == nil || r.rpc.Result["instructions"] == "" {
			t.Errorf("initialize result: %s", r.body)
		}
		if r.hdr.Get("Mcp-Session-Id") == "" {
			t.Errorf("no session id")
		}
	}
	// Missing protocolVersion is an invalid-params error.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`).wantError(t, 200, codeInvalidParams)
	// initialize as a notification is malformed.
	hs.post(tokStaff, `{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2025-06-18"}}`).wantError(t, 400, codeInvalidRequest)
}

func TestMCP_LegacySessionHeaders(t *testing.T) {
	hs := newHarness(t)
	sid := hs.initialize(tokStaff, "2025-06-18")
	ping := `{"jsonrpc":"2.0","id":5,"method":"ping"}`

	r := hs.post(tokStaff, ping, "Mcp-Session-Id", sid, "MCP-Protocol-Version", "2025-06-18").want(t, 200)
	if r.rpc.Error != nil || r.rpc.Result == nil || string(r.rpc.ID) != "5" {
		t.Fatalf("ping: %s", r.body)
	}
	// The header may be omitted (the negotiated version applies)…
	hs.post(tokStaff, ping, "Mcp-Session-Id", sid).want(t, 200)
	// …but must not contradict the session.
	hs.post(tokStaff, ping, "Mcp-Session-Id", sid, "MCP-Protocol-Version", "2025-03-26").wantError(t, 400, codeInvalidRequest)
	// No session: 400. Unknown session: 404. Another token's session: 404.
	hs.post(tokStaff, ping).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, ping, "Mcp-Session-Id", "0123456789abcdef0123456789abcdef").wantError(t, 404, codeInvalidRequest)
	hs.post(tokCust, ping, "Mcp-Session-Id", sid).wantError(t, 404, codeInvalidRequest)
	// A second initialize inside the session is refused.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":6,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, "Mcp-Session-Id", sid).want(t, 200)
	// Client responses and other notifications are accepted with 202.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":99,"result":{}}`, "Mcp-Session-Id", sid).want(t, 202)
	hs.post(tokStaff, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, "Mcp-Session-Id", sid).want(t, 202)
	// Unknown method inside a session: JSON-RPC -32601.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":7,"method":"prompts/list"}`, "Mcp-Session-Id", sid).wantError(t, 200, codeMethodNotFound)

	// DELETE ends it; only its own token may.
	hs.do(http.MethodDelete, tokCust, "", "Mcp-Session-Id", sid).want(t, 404)
	hs.do(http.MethodDelete, tokStaff, "", "Mcp-Session-Id", sid).want(t, 204)
	hs.post(tokStaff, ping, "Mcp-Session-Id", sid).want(t, 404)
	hs.do(http.MethodDelete, tokStaff, "").want(t, 405)

	// Idle sessions expire.
	sid = hs.initialize(tokStaff, "2025-11-25")
	hs.now = hs.now.Add(2 * time.Hour)
	hs.post(tokStaff, ping, "Mcp-Session-Id", sid).want(t, 404)
}

func TestMCP_SessionTableBounded(t *testing.T) {
	now := time.Unix(0, 0)
	s := newSessions(3, time.Hour, func() time.Time { return now })
	var ids []string
	for i := range 5 {
		now = now.Add(time.Minute)
		ids = append(ids, s.create("2025-06-18", "k", "c").id)
		if s.len() > 3 {
			t.Fatalf("table grew to %d after %d creates", s.len(), i+1)
		}
	}
	if _, ok := s.get(ids[0], "k"); ok {
		t.Fatal("the least recently used session survived")
	}
	if _, ok := s.get(ids[4], "k"); !ok {
		t.Fatal("the newest session was evicted")
	}
}

// ---- 2026-07-28 ----

func TestMCP_ModernStateless(t *testing.T) {
	hs := newHarness(t)
	r := hs.post(tokStaff, modernBody(1, "server/discover", ""), modernHeaders("server/discover", "")...).want(t, 200)
	res := r.rpc.Result
	if res["resultType"] != "complete" || res["cacheScope"] != "public" || res["ttlMs"] == nil {
		t.Fatalf("discover: %s", r.body)
	}
	if v, _ := res["supportedVersions"].([]any); len(v) != 4 || v[0] != "2026-07-28" {
		t.Fatalf("supportedVersions: %v", res["supportedVersions"])
	}
	meta, _ := res["_meta"].(map[string]any)
	if si, _ := meta["io.modelcontextprotocol/serverInfo"].(map[string]any); si["name"] != "featherdesk" {
		t.Fatalf("serverInfo: %s", r.body)
	}
	if r.hdr.Get("Mcp-Session-Id") != "" {
		t.Fatal("2026-07-28 must not mint sessions")
	}

	// tools/list needs no session; a stray session header is ignored.
	r = hs.post(tokStaff, modernBody(2, "tools/list", ""), append(modernHeaders("tools/list", ""), "Mcp-Session-Id", "nope")...).want(t, 200)
	if r.rpc.Result["cacheScope"] != "private" || r.rpc.Result["resultType"] != "complete" {
		t.Fatalf("tools/list: %s", r.body)
	}

	// tools/call: Mcp-Name must match params.name (plain or base64 sentinel).
	call := modernBody(3, "tools/call", `{"name":"search_kb","arguments":{"query":"reset"}}`)
	hs.post(tokStaff, call, modernHeaders("tools/call", "search_kb")...).want(t, 200)
	b64 := "=?base64?" + base64.StdEncoding.EncodeToString([]byte("search_kb")) + "?="
	hs.post(tokStaff, call, modernHeaders("tools/call", b64)...).want(t, 200)
	hs.post(tokStaff, call, modernHeaders("tools/call", "get_ticket")...).wantError(t, 400, codeHeaderMismatch)
	hs.post(tokStaff, call, modernHeaders("tools/call", "")...).wantError(t, 400, codeHeaderMismatch)
	// Mcp-Method and MCP-Protocol-Version must match the body.
	hs.post(tokStaff, call, "MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/list", "Mcp-Name", "search_kb").wantError(t, 400, codeHeaderMismatch)
	hs.post(tokStaff, call, "Mcp-Method", "tools/call", "Mcp-Name", "search_kb").wantError(t, 400, codeHeaderMismatch)

	// Unsupported version (in _meta or only in the header): 400 -32022 with the list.
	bad := strings.Replace(modernBody(4, "tools/list", ""), `protocolVersion":"2026-07-28"`, `protocolVersion":"2099-01-01"`, 1)
	r = hs.post(tokStaff, bad, "MCP-Protocol-Version", "2099-01-01", "Mcp-Method", "tools/list").wantError(t, 400, codeUnsupportedVersion)
	data, _ := r.rpc.Error.Data.(map[string]any)
	if data["requested"] != "2099-01-01" || len(data["supported"].([]any)) != 4 {
		t.Fatalf("unsupported data: %s", r.body)
	}
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`, "MCP-Protocol-Version", "2099-01-01").wantError(t, 400, codeUnsupportedVersion)
	// The modern header without _meta: required field missing → 400 -32602.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":6,"method":"tools/list"}`, modernHeaders("tools/list", "")...).wantError(t, 400, codeInvalidParams)
	// clientCapabilities is required.
	noCaps := strings.Replace(modernBody(7, "tools/list", ""), `"io.modelcontextprotocol/clientCapabilities":{},`, "", 1)
	hs.post(tokStaff, noCaps, modernHeaders("tools/list", "")...).wantError(t, 400, codeInvalidParams)
	// Unknown (and removed: ping) methods are 404 -32601.
	hs.post(tokStaff, modernBody(8, "prompts/list", ""), modernHeaders("prompts/list", "")...).wantError(t, 404, codeMethodNotFound)
	hs.post(tokStaff, modernBody(9, "ping", ""), modernHeaders("ping", "")...).wantError(t, 404, codeMethodNotFound)
	// Clients must not send responses.
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":10,"result":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`, modernHeaders("", "")...).wantError(t, 400, codeInvalidRequest)
}

// ---- malformed JSON-RPC ----

func TestMCP_MalformedJSONRPC(t *testing.T) {
	hs := newHarness(t)
	sid := hs.initialize(tokStaff, "2025-06-18")
	s := []string{"Mcp-Session-Id", sid}
	r := hs.post(tokStaff, `{"jsonrpc":"2.0","id":1,"method":`, s...).wantError(t, 400, codeParseError)
	if string(r.rpc.ID) != "null" {
		t.Fatalf("parse error id: %s", r.rpc.ID)
	}
	hs.post(tokStaff, `{"jsonrpc":"1.0","id":1,"method":"ping"}`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":null,"method":"ping"}`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":[1]}`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":1}`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `"ping"`, s...).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":2,"method":"no/such"}`, s...).wantError(t, 200, codeMethodNotFound)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"no_such_tool"}}`, s...).wantError(t, 200, codeInvalidParams)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{"cursor":"x"}}`, s...).wantError(t, 200, codeInvalidParams)
	// Not JSON at all, wrong media type, too large.
	hs.post(tokStaff, `x`, s...).wantError(t, 400, codeParseError)
	hs.post(tokStaff, `{}`, append(s, "Content-Type", "text/plain")...).want(t, 415)
	hs.post(tokStaff, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"`+strings.Repeat("a", maxBody)+`"}}`, s...).want(t, 413)
}

// ---- batches (2025-03-26 only) ----

func TestMCP_Batch(t *testing.T) {
	hs := newHarness(t)
	sid := hs.initialize(tokStaff, "2025-03-26")
	batch := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},` +
		`{"jsonrpc":"2.0","id":"b","method":"tools/list"},{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-03-26"}},{"bad":true}]`
	r := hs.post(tokStaff, batch, "Mcp-Session-Id", sid).want(t, 200)
	var out []response
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("batch body: %v %s", err, r.body)
	}
	if len(out) != 4 {
		t.Fatalf("want 4 responses (no notification), got %d: %s", len(out), r.body)
	}
	if string(out[0].ID) != "1" || out[0].Error != nil || string(out[1].ID) != `"b"` || out[1].Error != nil {
		t.Fatalf("batch results: %s", r.body)
	}
	if out[2].Error == nil || out[2].Error.Code != codeInvalidRequest || out[3].Error == nil {
		t.Fatalf("initialize in a batch and a bad element must fail: %s", r.body)
	}
	// 2025-03-26 tool results carry no structuredContent.
	hs.post(tokStaff, `[{"jsonrpc":"2.0","method":"notifications/initialized"}]`, "Mcp-Session-Id", sid).want(t, 202)
	hs.post(tokStaff, `[]`, "Mcp-Session-Id", sid).wantError(t, 400, codeInvalidRequest)
	many := "[" + strings.TrimSuffix(strings.Repeat(`{"jsonrpc":"2.0","id":1,"method":"ping"},`, maxBatch+1), ",") + "]"
	hs.post(tokStaff, many, "Mcp-Session-Id", sid).wantError(t, 400, codeInvalidRequest)

	// Later revisions removed batching.
	sid2 := hs.initialize(tokStaff, "2025-06-18")
	hs.post(tokStaff, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, "Mcp-Session-Id", sid2).wantError(t, 400, codeInvalidRequest)
	hs.post(tokStaff, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`).wantError(t, 400, codeInvalidRequest)
}

// ---- gate: setting, Origin, auth, GET ----

func TestMCP_Gate(t *testing.T) {
	hs := newHarness(t)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`
	hs.post(tokStaff, init, "Origin", "https://evil.example").want(t, 403)
	hs.post(tokStaff, init, "Origin", origin).want(t, 200)
	hs.post(tokStaff, init, "Origin", strings.ToUpper(origin)).want(t, 200)
	hs.post(tokStaff, init, "Origin", "http://localhost:3200").want(t, 200) // FD_ALLOWED_ORIGINS
	hs.post(tokStaff, init, "Origin", "null").want(t, 403)

	r := hs.post("", init).want(t, 401)
	if !strings.HasPrefix(r.hdr.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("WWW-Authenticate: %q", r.hdr.Get("WWW-Authenticate"))
	}
	hs.post("fd_pat_unknown", init).want(t, 401)
	hs.post("", init, "Cookie", "fd_session=abc").want(t, 401)

	hs.do(http.MethodGet, tokStaff, "").want(t, 405)
	hs.enabled = false
	for _, m := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		hs.do(m, tokStaff, init).want(t, 404)
	}
	hs.enabled = true

	// Every inner REST call carried the token and no pre-resolved principal.
	for _, a := range hs.api.auth {
		if !strings.HasPrefix(a, "Bearer fd_pat_") {
			t.Fatalf("inner call without the token: %q", a)
		}
	}
}

// ---- tools ----

func TestMCP_ToolsByKindAndScope(t *testing.T) {
	hs := newHarness(t)
	names := func(tok string) []string {
		sid := hs.initialize(tok, "2025-11-25")
		r := hs.post(tok, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "Mcp-Session-Id", sid).want(t, 200)
		var out []string
		for _, x := range r.rpc.Result["tools"].([]any) {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	staff := names(tokStaff)
	for _, n := range []string{"search_tickets", "list_my_tickets", "get_ticket", "create_ticket", "reply_to_ticket", "add_internal_note",
		"update_ticket", "escalate_ticket", "deescalate_ticket", "apply_macro", "search_kb", "get_kb_article", "list_teams", "list_categories"} {
		if !slices.Contains(staff, n) {
			t.Errorf("staff tools lack %s: %v", n, staff)
		}
	}
	cust := names(tokCust)
	for _, n := range []string{"add_internal_note", "escalate_ticket", "deescalate_ticket", "apply_macro", "list_teams"} {
		if slices.Contains(cust, n) {
			t.Errorf("customer tools include staff-only %s", n)
		}
	}
	if !slices.Contains(cust, "create_ticket") || !slices.Contains(cust, "search_kb") {
		t.Errorf("customer tools: %v", cust)
	}
	ro := names(tokRO)
	for _, n := range []string{"create_ticket", "reply_to_ticket", "update_ticket", "list_teams"} {
		if slices.Contains(ro, n) {
			t.Errorf("tickets:read token lists %s", n)
		}
	}
	// Deterministic order.
	if again := names(tokStaff); !slices.Equal(again, staff) {
		t.Fatalf("order changed: %v vs %v", again, staff)
	}

	// A customer cannot call a staff-only tool (unknown tool, -32602).
	sid := hs.initialize(tokCust, "2025-06-18")
	hs.post(tokCust, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add_internal_note","arguments":{"ticket":7,"body_md":"x"}}}`,
		"Mcp-Session-Id", sid).wantError(t, 200, codeInvalidParams)
}

func TestMCP_ToolResultsAndErrors(t *testing.T) {
	hs := newHarness(t)
	sid := hs.initialize(tokStaff, "2025-06-18")
	call := func(tok, sid, name, args string) map[string]any {
		t.Helper()
		r := hs.post(tok, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`, "Mcp-Session-Id", sid).want(t, 200)
		if r.rpc.Error != nil {
			t.Fatalf("%s: protocol error %s", name, r.body)
		}
		return r.rpc.Result
	}
	res := call(tokStaff, sid, "get_ticket", `{"ticket":"#7","max_messages":2}`)
	sc, _ := res["structuredContent"].(map[string]any)
	if res["isError"] != false || sc["id"] != "t-7" || len(sc["messages"].([]any)) != 2 || sc["messages_omitted"] != float64(1) {
		t.Fatalf("get_ticket: %v", res)
	}
	if text := res["content"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(text, `"t-7"`) {
		t.Fatalf("text content: %s", text)
	}
	// A REST problem becomes a tool error with its slug (and the allowed edges).
	res = call(tokStaff, sid, "update_ticket", `{"ticket":7,"status":"open"}`)
	sc, _ = res["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	if res["isError"] != true || e["slug"] != "invalid-transition" || e["status"] != float64(409) {
		t.Fatalf("update_ticket error: %v", res)
	}
	// Bad arguments are tool errors too (the model can fix them).
	for _, args := range []string{`{"ticket":7,"bogus":1}`, `{}`, `{"ticket":7}`} {
		res = call(tokStaff, sid, "update_ticket", args)
		e = res["structuredContent"].(map[string]any)["error"].(map[string]any)
		if res["isError"] != true || e["slug"] != "validation" {
			t.Fatalf("args %s: %v", args, res)
		}
	}
	// Scope: a tickets:read token's create is refused by the REST layer.
	rsid := hs.initialize(tokRO, "2025-06-18")
	res = call(tokRO, rsid, "create_ticket", `{"subject":"s","body_md":"b","requester_email":"c@example.test"}`)
	e = res["structuredContent"].(map[string]any)["error"].(map[string]any)
	if res["isError"] != true || e["slug"] != "forbidden" {
		t.Fatalf("read-only create: %v", res)
	}
}

func TestMCP_ToolDefinitionsWellFormed(t *testing.T) {
	nameRe := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	for kind, tools := range map[string][]*tool{"staff": staffTools, "customer": customerTools} {
		seen := map[string]bool{}
		for _, tl := range tools {
			if !nameRe.MatchString(tl.Name) || seen[tl.Name] {
				t.Errorf("%s: bad or duplicate name %q", kind, tl.Name)
			}
			seen[tl.Name] = true
			if tl.Schema["type"] != "object" || tl.Schema["additionalProperties"] != false {
				t.Errorf("%s/%s: schema must be a closed object", kind, tl.Name)
			}
			if len(tl.Description) < 40 || tl.Title == "" || len(tl.Ops) == 0 || tl.run == nil {
				t.Errorf("%s/%s: incomplete definition", kind, tl.Name)
			}
			if _, err := json.Marshal(tl.definition()); err != nil {
				t.Errorf("%s/%s: %v", kind, tl.Name, err)
			}
			for _, op := range tl.Ops {
				if kind == "customer" && strings.Contains(op, "/agent/") {
					t.Errorf("customer tool %s calls a staff route %s", tl.Name, op)
				}
			}
		}
	}
}
