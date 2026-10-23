// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/integrations/oauthflow"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// ---- fake Slack ----

type fakeSlack struct {
	*httptest.Server
	mu        sync.Mutex
	posts     []map[string]any
	postErr   map[string]string // channel → error code
	rateLimit atomic.Bool
	listCalls atomic.Int64
	revoked   atomic.Int64
	tsSeq     atomic.Int64
	exchange  string // error code for oauth.v2.access, "" = ok
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{postErr: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/oauth.v2.access":
			_ = r.ParseForm()
			f.mu.Lock()
			code := f.exchange
			f.mu.Unlock()
			if code != "" || r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "s3cret" {
				if code == "" {
					code = "invalid_code"
				}
				_, _ = io.WriteString(w, `{"ok":false,"error":"`+code+`"}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"access_token":"xoxb-test","token_type":"bot","scope":"chat:write,chat:write.public,channels:read,groups:read",
				"bot_user_id":"U0BOT","app_id":"A0APP","team":{"id":"T0ACME","name":"Acme"},"enterprise":null,"is_enterprise_install":false}`)
		case "/api/auth.test":
			_, _ = io.WriteString(w, `{"ok":true,"url":"https://acme.slack.com/","team":"Acme","user":"featherdesk","team_id":"T0ACME","user_id":"U0BOT"}`)
		case "/api/auth.revoke":
			f.revoked.Add(1)
			_, _ = io.WriteString(w, `{"ok":true,"revoked":true}`)
		case "/api/conversations.list":
			f.listCalls.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("cursor") == "" {
				_, _ = io.WriteString(w, `{"ok":true,"channels":[{"id":"C0SUPPORT","name":"support","is_private":false,"is_member":false,"num_members":12},
					{"id":"C0OLD","name":"old","is_archived":true}],"response_metadata":{"next_cursor":"p2"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"channels":[{"id":"G0URGENT","name":"alerts","is_private":true,"is_member":true,"num_members":3}],"response_metadata":{"next_cursor":""}}`)
		case "/api/chat.postMessage":
			if auth != "Bearer xoxb-test" {
				_, _ = io.WriteString(w, `{"ok":false,"error":"invalid_auth"}`)
				return
			}
			if f.rateLimit.Load() {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			ch, _ := m["channel"].(string)
			f.mu.Lock()
			code := f.postErr[ch]
			if code == "" {
				f.posts = append(f.posts, m)
			}
			f.mu.Unlock()
			if code != "" {
				_, _ = io.WriteString(w, `{"ok":false,"error":"`+code+`"}`)
				return
			}
			ts := "1700000000." + strings.Repeat("0", 5) + string(rune('0'+f.tsSeq.Add(1)%10))
			_, _ = io.WriteString(w, `{"ok":true,"channel":"`+ch+`","ts":"`+ts+`"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSlack) all() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.posts...)
}

// ---- harness ----

type harness struct {
	t     *testing.T
	db    *store.DB
	srv   *server.Server
	m     *Module
	slack *fakeSlack
	admin string
}

func newHarness(t *testing.T, db *store.DB) *harness {
	t.Helper()
	fs := newFakeSlack(t)
	envm := map[string]string{"FD_SECRET_KEY": strings.Repeat("k", 32), "FD_DATA_DIR": t.TempDir(), "FD_PUBLIC_URL": "https://help.example.test",
		EnvBaseURL: fs.URL}
	getenv := func(k string) string { return envm[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(server.Options{Config: cfg, Logger: log, DB: db})
	adminID := storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
	agentID := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
	srv.SetAuthenticator(server.AuthenticatorFunc(func(r *http.Request) (*domain.Principal, error) {
		switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
		case "admin":
			return &domain.Principal{UserID: adminID, Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3, TokenID: "t"}, nil
		case "agent":
			return &domain.Principal{UserID: agentID, Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1, TokenID: "t"}, nil
		}
		return nil, nil
	}))
	q := queue.New(db, queue.Options{Logger: log})
	st := settings.New(db, box, getenv)
	m, err := New(Deps{DB: db, Log: log, Queue: q, Settings: st, Secrets: box, Server: srv, Cfg: cfg, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	m.Mount(srv.Groups())
	m.RegisterQueue(q)
	return &harness{t: t, db: db, srv: srv, m: m, slack: fs, admin: adminID}
}

// do runs a request through the router and validates it against the spec.
func (h *harness) do(method, path, auth string, body any) (*http.Response, []byte) {
	h.t.Helper()
	mk := func() *http.Request {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rd)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		return req
	}
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, mk())
	resp := rec.Result()
	out, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	apispec.ValidateResponse(h.t, mk(), resp)
	return resp, out
}

func (h *harness) want(resp *http.Response, body []byte, status int) {
	h.t.Helper()
	if resp.StatusCode != status {
		h.t.Fatalf("%s %s: status %d, want %d: %s", resp.Request.Method, resp.Request.URL, resp.StatusCode, status, body)
	}
}

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) status() statusDTO {
	h.t.Helper()
	resp, body := h.do("GET", "/api/v1/admin/integrations/slack", "admin", nil)
	h.want(resp, body, 200)
	var s statusDTO
	if err := json.Unmarshal(body, &s); err != nil {
		h.t.Fatal(err)
	}
	return s
}

// connect saves the app credentials and runs the OAuth popup flow.
func (h *harness) connect() {
	h.t.Helper()
	resp, body := h.do("PATCH", "/api/v1/admin/integrations/slack", "admin", map[string]any{"client_id": "1234.5678", "client_secret": "s3cret"})
	h.want(resp, body, 200)
	resp, body = h.do("POST", "/api/v1/admin/integrations/slack/oauth/start", "admin", nil)
	h.want(resp, body, 200)
	var st struct{ State string }
	_ = json.Unmarshal(body, &st)
	loc := h.callback(url.Values{"code": {"good-code"}, "state": {st.State}})
	if loc.Query().Get("result") != "ok" {
		h.t.Fatalf("callback: %s", loc)
	}
}

// callback hits the public redirect target and returns the Location.
func (h *harness) callback(q url.Values) *url.URL {
	h.t.Helper()
	resp, body := h.do("GET", "/api/v1/integrations/slack/oauth/callback?"+q.Encode(), "", nil)
	h.want(resp, body, http.StatusFound)
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		h.t.Fatal(err)
	}
	return loc
}

func (h *harness) createRoute(body map[string]any) route {
	h.t.Helper()
	resp, out := h.do("POST", "/api/v1/admin/integrations/slack/routes", "admin", body)
	h.want(resp, out, 201)
	var rt route
	_ = json.Unmarshal(out, &rt)
	return rt
}

// ---- tests ----

func TestSlackSetupAndOAuth(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		if s := h.status(); s.State != "not_configured" || !s.HTTPS || s.RedirectURL != "https://help.example.test"+CallbackPath ||
			!strings.HasPrefix(s.ManifestURL, "https://api.slack.com/apps?new_app=1&manifest_json=") {
			t.Fatalf("initial status %+v", s)
		}
		if resp, body := h.do("GET", "/api/v1/admin/integrations/slack", "agent", nil); resp.StatusCode != 403 && resp.StatusCode != 404 {
			t.Fatalf("agent got %d: %s", resp.StatusCode, body)
		}
		resp, body := h.do("PATCH", "/api/v1/admin/integrations/slack", "admin", map[string]any{"client_id": "nope", "client_secret": ""})
		h.want(resp, body, 422)
		if !strings.Contains(string(body), `"pattern"`) || !strings.Contains(string(body), `"client_secret"`) {
			t.Fatalf("422 body: %s", body)
		}
		// Not configured yet: no OAuth.
		resp, body = h.do("POST", "/api/v1/admin/integrations/slack/oauth/start", "admin", nil)
		h.want(resp, body, 409)

		resp, body = h.do("PATCH", "/api/v1/admin/integrations/slack", "admin", map[string]any{"client_id": "1234.5678", "client_secret": "s3cret"})
		h.want(resp, body, 200)
		var raw string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'slack'`).Scan(&raw)
		if strings.Contains(raw, "s3cret") || strings.Contains(string(body), "s3cret") {
			t.Fatal("client secret stored or returned in plaintext")
		}
		if s := h.status(); s.State != "configured" || !s.ClientSecretSet || s.ClientID != "1234.5678" {
			t.Fatalf("configured status %+v", s)
		}

		resp, body = h.do("POST", "/api/v1/admin/integrations/slack/oauth/start", "admin", nil)
		h.want(resp, body, 200)
		var st SlackOAuthStartBody
		_ = json.Unmarshal(body, &st)
		au, _ := url.Parse(st.AuthorizeURL)
		if au.Path != "/oauth/v2/authorize" || au.Query().Get("client_id") != "1234.5678" || au.Query().Get("state") != st.State ||
			au.Query().Get("scope") != strings.Join(Scopes, ",") || au.Query().Get("redirect_uri") != "https://help.example.test"+CallbackPath {
			t.Fatalf("authorize url %s", st.AuthorizeURL)
		}

		// Unknown state, then the user cancels on Slack's page.
		if loc := h.callback(url.Values{"code": {"good-code"}, "state": {"forged"}}); loc.Query().Get("reason") != "invalid_state" || loc.Path != "/agent/oauth/complete" {
			t.Fatalf("forged state: %s", loc)
		}
		if loc := h.callback(url.Values{"error": {"access_denied"}, "state": {st.State}}); loc.Query().Get("reason") != "access_denied" || loc.Query().Get("state") != st.State {
			t.Fatalf("denied: %s", loc)
		}
		// The state was consumed by the denial: it cannot be replayed.
		if loc := h.callback(url.Values{"code": {"good-code"}, "state": {st.State}}); loc.Query().Get("reason") != "invalid_state" {
			t.Fatalf("replayed state: %s", loc)
		}
		// A code Slack refuses.
		_, body = h.do("POST", "/api/v1/admin/integrations/slack/oauth/start", "admin", nil)
		_ = json.Unmarshal(body, &st)
		if loc := h.callback(url.Values{"code": {"bad"}, "state": {st.State}}); loc.Query().Get("reason") != "invalid_code" {
			t.Fatalf("bad code: %s", loc)
		}
		// Expired state.
		_, body = h.do("POST", "/api/v1/admin/integrations/slack/oauth/start", "admin", nil)
		_ = json.Unmarshal(body, &st)
		h.m.states.Now = func() time.Time { return time.Now().Add(oauthflow.TTL + time.Minute) }
		if loc := h.callback(url.Values{"code": {"good-code"}, "state": {st.State}}); loc.Query().Get("reason") != "invalid_state" {
			t.Fatalf("expired state: %s", loc)
		}
		h.m.states.Now = time.Now

		h.connect()
		s := h.status()
		if s.State != "connected" || s.TeamName == nil || *s.TeamName != "Acme" || s.BotName == nil || *s.BotName != "featherdesk" ||
			s.ConnectedBy == nil || s.ConnectedBy.ID != h.admin || len(s.Scopes) != 4 {
			t.Fatalf("connected status %+v", s)
		}
		_ = db.Read().QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'slack'`).Scan(&raw)
		if strings.Contains(raw, "xoxb-test") {
			t.Fatal("bot token stored in plaintext")
		}
		if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'slack.connected' AND actor_id = ?`, h.admin); n != 1 {
			t.Fatalf("connect audit rows = %d", n)
		}

		resp, body = h.do("DELETE", "/api/v1/admin/integrations/slack/connection", "admin", nil)
		h.want(resp, body, 204)
		if s := h.status(); s.State != "configured" || s.TeamName != nil || h.slack.revoked.Load() != 1 {
			t.Fatalf("after disconnect %+v, revoked %d", s, h.slack.revoked.Load())
		}
	})
}

// SlackOAuthStartBody mirrors the SlackOAuthStart schema.
type SlackOAuthStartBody struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

func TestSlackChannelsAndRoutes(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		resp, body := h.do("GET", "/api/v1/admin/integrations/slack/channels", "admin", nil)
		h.want(resp, body, 409)
		h.connect()

		resp, body = h.do("GET", "/api/v1/admin/integrations/slack/channels", "admin", nil)
		h.want(resp, body, 200)
		var chs []map[string]any
		_ = json.Unmarshal(body, &chs)
		if len(chs) != 2 || chs[0]["name"] != "alerts" || chs[1]["name"] != "support" || chs[0]["is_private"] != true {
			t.Fatalf("channels %s", body)
		}
		calls := h.slack.listCalls.Load()
		h.do("GET", "/api/v1/admin/integrations/slack/channels", "admin", nil)
		if h.slack.listCalls.Load() != calls {
			t.Fatal("channel list not cached")
		}
		h.do("GET", "/api/v1/admin/integrations/slack/channels?refresh=true", "admin", nil)
		if h.slack.listCalls.Load() == calls {
			t.Fatal("refresh=true did not reload")
		}

		for _, c := range []struct {
			body map[string]any
			want string
		}{
			{map[string]any{"events": []string{"ticket.created"}}, `"channel_id"`},
			{map[string]any{"channel_id": "C0SUPPORT"}, `"events"`},
			{map[string]any{"channel_id": "C0SUPPORT", "events": []string{"note.created"}}, `"not_allowed"`},
			{map[string]any{"channel_id": "C0SUPPORT", "events": []string{"ticket.created"}, "priorities": []string{"meh"}}, `"priorities"`},
			{map[string]any{"channel_id": "C0SUPPORT", "events": []string{"ticket.created"}, "project_ids": []string{"NOPE"}}, `"not_found"`},
		} {
			resp, body := h.do("POST", "/api/v1/admin/integrations/slack/routes", "admin", c.body)
			h.want(resp, body, 422)
			if !strings.Contains(string(body), c.want) {
				t.Errorf("%v: want %s in %s", c.body, c.want, body)
			}
		}
		rt := h.createRoute(map[string]any{"channel_id": "C0SUPPORT", "channel_name": "#support", "events": []string{"ticket.created", "ticket.status_changed"},
			"project_ids": []string{"HD"}})
		if rt.ChannelName != "support" || !rt.Threads || !rt.IncludeBody || rt.Internal || !rt.Active || len(rt.ProjectIDs) != 1 || rt.ProjectIDs[0] != domain.DefaultProjectID {
			t.Fatalf("created %+v", rt)
		}
		resp, body = h.do("PATCH", "/api/v1/admin/integrations/slack/routes/"+rt.ID, "admin", map[string]any{"include_internal": true, "events": []string{"note.created"}, "priorities": []string{"urgent", "high"}})
		h.want(resp, body, 200)
		if !strings.Contains(string(body), `"priorities":["high","urgent"]`) {
			t.Fatalf("patched %s", body)
		}

		resp, body = h.do("POST", "/api/v1/admin/integrations/slack/routes/"+rt.ID+"/test", "admin", nil)
		h.want(resp, body, 200)
		if !strings.Contains(string(body), `"ok":true`) || len(h.slack.all()) != 1 {
			t.Fatalf("test post %s", body)
		}
		h.slack.mu.Lock()
		h.slack.postErr["C0SUPPORT"] = "not_in_channel"
		h.slack.mu.Unlock()
		resp, body = h.do("POST", "/api/v1/admin/integrations/slack/routes/"+rt.ID+"/test", "admin", nil)
		h.want(resp, body, 200)
		if !strings.Contains(string(body), `"error":"not_in_channel"`) {
			t.Fatalf("failed test post %s", body)
		}
		resp, body = h.do("GET", "/api/v1/admin/integrations/slack/routes", "admin", nil)
		h.want(resp, body, 200)
		if !strings.Contains(string(body), `"last_error":"not_in_channel"`) {
			t.Fatalf("route error not recorded: %s", body)
		}
		if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action LIKE 'slack.route_%'`); n != 4 {
			t.Fatalf("route audit rows = %d, want 4", n)
		}
		resp, body = h.do("DELETE", "/api/v1/admin/integrations/slack/routes/"+rt.ID, "admin", nil)
		h.want(resp, body, 204)
		if s := h.status(); s.Routes != 0 {
			t.Fatalf("routes after delete = %d", s.Routes)
		}
	})
}

// event writes a ticket (and optionally a message) and returns its event.
func event(t *testing.T, db *store.DB, tid, typ string, data any, prio string) events.Event {
	t.Helper()
	var num int64
	var req string
	_ = db.Read().QueryRowContext(context.Background(), `SELECT number, requester_id FROM tickets WHERE id = ?`, tid).Scan(&num, &req)
	e := events.Event{ID: store.NewID(), TicketID: tid, Type: typ, CreatedAt: store.Now(),
		Snapshot: events.Snapshot{Number: num, Subject: "Printer <on> fire", Status: "open", Priority: prio, Tier: 1, RequesterID: req, Channel: "portal"}}
	if data != nil {
		e.Data, _ = json.Marshal(data)
	}
	return e
}

func message(t *testing.T, db *store.DB, tid, author, kind, body string) string {
	t.Helper()
	id := store.NewID()
	err := db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, created_at) VALUES (?, ?, ?, ?, 'portal', ?, ?)`,
			id, tid, author, kind, body, store.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSlackDelivery(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		h := newHarness(t, db)
		h.connect()
		h.m.FirstRetry = func() time.Duration { return time.Hour }
		all := h.createRoute(map[string]any{"channel_id": "C0SUPPORT", "events": []string{"ticket.created", "ticket.status_changed", "message.customer", "note.created"},
			"include_internal": true})
		urgent := h.createRoute(map[string]any{"channel_id": "G0URGENT", "events": []string{"ticket.created"}, "priorities": []string{"urgent"}, "threads": false})
		other := storetest.CreateProject(t, db, "OPS")
		h.createRoute(map[string]any{"channel_id": "C0OPS", "events": []string{"ticket.created"}, "project_ids": []string{other}})

		cust := storetest.CreateCustomer(t, db, "")
		tid := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust})
		mid := message(t, db, tid, cust, "public", "The printer is *on fire* & smoking")
		if err := h.m.handleEvent(ctx, event(t, db, tid, events.TicketCreated, events.Created{Channel: "portal", MessageID: mid}, "normal")); err != nil {
			t.Fatal(err)
		}
		posts := h.slack.all()
		if len(posts) != 1 || posts[0]["channel"] != "C0SUPPORT" || posts[0]["thread_ts"] != nil {
			t.Fatalf("created posts %v", posts)
		}
		blob := fmt.Sprint(posts[0]["blocks"])
		if !strings.Contains(blob, "Printer &lt;on&gt; fire") || !strings.Contains(blob, "&amp; smoking") ||
			!strings.Contains(blob, "https://help.example.test/agent/tickets/HD-") {
			t.Fatalf("created blocks %s", blob)
		}
		root := ""
		_ = db.Read().QueryRowContext(ctx, `SELECT ts FROM slack_threads WHERE route_id = ? AND ticket_id = ?`, all.ID, tid).Scan(&root)
		if root == "" {
			t.Fatal("thread root not stored")
		}

		// Updates reply in the thread; staff replies are not selected.
		if err := h.m.handleEvent(ctx, event(t, db, tid, events.TicketStatusChanged, events.StatusChange{From: "new", To: "open"}, "normal")); err != nil {
			t.Fatal(err)
		}
		staff := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
		smid := message(t, db, tid, staff, "public", "On it")
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.MessageCreated, events.MessageRef{MessageID: smid, Kind: "public"}, "normal"))
		cmid := message(t, db, tid, cust, "public", "Still burning")
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.MessageCreated, events.MessageRef{MessageID: cmid, Kind: "public"}, "normal"))
		nmid := message(t, db, tid, staff, "internal", "Call the fire brigade")
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.NoteCreated, events.NoteRef{MessageID: nmid, Kind: "internal"}, "normal"))
		posts = h.slack.all()
		if len(posts) != 4 {
			t.Fatalf("posts after updates = %d: %v", len(posts), posts)
		}
		for _, p := range posts[1:] {
			if p["thread_ts"] != root {
				t.Fatalf("update not threaded: %v", p)
			}
		}
		blob = fmt.Sprint(posts[3])
		if !strings.Contains(blob, "internal note") || !strings.Contains(blob, "Call the fire brigade") {
			t.Fatalf("note post %s", blob)
		}

		// Priority filter: only the urgent route gets an urgent ticket, top-level.
		tid2 := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, Priority: domain.PriorityUrgent})
		_ = h.m.handleEvent(ctx, event(t, db, tid2, events.TicketCreated, events.Created{Channel: "portal"}, "urgent"))
		posts = h.slack.all()
		if len(posts) != 6 || posts[5]["channel"] != "G0URGENT" {
			t.Fatalf("urgent posts %v", posts[4:])
		}
		if h.count(`SELECT COUNT(*) FROM slack_threads WHERE route_id = ?`, urgent.ID) != 0 {
			t.Fatal("route without threads stored a root")
		}

		// Rate limited: one deferred job, nothing posted.
		h.slack.rateLimit.Store(true)
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.TicketStatusChanged, events.StatusChange{From: "open", To: "pending"}, "normal"))
		h.slack.rateLimit.Store(false)
		if n := h.count(`SELECT COUNT(*) FROM deferred_jobs WHERE kind = ?`, JobPost); n != 1 {
			t.Fatalf("deferred jobs = %d", n)
		}
		var payload string
		if err := db.Read().QueryRowContext(ctx, `SELECT payload FROM deferred_jobs WHERE kind = ?`, JobPost).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if err := h.m.handleRetry(ctx, json.RawMessage(payload)); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if n := len(h.slack.all()); n != 7 {
			t.Fatalf("posts after retry = %d", n)
		}

		// A revoked token breaks the connection; delivery stops.
		if err := h.m.markBrokenForTest(ctx); err != nil {
			t.Fatal(err)
		}
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.TicketStatusChanged, events.StatusChange{From: "pending", To: "open"}, "normal"))
		if s := h.status(); s.State != "error" || s.Error == nil || *s.Error != "invalid_auth" {
			t.Fatalf("status after invalid_auth %+v", s)
		}
		n := len(h.slack.all())
		_ = h.m.handleEvent(ctx, event(t, db, tid, events.TicketStatusChanged, events.StatusChange{From: "open", To: "pending"}, "normal"))
		if len(h.slack.all()) != n {
			t.Fatal("posted while the connection is broken")
		}
		// Reconnecting clears the error.
		h.connect()
		if s := h.status(); s.State != "connected" {
			t.Fatalf("after reconnect %+v", s)
		}
	})
}

// markBrokenForTest swaps the stored token for one the fake rejects.
func (m *Module) markBrokenForTest(ctx context.Context) error {
	d, err := m.load(ctx)
	if err != nil {
		return err
	}
	d.BotTokenEnc, err = m.box.SealString(settings.PurposeSlackToken, "xoxb-revoked")
	if err != nil {
		return err
	}
	return m.save(ctx, d, "")
}

func TestRenderTriggers(t *testing.T) {
	mk := func(typ string, changes string, msg string) *envelope {
		var env envelope
		raw := `{"type":"` + typ + `","data":{"ticket":{"ref":"HD-7","subject":"VPN <down>","status":"open","priority":"high","project":{"name":"Help"},
			"requester":{"name":"Ann","kind":"customer"},"assignee":{"name":"Bob","kind":"staff"}},"actor":{"name":"Eve","kind":"staff"},"changes":` + changes + msg + `}}`
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatal(err)
		}
		return &env
	}
	cases := []struct {
		env   *envelope
		trig  string
		reply bool
		want  []string
	}{
		{mk("ticket.created", `{}`, `,"message":{"kind":"public","body_md":"Help me","author":{"name":"Ann","kind":"customer"}}`), TrigCreated, false,
			[]string{"New ticket from *Ann*", "VPN &lt;down&gt;", "> Help me", "Open ticket", ":large_orange_circle: *High*"}},
		{mk("ticket.status_changed", `{"from":"open","to":"resolved"}`, ""), TrigStatus, true, []string{"*Open* → *Resolved* by Eve"}},
		{mk("ticket.assigned", `{"from":"","to":"u1"}`, ""), TrigAssigned, true, []string{"Assigned to *Bob* by Eve"}},
		{mk("ticket.escalated", `{"from_tier":1,"to_tier":2,"reason":"needs L2"}`, ""), TrigEscalated, true, []string{"Escalated *L1* → *L2*", "needs L2"}},
		{mk("ticket.deescalated", `{"from_tier":2,"to_tier":1}`, ""), TrigEscalated, true, []string{"De-escalated *L2* → *L1*"}},
		{mk("message.created", `{}`, `,"message":{"kind":"public","body_md":"ok","author":{"name":"Bob","kind":"staff"}}`), TrigStaff, true, []string{"*Bob* replied to the customer"}},
		{mk("sla.breached", `{"which":"first_response","due_at":"2026-10-23T10:00:00Z"}`, ""), TrigSLABreach, true, []string{"First response SLA *breached*", "<!date^"}},
		{mk("ticket.csat", `{"score":4}`, ""), TrigCSAT, true, []string{"(4/5)", ":star::star::star::star:"}},
	}
	for _, c := range cases {
		if got := c.env.trigger(); got != c.trig {
			t.Errorf("%s: trigger %q, want %q", c.env.Type, got, c.trig)
		}
		msg := render(c.env, c.trig, "https://x/agent/tickets/HD-7", true, c.reply)
		b := fmt.Sprint(msg.Blocks) // unescaped text, unlike JSON
		for _, w := range c.want {
			if !strings.Contains(b, w) {
				t.Errorf("%s: %q not in %s", c.trig, w, b)
			}
		}
		if msg.Text == "" {
			t.Errorf("%s: empty fallback text", c.trig)
		}
		if c.reply && strings.Contains(b, "open_ticket") {
			t.Errorf("%s: thread reply carries the ticket button", c.trig)
		}
	}
	if got := clip(strings.Repeat("word ", 200), 50); len([]rune(got)) > 51 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip = %q", got)
	}
}
