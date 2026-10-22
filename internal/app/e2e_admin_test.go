// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/FeatherDesk/api"
	"github.com/radioBros/FeatherDesk/internal/app"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestAdmin_AuditLogStorageAndDocs: GET /admin/audit-log pages and filters
// the audit trail, GET /admin/storage reports the blob backend, and
// /api/docs is a self-contained page; admin routes stay admin-only.
func TestAdmin_AuditLogStorageAndDocs(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		admin, adminID := s.staff(domain.RoleAdmin, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1)
		admin.must(200, "PATCH", "/api/v1/admin/staff/"+agentID, map[string]any{"name": "Renamed Agent"})
		admin.must(200, "PATCH", "/api/v1/admin/staff/"+agentID, map[string]any{"name": "Renamed Again"})

		page := admin.must(200, "GET", "/api/v1/admin/audit-log?action=auth.staff_changed", nil)
		items := page["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("audit items %v", items)
		}
		first := items[0].(map[string]any)
		if first["action"] != "auth.staff_changed" || first["target_id"] != agentID || first["actor"].(map[string]any)["id"] != adminID {
			t.Fatalf("entry %v", first)
		}
		p1 := admin.must(200, "GET", "/api/v1/admin/audit-log?limit=1&actor_id="+adminID, nil)
		next, _ := p1["next_cursor"].(string)
		if len(p1["items"].([]any)) != 1 || next == "" {
			t.Fatalf("page 1 %v", p1)
		}
		p2 := admin.must(200, "GET", "/api/v1/admin/audit-log?limit=1&actor_id="+adminID+"&cursor="+next, nil)
		if p2["items"].([]any)[0].(map[string]any)["id"] == p1["items"].([]any)[0].(map[string]any)["id"] {
			t.Fatal("cursor did not advance")
		}
		prefix := admin.must(200, "GET", "/api/v1/admin/audit-log?action=auth.", nil)
		for _, it := range prefix["items"].([]any) {
			if !strings.HasPrefix(it.(map[string]any)["action"].(string), "auth.") {
				t.Fatalf("prefix filter leaked %v", it)
			}
		}
		if empty := admin.must(200, "GET", "/api/v1/admin/audit-log?since=2999-01-01T00:00:00Z", nil); len(empty["items"].([]any)) != 0 {
			t.Fatalf("since filter %v", empty)
		}
		admin.must(422, "GET", "/api/v1/admin/audit-log?limit=0", nil)
		admin.must(422, "GET", "/api/v1/admin/audit-log?cursor=!!!", nil)
		agent.must(403, "GET", "/api/v1/admin/audit-log", nil)
		cust := s.loginFixture(storetest.CreateCustomer(t, s.db, ""), domain.KindCustomer)
		cust.must(404, "GET", "/api/v1/admin/audit-log", nil)

		st := admin.must(200, "GET", "/api/v1/admin/storage", nil)
		if st["backend"] != "local" || st["ok"] != true || st["used_bytes"] == nil || st["bucket"] != nil {
			t.Fatalf("storage %v", st)
		}
		agent.must(403, "GET", "/api/v1/admin/storage", nil)

		spec := httptest.NewRecorder()
		s.a.Server.Handler().ServeHTTP(spec, httptest.NewRequest("GET", "/api/v1/openapi.yaml", nil))
		if spec.Code != http.StatusOK || !bytes.Equal(spec.Body.Bytes(), api.Spec) {
			t.Fatalf("/api/v1/openapi.yaml is not the embedded spec: %d", spec.Code)
		}
		rec := httptest.NewRecorder()
		s.a.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/docs", nil))
		body, _ := io.ReadAll(rec.Result().Body)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") ||
			!strings.Contains(string(body), "listAuditLog") || !strings.Contains(string(body), "/api/v1/openapi.yaml") ||
			strings.Contains(string(body), "<script") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("/api/docs: %d %q %s", rec.Code, rec.Header(), tail(string(body), 300))
		}
	})
}

// sortRows writes 7 rows through insert and returns their ids oldest first.
// Two pairs share a timestamp (one pair straddles a limit-2 page boundary
// ascending, the other descending), and the 4th row has the newest id but a
// middle time, so only a (time, id) order lists them as returned.
func sortRows(t *testing.T, insert func(id string, at time.Time)) []string {
	t.Helper()
	ids := make([]string, 7)
	for i := range ids {
		ids[i] = store.NewID()
	}
	slices.Sort(ids)
	order := []string{ids[0], ids[1], ids[2], ids[6], ids[3], ids[4], ids[5]}
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i, m := range []int{0, 1, 1, 2, 3, 3, 4} {
		insert(order[i], base.Add(time.Duration(m)*time.Minute))
	}
	return order
}

// TestAdmin_AuditLogSort: sort=created_at pages the audit log oldest first and
// the default (or -created_at) newest first, both by (created_at, id) and with
// the filters applied; the default cursor stays base64url(id), and a cursor
// only works in the direction that issued it.
func TestAdmin_AuditLogSort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		admin, _ := s.staff(domain.RoleAdmin, 3)
		ctx := context.Background()
		insert := func(id, action string, at time.Time) {
			if err := s.db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, NULL, ?, 'test', '', '{}', '', ?)`, id, action, at)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		want := sortRows(t, func(id string, at time.Time) { insert(id, "sorttest.row", at) })
		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		insert(store.NewID(), "sorttest.other", base.Add(90*time.Second)) // filtered out
		rev := slices.Clone(want)
		slices.Reverse(rev)

		const path = "/api/v1/admin/audit-log?limit=2&action=sorttest.row"
		pageAll := func(query string) (got, cursors []string) {
			t.Helper()
			cursor := ""
			for range 10 {
				p := admin.must(200, "GET", path+query+cursor, nil)
				for _, it := range p["items"].([]any) {
					got = append(got, it.(map[string]any)["id"].(string))
				}
				next, _ := p["next_cursor"].(string)
				if next == "" {
					return got, cursors
				}
				cursors = append(cursors, next)
				cursor = "&cursor=" + next
			}
			t.Fatalf("%s: no last page", query)
			return nil, nil
		}

		asc, ascCursors := pageAll("&sort=created_at")
		if !slices.Equal(asc, want) {
			t.Fatalf("ascending\n got %v\nwant %v", asc, want)
		}
		desc, descCursors := pageAll("")
		if !slices.Equal(desc, rev) {
			t.Fatalf("default\n got %v\nwant %v", desc, rev)
		}
		for i, c := range descCursors {
			if c != base64.RawURLEncoding.EncodeToString([]byte(desc[2*i+1])) {
				t.Fatalf("default cursor %d = %q, want base64url(id)", i, c)
			}
		}
		if explicit, _ := pageAll("&sort=-created_at"); !slices.Equal(explicit, rev) {
			t.Fatalf("-created_at\n got %v\nwant %v", explicit, rev)
		}
		if since, _ := pageAll("&sort=created_at&since=" + base.Add(2*time.Minute).Format(time.RFC3339)); !slices.Equal(since, want[3:]) {
			t.Fatalf("since ascending\n got %v\nwant %v", since, want[3:])
		}

		code, body := admin.do("GET", path+"&sort=id", nil)
		if code != 422 || !strings.Contains(string(body), `"sort"`) {
			t.Fatalf("bad sort: %d %s", code, body)
		}
		admin.must(422, "GET", path+"&cursor="+ascCursors[0], nil)
		admin.must(422, "GET", path+"&sort=-created_at&cursor="+ascCursors[0], nil)
		admin.must(422, "GET", path+"&sort=created_at&cursor="+descCursors[0], nil)
		admin.must(422, "GET", path+"&cursor="+base64.RawURLEncoding.EncodeToString([]byte(store.NewID())), nil)
	})
}

// TestStaffPatch_RevalidatesM4: PATCH /admin/staff/{id} runs the M-4
// re-validation (TKT-017) through directory.MembershipChanged in the full
// app: lowering the tier unassigns tickets above it, leaving a team unassigns
// that team's tickets, disabling unassigns everything. Each unassignment
// writes a ticket.assigned event.
func TestStaffPatch_RevalidatesM4(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		admin, _ := s.staff(domain.RoleAdmin, 3)
		teamA, teamB := storetest.CreateTeam(t, s.db, "A"), storetest.CreateTeam(t, s.db, "B")
		_, agentID := s.staff(domain.RoleAgent, 2, teamA, teamB)
		cust := storetest.CreateCustomer(t, s.db, "")
		mk := func(team string, tier int) string {
			return storetest.CreateTicket(t, s.db, storetest.Ticket{Requester: cust, TeamID: team, Tier: tier, AssigneeID: agentID, Status: domain.StatusOpen})
		}
		a1, a2, b1 := mk(teamA, 1), mk(teamA, 2), mk(teamB, 1)
		assignee := func(id string) string {
			var a *string
			if err := s.db.Read().QueryRowContext(t.Context(), `SELECT assignee_id FROM tickets WHERE id = ?`, id).Scan(&a); err != nil {
				t.Fatal(err)
			}
			if a == nil {
				return ""
			}
			return *a
		}
		events := func(id string) int {
			var n int
			_ = s.db.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.assigned'`, id).Scan(&n)
			return n
		}

		admin.must(200, "PATCH", "/api/v1/admin/staff/"+agentID, map[string]any{"tier": 1})
		if assignee(a2) != "" || events(a2) != 1 || assignee(a1) != agentID || assignee(b1) != agentID {
			t.Fatalf("tier 2→1: a1=%q a2=%q b1=%q", assignee(a1), assignee(a2), assignee(b1))
		}
		admin.must(200, "PATCH", "/api/v1/admin/staff/"+agentID, map[string]any{"team_ids": []string{teamA}})
		if assignee(b1) != "" || events(b1) != 1 || assignee(a1) != agentID {
			t.Fatalf("left team B: a1=%q b1=%q", assignee(a1), assignee(b1))
		}
		admin.must(200, "PATCH", "/api/v1/admin/staff/"+agentID, map[string]any{"status": "disabled"})
		if assignee(a1) != "" || events(a1) != 1 {
			t.Fatalf("disabled: a1=%q", assignee(a1))
		}
	})
}

// TestWS_AdminSystemTopic: admins may subscribe to admin:system and get a
// queue.changed event once the queue counters move; other staff are denied.
func TestWS_AdminSystemTopic(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		defer app.SetAdminSystemEvery(time.Second)()
		sink := newSink(t)
		team := storetest.CreateTeam(t, db, "Support")
		category := storetest.CreateCategory(t, db, "General", team)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		s.run()
		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, _ := s.staff(domain.RoleAgent, 1, team)
		aw := s.ws(admin)
		aw.subscribe("s1", "admin:system")

		gw := s.ws(agent)
		b, _ := json.Marshal(map[string]any{"op": "subscribe", "id": "g1", "topics": []string{"admin:system"}})
		if err := gw.c.Write(t.Context(), websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
		ack := gw.waitFor(3*time.Second, func(f map[string]any) bool { return f["type"] == "ack" && f["id"] == "g1" })
		if d, _ := ack["denied"].([]any); len(d) != 1 || d[0] != "admin:system" {
			t.Fatalf("agent ack %v", ack)
		}

		cust := s.loginFixture(storetest.CreateCustomer(t, s.db, ""), domain.KindCustomer)
		cust.must(201, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "Queue moves", "body_md": "x", "category_id": category}, "Idempotency-Key", "adm-1")
		if f := aw.waitFor(5*time.Second, func(f map[string]any) bool { return f["topic"] == "admin:system" && f["event"] == "queue.changed" }); f == nil {
			t.Fatalf("no queue.changed for the admin: %s", aw.dump())
		}
		if f := gw.waitFor(200*time.Millisecond, func(f map[string]any) bool { return f["topic"] == "admin:system" }); f != nil {
			t.Fatalf("agent received %v", f)
		}
	})
}

// TestTeamsChanged_InvalidatesAuthnCache: modules.go wires the directory's
// OnTeamsChanged to authn.InvalidateTeams (AUTH-002), so a membership change
// through the team API applies to the member's very next request (ticket
// visibility uses the principal's cached teams), inside the 30 s window.
func TestTeamsChanged_InvalidatesAuthnCache(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		admin, _ := s.staff(domain.RoleAdmin, 3)
		agent, agentID := s.staff(domain.RoleAgent, 1)
		team := admin.must(201, "POST", "/api/v1/admin/teams", map[string]any{"name": "Night shift"})["id"].(string)
		tk := storetest.CreateTicket(t, s.db, storetest.Ticket{Requester: storetest.CreateCustomer(t, s.db, ""), TeamID: team, Tier: 1, Status: domain.StatusOpen})
		path := "/api/v1/agent/tickets/" + tk
		agent.must(404, "GET", path, nil) // warms the authn team cache (no teams)
		admin.must(201, "POST", "/api/v1/admin/teams/"+team+"/members", map[string]any{"user_id": agentID})
		agent.must(200, "GET", path, nil)
		if code, _ := admin.do("DELETE", "/api/v1/admin/teams/"+team+"/members/"+agentID, nil); code != 204 {
			t.Fatalf("remove member: %d", code)
		}
		agent.must(404, "GET", path, nil)
	})
}
