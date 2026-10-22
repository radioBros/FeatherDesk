// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/notify"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// I-12: tickets:act_as_customer, POST /agent/tickets/{id}/customer-messages.

type obFix struct {
	e       *env
	org     string
	team    string
	agent   *domain.Principal // the assignee
	admin   *domain.Principal // the token owner (session)
	integ   *domain.Principal // the integration token
	tokenID string
	cust    *domain.Principal
}

// integration inserts a real api_tokens row for owner (via_token_id is a
// foreign key) and registers its token principal.
func (e *env) integration(owner *domain.Principal, name string, scopes ...string) *domain.Principal {
	e.t.Helper()
	id := store.NewID()
	e.exec(`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, owner.UserID, name, "abcd1234", "hash-"+id, store.MustJSON(scopes), store.Now())
	cp := *owner
	cp.SessionID, cp.TokenID, cp.Scopes = "", id, scopes
	e.mu.Lock()
	e.users["pat-"+owner.UserID] = &cp
	e.mu.Unlock()
	return &cp
}

func newOB(t *testing.T, db *store.DB) *obFix {
	e := newEnv(t, db)
	f := &obFix{e: e, org: storetest.CreateOrg(t, db, "")}
	f.team = storetest.CreateTeam(t, db, "Support")
	f.agent = e.staff(domain.RoleAgent, 1, f.team)
	f.admin = e.staff(domain.RoleAdmin, 3)
	f.cust = e.customer(f.org, domain.RoleRequester)
	f.integ = f.e.integration(f.admin, "Verbalizza", domain.ScopeActAsCustomer)
	f.tokenID = f.integ.TokenID
	return f
}

func (f *obFix) ticket(status domain.TicketStatus) string {
	return storetest.CreateTicket(f.e.t, f.e.db, storetest.Ticket{Requester: f.cust.UserID, OrgID: f.org, TeamID: f.team, AssigneeID: f.agent.UserID, Status: status})
}

func obPath(id string) string { return "/api/v1/agent/tickets/" + id + "/customer-messages" }

func (f *obFix) reply(id string, body map[string]any, hdr ...string) resp {
	if body["on_behalf_of"] == nil {
		body["on_behalf_of"] = f.cust.UserID
	}
	if body["body_md"] == nil {
		body["body_md"] = "Still broken."
	}
	return f.e.do(f.integ, "POST", obPath(id), body, hdr...)
}

func TestOnBehalf_ReplyAuthoredByCustomer(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		// The integration's deployment: the customer portal is switched off.
		setSupport(t, f.e, func(s *settings.Support) { s.PortalEnabled = false })
		id := f.ticket(domain.StatusPending)
		var out struct {
			Message struct {
				ID, TicketID, Kind, Channel string
				Author                      struct{ ID, Kind string }
				ViaIntegration              bool    `json:"via_integration"`
				ViaName                     *string `json:"via_integration_name"`
			}
			TicketID       string `json:"ticket_id"`
			Ref, Status    string
			FollowupTicket any `json:"followup_ticket"`
		}
		f.reply(id, map[string]any{}).expect(t, 201).json(t, &out)
		m := out.Message
		if m.Author.ID != f.cust.UserID || m.Author.Kind != "customer" || !m.ViaIntegration || m.ViaName == nil || *m.ViaName != "Verbalizza" ||
			m.Kind != "public" || m.Channel != "api" || out.TicketID != id || out.Status != "open" || out.Ref == "" || out.FollowupTicket != nil {
			t.Fatalf("result %+v", out)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE id = ? AND author_id = ? AND via_token_id = ?`, m.ID, f.cust.UserID, f.tokenID); n != 1 {
			t.Fatal("stored message: author or via_token_id")
		}
		// Staff thread shows "via"; the customer view never does.
		d := f.e.do(f.admin, "GET", "/api/v1/agent/tickets/"+id, nil).expect(t, 200).m(t)
		last := d["messages"].([]any)[len(d["messages"].([]any))-1].(map[string]any)
		if last["via_integration"] != true || last["via_integration_name"] != "Verbalizza" {
			t.Fatalf("staff thread: %v", last)
		}
		cb := f.e.do(f.cust, "GET", "/api/v1/portal/tickets/"+id, nil).expect(t, 200)
		if s := string(cb.body); containsAny(s, "via_integration", "Verbalizza") {
			t.Fatalf("customer view shows via: %s", s)
		}
		// Audit row: actor = the token owner.
		var actor, data string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT actor_id, data FROM audit_log WHERE action = ?`, AuditOnBehalf).Scan(&actor, &data); err != nil {
			t.Fatal(err)
		}
		var ad map[string]any
		_ = json.Unmarshal([]byte(data), &ad)
		if actor != f.admin.UserID || ad["on_behalf_of"] != f.cust.UserID || ad["ticket_id"] != id || ad["message_id"] != m.ID || ad["token_id"] != f.tokenID {
			t.Fatalf("audit actor %s data %v", actor, ad)
		}
		// The message.created event: actor = the customer, via_integration.
		evs, _ := events.ForTicket(context.Background(), db.Read(), id)
		var mc *events.Event
		for i := range evs {
			if evs[i].Type == events.MessageCreated {
				mc = &evs[i]
			}
		}
		var ref events.MessageRef
		if mc == nil || mc.ActorID != f.cust.UserID || mc.Decode(&ref) != nil || !ref.ViaIntegration {
			t.Fatalf("message.created %+v", mc)
		}
	})
}

func containsAny(s string, subs ...string) bool {
	return slices.ContainsFunc(subs, func(x string) bool { return strings.Contains(s, x) })
}

func TestOnBehalf_TargetChecks(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusOpen)
		slug := func(r resp) string { return fmt.Sprint(r.m(t)["type"]) }
		// Wrong customer (another organization) → 403.
		other := f.e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		if r := f.reply(id, map[string]any{"on_behalf_of": other.UserID}).expect(t, 403); !containsAny(slug(r), "on-behalf-not-visible") {
			t.Fatalf("wrong customer: %s", r.body)
		}
		// A colleague who is org_manager may (I-3); a plain colleague may not.
		mgr := f.e.customer(f.org, domain.RoleOrgManager)
		f.reply(id, map[string]any{"on_behalf_of": mgr.UserID}).expect(t, 201)
		peer := f.e.customer(f.org, domain.RoleRequester)
		f.reply(id, map[string]any{"on_behalf_of": peer.UserID}).expect(t, 403)
		// Staff target → 403 on-behalf-not-customer.
		if r := f.reply(id, map[string]any{"on_behalf_of": f.agent.UserID}).expect(t, 403); !containsAny(slug(r), "on-behalf-not-customer") {
			t.Fatalf("staff target: %s", r.body)
		}
		// Unknown customer, unknown ticket → 404.
		f.reply(id, map[string]any{"on_behalf_of": store.NewID()}).expect(t, 404)
		f.reply(store.NewID(), map[string]any{}).expect(t, 404)
		// Disabled customer → 409.
		f.e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, peer.UserID)
		dis := storetest.CreateTicket(t, db, storetest.Ticket{Requester: peer.UserID, OrgID: f.org, TeamID: f.team})
		if r := f.reply(dis, map[string]any{"on_behalf_of": peer.UserID}).expect(t, 409); !containsAny(slug(r), "on-behalf-disabled") {
			t.Fatalf("disabled: %s", r.body)
		}
		// Internal kind, other set_status → 422; no internal note was written.
		f.reply(id, map[string]any{"kind": "internal"}).expect(t, 422)
		f.reply(id, map[string]any{"set_status": "closed"}).expect(t, 422)
		f.reply(id, map[string]any{"kind": "public"}).expect(t, 201)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND kind <> 'public'`, id); n != 0 {
			t.Fatalf("%d non-public messages", n)
		}
		// Attachments: only the token owner's drafts.
		mine, theirs := store.NewID(), store.NewID()
		for _, a := range [][2]string{{mine, f.admin.UserID}, {theirs, f.agent.UserID}} {
			f.e.exec(`INSERT INTO attachments (id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at) VALUES (?, ?, 'a.txt', 'text/plain', 1, 'x', ?, ?)`,
				a[0], a[1], "k/"+a[0], store.Now())
		}
		f.reply(id, map[string]any{"attachment_ids": []string{theirs}}).expect(t, 422)
		r := f.reply(id, map[string]any{"attachment_ids": []string{mine}}).expect(t, 201).m(t)
		if atts := r["message"].(map[string]any)["attachments"].([]any); len(atts) != 1 {
			t.Fatalf("attachments %v", atts)
		}
	})
}

func TestOnBehalf_ScopeRequired(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusOpen)
		body := map[string]any{"on_behalf_of": f.cust.UserID, "body_md": "x"}
		// The admin's session, a tickets:write token and an admin-scope token
		// never act as a customer: the scope must be explicit.
		f.e.do(f.admin, "POST", obPath(id), body).expect(t, 403)
		f.e.integration(f.admin, "writer", domain.ScopeTicketsWrite)
		f.e.do(f.e.users["pat-"+f.admin.UserID], "POST", obPath(id), body).expect(t, 403)
		adm := f.e.integration(f.admin, "admin", domain.ScopeAdmin)
		f.e.do(adm, "POST", obPath(id), body).expect(t, 403)
		if adm.HasScope(domain.ScopeActAsCustomer) || f.admin.HasScope(domain.ScopeActAsCustomer) {
			t.Fatal("admin scope or session implies act_as_customer")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ?`, id); n != 0 {
			t.Fatalf("%d messages written", n)
		}
	})
}

// The on-behalf path and the portal path produce the same status, events,
// follow-ups and notification recipients.
func TestOnBehalf_SameAsPortalReply(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		box, err := crypto.NewBox(f.e.cfg.SecretKey, f.e.cfg.SecretKeyPrev)
		if err != nil {
			t.Fatal(err)
		}
		nm, err := notify.New(notify.Deps{DB: db, Settings: f.e.set, Secrets: box, Cfg: f.e.cfg, Getenv: func(string) string { return "" }})
		if err != nil {
			t.Fatal(err)
		}
		now := store.Now()
		cases := []struct {
			name     string
			status   domain.TicketStatus
			resolved time.Time
			want     domain.TicketStatus
			followup bool
		}{
			{"pending", domain.StatusPending, time.Time{}, domain.StatusOpen, false},
			{"resolved_inside_window", domain.StatusResolved, now.Add(-24 * time.Hour), domain.StatusOpen, false},
			{"resolved_after_window", domain.StatusResolved, now.Add(-8 * 24 * time.Hour), domain.StatusResolved, true},
			{"closed", domain.StatusClosed, now.Add(-30 * 24 * time.Hour), domain.StatusClosed, true},
		}
		type outcome struct {
			status   domain.TicketStatus
			followup string
			events   []string
			actors   []string
			author   string
			targets  []string
		}
		observe := func(id, followup string) outcome {
			o := outcome{followup: followup}
			if err := db.Read().QueryRowContext(context.Background(), `SELECT status FROM tickets WHERE id = ?`, id).Scan(&o.status); err != nil {
				t.Fatal(err)
			}
			msgTicket := id
			if followup != "" {
				msgTicket = followup
			}
			for _, tid := range []string{id, followup} {
				if tid == "" {
					continue
				}
				evs, _ := events.ForTicket(context.Background(), db.Read(), tid)
				for _, ev := range evs {
					o.events = append(o.events, ev.Type)
					o.actors = append(o.actors, ev.ActorID)
					if ev.Type == events.MessageCreated || (ev.Type == events.TicketCreated && followup != "") {
						ts, err := nm.Recipients(context.Background(), ev)
						if err != nil {
							t.Fatal(err)
						}
						for _, x := range ts {
							o.targets = append(o.targets, x.Key+"|"+x.R.UserID)
						}
					}
				}
			}
			_ = db.Read().QueryRowContext(context.Background(), `SELECT author_id FROM ticket_messages WHERE ticket_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, msgTicket).Scan(&o.author)
			slices.Sort(o.targets)
			return o
		}
		for _, c := range cases {
			mk := func() string {
				id := f.ticket(c.status)
				if !c.resolved.IsZero() {
					f.e.exec(`UPDATE tickets SET resolved_at = ? WHERE id = ?`, c.resolved, id)
				}
				return id
			}
			portalID, obID := mk(), mk()
			var portalFollowup string
			r := f.e.do(f.cust, "POST", "/api/v1/portal/tickets/"+portalID+"/messages", map[string]any{"body_md": "Again."})
			if c.followup {
				portalFollowup = r.expect(t, 409).m(t)["detail"].(string)
			} else {
				r.expect(t, 201)
			}
			var res struct {
				Status         string
				FollowupTicket *struct{ ID string } `json:"followup_ticket"`
				Message        struct {
					TicketID string `json:"ticket_id"`
				}
			}
			f.reply(obID, map[string]any{"body_md": "Again."}).expect(t, 201).json(t, &res)
			obFollowup := ""
			if res.FollowupTicket != nil {
				obFollowup = res.FollowupTicket.ID
				if res.Message.TicketID != obFollowup {
					t.Fatalf("%s: message on %s, follow-up %s", c.name, res.Message.TicketID, obFollowup)
				}
			}
			p, o := observe(portalID, portalFollowup), observe(obID, obFollowup)
			if p.status != c.want || o.status != p.status || (o.followup != "") != c.followup || (p.followup != "") != c.followup {
				t.Fatalf("%s: portal %s/%q on-behalf %s/%q", c.name, p.status, p.followup, o.status, o.followup)
			}
			if res.Status != CustomerStatus(c.want) {
				t.Fatalf("%s: answer status %s", c.name, res.Status)
			}
			if !slices.Equal(p.events, o.events) || p.author != f.cust.UserID || o.author != f.cust.UserID {
				t.Fatalf("%s: events %v vs %v, authors %s %s", c.name, p.events, o.events, p.author, o.author)
			}
			for i := range p.actors {
				if p.actors[i] != o.actors[i] {
					t.Fatalf("%s: actor of %s: %s vs %s", c.name, p.events[i], p.actors[i], o.actors[i])
				}
			}
			if len(p.targets) == 0 || !slices.Equal(p.targets, o.targets) {
				t.Fatalf("%s: recipients %v vs %v", c.name, p.targets, o.targets)
			}
		}
	})
}

func TestOnBehalf_SetStatusResolved(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusPending)
		r := f.reply(id, map[string]any{"body_md": "Works now, thanks.", "set_status": "resolved"}).expect(t, 201).m(t)
		if r["status"] != "resolved" || f.e.ticketField(id, "status") != "resolved" || f.e.ticketField(id, "resolved_at") == "" {
			t.Fatalf("status %v / %s", r["status"], f.e.ticketField(id, "status"))
		}
	})
}

// First-response SLA: a customer message never counts as the first response.
func TestOnBehalf_FirstResponseUntouched(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusNew)
		due := store.Now().Add(4 * time.Hour)
		f.e.exec(`UPDATE tickets SET first_response_due_at = ?, last_customer_activity_at = ? WHERE id = ?`, due, store.Now().Add(-time.Hour), id)
		before := f.e.ticketField(id, "last_customer_activity_at")
		dueBefore := f.e.ticketField(id, "first_response_due_at")
		f.reply(id, map[string]any{}).expect(t, 201)
		if f.e.ticketField(id, "first_responded_at") != "" || f.e.ticketField(id, "last_staff_activity_at") != "" {
			t.Fatal("on-behalf message counted as a staff response")
		}
		if f.e.ticketField(id, "first_response_due_at") != dueBefore || f.e.ticketField(id, "last_customer_activity_at") == before {
			t.Fatal("customer activity not recorded or first-response due moved")
		}
		if f.e.ticketField(id, "status") != "new" {
			t.Fatalf("status %s", f.e.ticketField(id, "status"))
		}
	})
}

func TestOnBehalf_IdempotentRetry(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusOpen)
		a := f.reply(id, map[string]any{"body_md": "once"}, "Idempotency-Key", "ob-1").expect(t, 201)
		b := f.reply(id, map[string]any{"body_md": "once"}, "Idempotency-Key", "ob-1").expect(t, 201)
		if string(a.body) != string(b.body) {
			t.Fatalf("replay differs:\n%s\n%s", a.body, b.body)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ?`, id); n != 1 {
			t.Fatalf("%d messages", n)
		}
		if n := f.e.count(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, AuditOnBehalf); n != 1 {
			t.Fatalf("%d audit rows", n)
		}
	})
}

func TestOnBehalf_RateLimited(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		id := f.ticket(domain.StatusOpen)
		for range onBehalfBurst {
			_ = f.e.srv.AllowKey("on_behalf", "t:"+f.tokenID, onBehalfPerSecond, onBehalfBurst)
		}
		r := f.reply(id, map[string]any{}, "Idempotency-Key", "rl-1").expect(t, 429)
		if r.Header.Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
		// The 429 is not stored under the key: a later retry with it succeeds.
		if n := f.e.count(`SELECT COUNT(*) FROM idempotency_keys WHERE key = ?`, "rl-1"); n != 0 {
			t.Fatal("429 stored as the idempotent answer")
		}
		// Creates under the scope share the per-token limit.
		f.e.do(f.integ, "POST", "/api/v1/agent/tickets", map[string]any{"subject": "x", "body_md": "y", "requester_id": f.cust.UserID}).expect(t, 429)
	})
}

// Create under the scope: the first message is the customer's, posted via
// the integration, channel api; the answer is the minimal result.
func TestOnBehalf_CreateFirstMessageIsCustomers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newOB(t, db)
		body := map[string]any{"subject": "Printer offline", "body_md": "It does not print.", "requester_id": f.cust.UserID}
		r := f.e.do(f.integ, "POST", "/api/v1/agent/tickets", body).expect(t, 201).m(t)
		msg := r["message"].(map[string]any)
		if msg["author"].(map[string]any)["id"] != f.cust.UserID || msg["via_integration"] != true || msg["channel"] != "api" || r["status"] != "open" || r["ref"] == "" {
			t.Fatalf("create result %v", r)
		}
		if _, leaked := r["allowed_actions"]; leaked {
			t.Fatal("staff ticket returned under the scope")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, AuditOnBehalf); n != 1 {
			t.Fatalf("%d audit rows", n)
		}
		// Staff-only inputs need tickets:write as well.
		body["team_id"] = f.team
		f.e.do(f.integ, "POST", "/api/v1/agent/tickets", body).expect(t, 403)
		// Without the scope (tickets:write) the first message stays the
		// owner's, as before.
		w := f.e.integration(f.admin, "writer", domain.ScopeTicketsWrite)
		delete(body, "team_id")
		tk := f.e.do(w, "POST", "/api/v1/agent/tickets", body).expect(t, 201).m(t)
		if n := f.e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND author_id = ? AND via_token_id IS NULL`, tk["id"], f.admin.UserID); n != 1 {
			t.Fatal("tickets:write create changed its first message")
		}
	})
}
