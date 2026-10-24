// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/mdrender"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func fieldCode(t *testing.T, p map[string]any, field string) string {
	t.Helper()
	for _, e := range p["errors"].([]any) {
		m := e.(map[string]any)
		if m["field"] == field {
			return fmt.Sprint(m["code"])
		}
	}
	t.Fatalf("no error on %s in %v", field, p["errors"])
	return ""
}

func TestMessageBodyHTML_AgentReply(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		html := "<p>Hello <strong>world</strong></p>"
		r := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_html": html}).expect(t, 201).m(t)
		wantMD := "Hello **world**"
		if r["body_md"] != wantMD {
			t.Fatalf("body_md %q want %q", r["body_md"], wantMD)
		}
		if r["body_html"] != mdrender.Render(wantMD) {
			t.Fatalf("body_html mismatch")
		}
	})
}

func TestMessageBodyHTML_PortalCreateAndReply(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		cr := e.do(cust, "POST", "/api/v1/portal/tickets", map[string]any{
			"subject": "Help", "body_html": "<p>Printer <em>offline</em></p>",
		}).expect(t, 201).m(t)
		id := cr["id"].(string)
		msgs := cr["messages"].([]any)
		if len(msgs) != 1 {
			t.Fatalf("messages %d", len(msgs))
		}
		m0 := msgs[0].(map[string]any)
		if m0["body_md"] != "Printer _offline_" {
			t.Fatalf("create body_md %q", m0["body_md"])
		}
		if m0["body_html"] == "" {
			t.Fatal("missing body_html on create")
		}
		rep := e.do(cust, "POST", "/api/v1/portal/tickets/"+id+"/messages", map[string]any{
			"body_html": "<p>Still broken</p>",
		}).expect(t, 201).m(t)
		if rep["body_md"] != "Still broken" {
			t.Fatalf("reply body_md %q", rep["body_md"])
		}
		if rep["body_html"] != mdrender.Render("Still broken") {
			t.Fatal("reply body_html mismatch")
		}
	})
}

func TestMessageBodyHTML_EditNoteAndOnBehalf(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		note := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "internal", "body_md": "draft"}).expect(t, 201).m(t)
		l.e.do(l.agent, "PATCH", l.path("/messages/"+note["id"].(string)), map[string]any{
			"body_html": "<p>Updated <strong>note</strong></p>",
		}).expect(t, 200)
		got := l.e.do(l.agent, "GET", l.path(""), nil).expect(t, 200).m(t)
		var found map[string]any
		for _, m := range got["messages"].([]any) {
			msg := m.(map[string]any)
			if msg["id"] == note["id"] {
				found = msg
				break
			}
		}
		if found["body_md"] != "Updated **note**" {
			t.Fatalf("edited body_md %v", found["body_md"])
		}

		f := newOB(t, db)
		tid := f.ticket("")
		ob := f.e.do(f.integ, "POST", obPath(tid), map[string]any{
			"on_behalf_of": f.cust.UserID,
			"body_html":    "<p>Thanks!</p>",
		}).expect(t, 201).m(t)
		msg := ob["message"].(map[string]any)
		if msg["body_md"] != "Thanks!" {
			t.Fatalf("on-behalf body_md %q", msg["body_md"])
		}
		if msg["body_html"] != mdrender.Render("Thanks!") {
			t.Fatal("on-behalf body_html mismatch")
		}
	})
}

func TestMessageBodyHTML_Validation(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		p := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{
			"kind": "public", "body_md": "a", "body_html": "<p>b</p>",
		}).expect(t, 422).problem(t)
		if fieldCode(t, p, "body_html") != "not_allowed" {
			t.Fatalf("both fields: %v", p["errors"])
		}
		p = l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public"}).expect(t, 422).problem(t)
		if fieldCode(t, p, "body_md") != "required" {
			t.Fatalf("neither field: %v", p["errors"])
		}
		big := strings.Repeat("x", MaxHTMLBytes+1)
		p = l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{"kind": "public", "body_html": big}).expect(t, 422).problem(t)
		if fieldCode(t, p, "body_html") != "max_length" {
			t.Fatalf("oversize html: %v", p["errors"])
		}
	})
}

func TestMessageBodyHTML_StripsActiveContent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		r := l.e.do(l.agent, "POST", l.path("/messages"), map[string]any{
			"kind":      "public",
			"body_html": "<script>alert(1)</script><p>ok</p><style>.x{}</style><a href=\"javascript:alert(1)\">x</a>",
		}).expect(t, 201).m(t)
		md := r["body_md"].(string)
		if strings.Contains(md, "script") || strings.Contains(md, "javascript:") {
			t.Fatalf("stored md leaked active content: %q", md)
		}
		if !strings.Contains(md, "ok") {
			t.Fatalf("body stripped too much: %q", md)
		}
	})
}

func TestMessageBodyHTML_WebSocketPayload(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnvHub(t, db, 1, realtime.Options{})
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		cat := storetest.CreateCategory(t, db, "", team)
		tid := e.createTicket(cust, CreateInput{CategoryID: &cat, Subject: "WS"})
		path := func(s string) string { return "/api/v1/agent/tickets/" + tid + s }
		c := e.wsDial(agent)
		c.subscribe("s", "ticket:"+tid)
		e.do(agent, "POST", path("/messages"), map[string]any{"kind": "public", "body_html": "<p>live</p>"}).expect(t, 201)
		var cursor string
		e.dispatch(&cursor)
		f := c.waitFor(3*time.Second, func(f map[string]any) bool {
			if f["type"] != "event" || f["event"] != "message.created" {
				return false
			}
			data, _ := f["data"].(map[string]any)
			msg, _ := data["message"].(map[string]any)
			return msg != nil && msg["body_html"] != nil && msg["body_html"] != ""
		})
		if f == nil {
			t.Fatal("no message.created with body_html")
		}
		data := f["data"].(map[string]any)
		msg := data["message"].(map[string]any)
		if msg["body_md"] != "live" {
			t.Fatalf("ws body_md %v", msg["body_md"])
		}
	})
}
