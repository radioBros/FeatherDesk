// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"time"
)

// tk is a ticket the seeder created; it tracks only what later steps need.
type tk struct {
	id      string
	number  int
	subject string
	req     *person
	cat     string // category key ("" = none)
	project string // project key (D-20; "web" for tickets without a category)
	team    string // team key ("" = triage)
	tier    int
	handler *person // current assignee (nil = unassigned)
	status  string
	created time.Time
	rc      bool // root cause recorded (staff field that blocks resolving technical tickets)
}

type attach struct {
	name, ctype string
	data        []byte
}

// spec describes a ticket to open.
type spec struct {
	subject, body string
	cat           string
	prio          string
	cf            map[string]any
	tags          []string
	attach        []attach
}

type ticketResp struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Status   string `json:"status"`
	Tier     int    `json:"tier"`
	TeamID   string `json:"team_id"`
	Assignee *struct {
		ID string `json:"id"`
	} `json:"assignee"`
	Messages []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Body string `json:"body_md"`
	} `json:"messages"`
}

type outcomeResp struct {
	Ticket  ticketResp `json:"ticket"`
	Visible bool       `json:"visible"`
}

func (s *seeder) teamKeyOf(id string) string {
	for k, t := range s.teams {
		if t.ID == id {
			return k
		}
	}
	return ""
}

func (s *seeder) staffByID(id string) *person {
	for _, p := range s.staff {
		if p.id == id {
			return p
		}
	}
	return nil
}

func (s *seeder) track(t *tk, r ticketResp) {
	t.id, t.number, t.status, t.tier = r.ID, r.Number, r.Status, r.Tier
	if r.TeamID != "" {
		t.team = s.teamKeyOf(r.TeamID)
	}
	t.handler = nil
	if r.Assignee != nil {
		t.handler = s.staffByID(r.Assignee.ID)
	}
}

// wait advances the simulated clock by a random duration in [lo, hi], never
// more than a quarter of the time left until "now", so a young ticket's whole
// story still fits before the present.
func (s *seeder) wait(lo, hi time.Duration) {
	d := lo
	if hi > lo {
		d = lo + time.Duration(s.rng.Int64N(int64(hi-lo)))
	}
	left := s.now.Add(-time.Minute).Sub(s.cur())
	if left < 0 {
		left = 0
	}
	if cap := left / 4; d > cap {
		d = cap
	}
	s.advance(d)
}

func (s *seeder) upload(c *client, area string, a attach) (string, error) {
	var out idResp
	if err := c.upload("/"+area+"/attachments", a.name, a.ctype, a.data, &out); err != nil {
		return "", fmt.Errorf("upload %s: %w", a.name, err)
	}
	return out.ID, nil
}

func (s *seeder) attachIDs(c *client, area string, list []attach) ([]string, error) {
	var ids []string
	for _, a := range list {
		id, err := s.upload(c, area, a)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// portalOpen opens a ticket as the customer (channel "portal").
func (s *seeder) portalOpen(c *person, sp spec) (*tk, error) {
	ids, err := s.attachIDs(c.cl, "portal", sp.attach)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"subject": sp.subject, "body_md": sp.body, "attachment_ids": ids}
	if sp.cat != "" {
		body["category_id"] = s.cats[sp.cat].ID
	}
	if sp.prio != "" {
		body["priority"] = sp.prio
	}
	if sp.cf != nil {
		body["custom_fields"] = sp.cf
	}
	var r ticketResp
	if err := c.cl.post("/portal/tickets", body, &r); err != nil {
		return nil, fmt.Errorf("portal ticket %q: %w", sp.subject, err)
	}
	t := &tk{subject: sp.subject, req: c, cat: sp.cat, created: s.cur(), project: s.projectOf(sp.cat)}
	if sp.cat != "" {
		t.team = s.cats[sp.cat].Team
	}
	s.track(t, r)
	s.tickets = append(s.tickets, t)
	return t, nil
}

// agentOpen opens a ticket on behalf of a customer, signed in as `by` (a
// staff member, channel "agent") or through the admin PAT (channel "api").
func (s *seeder) agentOpen(by *client, byPerson *person, c *person, sp spec, assignee *person) (*tk, error) {
	body := map[string]any{"subject": sp.subject, "body_md": sp.body, "requester_id": c.id}
	if sp.cat != "" {
		body["category_id"] = s.cats[sp.cat].ID
	}
	if sp.prio != "" {
		body["priority"] = sp.prio
	}
	if sp.cf != nil {
		body["custom_fields"] = sp.cf
	}
	if len(sp.tags) > 0 {
		body["tags"] = sp.tags
	}
	if assignee != nil {
		body["assignee_id"] = assignee.id
	}
	if len(sp.attach) > 0 {
		ids, err := s.attachIDs(by, "agent", sp.attach)
		if err != nil {
			return nil, err
		}
		body["attachment_ids"] = ids
	}
	var r ticketResp
	if err := by.post("/agent/tickets", body, &r); err != nil {
		return nil, fmt.Errorf("agent ticket %q: %w", sp.subject, err)
	}
	t := &tk{subject: sp.subject, req: c, cat: sp.cat, created: s.cur(), project: s.projectOf(sp.cat)}
	if sp.cat != "" {
		t.team = s.cats[sp.cat].Team
	}
	s.track(t, r)
	s.tickets = append(s.tickets, t)
	return t, nil
}

// patch applies PATCH /agent/tickets/{id} as by and refreshes the tracker.
func (s *seeder) patch(t *tk, by *person, body map[string]any) error {
	var out outcomeResp
	if err := by.cl.patch("/agent/tickets/"+t.id, body, &out); err != nil {
		return fmt.Errorf("ticket #%d %v: %w", t.number, body, err)
	}
	if out.Ticket.ID != "" {
		s.track(t, out.Ticket)
	}
	return nil
}

func (s *seeder) assign(t *tk, by, to *person) error {
	return s.patch(t, by, map[string]any{"assignee_id": to.id})
}

// reply adds a public message: from a customer through the portal, from
// staff through the agent API (optionally moving the status).
func (s *seeder) reply(t *tk, by *person, body, setStatus string) error {
	if by.isStaff() {
		in := map[string]any{"kind": "public", "body_md": body}
		if setStatus != "" {
			in["set_status"] = setStatus
		}
		if err := by.cl.post("/agent/tickets/"+t.id+"/messages", in, nil); err != nil {
			return fmt.Errorf("reply #%d: %w", t.number, err)
		}
		if setStatus != "" {
			t.status = setStatus
		} else if t.status == "new" {
			t.status = "open"
		}
		return nil
	}
	if err := by.cl.post("/portal/tickets/"+t.id+"/messages", map[string]any{"body_md": body}, nil); err != nil {
		return fmt.Errorf("customer reply #%d: %w", t.number, err)
	}
	if t.status == "pending" || t.status == "on_hold" || t.status == "resolved" {
		t.status = "open"
	}
	return nil
}

func (s *seeder) note(t *tk, by *person, body string) error {
	if err := by.cl.post("/agent/tickets/"+t.id+"/messages", map[string]any{"kind": "internal", "body_md": body}, nil); err != nil {
		return fmt.Errorf("note #%d: %w", t.number, err)
	}
	return nil
}

func (s *seeder) escalate(t *tk, by *person, reason, targetTeam string) error {
	in := map[string]any{"reason": reason}
	if targetTeam != "" {
		in["target_team_id"] = s.teams[targetTeam].ID
	}
	var out outcomeResp
	if err := by.cl.post("/agent/tickets/"+t.id+"/escalate", in, &out); err != nil {
		return fmt.Errorf("escalate #%d: %w", t.number, err)
	}
	s.track(t, out.Ticket)
	return nil
}

func (s *seeder) deescalate(t *tk, by *person, reason string) error {
	var out outcomeResp
	if err := by.cl.post("/agent/tickets/"+t.id+"/deescalate", map[string]any{"reason": reason}, &out); err != nil {
		return fmt.Errorf("deescalate #%d: %w", t.number, err)
	}
	s.track(t, out.Ticket)
	return nil
}

func (s *seeder) setFields(t *tk, by *person, cf map[string]any) error {
	return s.patch(t, by, map[string]any{"custom_fields": cf})
}

func (s *seeder) watch(t *tk, by *person, who *person) error {
	if err := by.cl.post("/agent/tickets/"+t.id+"/watchers", map[string]any{"user_id": who.id}, nil); err != nil {
		return fmt.Errorf("watcher #%d: %w", t.number, err)
	}
	return nil
}

func (s *seeder) link(t, other *tk, by *person, typ string) error {
	if err := by.cl.post("/agent/tickets/"+t.id+"/links", map[string]any{"to_ticket_id": other.id, "type": typ}, nil); err != nil {
		return fmt.Errorf("link #%d -> #%d: %w", t.number, other.number, err)
	}
	return nil
}

func (s *seeder) csat(t *tk, score int, comment string) error {
	body := map[string]any{"score": score}
	if comment != "" {
		body["comment"] = comment
	}
	if err := t.req.cl.post("/portal/tickets/"+t.id+"/csat", body, nil); err != nil {
		return fmt.Errorf("csat #%d: %w", t.number, err)
	}
	return nil
}

// actor is a staff member allowed to see and work t at its current tier.
func (s *seeder) actor(t *tk) *person {
	if t.handler != nil {
		return t.handler
	}
	if t.team == "" {
		return s.staff["sup"]
	}
	return s.staffBy(t.team, max(t.tier, 1))
}

func (s *seeder) mention(p *person) string { return fmt.Sprintf("@[%s](%s)", p.name, p.id) }

// projectOf is the project key of a category ("web" without one: the
// default project).
func (s *seeder) projectOf(cat string) string {
	if c, ok := s.cats[cat]; ok && c.Project != "" {
		return c.Project
	}
	return "web"
}
