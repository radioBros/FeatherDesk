// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// automate runs the automation consumer over every event after *cursor the
// way the queue does (filter, then handler) until nothing new is written. A
// rule loop would never settle and fails the test.
func (e *env) automate(cursor *string) {
	e.t.Helper()
	ctx := context.Background()
	for round := 0; round < 8; round++ {
		evs, err := events.ReadAfter(ctx, e.db.Read(), *cursor, 10000)
		if err != nil {
			e.t.Fatal(err)
		}
		if len(evs) == 0 {
			return
		}
		for _, ev := range evs {
			*cursor = ev.ID
			if triggerOf(ev) == "" {
				continue
			}
			if err := e.svc.HandleAutomationEvent(ctx, ev); err != nil {
				e.t.Fatalf("handle %s: %v", ev.Type, err)
			}
		}
	}
	e.t.Fatal("automation did not settle: a rule loop")
}

// rule creates a rule through the API as p.
func (e *env) rule(p *domain.Principal, body map[string]any) string {
	e.t.Helper()
	return e.do(p, "POST", "/api/v1/admin/automation/rules", body).expect(e.t, 201).m(e.t)["id"].(string)
}

func (e *env) tagsOf(id string) []string {
	e.t.Helper()
	tags, err := ticketTags(context.Background(), e.db.Read(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return tags
}

// applied returns the data of every ticket.automation_applied event.
func (e *env) applied(id string) []events.AutomationApplied {
	e.t.Helper()
	evs, err := events.ForTicket(context.Background(), e.db.Read(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []events.AutomationApplied
	for _, ev := range evs {
		if ev.Type == events.TicketAutomationApplied {
			if ev.ActorID != events.ActorAutomation {
				e.t.Fatalf("automation_applied by %q", ev.ActorID)
			}
			var d events.AutomationApplied
			_ = ev.Decode(&d)
			out = append(out, d)
		}
	}
	return out
}

func (e *env) lastEventID() string {
	e.t.Helper()
	var id *string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT MAX(id) FROM ticket_events`).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	if id == nil {
		return ""
	}
	return *id
}

func (e *env) ticketField(id, col string) string {
	e.t.Helper()
	var v *string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT `+col+` FROM tickets WHERE id = ?`, id).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// TestAutomation_ConditionsFromStore: the facts a ticket.created rule sees
// (subject/body regex, channel, priority, team, category, organization,
// requester domain, tags, custom field, business hours) are read from the
// store; one differing fact makes an ALL rule miss.
func TestAutomation_ConditionsFromStore(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		org := storetest.CreateOrg(t, db, "")
		cust := e.customer(org, domain.RoleRequester)
		var email string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, cust.UserID).Scan(&email); err != nil {
			t.Fatal(err)
		}
		domainPart := email[strings.LastIndexByte(email, '@')+1:]
		cfCreate(t, e, admin, map[string]any{"key": "plan", "label": map[string]string{"en": "Plan"}, "type": "text"})
		conds := []map[string]any{
			{"field": "subject", "op": "regex", "value": `^invoice #\d+`},
			{"field": "body", "op": "contains", "value": "REFUND"},
			{"field": "channel", "op": "is", "value": "agent"},
			{"field": "priority", "op": "is", "value": []string{"high", "urgent"}},
			{"field": "status", "op": "is_not", "value": "closed"},
			{"field": "tier", "op": "lte", "value": 1},
			{"field": "team_id", "op": "is", "value": team},
			{"field": "category_id", "op": "is", "value": cat},
			{"field": "organization_id", "op": "is", "value": org},
			{"field": "requester_domain", "op": "is", "value": domainPart},
			{"field": "tag", "op": "present", "value": "billing"},
			{"field": "custom_field", "op": "equals", "key": "plan", "value": "gold"},
			{"field": "business_hours", "op": "is", "value": true},
		}
		e.rule(admin, map[string]any{"name": "all facts", "event": "ticket.created", "conditions": conds,
			"actions": []map[string]any{{"type": "add_tag", "value": "matched"}}})
		cur := e.lastEventID()
		agent := e.staff(domain.RoleAgent, 1, team)
		in := CreateInput{Subject: "Invoice #42 wrong", BodyMD: "I want a refund", CategoryID: sp(cat), Priority: "high",
			RequesterID: sp(cust.UserID), Tags: []string{"billing"}, CustomFields: map[string]any{"plan": "Gold"}}
		hit := e.createTicket(agent, in)
		in.CustomFields = map[string]any{"plan": "silver"}
		miss := e.createTicket(agent, in)
		e.automate(&cur)
		if !slices.Contains(e.tagsOf(hit), "matched") {
			t.Fatalf("all facts did not match: %v", e.tagsOf(hit))
		}
		if slices.Contains(e.tagsOf(miss), "matched") {
			t.Fatal("a differing custom field still matched")
		}
	})
}

// TestAutomation_EachAction: every action type applies under the
// automation actor, and the timeline records each outcome.
func TestAutomation_EachAction(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		t1, t2 := storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")
		cat1, cat2 := storetest.CreateCategory(t, db, "", t1), storetest.CreateCategory(t, db, "", t1)
		watcher := e.staff(domain.RoleAgent, 1, t1)
		l3 := e.staff(domain.RoleAgent, 3, t2)
		canned := e.do(admin, "POST", "/api/v1/admin/canned-responses", map[string]any{"title": "Ack", "body_md": "Hello {{customer.name}}, we got #{{ticket.number}}."}).expect(t, 201).m(t)["id"].(string)
		e.rule(admin, map[string]any{"name": "alpha", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "alpha"}},
			"actions": []map[string]any{
				{"type": "set_priority", "value": "urgent"}, {"type": "set_category", "value": cat2},
				{"type": "add_tag", "value": "auto"}, {"type": "remove_tag", "value": "old"},
				{"type": "add_watcher", "user_id": watcher.UserID}, {"type": "add_note", "body_md": "Automated note"},
				{"type": "send_reply", "canned_response_id": canned},
			}})
		e.rule(admin, map[string]any{"name": "beta", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "beta"}},
			"actions": []map[string]any{
				{"type": "set_team", "value": t2}, {"type": "escalate"}, {"type": "assign", "user_id": l3.UserID},
				{"type": "set_status", "value": "pending"},
			}})
		e.rule(admin, map[string]any{"name": "gamma", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "gamma"}},
			"actions":    []map[string]any{{"type": "set_tier", "value": 3}, {"type": "set_status", "value": "closed"}}})
		cur := e.lastEventID()
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		agent := e.staff(domain.RoleAgent, 1, t1)
		alpha := e.createTicket(agent, CreateInput{Subject: "alpha", CategoryID: sp(cat1), RequesterID: sp(cust.UserID), Tags: []string{"old", "keep"}})
		beta := e.createTicket(cust, CreateInput{Subject: "beta", CategoryID: sp(cat1)})
		gamma := e.createTicket(cust, CreateInput{Subject: "gamma", CategoryID: sp(cat1)})
		e.automate(&cur)

		// alpha
		if p := e.ticketField(alpha, "priority"); p != "urgent" {
			t.Fatalf("priority %s", p)
		}
		if c := e.ticketField(alpha, "category_id"); c != cat2 {
			t.Fatalf("category %s", c)
		}
		if tags := e.tagsOf(alpha); !slices.Equal(tags, []string{"auto", "keep"}) {
			t.Fatalf("tags %v", tags)
		}
		if e.count(`SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?`, alpha, watcher.UserID) != 1 {
			t.Fatal("watcher not added")
		}
		if e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND kind = 'internal' AND author_id IS NULL AND body_md = 'Automated note'`, alpha) != 1 {
			t.Fatal("note not added")
		}
		var reply string
		if err := db.Read().QueryRowContext(ctx, `SELECT body_md FROM ticket_messages WHERE ticket_id = ? AND kind = 'public' AND author_id IS NULL`, alpha).Scan(&reply); err != nil {
			t.Fatalf("reply: %v", err)
		}
		if strings.Contains(reply, "{{") || !strings.HasPrefix(reply, "Hello ") {
			t.Fatalf("placeholders not rendered: %q", reply)
		}
		if e.ticketField(alpha, "first_responded_at") != "" {
			t.Fatal("an automated reply counted as the SLA first response")
		}
		ap := e.applied(alpha)
		if len(ap) != 1 || ap[0].RuleName != "alpha" || len(ap[0].Actions) != 7 || ap[0].Trigger != "ticket.created" {
			t.Fatalf("applied: %+v", ap)
		}
		for _, a := range ap[0].Actions {
			if a.Outcome != "applied" {
				t.Fatalf("action %s: %s (%s)", a.Type, a.Outcome, a.Detail)
			}
		}
		// beta: team, escalate, assign (M-4 holds in the new team at tier 2), status.
		if e.ticketField(beta, "team_id") != t2 || e.ticketField(beta, "assignee_id") != l3.UserID || e.ticketField(beta, "status") != "pending" {
			t.Fatalf("beta: team %s assignee %s status %s", e.ticketField(beta, "team_id"), e.ticketField(beta, "assignee_id"), e.ticketField(beta, "status"))
		}
		var tier int
		_ = db.Read().QueryRowContext(ctx, `SELECT tier FROM tickets WHERE id = ?`, beta).Scan(&tier)
		if tier != 2 {
			t.Fatalf("beta tier %d", tier)
		}
		// gamma: tier 3, closed through resolved.
		_ = db.Read().QueryRowContext(ctx, `SELECT tier FROM tickets WHERE id = ?`, gamma).Scan(&tier)
		if tier != 3 || e.ticketField(gamma, "status") != "closed" {
			t.Fatalf("gamma tier %d status %s", tier, e.ticketField(gamma, "status"))
		}
		evs := e.eventsOf(gamma)
		if !slices.Contains(evs, events.TicketEscalated) || slices.Index(evs, events.TicketStatusChanged) < 0 {
			t.Fatalf("gamma events %v", evs)
		}
		for _, id := range []string{alpha, beta, gamma} {
			all, _ := events.ForTicket(ctx, db.Read(), id)
			for _, ev := range all[1:] {
				if ev.Type != events.TicketCreated && ev.ActorID != events.ActorAutomation && ev.Type != events.TicketFollowupCreated {
					t.Fatalf("%s written by %q, want the automation actor", ev.Type, ev.ActorID)
				}
			}
		}
		// Assignment to an agent that M-4 refuses is skipped and recorded.
		e.rule(admin, map[string]any{"name": "bad assign", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "delta"}},
			"actions":    []map[string]any{{"type": "assign", "user_id": l3.UserID}, {"type": "add_tag", "value": "after-skip"}}})
		delta := e.createTicket(cust, CreateInput{Subject: "delta", CategoryID: sp(cat1)})
		e.automate(&cur)
		ap = e.applied(delta)
		if len(ap) != 1 || ap[0].Actions[0].Outcome != "skipped" || ap[0].Actions[1].Outcome != "applied" || e.assigneeOf(delta) == l3.UserID {
			t.Fatalf("delta: %+v", ap)
		}
	})
}

// TestAutomation_LoopGuard_PriorityRuleDoesNotRefire: a ticket.updated rule
// that changes the priority does not re-trigger rules (its own or others),
// and the run settles after one application.
func TestAutomation_LoopGuard_PriorityRuleDoesNotRefire(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		e.rule(admin, map[string]any{"name": "bump", "event": "ticket.updated",
			"conditions": []map[string]any{{"field": "priority", "op": "is", "value": []string{"high", "urgent"}}},
			"actions":    []map[string]any{{"type": "set_priority", "value": "urgent"}, {"type": "add_note", "body_md": "bumped"}}})
		e.rule(admin, map[string]any{"name": "flip", "event": "ticket.updated",
			"conditions": []map[string]any{{"field": "priority", "op": "is", "value": "urgent"}},
			"actions":    []map[string]any{{"type": "set_priority", "value": "high"}}})
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{CategoryID: sp(storetest.CreateCategory(t, db, "", team))})
		cur := e.lastEventID()
		if _, err := e.svc.Patch(ctx, admin, id, PatchInput{Priority: optStr{true, sp("high")}}, ""); err != nil {
			t.Fatal(err)
		}
		e.automate(&cur)
		// bump fires (high → urgent), then flip sees urgent in the same run
		// and sets high again: two rules, one evaluation, no re-trigger.
		ap := e.applied(id)
		if len(ap) != 2 || ap[0].RuleName != "bump" || ap[1].RuleName != "flip" {
			t.Fatalf("applied %+v", ap)
		}
		if e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'bumped'`, id) != 1 {
			t.Fatal("the note was added more than once: rules re-fired on their own change")
		}
		if p := e.ticketField(id, "priority"); p != "high" {
			t.Fatalf("priority %s", p)
		}
		before := len(e.eventsOf(id))
		e.automate(&cur)
		if len(e.eventsOf(id)) != before {
			t.Fatal("a second pass wrote events")
		}
	})
}

// TestAutomation_ActionCap: at most 10 actions per event across all rules;
// the rule cut short is marked truncated.
func TestAutomation_ActionCap(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		tags := func(prefix string) []map[string]any {
			var out []map[string]any
			for i := 0; i < 6; i++ {
				out = append(out, map[string]any{"type": "add_tag", "value": prefix + string(rune('a'+i))})
			}
			return out
		}
		e.rule(admin, map[string]any{"name": "six a", "event": "ticket.created", "actions": tags("a-")})
		e.rule(admin, map[string]any{"name": "six b", "event": "ticket.created", "actions": tags("b-")})
		cur := e.lastEventID()
		id := e.createTicket(e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester), CreateInput{})
		e.automate(&cur)
		if n := len(e.tagsOf(id)); n != 10 {
			t.Fatalf("%d actions applied, want the cap of 10: %v", n, e.tagsOf(id))
		}
		ap := e.applied(id)
		if len(ap) != 2 || ap[0].Truncated || !ap[1].Truncated || len(ap[1].Actions) != 4 {
			t.Fatalf("applied %+v", ap)
		}
	})
}

// TestAutomation_OrderAndStopProcessing: rules run in position order, a
// matching stop_processing rule ends the evaluation, disabled rules are
// skipped, and reorder changes the order.
func TestAutomation_OrderAndStopProcessing(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		r1 := e.rule(admin, map[string]any{"name": "first", "event": "ticket.created", "stop_processing": true,
			"actions": []map[string]any{{"type": "set_priority", "value": "low"}, {"type": "add_tag", "value": "first"}}})
		r2 := e.rule(admin, map[string]any{"name": "second", "event": "ticket.created",
			"actions": []map[string]any{{"type": "set_priority", "value": "high"}, {"type": "add_tag", "value": "second"}}})
		cur := e.lastEventID()
		a := e.createTicket(cust, CreateInput{})
		e.automate(&cur)
		if tags := e.tagsOf(a); !slices.Equal(tags, []string{"first"}) {
			t.Fatalf("stop_processing ignored: %v", tags)
		}
		list := e.do(admin, "POST", "/api/v1/admin/automation/rules/reorder", map[string]any{"ids": []string{r2, r1}}).expect(t, 200)
		var rules []map[string]any
		list.json(t, &rules)
		if rules[0]["id"] != r2 || rules[0]["position"].(float64) != 1 {
			t.Fatalf("reorder: %v", rules)
		}
		b := e.createTicket(cust, CreateInput{})
		e.automate(&cur)
		if tags := e.tagsOf(b); !slices.Equal(tags, []string{"first", "second"}) || e.ticketField(b, "priority") != "low" {
			t.Fatalf("order: tags %v priority %s", tags, e.ticketField(b, "priority"))
		}
		e.do(admin, "PATCH", "/api/v1/admin/automation/rules/"+r1, map[string]any{"enabled": false}).expect(t, 200)
		c := e.createTicket(cust, CreateInput{})
		e.automate(&cur)
		if tags := e.tagsOf(c); !slices.Equal(tags, []string{"second"}) {
			t.Fatalf("disabled rule ran: %v", tags)
		}
	})
}

// TestAutomation_MultiEventChangeFiresOnce: a PATCH that writes several
// events (priority, category, subject) runs a ticket.updated rule once.
func TestAutomation_MultiEventChangeFiresOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		admin := e.staff(domain.RoleAdmin, 3)
		cat := storetest.CreateCategory(t, db, "", "")
		e.rule(admin, map[string]any{"name": "any update", "event": "ticket.updated",
			"actions": []map[string]any{{"type": "add_note", "body_md": "changed"}}})
		id := e.createTicket(e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester), CreateInput{})
		cur := e.lastEventID()
		if _, err := e.svc.Patch(context.Background(), admin, id, PatchInput{Priority: optStr{true, sp("high")}, CategoryID: optStr{true, sp(cat)},
			Subject: optStr{true, sp("New subject")}}, ""); err != nil {
			t.Fatal(err)
		}
		e.automate(&cur)
		if n := e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'changed'`, id); n != 1 {
			t.Fatalf("rule ran %d times for one change", n)
		}
	})
}

// TestAutomation_ReplayIsNoop: the queue is at-least-once; a replayed event
// is never applied twice (automation_runs).
func TestAutomation_ReplayIsNoop(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		e.rule(admin, map[string]any{"name": "note", "event": "ticket.created", "actions": []map[string]any{{"type": "add_note", "body_md": "once"}}})
		cur := e.lastEventID()
		id := e.createTicket(e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester), CreateInput{})
		evs, _ := events.ReadAfter(ctx, db.Read(), cur, 100)
		for i := 0; i < 3; i++ {
			if err := e.svc.HandleAutomationEvent(ctx, evs[0]); err != nil {
				t.Fatal(err)
			}
		}
		if n := e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'once'`, id); n != 1 {
			t.Fatalf("replayed event applied %d times", n)
		}
	})
}

// TestAutomation_MessageCreatedCustomerOnly: message.created rules fire on
// customer replies only, with the reply as the body.
func TestAutomation_MessageCreatedCustomerOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		e.rule(admin, map[string]any{"name": "refund", "event": "message.created",
			"conditions": []map[string]any{{"field": "body", "op": "contains", "value": "refund"}},
			"actions":    []map[string]any{{"type": "add_tag", "value": "refund"}}})
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		id := e.createTicket(cust, CreateInput{CategoryID: sp(storetest.CreateCategory(t, db, "", team)), BodyMD: "no keyword here"})
		cur := e.lastEventID()
		if _, err := e.svc.AddStaffMessage(ctx, agent, id, MessageInput{Kind: "public", BodyMD: "a refund is possible"}); err != nil {
			t.Fatal(err)
		}
		e.automate(&cur)
		if len(e.tagsOf(id)) != 0 {
			t.Fatal("a staff reply triggered a message.created rule")
		}
		if _, err := e.svc.AddCustomerReply(ctx, cust, id, MessageInput{BodyMD: "I want a REFUND"}); err != nil {
			t.Fatal(err)
		}
		e.automate(&cur)
		if !slices.Equal(e.tagsOf(id), []string{"refund"}) {
			t.Fatalf("customer reply did not trigger: %v", e.tagsOf(id))
		}
	})
}

// TestTimeRules_FakeClock: time-based rules fire on the hourly scheduler
// once their threshold passed, once per ticket until it changes again;
// "close" from pending resolves first.
func TestTimeRules_FakeClock(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		e.rule(admin, map[string]any{"name": "remind", "event": "time",
			"conditions": []map[string]any{{"field": "status", "op": "is", "value": "pending"}, {"field": "hours_since_status", "op": "gte", "value": 72}},
			"actions":    []map[string]any{{"type": "add_note", "body_md": "Customer silent for 72 h"}}})
		e.rule(admin, map[string]any{"name": "close", "event": "time",
			"conditions": []map[string]any{{"field": "status", "op": "is", "value": "pending"}, {"field": "hours_since_status", "op": "gte", "value": 96}},
			"actions":    []map[string]any{{"type": "set_status", "value": "closed"}}})
		// A time condition on a trigger rule is refused.
		e.do(admin, "POST", "/api/v1/admin/automation/rules", map[string]any{"name": "bad", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "hours_since_update", "op": "gte", "value": 1}}, "actions": []map[string]any{{"type": "escalate"}}}).expect(t, 422)

		t0 := time.Now().UTC()
		clock := t0
		e.svc.now = func() time.Time { return clock }
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		cat := storetest.CreateCategory(t, db, "", team)
		e.svc.dir.Invalidate()
		id := e.createTicket(cust, CreateInput{CategoryID: sp(cat)})
		fresh := e.createTicket(cust, CreateInput{CategoryID: sp(cat)})
		for _, x := range []string{id, fresh} {
			if _, err := e.svc.Patch(ctx, agent, x, PatchInput{Status: optStr{true, sp("pending")}}, ""); err != nil {
				t.Fatal(err)
			}
		}
		notes := func(x string) int {
			return e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md = 'Customer silent for 72 h'`, x)
		}
		run := func(h time.Duration) int {
			clock = t0.Add(h)
			n, err := e.svc.RunTimeRules(ctx, clock)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := run(71 * time.Hour); n != 0 || notes(id) != 0 {
			t.Fatalf("fired before the threshold (%d)", n)
		}
		if n := run(73 * time.Hour); n != 2 || notes(id) != 1 {
			t.Fatalf("73 h: changed %d, notes %d", n, notes(id))
		}
		if n := run(74 * time.Hour); n != 0 || notes(id) != 1 {
			t.Fatalf("re-fired without a change: %d, notes %d", n, notes(id))
		}
		// The other ticket is answered by the customer: it leaves pending.
		if _, err := e.svc.AddCustomerReply(ctx, cust, fresh, MessageInput{BodyMD: "here I am"}); err != nil {
			t.Fatal(err)
		}
		if n := run(97 * time.Hour); n != 1 {
			t.Fatalf("97 h: changed %d", n)
		}
		if st := e.ticketField(id, "status"); st != "closed" {
			t.Fatalf("pending ticket not closed at 97 h: %s", st)
		}
		if st := e.ticketField(fresh, "status"); st != "open" {
			t.Fatalf("answered ticket changed: %s", st)
		}
		ap := e.applied(id)
		if len(ap) != 2 || ap[0].Trigger != "time" || ap[1].RuleName != "close" {
			t.Fatalf("applied %+v", ap)
		}
		if n := run(200 * time.Hour); n != 0 {
			t.Fatalf("closed ticket considered: %d", n)
		}
	})
}

// TestAutomation_I7_NotesNeverReachCustomers (I-7/I-9): an automation note
// and the ticket.automation_applied event reach staff only: not the portal
// detail, not a customer WebSocket frame.
func TestAutomation_I7_NotesNeverReachCustomers(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		team := storetest.CreateTeam(t, db, "")
		cat := storetest.CreateCategory(t, db, "", team)
		agent := e.staff(domain.RoleAgent, 1, team)
		const secret = "AUTOMATION-SECRET-NOTE-31b7"
		e.rule(admin, map[string]any{"name": "SECRET-RULE-NAME", "event": "ticket.created",
			"actions": []map[string]any{{"type": "add_note", "body_md": secret}, {"type": "add_tag", "value": "secret-tag"}, {"type": "set_priority", "value": "high"}}})
		if events.CustomerVisible(events.TicketAutomationApplied) {
			t.Fatal("ticket.automation_applied is customer-visible")
		}
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		cur, wsCur := e.lastEventID(), ""
		id := e.createTicket(cust, CreateInput{CategoryID: sp(cat)})
		ws := e.wsDial(cust)
		if denied := ws.subscribe("s", "tickets", "ticket:"+id, "me"); len(denied) != 0 {
			t.Fatalf("denied %v", denied)
		}
		staffWS := e.wsDial(agent)
		staffWS.subscribe("s", "ticket:"+id)
		e.automate(&cur)
		e.dispatch(&wsCur)
		if staffWS.waitFor(3*time.Second, func(f map[string]any) bool { return f["event"] == events.TicketAutomationApplied }) == nil {
			t.Fatalf("staff did not get ticket.automation_applied: %s", staffWS.rawJoined())
		}
		time.Sleep(100 * time.Millisecond)
		raw := ws.rawJoined()
		for _, leak := range []string{secret, "SECRET-RULE-NAME", events.TicketAutomationApplied, "secret-tag"} {
			if strings.Contains(raw, leak) {
				t.Fatalf("customer frame carries %q: %s", leak, raw)
			}
		}
		portal := e.do(cust, "GET", "/api/v1/portal/tickets/"+id, nil).expect(t, 200)
		if strings.Contains(string(portal.body), secret) {
			t.Fatal("portal detail shows the automation note")
		}
		staff := e.do(agent, "GET", "/api/v1/agent/tickets/"+id, nil).expect(t, 200)
		if !strings.Contains(string(staff.body), secret) {
			t.Fatal("staff detail lacks the automation note")
		}
		tl := e.do(agent, "GET", "/api/v1/agent/tickets/"+id+"/events", nil).expect(t, 200).m(t)
		found := false
		for _, it := range tl["items"].([]any) {
			ev := it.(map[string]any)
			if ev["type"] == events.TicketAutomationApplied {
				found = true
				b, _ := json.Marshal(ev["data"])
				if !strings.Contains(string(b), "SECRET-RULE-NAME") || ev["actor"] != nil {
					t.Fatalf("timeline entry: %v", ev)
				}
			}
		}
		if !found {
			t.Fatal("timeline lacks ticket.automation_applied")
		}
		_ = ctx
	})
}

// TestRulesAPI_CRUDValidationDryRun: supervisor+ only (agent 403, customer
// 404, PAT without admin 403); validation 422 with paths; dry-run reports
// without writing.
func TestRulesAPI_CRUDValidationDryRun(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		sup := e.staff(domain.RoleSupervisor, 3)
		team := storetest.CreateTeam(t, db, "")
		agent := e.staff(domain.RoleAgent, 1, team)
		cust := e.customer(storetest.CreateOrg(t, db, ""), domain.RoleRequester)
		body := map[string]any{"name": "Urgent VIP", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "subject", "op": "contains", "value": "down"}},
			"actions":    []map[string]any{{"type": "set_priority", "value": "urgent"}, {"type": "add_tag", "value": "vip"}}}
		e.do(agent, "POST", "/api/v1/admin/automation/rules", body).expect(t, 403)
		e.do(cust, "GET", "/api/v1/admin/automation/rules", nil).expect(t, 404)
		e.do(e.pat(sup, domain.ScopeTicketsWrite), "POST", "/api/v1/admin/automation/rules", body).expect(t, 403)
		id := e.rule(e.pat(sup, domain.ScopeAdmin), body)

		got := e.do(sup, "GET", "/api/v1/admin/automation/rules/"+id, nil).expect(t, 200).m(t)
		if got["match"] != "all" || got["enabled"] != true || got["stop_processing"] != false || got["position"].(float64) != 1 {
			t.Fatalf("defaults: %v", got)
		}
		bad := []struct {
			patch map[string]any
			field string
		}{
			{map[string]any{"conditions": []map[string]any{{"field": "subject", "op": "regex", "value": "(unclosed"}}}, "conditions[0].value"},
			{map[string]any{"actions": []map[string]any{{"type": "set_team", "value": store.NewID()}}}, "actions[0].value"},
			{map[string]any{"actions": []map[string]any{{"type": "assign", "user_id": cust.UserID}}}, "actions[0].user_id"},
			{map[string]any{"actions": []map[string]any{{"type": "send_reply", "canned_response_id": store.NewID()}}}, "actions[0].canned_response_id"},
			{map[string]any{"conditions": []map[string]any{{"field": "custom_field", "op": "equals", "key": "nope", "value": "x"}}}, "conditions[0].key"},
			{map[string]any{"name": ""}, "name"},
		}
		for _, b := range bad {
			p := e.do(sup, "PATCH", "/api/v1/admin/automation/rules/"+id, b.patch).expect(t, 422).problem(t)
			if p["errors"].([]any)[0].(map[string]any)["field"] != b.field {
				t.Errorf("%v: %v", b.patch, p)
			}
		}
		e.do(sup, "POST", "/api/v1/admin/automation/rules/reorder", map[string]any{"ids": []string{}}).expect(t, 422)
		e.do(sup, "POST", "/api/v1/admin/automation/rules/reorder", map[string]any{"ids": []string{id, id}}).expect(t, 422)

		// Dry-run: conditions and action outcomes, nothing written.
		tk := e.createTicket(cust, CreateInput{Subject: "Server down", TeamID: sp(team)})
		before := len(e.eventsOf(tk))
		dr := e.do(sup, "POST", "/api/v1/admin/automation/rules/"+id+"/dry-run", map[string]any{"ticket_id": tk}).expect(t, 200).m(t)
		if dr["matched"] != true {
			t.Fatalf("dry-run: %v", dr)
		}
		acts := dr["actions"].([]any)
		if len(acts) != 2 || acts[0].(map[string]any)["outcome"] != "applied" {
			t.Fatalf("dry-run actions: %v", dr)
		}
		if len(e.eventsOf(tk)) != before || e.ticketField(tk, "priority") != "normal" || len(e.tagsOf(tk)) != 0 {
			t.Fatal("dry-run wrote to the ticket")
		}
		other := e.createTicket(cust, CreateInput{Subject: "Question", TeamID: sp(team)})
		dr = e.do(sup, "POST", "/api/v1/admin/automation/rules/"+id+"/dry-run", map[string]any{"ticket_id": other}).expect(t, 200).m(t)
		if dr["matched"] != false || len(dr["actions"].([]any)) != 0 || dr["conditions"].([]any)[0].(map[string]any)["matched"] != false {
			t.Fatalf("dry-run miss: %v", dr)
		}
		e.do(sup, "POST", "/api/v1/admin/automation/rules/"+id+"/dry-run", map[string]any{"ticket_id": store.NewID()}).expect(t, 422)

		list := e.do(sup, "GET", "/api/v1/admin/automation/rules", nil).expect(t, 200)
		var rules []map[string]any
		list.json(t, &rules)
		if len(rules) != 1 {
			t.Fatalf("list: %v", rules)
		}
		e.do(sup, "DELETE", "/api/v1/admin/automation/rules/"+id, nil).expect(t, 204)
		e.do(sup, "GET", "/api/v1/admin/automation/rules/"+id, nil).expect(t, 404)
		e.do(sup, "DELETE", "/api/v1/admin/automation/rules/"+id, nil).expect(t, 404)
		if rs, _ := e.svc.automationRules(context.Background()); len(rs) != 0 {
			t.Fatal("cache not invalidated after delete")
		}
	})
}

// TestRules_CacheZeroReadsWhenWarm: the consumer reads no rule rows while
// the cache is warm, and sees a change right after the write.
func TestRules_CacheZeroReadsWhenWarm(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		admin := e.staff(domain.RoleAdmin, 3)
		e.rule(admin, map[string]any{"name": "r", "event": "ticket.created", "actions": []map[string]any{{"type": "escalate"}}})
		a, _ := e.svc.automationRules(ctx)
		b, _ := e.svc.automationRules(ctx)
		if len(a) != 1 || &a[0] != &b[0] {
			t.Fatal("second call did not use the cache")
		}
		e.rule(admin, map[string]any{"name": "r2", "event": "time",
			"conditions": []map[string]any{{"field": "hours_since_update", "op": "gte", "value": 1}}, "actions": []map[string]any{{"type": "escalate"}}})
		c, _ := e.svc.automationRules(ctx)
		if len(c) != 2 {
			t.Fatal("cache not reloaded after a change")
		}
	})
}
