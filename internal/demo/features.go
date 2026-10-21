// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/privacy"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// MFAAccount is a demo user enrolled in two-step verification. The secret
// and the recovery codes are shown once, like the API token.
type MFAAccount struct {
	Email         string
	Secret        string // base32 TOTP secret (add it to an authenticator app)
	OTPAuthURI    string
	RecoveryCodes []string
}

// mfaUserKey is the demo user enrolled in TOTP: a staff member nobody else
// signs in as (not the admin or the supervisor the tests use, not a customer
// web/scripts/screenshots.mjs may pick).
const mfaUserKey = "te3"

// ---- automation: rules, macros, team strategies, availability ----

func (s *seeder) setupAutomation() error {
	sup, admin := s.staff["sup"], s.admin
	// Shared macros (supervisor), a month back.
	s.sim(s.now.AddDate(0, 0, -30))
	shared := []map[string]any{
		{"name": "Ask for logs", "reply_kind": "public", "status": "pending", "add_tags": []string{"waiting-logs"}, "assignee": "me",
			"reply_md": "Hi {{customer.name}},\n\nto look into ticket #{{ticket.number}} we need the application log from the time of the problem (*Help > Diagnostics > Export logs*). Please attach it to your reply.\n\nThanks,\n{{agent.name}}"},
		{"name": "Escalation handover", "reply_kind": "internal", "priority": "high", "add_tags": []string{"handover"},
			"reply_md": "**Handover**\n\n- Steps tried:\n- Customer impact:\n- Suggested next step:"},
		{"name": "Duplicate: close", "reply_kind": "public", "status": "resolved", "add_tags": []string{"duplicate"},
			"reply_md": "Hi {{customer.name}},\n\nthis request is already being handled in another ticket, so we are closing #{{ticket.number}}. You will get every update there.\n\n{{agent.name}}"},
		{"name": "Refund approved", "reply_kind": "public", "status": "pending", "add_tags": []string{"refund"}, "remove_tags": []string{"waiting-customer"},
			"reply_md": "Hi {{customer.name}},\n\nyour refund has been approved and will reach your account within 5-7 business days.\n\nKind regards,\n{{agent.name}}"},
	}
	for _, m := range shared {
		if err := sup.cl.post("/admin/macros", m, nil); err != nil {
			return fmt.Errorf("shared macro %v: %w", m["name"], err)
		}
		s.advance(7 * time.Minute)
	}
	// Personal macros of a few agents.
	personal := []struct {
		by    string
		macro map[string]any
	}{
		{"fd1", map[string]any{"name": "Password reset done", "reply_kind": "public", "status": "resolved", "add_tags": []string{"account"},
			"reply_md": "Hi {{customer.name}},\n\nI have reset your password; you will receive the link by email in a minute. Let me know if it does not arrive.\n\n{{agent.name}}"}},
		{"te2", map[string]any{"name": "VPN checklist", "reply_kind": "internal", "add_tags": []string{"vpn"}, "assignee": "me",
			"reply_md": "VPN checklist:\n\n- [ ] client version\n- [ ] certificate expiry\n- [ ] split tunnelling on/off\n- [ ] gateway in the status page"}},
		{"te3", map[string]any{"name": "Back to the L3 queue", "reply_kind": "internal", "assignee": "none", "priority": "high",
			"reply_md": "Releasing this one back to the L3 queue: I am out tomorrow."}},
		{"bi2", map[string]any{"name": "Payment reminder sent", "reply_kind": "public", "status": "pending", "add_tags": []string{"payment-reminder"},
			"reply_md": "Hi {{customer.name}},\n\na friendly reminder that the invoice from ticket #{{ticket.number}} is still open. If you have already paid, please ignore this message.\n\n{{agent.name}}"}},
	}
	for _, m := range personal {
		if err := s.staff[m.by].cl.post("/agent/macros", m.macro, nil); err != nil {
			return fmt.Errorf("personal macro %v: %w", m.macro["name"], err)
		}
		s.advance(11 * time.Minute)
	}

	// Automation rules (admin), three weeks back. Every event, enabled and
	// disabled ones; the time rules are run once by runTimeRules.
	s.sim(s.now.AddDate(0, 0, -21))
	outage := `\b(outage|down|offline|not working)\b`
	rules := []map[string]any{
		{"name": "Outage keywords: raise the priority", "event": "ticket.created", "match": "any",
			"conditions": []map[string]any{{"field": "subject", "op": "regex", "value": outage}, {"field": "body", "op": "regex", "value": outage}},
			"actions":    []map[string]any{{"type": "set_priority", "value": "high"}, {"type": "add_tag", "value": "outage"}}},
		{"name": "Key account: Acme", "event": "ticket.created",
			"conditions": []map[string]any{{"field": "requester_domain", "op": "is", "value": []string{"acme.test", "acme-corp.test"}}},
			"actions":    []map[string]any{{"type": "add_tag", "value": "key-account"}, {"type": "add_watcher", "user_id": sup.id}}},
		{"name": "Refund requests go to Billing", "event": "message.created", "stop_processing": true,
			"conditions": []map[string]any{{"field": "body", "op": "regex", "value": `\brefund(s|ed)?\b`}, {"field": "team_id", "op": "is_not", "value": s.teams["billing"].ID}},
			"actions": []map[string]any{{"type": "set_team", "value": s.teams["billing"].ID}, {"type": "add_tag", "value": "refund"},
				{"type": "add_watcher", "user_id": s.staff["bi2"].id}}},
		{"name": "Urgent technical tickets: least busy engineer", "event": "ticket.updated",
			"conditions": []map[string]any{{"field": "priority", "op": "is", "value": "urgent"}, {"field": "team_id", "op": "is", "value": s.teams["tech"].ID}},
			"actions":    []map[string]any{{"type": "assign", "strategy": "least_busy"}, {"type": "add_watcher", "user_id": sup.id}}},
		{"name": "After-hours auto-reply (draft)", "event": "ticket.created", "enabled": false,
			"conditions": []map[string]any{{"field": "business_hours", "op": "is", "value": false}, {"field": "channel", "op": "is", "value": []string{"portal", "email"}}},
			"actions":    []map[string]any{{"type": "send_reply", "canned_response_id": s.canned["Outside business hours"]}}},
		{"name": "Pending for 3 days: remind the owner", "event": "time",
			"conditions": []map[string]any{{"field": "status", "op": "is", "value": "pending"}, {"field": "hours_since_status", "op": "gte", "value": 72}},
			"actions": []map[string]any{{"type": "add_note", "body_md": "The customer has not answered for 3 days. Send a reminder, or resolve the ticket if the problem is gone."},
				{"type": "add_tag", "value": "stale"}}},
		{"name": "Close silent tickets after 7 days", "event": "time", "enabled": false,
			"conditions": []map[string]any{{"field": "status", "op": "is", "value": "pending"}, {"field": "hours_since_status", "op": "gte", "value": 168}},
			"actions": []map[string]any{{"type": "add_note", "body_md": "Closed automatically: no answer from the customer for 7 days."},
				{"type": "set_status", "value": "closed"}}},
		{"name": "Urgent L1 tickets: escalate after 4 hours", "event": "time", "enabled": false,
			"conditions": []map[string]any{{"field": "priority", "op": "is", "value": "urgent"}, {"field": "tier", "op": "is", "value": 1},
				{"field": "hours_since_update", "op": "gte", "value": 4}},
			"actions": []map[string]any{{"type": "escalate"}}},
	}
	for _, r := range rules {
		if err := admin.post("/admin/automation/rules", r, nil); err != nil {
			return fmt.Errorf("automation rule %q: %w", r["name"], err)
		}
		s.advance(9 * time.Minute)
	}

	// Team strategies, set only now: on the scripted tickets they would have
	// auto-assigned the unassigned ones. A team PATCH replaces every field.
	strategy := map[string]string{"front": "round_robin", "tech": "least_busy"}
	for _, d := range teamDefs {
		st, ok := strategy[d.key]
		if !ok {
			continue
		}
		body := map[string]any{"name": d.name, "description": d.desc, "notification_email": d.email, "notify_mode": d.mode,
			"calendar_id": s.cals[d.cal], "assignment_strategy": st}
		if err := admin.patch("/admin/teams/"+s.teams[d.key].ID, body, nil); err != nil {
			return fmt.Errorf("team %s strategy: %w", d.key, err)
		}
	}
	// One front-desk agent is away: round robin skips them.
	s.sim(s.now.Add(-20 * time.Hour))
	return s.staff["fd2"].cl.patch("/me/availability", map[string]any{"away": true}, nil)
}

// runTimeRules runs the hourly time-rule check once (the scheduler's own
// entry point), so the enabled "pending for 3 days" rule leaves real
// ticket.automation_applied entries. It only adds a note and a tag.
func (s *seeder) runTimeRules() error {
	s.sim(s.now.Add(-25 * time.Minute))
	defer s.wall()
	_, err := s.svc.RunTimeRules(s.ctx, s.cur())
	return err
}

// ---- privacy: settings, consents, exports, erasure requests ----

func (s *seeder) setupPrivacy() error {
	admin := s.admin
	// Retention: nothing in two months of demo history qualifies, so the
	// daily job never touches the demo data. Erasure stays an admin decision.
	s.sim(s.now.AddDate(0, 0, -63))
	err := admin.put("/admin/privacy/settings", map[string]any{
		"retention": map[string]any{"closed_tickets_months": 24, "closed_tickets_action": "anonymize", "inbound_email_days": 90,
			"audit_log_months": 12, "export_days": 14},
		"erasure_auto_approve_days": 0, "erasure_auto_approve_mode": "anonymize", "policy_version": "2026-08-01",
	}, nil)
	if err != nil {
		return fmt.Errorf("privacy settings: %w", err)
	}
	// Consent records: only portal self-registration writes them, and that
	// needs working email; the demo customers are created by staff. Record
	// them with the registration hook's own function, as if these customers
	// had registered through the portal.
	for i, k := range []string{"chloe", "ivan", "hank", "pia"} {
		p := s.custs[k]
		s.sim(s.now.AddDate(0, 0, -62).Add(time.Duration(i) * 97 * time.Minute))
		err := s.db.Tx(s.ctx, func(tx *store.Tx) error {
			return privacy.RecordConsent(s.ctx, tx, p.id, "https://demo.example.com/privacy", fmt.Sprintf("203.0.113.%d", 20+i))
		})
		if err != nil {
			return fmt.Errorf("consent %s: %w", k, err)
		}
	}
	// Exports, produced at once by the export job's own entry point (the
	// seeder never starts the queue, which would otherwise run them).
	exports := []struct {
		ago      time.Duration
		by, user *person
		admin    bool
	}{
		{6 * day, s.staff["admin"], s.custs["hank"], true},
		{3 * day, s.custs["uma"], s.custs["uma"], false},
		{26 * time.Hour, s.staff["bi1"], s.staff["bi1"], false},
	}
	for _, e := range exports {
		s.sim(s.now.Add(-e.ago))
		var req idResp
		var err error
		if e.admin {
			err = e.by.cl.post("/admin/privacy/users/"+e.user.id+"/export", map[string]any{"include_internal": true}, &req)
		} else {
			err = e.by.cl.post("/me/privacy/export", nil, &req)
		}
		if err != nil {
			return fmt.Errorf("export of %s: %w", e.user.email, err)
		}
		s.advance(2 * time.Minute)
		if err := s.privacyMod.RunExport(s.ctx, req.ID); err != nil {
			return fmt.Errorf("export of %s: %w", e.user.email, err)
		}
	}
	// Erasure: one request rejected by the admin, one waiting for a decision.
	s.sim(s.now.Add(-9 * day))
	var rejected idResp
	if err := s.custs["ivan"].cl.post("/me/privacy/erasure", map[string]any{"reason": "I no longer work with Initech's systems. Please delete my account and my data."}, &rejected); err != nil {
		return fmt.Errorf("erasure request: %w", err)
	}
	s.advance(20 * time.Hour)
	if err := admin.post("/admin/privacy/requests/"+rejected.ID+"/reject", map[string]any{
		"reason": "Initech's support contract is still active and your account is its technical contact. Please ask your organization manager to name a new contact first."}, nil); err != nil {
		return fmt.Errorf("reject erasure: %w", err)
	}
	s.sim(s.now.Add(-2 * day))
	if err := s.custs["jack"].cl.post("/me/privacy/erasure", map[string]any{"reason": "I am leaving Globex at the end of the month. Please delete my account."}, nil); err != nil {
		return fmt.Errorf("erasure request: %w", err)
	}
	return nil
}

// ---- in-app notification centre ----

// notifReaders mark everything read as of a day and a half ago (read-all);
// the others never opened the bell. A few then read single newer ones.
var (
	notifReaders = []string{"admin", "sup", "fd1", "fd3", "te1", "te2", "te3", "bi1", "bi2", "mia", "uma", "ben", "gus", "ida", "ines"}
	notifSingle  = []string{"sup", "te2", "bi2", "mia", "gus"}
)

// seedNotifications replays every seeded ticket event through the notify
// consumer's in-app path (notify.Module.DeliverInApp: same recipients, same
// rows, no email), each at its own simulated time, then reads some through
// the /me/notifications API.
func (s *seeder) seedNotifications() error {
	if s.notifyMod == nil {
		return errors.New("notify module missing from the app")
	}
	defer s.wall()
	var evs []events.Event
	for cursor := ""; ; {
		page, err := events.ReadAfter(s.ctx, s.db.Read(), cursor, 1000)
		if err != nil {
			return err
		}
		evs = append(evs, page...)
		if len(page) < 1000 {
			break
		}
		cursor = page[len(page)-1].ID
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].CreatedAt.Before(evs[j].CreatedAt) })
	cutoff := s.now.Add(-36 * time.Hour)
	deliver := func(e events.Event) error {
		s.sim(e.CreatedAt)
		if err := s.notifyMod.DeliverInApp(s.ctx, e); err != nil {
			return fmt.Errorf("notifications of event %s: %w", e.ID, err)
		}
		return nil
	}
	i := 0
	for ; i < len(evs) && !evs[i].CreatedAt.After(cutoff); i++ {
		if err := deliver(evs[i]); err != nil {
			return err
		}
	}
	s.sim(cutoff.Add(time.Minute))
	for _, k := range notifReaders {
		if err := s.personByKey(k).cl.post("/me/notifications/read-all", nil, nil); err != nil {
			return fmt.Errorf("read-all %s: %w", k, err)
		}
	}
	for ; i < len(evs); i++ {
		if err := deliver(evs[i]); err != nil {
			return err
		}
	}
	// Single reads now (after every notification, whatever the clock).
	s.wall()
	for _, k := range notifSingle {
		c := s.personByKey(k).cl
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := c.get("/me/notifications?unread=true&limit=2", &list); err != nil {
			return fmt.Errorf("notifications of %s: %w", k, err)
		}
		if len(list.Items) > 0 {
			if err := c.post("/me/notifications/"+url.PathEscape(list.Items[0].ID)+"/read", nil, nil); err != nil {
				return fmt.Errorf("read notification of %s: %w", k, err)
			}
		}
	}
	return nil
}

func (s *seeder) personByKey(k string) *person {
	if p, ok := s.staff[k]; ok {
		return p
	}
	return s.custs[k]
}

// ---- two-step verification ----

// enrolMFA turns on TOTP for one staff member through the real /me/mfa API,
// computing the confirmation code like an authenticator app. It runs last
// (authn works on the wall clock, and the seeder never signs this user in
// again: their session predates the enrolment).
func (s *seeder) enrolMFA() error {
	s.wall()
	p := s.staff[mfaUserKey]
	if err := p.cl.post("/me/mfa/reauth", map[string]string{"password": s.opt.Password}, nil); err != nil {
		return fmt.Errorf("reauth: %w", err)
	}
	var setup struct {
		Secret     string `json:"secret"`
		OTPAuthURI string `json:"otpauth_uri"`
	}
	if err := p.cl.post("/me/mfa/totp/setup", nil, &setup); err != nil {
		return fmt.Errorf("totp setup: %w", err)
	}
	code, err := authn.TOTPCode(setup.Secret, time.Now())
	if err != nil {
		return err
	}
	var res struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := p.cl.post("/me/mfa/totp/confirm", map[string]string{"code": code}, &res); err != nil {
		return fmt.Errorf("totp confirm: %w", err)
	}
	s.mfa = append(s.mfa, MFAAccount{Email: p.email, Secret: setup.Secret, OTPAuthURI: setup.OTPAuthURI, RecoveryCodes: res.RecoveryCodes})
	return nil
}
