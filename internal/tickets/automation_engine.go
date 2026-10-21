// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/automation"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// The automation engine (D-19 §A, TKT-045): a durable queue consumer that
// evaluates the enabled trigger rules of an event in position order and
// applies the actions of the matching ones under the system actor
// events.ActorAutomation.
//
//   - Loop guard: events written by automation (actor `automation`) never
//     trigger rules, so a rule that changes the priority cannot re-fire.
//   - At most automation.MaxActions actions run per source event, across all
//     rules; a rule with stop_processing ends the evaluation when it matched.
//   - One write transaction per source event, and none at all when no rule
//     matches or nothing would change. automation_runs makes a replayed event
//     (at-least-once queue) a no-op.
//   - Business failures (no eligible agent, invalid status edge, deleted
//     team) skip the action and are recorded in ticket.automation_applied;
//     only store errors are returned, so the queue retries those alone.

// updateTypes make up the rule event ticket.updated.
var updateTypes = map[string]bool{
	events.TicketStatusChanged:   true,
	events.TicketPriorityChanged: true,
	events.TicketAssigned:        true,
	events.TicketTeamChanged:     true,
	events.TicketEscalated:       true,
	events.TicketDeescalated:     true,
	events.TicketCategoryChanged: true,
	events.TicketTagsChanged:     true,
	events.TicketFieldsChanged:   true,
	events.TicketSubjectChanged:  true,
}

// triggerOf maps a stored event to the rule event it raises ("" = none).
func triggerOf(e events.Event) string {
	if e.ActorID == events.ActorAutomation {
		return "" // loop guard
	}
	switch {
	case e.Type == events.TicketCreated:
		return automation.EventTicketCreated
	case e.Type == events.MessageCreated:
		return automation.EventMessageCreated
	case updateTypes[e.Type]:
		return automation.EventTicketUpdated
	}
	return ""
}

const automationConsumer = "automation"

// registerAutomation subscribes the consumer and schedules the time rules,
// the round-robin flush and the run-marker prune.
func (m *Module) registerAutomation(q *queue.Queue) {
	q.Subscribe(automationConsumer, queue.SubscribeOptions{Durable: true, Workers: 1, Timeout: 30 * time.Second},
		func(e events.Event) bool { return triggerOf(e) != "" },
		m.svc.HandleAutomationEvent)
	q.Every("automation.time_rules", time.Hour, func(ctx context.Context) error {
		_, err := m.svc.RunTimeRules(ctx, time.Now())
		return err
	})
	q.Every("automation.rr_flush", 30*time.Second, m.svc.FlushRoundRobin)
	q.Every("automation.prune", 24*time.Hour, func(ctx context.Context) error {
		return m.svc.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM automation_runs WHERE created_at < ?`, time.Now().UTC().Add(-30*24*time.Hour))
			return err
		})
	})
}

// errRollback ends a transaction that must not commit (nothing to write, dry run).
var errRollback = errors.New("automation: rollback")

// HandleAutomationEvent is the consumer handler. It is idempotent.
func (s *Service) HandleAutomationEvent(ctx context.Context, e events.Event) error {
	trigger := triggerOf(e)
	if trigger == "" {
		return nil
	}
	rules, err := s.enabledRules(ctx, trigger)
	if err != nil || len(rules) == 0 {
		return err
	}
	q := s.db.Read()
	if trigger == automation.EventTicketUpdated {
		// One change writes several events (status + assignee, …): only the
		// last event of the burst evaluates, on the final state.
		later, err := laterSibling(ctx, q, e)
		if err != nil || later {
			return err
		}
	}
	var body string
	switch trigger {
	case automation.EventMessageCreated:
		var ref events.MessageRef
		_ = e.Decode(&ref)
		if ref.Kind != string(domain.MessagePublic) || ref.MessageID == "" {
			return nil
		}
		var kind sql.NullString
		err := q.QueryRowContext(ctx, `SELECT u.kind, m.body_md FROM ticket_messages m LEFT JOIN users u ON u.id = m.author_id WHERE m.id = ?`, ref.MessageID).
			Scan(&kind, &body)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if kind.String != string(domain.KindCustomer) {
			return nil // staff replies and automated replies never trigger
		}
	default:
		if body, err = firstPublicBody(ctx, q, e.TicketID); err != nil {
			return err
		}
	}
	// Cheap read-only pass first: no write transaction when nothing matches.
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`, e.TicketID))
	if errors.Is(err, store.ErrNotFound) {
		return nil // hard-deleted meanwhile
	}
	if err != nil {
		return err
	}
	now := s.clock()
	facts, err := s.loadFacts(ctx, q, t, body, now, false)
	if err != nil {
		return err
	}
	matched := false
	for _, r := range rules {
		if ok, _ := r.Evaluate(facts); ok {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	if err := s.warm(ctx); err != nil {
		return err
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO automation_runs (event_id, ticket_id, created_at) VALUES (?, ?, ?) ON CONFLICT (event_id) DO NOTHING`,
			e.ID, e.TicketID, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errRollback // replay of an event already applied
		}
		t, err := lockTicket(ctx, tx, e.TicketID)
		if errors.Is(err, store.ErrNotFound) {
			return errRollback
		}
		if err != nil {
			return err
		}
		run := &ruleRun{s: s, tx: tx, m: &mutation{t: t}, now: now, trigger: trigger, sourceID: e.ID, body: body}
		if err := run.exec(ctx, rules); err != nil {
			return err
		}
		if !run.changed {
			return errRollback
		}
		return s.flush(ctx, tx, run.m, events.ActorAutomation)
	})
	if errors.Is(err, errRollback) {
		return nil
	}
	return err
}

// laterSibling reports whether the same actor wrote another ticket.updated
// event for the ticket in the same change (within one second after e).
func laterSibling(ctx context.Context, q store.Querier, e events.Event) (bool, error) {
	types := make([]any, 0, len(updateTypes))
	for typ := range updateTypes {
		types = append(types, typ)
	}
	args := append([]any{e.TicketID, e.ID, e.CreatedAt.Add(time.Second), e.ActorID}, types...)
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM ticket_events WHERE ticket_id = ? AND id > ? AND created_at <= ?
		AND COALESCE(actor_id, '') = ? AND type IN (`+placeholders(len(types))+`) LIMIT 1`, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func firstPublicBody(ctx context.Context, q store.Querier, ticketID string) (string, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT body_md FROM ticket_messages WHERE ticket_id = ? AND kind = 'public' ORDER BY created_at, id LIMIT 1`, ticketID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return body, err
}

// inBusinessHours: a nil calendar (or one without hours) is 24×7.
func inBusinessHours(cal *directory.Calendar, now time.Time) bool {
	return directory.BusinessDuration(cal, now, now.Add(time.Minute)) > 0
}

// loadFacts reads what the conditions look at. statusSince also reads the
// last status change (time rules only).
func (s *Service) loadFacts(ctx context.Context, q store.Querier, t *ticketRow, body string, now time.Time, statusSince bool) (automation.Facts, error) {
	f := automation.Facts{
		Subject: t.Subject, Body: body, Channel: string(t.Channel), Priority: string(t.Priority), Status: string(t.Status),
		Tier: t.Tier, CategoryID: t.CategoryID, TeamID: t.TeamID, OrganizationID: t.OrganizationID,
		HoursSinceUpdate: now.Sub(t.UpdatedAt).Hours(),
	}
	var err error
	if f.Tags, err = ticketTags(ctx, q, t.ID); err != nil {
		return f, err
	}
	vals, err := loadFieldValues(ctx, q, []string{t.ID})
	if err != nil {
		return f, err
	}
	f.Fields = map[string]any{}
	for fid, v := range vals[t.ID] {
		if def, ok := s.dir.FieldByID(ctx, fid); ok {
			f.Fields[def.Key] = factValue(v)
		}
	}
	var email string
	if err := q.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, t.RequesterID).Scan(&email); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	if i := strings.LastIndexByte(email, '@'); i >= 0 {
		f.RequesterDomain = strings.ToLower(email[i+1:])
	}
	cal, err := s.dir.TeamCalendar(ctx, t.TeamID)
	if err != nil {
		return f, err
	}
	f.BusinessHours = inBusinessHours(cal, now)
	if statusSince {
		since, err := lastStatusChange(ctx, q, t)
		if err != nil {
			return f, err
		}
		f.HoursSinceStatus = now.Sub(since).Hours()
	}
	return f, nil
}

func factValue(v directory.FieldValue) any {
	switch {
	case v.Text != nil:
		return *v.Text
	case v.Num != nil:
		return *v.Num
	case v.Date != nil:
		return *v.Date
	}
	switch x := v.JSON.(type) {
	case bool:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// lastStatusChange is when the ticket entered its current status.
func lastStatusChange(ctx context.Context, q store.Querier, t *ticketRow) (time.Time, error) {
	var at time.Time
	err := q.QueryRowContext(ctx, `SELECT created_at FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.status_changed' ORDER BY id DESC LIMIT 1`, t.ID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return t.CreatedAt, nil
	}
	return at.UTC(), err
}

// ruleRun executes rules on one locked ticket inside a write transaction.
type ruleRun struct {
	s        *Service
	tx       *store.Tx
	m        *mutation
	now      time.Time
	trigger  string
	sourceID string
	body     string
	timed    bool // time rules: facts include hours since status
	dry      bool // dry run: no automation_applied event

	budget  int
	changed bool // at least one action applied
	results []ruleResult
}

type ruleResult struct {
	rule      *automation.Rule
	matched   bool
	conds     []bool
	outcomes  []events.AutomationAction
	truncated bool
}

func (r *ruleRun) exec(ctx context.Context, rules []*automation.Rule) error {
	if r.budget == 0 {
		r.budget = automation.MaxActions
	}
	var facts automation.Facts
	stale := true
	for _, rule := range rules {
		if stale {
			f, err := r.s.loadFacts(ctx, r.tx, r.m.t, r.body, r.now, r.timed)
			if err != nil {
				return err
			}
			facts, stale = f, false
		}
		ok, conds := rule.Evaluate(facts)
		res := ruleResult{rule: rule, matched: ok, conds: conds}
		if !ok {
			r.results = append(r.results, res)
			continue
		}
		applied := false
		for _, a := range rule.Actions {
			if r.budget == 0 {
				res.truncated = true
				break
			}
			r.budget--
			out, err := r.action(ctx, rule, a)
			if err != nil {
				return err
			}
			res.outcomes = append(res.outcomes, out)
			if out.Outcome == "applied" {
				applied, stale = true, true
			}
		}
		if applied {
			r.changed = true
			if !r.dry {
				r.m.add(events.TicketAutomationApplied, events.AutomationApplied{RuleID: rule.ID, RuleName: rule.Name, Trigger: r.trigger,
					SourceEventID: r.sourceID, Actions: res.outcomes, Truncated: res.truncated})
			}
		}
		r.results = append(r.results, res)
		if rule.StopProcessing || r.budget == 0 {
			break
		}
	}
	return nil
}

func outcome(typ, o, detail string) events.AutomationAction {
	return events.AutomationAction{Type: typ, Outcome: o, Detail: detail}
}

// skipped turns a business error into a skipped outcome; other errors abort.
func skipped(typ string, err error) (events.AutomationAction, error) {
	var pe *problems.Error
	if errors.As(err, &pe) {
		d := pe.Detail
		if d == "" && len(pe.Fields) > 0 {
			d = pe.Fields[0].Message
		}
		if d == "" {
			d = pe.Slug
		}
		return outcome(typ, "skipped", d), nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return outcome(typ, "skipped", "not found"), nil
	}
	return events.AutomationAction{}, err
}

// action applies one action to r.m.t; the events go to r.m (actor automation).
func (r *ruleRun) action(ctx context.Context, rule *automation.Rule, a automation.Action) (events.AutomationAction, error) {
	s, tx, m, t := r.s, r.tx, r.m, r.m.t
	applied := func() (events.AutomationAction, error) { return outcome(a.Type, "applied", ""), nil }
	unchanged := func() (events.AutomationAction, error) { return outcome(a.Type, "unchanged", ""), nil }
	skip := func(detail string) (events.AutomationAction, error) { return outcome(a.Type, "skipped", detail), nil }
	if t.Status == domain.StatusClosed && a.Type != automation.ActAddNote {
		return skip("the ticket is closed")
	}
	switch a.Type {
	case automation.ActSetPriority:
		pr := domain.Priority(a.Str())
		if pr == t.Priority {
			return unchanged()
		}
		m.add(events.TicketPriorityChanged, events.Change{From: string(t.Priority), To: string(pr)})
		t.Priority = pr
		if err := s.computeDue(ctx, t); err != nil {
			return events.AutomationAction{}, err
		}
		return applied()

	case automation.ActSetCategory:
		cat := a.Str()
		if cat == t.CategoryID {
			return unchanged()
		}
		if cat != "" {
			if _, err := s.dir.Category(ctx, cat); err != nil {
				return skip("unknown category")
			}
		}
		m.add(events.TicketCategoryChanged, events.Change{From: nilIfEmpty(t.CategoryID), To: nilIfEmpty(cat)})
		t.CategoryID = cat
		return applied()

	case automation.ActSetTeam:
		team := a.Str()
		if team == t.TeamID {
			return unchanged()
		}
		prev := t.AssigneeID
		if err := s.transfer(ctx, tx, m, team); err != nil {
			return skipped(a.Type, err)
		}
		if err := s.autoAssign(ctx, tx, m, prev); err != nil {
			return events.AutomationAction{}, err
		}
		return applied()

	case automation.ActSetStatus:
		return r.setStatus(ctx, a.Type, domain.TicketStatus(a.Str()))

	case automation.ActSetTier, automation.ActEscalate:
		to := t.Tier + 1
		if a.Type == automation.ActSetTier {
			to, _ = a.Int()
		}
		if to == t.Tier {
			return unchanged()
		}
		if to < domain.MinTier || to > domain.MaxTier {
			return skip(fmt.Sprintf("the ticket is already at tier %d", t.Tier))
		}
		if err := r.setTier(ctx, rule, to); err != nil {
			return events.AutomationAction{}, err
		}
		return applied()

	case automation.ActAddTag, automation.ActRemoveTag:
		cur, err := ticketTags(ctx, tx, t.ID)
		if err != nil {
			return events.AutomationAction{}, err
		}
		tag := a.Str()
		next := slices.Clone(cur)
		if a.Type == automation.ActAddTag {
			if slices.Contains(cur, tag) {
				return unchanged()
			}
			next = append(next, tag)
		} else {
			if !slices.Contains(cur, tag) {
				return unchanged()
			}
			next = slices.DeleteFunc(next, func(x string) bool { return x == tag })
		}
		from, to, err := s.setTags(ctx, tx, t, next)
		if err != nil {
			return skipped(a.Type, err)
		}
		m.add(events.TicketTagsChanged, events.Change{From: from, To: to})
		return applied()

	case automation.ActAssign:
		if a.UserID != "" {
			if a.UserID == t.AssigneeID {
				return unchanged()
			}
			st, err := loadStaff(ctx, tx, a.UserID)
			if err != nil {
				return skipped(a.Type, err)
			}
			if !satisfiesM4(st, t.TeamID, t.Tier) {
				return skip("the agent cannot own this ticket (team, tier or status)")
			}
			if err := s.setAssigneeAuto(ctx, tx, m, a.UserID); err != nil {
				return skipped(a.Type, err)
			}
			return applied()
		}
		if t.AssigneeID != "" {
			return unchanged()
		}
		ok, err := s.assignByStrategy(ctx, tx, m, a.Strategy, "")
		if err != nil {
			return events.AutomationAction{}, err
		}
		if !ok {
			return skip("no eligible agent")
		}
		return applied()

	case automation.ActAddNote:
		msg := &msgRow{TicketID: t.ID, Kind: domain.MessageInternal, Channel: domain.ChannelAgent, Body: a.BodyMD, CreatedAt: r.now}
		if err := insertMessage(ctx, tx, msg); err != nil {
			return events.AutomationAction{}, err
		}
		m.add(events.NoteCreated, map[string]any{"message_id": msg.ID, "kind": string(domain.MessageInternal)})
		return applied()

	case automation.ActSendReply:
		return r.sendReply(ctx, a)

	case automation.ActAddWatcher:
		if slices.Contains(t.Watchers, a.UserID) {
			return unchanged()
		}
		st, err := loadStaff(ctx, tx, a.UserID)
		if err != nil {
			return skipped(a.Type, err)
		}
		if st.Status != domain.UserActive {
			return skip("the user is not active")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_watchers (ticket_id, user_id, created_at) VALUES (?, ?, ?)`, t.ID, a.UserID, r.now); err != nil {
			return events.AutomationAction{}, err
		}
		t.Watchers = append(t.Watchers, a.UserID)
		m.add(events.TicketWatchersChanged, map[string]any{"added": a.UserID})
		return applied()
	}
	return skip("unknown action")
}

// setStatus moves the ticket like a staff member would (transition map, M-9
// required fields). "closed" from a working status resolves first.
func (r *ruleRun) setStatus(ctx context.Context, typ string, to domain.TicketStatus) (events.AutomationAction, error) {
	s, tx, m, t := r.s, r.tx, r.m, r.m.t
	if to == t.Status {
		return outcome(typ, "unchanged", ""), nil
	}
	steps := []domain.TicketStatus{to}
	if to == domain.StatusClosed && t.Status != domain.StatusResolved {
		steps = []domain.TicketStatus{domain.StatusResolved, domain.StatusClosed}
	}
	for from, i := t.Status, 0; i < len(steps); from, i = steps[i], i+1 {
		if !mayTrigger(actorStaff, from, steps[i]) {
			return outcome(typ, "skipped", fmt.Sprintf("%s → %s is not allowed", from, steps[i])), nil
		}
	}
	for _, st := range steps {
		from := t.Status
		if _, err := s.applyStatus(ctx, tx, t, actorStaff, st, statusOpts{}); err != nil {
			return skipped(typ, err)
		}
		m.add(events.TicketStatusChanged, statusEvent(from, st))
		if st == domain.StatusResolved {
			if err := s.onResolved(ctx, tx, m, events.ActorAutomation); err != nil {
				return events.AutomationAction{}, err
			}
		}
	}
	return outcome(typ, "applied", ""), nil
}

// setTier changes the tier like an escalation: an internal system message
// with the rule as reason, one ticket.escalated / deescalated event, the
// assignee cleared when M-4 fails at a higher tier and then re-assigned by
// the team strategy.
func (r *ruleRun) setTier(ctx context.Context, rule *automation.Rule, to int) error {
	s, tx, m, t := r.s, r.tx, r.m, r.m.t
	from := t.Tier
	t.Tier = to
	typ, verb := events.TicketEscalated, "Escalated"
	if to < from {
		typ, verb = events.TicketDeescalated, "De-escalated"
	}
	reason := "Automation rule: " + rule.Name
	var cleared string
	if to > from {
		probe := &mutation{t: t}
		if err := s.revalidateAssignee(ctx, tx, probe); err != nil {
			return err
		}
		if len(probe.events) > 0 {
			cleared = probe.events[0].data.(events.Assignment).From
		}
	}
	body := fmt.Sprintf("%s from L%d to L%d: %s", verb, from, to, reason)
	if err := insertMessage(ctx, tx, &msgRow{TicketID: t.ID, Kind: domain.MessageSystem, Channel: domain.ChannelAgent, Body: body, CreatedAt: r.now}); err != nil {
		return err
	}
	team := nilIfEmpty(t.TeamID)
	data := map[string]any{"from_tier": from, "to_tier": to, "from_team": team, "to_team": team, "reason": reason}
	if cleared != "" {
		data["unassigned"] = cleared
	}
	m.add(typ, data)
	if cleared != "" {
		return s.autoAssign(ctx, tx, m, cleared)
	}
	return nil
}

// sendReply posts a canned response as a public reply. It is an automated
// message: it updates last staff activity but never counts as the SLA first
// response and never changes the status.
func (r *ruleRun) sendReply(ctx context.Context, a automation.Action) (events.AutomationAction, error) {
	tx, m, t := r.tx, r.m, r.m.t
	var body string
	err := tx.QueryRowContext(ctx, `SELECT body_md FROM canned_responses WHERE id = ?`, a.CannedResponseID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome(a.Type, "skipped", "unknown canned response"), nil
	}
	if err != nil {
		return events.AutomationAction{}, err
	}
	refs, err := loadUserRefs(ctx, tx, []string{t.RequesterID, t.AssigneeID})
	if err != nil {
		return events.AutomationAction{}, err
	}
	name := refs[t.RequesterID].Name
	vars := map[string]string{"customer.name": name, "requester.name": name, "ticket.number": fmt.Sprint(t.Number), "agent.name": refs[t.AssigneeID].Name}
	text, err := validBody("body_md", RenderCanned(body, vars))
	if err != nil {
		return skipped(a.Type, err)
	}
	msg := &msgRow{TicketID: t.ID, Kind: domain.MessagePublic, Channel: domain.ChannelAgent, Body: text, CreatedAt: r.now}
	if err := insertMessage(ctx, tx, msg); err != nil {
		return events.AutomationAction{}, err
	}
	t.LastStaffActivityAt = sql.NullTime{Time: r.now, Valid: true}
	m.add(events.MessageCreated, events.MessageRef{MessageID: msg.ID, Kind: string(domain.MessagePublic), Channel: string(domain.ChannelAgent)})
	return outcome(a.Type, "applied", ""), nil
}

// ---- dry run (TKT-047) ----

type dryRunCond struct {
	Index   int    `json:"index"`
	Field   string `json:"field"`
	Matched bool   `json:"matched"`
}

type dryRunDTO struct {
	RuleID     string                    `json:"rule_id"`
	TicketID   string                    `json:"ticket_id"`
	Matched    bool                      `json:"matched"`
	Conditions []dryRunCond              `json:"conditions"`
	Actions    []events.AutomationAction `json:"actions"`
}

// DryRun evaluates one rule (enabled or not) against a ticket and reports
// what its actions would do. It runs the real executor in a transaction that
// is always rolled back, so the report matches a real run exactly.
func (s *Service) DryRun(ctx context.Context, ruleID, ticketID string) (*dryRunDTO, error) {
	rule, err := s.loadRule(ctx, s.db.Read(), ruleID)
	if err != nil {
		return nil, err
	}
	if !validID(ticketID) {
		return nil, problems.Validation(problems.Field("ticket_id", "not_found", "unknown ticket"))
	}
	q := s.db.Read()
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE id = ?`, ticketID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, problems.Validation(problems.Field("ticket_id", "not_found", "unknown ticket"))
	}
	body, err := firstPublicBody(ctx, q, ticketID)
	if err != nil {
		return nil, err
	}
	if rule.Event == automation.EventMessageCreated {
		_ = q.QueryRowContext(ctx, `SELECT m.body_md FROM ticket_messages m JOIN users u ON u.id = m.author_id
			WHERE m.ticket_id = ? AND m.kind = 'public' AND u.kind = 'customer' ORDER BY m.created_at DESC, m.id DESC LIMIT 1`, ticketID).Scan(&body)
	}
	if err := s.warm(ctx); err != nil {
		return nil, err
	}
	out := &dryRunDTO{RuleID: rule.ID, TicketID: ticketID, Conditions: []dryRunCond{}, Actions: []events.AutomationAction{}}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, ticketID)
		if err != nil {
			return err
		}
		run := &ruleRun{s: s, tx: tx, m: &mutation{t: t}, now: s.clock(), trigger: rule.Event, body: body,
			timed: rule.Event == automation.EventTime, dry: true}
		if err := run.exec(ctx, []*automation.Rule{rule}); err != nil {
			return err
		}
		res := run.results[0]
		out.Matched = res.matched
		for i, c := range rule.Conditions {
			out.Conditions = append(out.Conditions, dryRunCond{Index: i, Field: c.Field, Matched: res.conds[i]})
		}
		if res.outcomes != nil {
			out.Actions = res.outcomes
		}
		return errRollback
	})
	if err != nil && !errors.Is(err, errRollback) {
		return nil, err
	}
	return out, nil
}
