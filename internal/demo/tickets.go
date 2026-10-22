// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const day = 24 * time.Hour

// scenario is one scripted ticket story, started `age` before now.
type scenario struct {
	name string
	age  time.Duration
	n    int // tickets the story creates
	run  func() error
}

// customerCF returns plausible customer-editable field values for a category.
func (s *seeder) customerCF(cat string) map[string]any {
	switch cat {
	case "tech":
		cf := map[string]any{
			"os":               pick(s, []string{"windows", "macos", "linux", "ios", "android"}),
			"affected_users":   1 + s.rng.IntN(40),
			"affected_systems": pick(s, [][]string{{"vpn"}, {"email", "crm"}, {"erp"}, {"wifi", "vpn"}}),
			"blocking_work":    s.rng.IntN(3) == 0,
		}
		if s.rng.IntN(2) == 0 {
			cf["error_message"] = pick(s, []string{"Error 0x80070057: the parameter is incorrect", "ERR_CONNECTION_TIMED_OUT", "502 Bad Gateway", "Authentication failed (code 401)"})
		}
		return cf
	case "access":
		return map[string]any{"os": pick(s, []string{"windows", "macos", "android"}), "error_message": "Your account is temporarily locked"}
	case "billing":
		return map[string]any{"order_number": fmt.Sprintf("INV-2026-%04d", 100+s.rng.IntN(900)),
			"invoice_date": s.now.AddDate(0, 0, -(10 + s.rng.IntN(60))).Format("2006-01-02")}
	case "mobile":
		return map[string]any{"os": pick(s, []string{"ios", "android"}), "app_version": pick(s, []string{"3.8.0", "3.8.1", "3.9.0"})}
	}
	return nil
}

// resolve sets the staff fields the category requires, then resolves.
func (s *seeder) resolve(t *tk, by *person, text string) error {
	if t.cat == "tech" && !t.rc {
		cf := map[string]any{"root_cause": pick(s, []string{"bug", "config", "user_error", "hardware", "third_party"}), "internal_ref": fmt.Sprintf("ENG-%d", 1000+s.rng.IntN(900))}
		if err := s.setFields(t, by, cf); err != nil {
			return err
		}
		t.rc = true
	}
	return s.reply(t, by, text, "resolved")
}

func (s *seeder) closeTicket(t *tk) error {
	return s.patch(t, s.staff["sup"], map[string]any{"status": "closed"})
}

func (s *seeder) rate(t *tk, score int) error {
	s.wait(2*time.Hour, 20*time.Hour)
	return s.csat(t, score, pick(s, csatComments[score]))
}

func scriptedCount(sc []scenario) int {
	n := 0
	for _, x := range sc {
		n += x.n
	}
	return n
}

func (s *seeder) start(age time.Duration) time.Time { s.sim(s.now.Add(-age)); return s.cur() }

// seedTickets runs the scripted scenarios plus generated tickets up to
// --tickets, oldest first so ticket numbers follow the timeline.
func (s *seeder) seedTickets() error {
	sc := s.scenarios()
	var plan []scenario
	plan = append(plan, sc...)
	filler := s.opt.Tickets - scriptedCount(sc) - projectExtraTickets
	for i := 0; i < filler; i++ {
		u := (float64(i) + s.rng.Float64()) / float64(filler)
		age := time.Duration(math.Pow(u, 1.35)*58*float64(day)) + 3*time.Hour
		plan = append(plan, scenario{name: "generated", age: age, run: nil})
	}
	sort.SliceStable(plan, func(i, j int) bool { return plan[i].age > plan[j].age })
	for _, p := range plan {
		s.start(p.age)
		var err error
		if p.run != nil {
			err = p.run()
		} else {
			err = s.generated(p.age)
		}
		if err != nil {
			return fmt.Errorf("%s (age %s): %w", p.name, p.age.Round(time.Minute), err)
		}
	}
	s.logf("demo: %d tickets created", len(s.tickets))
	return nil
}

type life struct {
	final     string
	tier      int
	replies   int
	notes     int
	csat      int
	custSolve bool
	bump      bool
	watch     bool
	cat       string // category for tickets that arrive without one
	tags      []string
}

// pickCustomerFor returns a random customer who may be the requester of a
// ticket in project (organization limits, D-20).
func (s *seeder) pickCustomerFor(project string) *person {
	keys := []string{"mia", "uma", "ben", "chloe", "gus", "hank", "ines", "jack", "ida", "ivan"}
	if project == "it" {
		keys = []string{"eva", "omar"}
	}
	for {
		c := s.custs[pick(s, keys)]
		if c.cl != nil && s.customerAllowed(c, project) {
			return c
		}
	}
}

// generated creates one random ticket whose story fits `age`.
func (s *seeder) generated(age time.Duration) error {
	// D-20: the project first, with equal weight, then a topic of it, so the
	// generated tickets split about evenly across WEB, MOB and IT.
	project := pick(s, []string{"web", "mob", "it"})
	var pool []topic
	for _, x := range topics {
		if s.projectOf(x.cat) == project {
			pool = append(pool, x)
		}
	}
	tp := pick(s, pool)
	c := s.pickCustomerFor(project)
	sp := spec{subject: tp.subject, body: tp.body, cat: tp.cat, cf: s.customerCF(tp.cat)}
	switch r := s.rng.IntN(100); {
	case r < 14:
		sp.prio = "low"
	case r < 70:
		sp.prio = "normal"
	case r < 92:
		sp.prio = "high"
	default:
		sp.prio = "urgent"
	}
	if s.rng.IntN(5) == 0 {
		sp.attach = []attach{{"screenshot.png", "image/png", screenshotPNG(s.rng.Uint64())}}
	}
	lp := life{cat: tp.cat, tier: 1, replies: s.rng.IntN(3), notes: s.rng.IntN(3), tags: topicTags(tp.cat)}
	days := float64(age) / float64(day)
	r := s.rng.IntN(100)
	switch {
	case days < 1:
		lp.final = pickByWeight(r, []string{"new", "open", "pending", "on_hold"}, []int{30, 45, 15, 10})
	case days < 6:
		lp.final = pickByWeight(r, []string{"open", "pending", "on_hold", "resolved"}, []int{30, 20, 10, 40})
	case days < 20:
		lp.final = pickByWeight(r, []string{"open", "pending", "on_hold", "closed"}, []int{15, 10, 10, 65})
	default:
		lp.final = pickByWeight(r, []string{"closed", "open", "on_hold"}, []int{90, 5, 5})
	}
	if lp.final != "new" {
		switch t := s.rng.IntN(100); {
		case t < 25:
			lp.tier = 2
		case t < 40:
			lp.tier = 3
		}
	}
	if lp.final == "resolved" || lp.final == "closed" {
		if s.rng.IntN(100) < 65 {
			lp.csat = []int{5, 4, 3, 2, 1}[indexByWeight(s.rng.IntN(100), []int{40, 25, 15, 12, 8})]
		}
		lp.custSolve = s.rng.IntN(4) == 0
	}
	lp.bump = s.rng.IntN(8) == 0
	lp.watch = s.rng.IntN(7) == 0
	// Channel mix.
	var t *tk
	var err error
	ch := s.rng.IntN(100)
	switch {
	case project == "it" && ch < 70:
		// Internal IT is staff-only (not in the portal): the IT desk files it.
		creator := s.staffBy(s.cats[tp.cat].Team, 1+s.rng.IntN(2))
		if sp.prio == "urgent" {
			creator = s.staff["sup"]
		}
		t, err = s.agentOpen(creator.cl, creator, c, sp, nil)
	case project == "it":
		t, err = s.agentOpen(s.pat, s.staff["admin"], c, sp, nil)
	case ch < 60 || (ch >= 85 && project != "web"):
		// Mail arrives on the default mailbox (Web App): other projects' topics
		// come in through the portal instead.
		t, err = s.portalOpen(c, sp)
	case ch < 75:
		creator := s.staffBy(s.cats[tp.cat].Team, 1+s.rng.IntN(2))
		cl := creator.cl
		if sp.prio == "urgent" {
			creator, cl = s.staff["sup"], s.staff["sup"].cl
		}
		t, err = s.agentOpen(cl, creator, c, sp, nil)
	case ch < 85:
		t, err = s.agentOpen(s.pat, s.staff["admin"], c, sp, nil)
	default:
		sp.cat = ""
		team := "front"
		t, err = s.emailOpen(c, sp, team)
	}
	if err != nil {
		return err
	}
	return s.lifecycle(t, lp)
}

func topicTags(cat string) []string {
	switch cat {
	case "tech":
		return []string{"incident"}
	case "billing":
		return []string{"finance"}
	case "feature":
		return []string{"feature-request"}
	case "access":
		return []string{"account"}
	case "mobile":
		return []string{"mobile"}
	case "store":
		return []string{"finance", "mobile"}
	case "hardware":
		return []string{"hardware"}
	case "onboarding":
		return []string{"onboarding"}
	}
	return nil
}

func indexByWeight(r int, w []int) int {
	sum := 0
	for i, x := range w {
		sum += x
		if r < sum {
			return i
		}
	}
	return len(w) - 1
}

func pickByWeight(r int, vals []string, w []int) string { return vals[indexByWeight(r, w)] }

// lifecycle plays a ticket's story up to its final status.
func (s *seeder) lifecycle(t *tk, lp life) error {
	if lp.final == "new" {
		return nil
	}
	s.wait(20*time.Minute, 4*time.Hour)
	sup := s.staff["sup"]
	if t.cat == "" && lp.cat != "" {
		if err := s.patch(t, sup, map[string]any{"category_id": s.cats[lp.cat].ID, "team_id": s.teams[s.cats[lp.cat].Team].ID}); err != nil {
			return err
		}
		t.cat = lp.cat
		s.wait(5*time.Minute, time.Hour)
	}
	l1 := s.staffBy(t.team, 1)
	if lp.final == "open" && s.rng.IntN(5) == 0 && t.tier == 1 && lp.tier == 1 {
		// Open but still waiting in the team queue (nobody took it yet).
		if err := s.patch(t, sup, map[string]any{"status": "open"}); err != nil {
			return err
		}
		return nil
	}
	body := map[string]any{"assignee_id": l1.id}
	if len(lp.tags) > 0 {
		body["tags"] = lp.tags
	}
	if err := s.patch(t, l1, body); err != nil {
		return err
	}
	s.wait(10*time.Minute, 2*time.Hour)
	if err := s.reply(t, l1, agentReply(s, t.req.name), ""); err != nil {
		return err
	}
	if lp.bump {
		s.wait(5*time.Minute, time.Hour)
		if err := s.patch(t, l1, map[string]any{"priority": "high"}); err != nil {
			return err
		}
	}
	for t.tier < lp.tier {
		actor := s.actor(t)
		s.wait(30*time.Minute, 5*time.Hour)
		if err := s.note(t, actor, pick(s, internalNotes)); err != nil {
			return err
		}
		s.wait(5*time.Minute, time.Hour)
		if err := s.escalate(t, actor, pick(s, escalationReasons), ""); err != nil {
			return err
		}
		s.wait(10*time.Minute, 2*time.Hour)
		next := s.staffBy(t.team, t.tier)
		if err := s.assign(t, next, next); err != nil {
			return err
		}
		s.wait(10*time.Minute, 2*time.Hour)
		if err := s.note(t, next, fmt.Sprintf("Picked this up from L%d. %s", t.tier-1, pick(s, internalNotes))); err != nil {
			return err
		}
	}
	if lp.watch {
		var col *person
		for _, d := range custDefs { // fixed order keeps the seed deterministic
			if c := s.custs[d.key]; c.org == t.req.org && c.id != t.req.id && c.cl != nil {
				col = c
				break
			}
		}
		if col != nil {
			if err := s.watch(t, s.actor(t), col); err != nil {
				return err
			}
		}
	}
	for r := 0; r < lp.replies; r++ {
		s.wait(time.Hour, 20*time.Hour)
		if err := s.reply(t, t.req, pick(s, customerReplies), ""); err != nil {
			return err
		}
		s.wait(30*time.Minute, 6*time.Hour)
		if err := s.reply(t, s.actor(t), agentReply(s, t.req.name), ""); err != nil {
			return err
		}
	}
	for n := 0; n < lp.notes; n++ {
		s.wait(20*time.Minute, 5*time.Hour)
		author := s.actor(t)
		if s.rng.IntN(3) == 0 {
			author = sup
		}
		txt := pick(s, internalNotes)
		if s.rng.IntN(3) == 0 && author.id != sup.id {
			txt += " cc " + s.mention(sup)
		}
		if err := s.note(t, author, txt); err != nil {
			return err
		}
	}
	actor := s.actor(t)
	s.wait(time.Hour, 12*time.Hour)
	switch lp.final {
	case "open":
		return s.reply(t, t.req, pick(s, customerReplies), "")
	case "pending":
		return s.reply(t, actor, "Could you please confirm whether this still happens after a restart? I will keep the ticket open until I hear from you.", "pending")
	case "on_hold":
		if err := s.note(t, actor, "Waiting for the vendor's confirmation before we can continue."); err != nil {
			return err
		}
		return s.patch(t, actor, map[string]any{"status": "on_hold"})
	case "resolved", "closed":
		if lp.custSolve && t.cat != "tech" {
			if err := t.req.cl.post("/portal/tickets/"+t.id+"/resolve", nil, nil); err != nil {
				return fmt.Errorf("customer resolve #%d: %w", t.number, err)
			}
		} else if err := s.resolve(t, actor, pick(s, resolutionTexts)); err != nil {
			return err
		}
		if lp.csat > 0 {
			if err := s.rate(t, lp.csat); err != nil {
				return err
			}
		}
		if lp.final == "closed" {
			s.wait(day, 3*day)
			return s.closeTicket(t)
		}
	}
	return nil
}
