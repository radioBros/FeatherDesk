// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

func (s *seeder) setupViews() error {
	s.wall()
	type view struct {
		by     string
		name   string
		shared bool
		filter map[string]any
	}
	views := []view{
		{"fd1", "My open tickets", false, map[string]any{"assignee": "me", "status": []string{"open", "pending"}}},
		{"te2", "Tier 2: technical queue", false, map[string]any{"team_id": s.teams["tech"].ID, "tier": 2}},
		{"bi1", "Billing: waiting for the customer", false, map[string]any{"team_id": s.teams["billing"].ID, "status": []string{"pending"}}},
		{"sup", "Urgent and high priority", true, map[string]any{"priority": []string{"urgent", "high"}, "status": []string{"new", "open", "pending"}}},
		{"sup", "SLA breached", true, map[string]any{"breached": true}},
		{"sup", "Unassigned and open", true, map[string]any{"assignee": "none", "status": []string{"new", "open"}}},
		{"sup", "Escalated to L3", false, map[string]any{"tier": 3}},
	}
	for _, v := range views {
		if err := s.staff[v.by].cl.post("/agent/views", map[string]any{"name": v.name, "filter": v.filter, "shared": v.shared}, nil); err != nil {
			return fmt.Errorf("view %q: %w", v.name, err)
		}
	}
	return nil
}

func (s *seeder) finish() error {
	s.wall()
	if err := s.kbCounters(); err != nil {
		return fmt.Errorf("kb counters: %w", err)
	}
	// Account states that would have blocked the earlier sign-ins.
	// pending_verification only arises from self-registration (which needs
	// working email), so the row is set directly.
	if err := s.exec(`UPDATE users SET status = 'pending_verification', password_hash = NULL WHERE id = ?`, s.custs["pia"].id); err != nil {
		return fmt.Errorf("pending customer: %w", err)
	}
	if err := s.admin.patch("/agent/customers/"+s.custs["dan"].id, map[string]any{"status": "disabled"}, nil); err != nil {
		return fmt.Errorf("disabled customer: %w", err)
	}
	// SLA warnings and breaches for the tickets whose windows ran out.
	if _, err := s.svc.CheckSLA(s.ctx, time.Now()); err != nil {
		return fmt.Errorf("sla check: %w", err)
	}
	if err := s.runTimeRules(); err != nil {
		return fmt.Errorf("time rules: %w", err)
	}
	if err := s.webhookLog(); err != nil {
		return fmt.Errorf("webhook log: %w", err)
	}
	if err := s.inboundLog(); err != nil {
		return fmt.Errorf("inbound log: %w", err)
	}
	if err := s.emailSettings(); err != nil {
		return fmt.Errorf("email settings: %w", err)
	}
	// The notification centre is filled from every event above, so it runs
	// after the SLA check and the time rules, before the sessions go.
	if err := s.seedNotifications(); err != nil {
		return fmt.Errorf("notifications: %w", err)
	}
	if err := s.enrolMFA(); err != nil {
		return fmt.Errorf("two-step verification: %w", err)
	}
	// The sign-ins of the seeder must not outlive it.
	if err := s.exec(`DELETE FROM sessions`); err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	// No queue runs during seeding: build the ticket search index now and
	// move the dispatcher cursor past the seeded events, so a server started
	// on this database neither replays them (no mail, no webhooks) nor misses
	// the index.
	if _, err := tickets.Reindex(s.ctx, s.db, nil); err != nil {
		return fmt.Errorf("reindex: %w", err)
	}
	err := s.db.Tx(s.ctx, func(tx *store.Tx) error {
		var last string
		if err := tx.QueryRowContext(s.ctx, `SELECT COALESCE(MAX(id), '') FROM ticket_events`).Scan(&last); err != nil {
			return err
		}
		return store.SetCursor(s.ctx, tx, "dispatch_cursor", last)
	})
	if err != nil {
		return err
	}
	return s.a.Deps.Settings.Put(s.ctx, settingsKey, map[string]any{"seeded_at": s.now.Format(time.RFC3339), "complete": true, "tickets": len(s.tickets)}, "")
}

// webhookLog adds sample delivery rows to the (disabled) webhook; the log has
// no create API because only the delivery worker writes it.
func (s *seeder) webhookLog() error {
	rows, err := s.db.Read().QueryContext(s.ctx, `SELECT id, type FROM ticket_events WHERE type IN ('ticket.created', 'ticket.status_changed', 'message.created', 'ticket.escalated', 'sla.breached') ORDER BY id DESC LIMIT 400`)
	if err != nil {
		return err
	}
	type ev struct{ id, typ string }
	var evs []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.typ); err != nil {
			rows.Close()
			return err
		}
		evs = append(evs, e)
	}
	rows.Close()
	if len(evs) == 0 {
		return nil
	}
	return s.db.Tx(s.ctx, func(tx *store.Tx) error {
		n := min(len(evs), 9)
		for i := 0; i < n; i++ {
			e := evs[i*len(evs)/n]
			at := s.now.Add(-time.Duration(30+i*70) * time.Hour)
			var ok bool
			var status, ms any
			errText := ""
			attempt := 1
			switch i % 3 {
			case 0:
				ok, status, ms = true, 200, 80+s.rng.IntN(200)
			case 1:
				ok, status, ms, errText = false, 500, 1200+s.rng.IntN(800), "HTTP 500 from the receiver"
			default:
				ok, errText, attempt = false, "dial tcp 127.0.0.1:9: connect: connection refused", 2
			}
			if _, err := tx.ExecContext(s.ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, attempt, success, status_code, response_ms, error, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, store.NewID(), s.webhookID, e.id, e.typ, attempt, ok, status, ms, errText, at); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(s.ctx, `UPDATE webhooks SET consecutive_failures = 3, disabled_reason = ? WHERE id = ?`,
			"Demo webhook: kept disabled on purpose, so nothing is ever sent.", s.webhookID)
		return err
	})
}

// inboundLog adds the rejected side of the mailbox log (the accepted messages
// were logged next to the email tickets).
func (s *seeder) inboundLog() error {
	rows := []struct {
		ago                           time.Duration
		from, subject, status, reason string
	}{
		{9 * time.Hour, "newsletter@promo.example.net", "You won a free cruise", "quarantined", "unknown_sender"},
		{31 * time.Hour, "ceo@demo.test", "Urgent: open a ticket for me", "quarantined", "staff_spoof"},
		{52 * time.Hour, "mailer-daemon@mx.example.net", "Delivery Status Notification (Failure)", "ignored", "own_message"},
		{80 * time.Hour, "bot@alerts.example.org", "Re: alert #4412", "quarantined", "rate_limited"},
		{5 * day, "no-reply@vendor.example.com", "Auto-reply: out of office", "ignored", "blocked"},
	}
	for _, r := range rows {
		s.sim(s.now.Add(-r.ago))
		if err := s.inboundRow(store.NewID(), r.from, r.subject, r.status, r.reason, ""); err != nil {
			return err
		}
	}
	s.wall()
	return nil
}

// emailSettings configures senders and the triage mailbox; SMTP stays unset
// (email disabled) unless --smtp-mailpit points it at Mailpit.
func (s *seeder) emailSettings() error {
	var cur map[string]any
	if err := s.admin.get("/admin/email", &cur); err != nil {
		return err
	}
	cur["senders"] = map[string]any{
		"customer": map[string]any{"from_address": "support@demo.test", "from_name": "SimpleTickets Demo Support", "reply_to": "support@demo.test"},
		"staff":    map[string]any{"from_address": "desk@demo.test", "from_name": "SimpleTickets Demo Desk"},
	}
	cur["triage_mailbox"] = "triage@demo.test"
	cur["plus_addressing"] = true
	if s.opt.SMTPMailpit {
		cur["smtp"] = map[string]any{"host": "localhost", "port": 1025, "security": "none", "auth": "none", "username": "", "timeout_seconds": 10, "allow_insecure_tls": false}
	}
	return s.admin.put("/admin/email", cur, nil)
}

func (s *seeder) summary(cfg *config.Config) (*Summary, error) {
	sum := &Summary{PortalURL: cfg.PortalURL, AgentURL: cfg.AgentURL, APIToken: s.patToken, MFA: s.mfa}
	counts := []struct{ name, q string }{
		{"organizations", `SELECT COUNT(*) FROM organizations`},
		{"staff", `SELECT COUNT(*) FROM users WHERE kind = 'staff'`},
		{"customers", `SELECT COUNT(*) FROM users WHERE kind = 'customer'`},
		{"projects", `SELECT COUNT(*) FROM projects`},
		{"teams", `SELECT COUNT(*) FROM teams`},
		{"business calendars", `SELECT COUNT(*) FROM business_calendars`},
		{"categories", `SELECT COUNT(*) FROM categories`},
		{"custom fields", `SELECT COUNT(*) FROM custom_field_defs`},
		{"SLA policies", `SELECT COUNT(*) FROM sla_policies`},
		{"tickets", `SELECT COUNT(*) FROM tickets`},
		{"public messages", `SELECT COUNT(*) FROM ticket_messages WHERE kind = 'public'`},
		{"internal notes", `SELECT COUNT(*) FROM ticket_messages WHERE kind = 'internal'`},
		{"attachments", `SELECT COUNT(*) FROM attachments`},
		{"ticket events", `SELECT COUNT(*) FROM ticket_events`},
		{"ticket links", `SELECT COUNT(*) FROM ticket_links`},
		{"watchers (CC)", `SELECT COUNT(*) FROM ticket_watchers`},
		{"CSAT ratings", `SELECT COUNT(*) FROM tickets WHERE csat_score IS NOT NULL`},
		{"canned responses", `SELECT COUNT(*) FROM canned_responses`},
		{"saved views", `SELECT COUNT(*) FROM saved_views`},
		{"KB categories", `SELECT COUNT(*) FROM kb_categories`},
		{"KB articles", `SELECT COUNT(*) FROM kb_articles`},
		{"KB translations", `SELECT COUNT(*) FROM kb_article_translations`},
		{"webhooks", `SELECT COUNT(*) FROM webhooks`},
		{"webhook deliveries", `SELECT COUNT(*) FROM webhook_deliveries`},
		{"inbound mailboxes", `SELECT COUNT(*) FROM inbound_mailboxes`},
		{"inbound log entries", `SELECT COUNT(*) FROM inbound_messages`},
		{"API tokens", `SELECT COUNT(*) FROM api_tokens`},
		{"automation rules", `SELECT COUNT(*) FROM automation_rules`},
		{"automation rule runs", `SELECT COUNT(*) FROM ticket_events WHERE type = 'ticket.automation_applied'`},
		{"macros", `SELECT COUNT(*) FROM macros`},
		{"MFA-enrolled users", `SELECT COUNT(*) FROM user_mfa WHERE totp_enabled_at IS NOT NULL OR email_enabled`},
		{"privacy requests", `SELECT COUNT(*) FROM privacy_requests`},
		{"consent records", `SELECT COUNT(*) FROM user_consents`},
		{"notifications", `SELECT COUNT(*) FROM notifications`},
		{"unread notifications", `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`},
		{"audit log entries", `SELECT COUNT(*) FROM audit_log`},
	}
	for _, c := range counts {
		var n int
		if err := s.db.Read().QueryRowContext(s.ctx, c.q).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", c.name, err)
		}
		sum.Counts = append(sum.Counts, Count{c.name, n})
	}
	pw := s.opt.Password
	team := func(p *person) string {
		var n []string
		for _, k := range p.teams {
			n = append(n, s.teams[k].Name)
		}
		return strings.Join(n, ", ")
	}
	var staff []*person
	for _, p := range s.staff {
		staff = append(staff, p)
	}
	sort.Slice(staff, func(i, j int) bool {
		rank := map[string]int{"admin": 0, "supervisor": 1, "agent": 2}
		if rank[staff[i].role] != rank[staff[j].role] {
			return rank[staff[i].role] < rank[staff[j].role]
		}
		return staff[i].email < staff[j].email
	})
	for _, p := range staff {
		a := Account{Role: p.role, Email: p.email, Password: pw, Area: "agent"}
		switch {
		case p.role == "admin":
			a.Note = "break-glass: local sign-in always works"
		case p.idp:
			a.Role, a.Password, a.Note = "agent (IdP-managed)", "-", fmt.Sprintf("L%d, %s; signs in through SSO only", p.tier, team(p))
		case p.role == "supervisor":
			a.Note = "sees everything; all teams"
		default:
			a.Note = fmt.Sprintf("L%d, %s", p.tier, team(p))
		}
		if p.key == "fd2" {
			a.Note += "; marked away (no automatic assignments)"
		}
		switch p.key {
		case singleProjectKey:
			a.Note += "; works in the Mobile project only (sees no other project's tickets)"
		case projectAdminKey:
			a.Note += "; project admin of Mobile"
		}
		for _, m := range s.mfa {
			if m.Email == p.email {
				a.Note += "; two-step verification on (TOTP secret and recovery codes below)"
			}
		}
		sum.Accounts = append(sum.Accounts, a)
	}
	var custs []*person
	for _, p := range s.custs {
		custs = append(custs, p)
	}
	sort.Slice(custs, func(i, j int) bool { return custs[i].email < custs[j].email })
	for _, p := range custs {
		a := Account{Role: p.role, Email: p.email, Password: pw, Area: "portal", Note: s.orgs[p.org].Name}
		switch p.key {
		case "pia":
			a.Note += "; pending verification (cannot sign in yet)"
		case "dan":
			a.Note += "; disabled"
		}
		sum.Accounts = append(sum.Accounts, a)
	}
	if s.opt.SMTPMailpit {
		sum.MailpitURL = "http://localhost:8025"
	}
	projects, err := s.projectSummary(s.ctx)
	if err != nil {
		return nil, err
	}
	sum.Projects = projects
	sum.Notes = append(sum.Notes,
		"Staff other than the admin sign in locally only when ST_AUTH_STAFF_LOCAL=true.",
		"Restart the server after seeding: it caches settings, teams and categories in memory.",
		"The API token is shown once; the webhook (http://localhost:9/demo) and the inbound mailbox are inactive on purpose.")
	if !s.opt.SMTPMailpit {
		sum.Notes = append(sum.Notes, "Email is disabled (no SMTP host). Re-run with --smtp-mailpit --force to deliver to Mailpit on localhost:1025.")
	}
	sum.Notes = append(sum.Notes, s.warnings...)
	return sum, nil
}

// Print writes the human-readable summary.
func (s *Summary) Print(w io.Writer) {
	fmt.Fprintln(w, "\nDemo data created")
	fmt.Fprintln(w, strings.Repeat("-", 40))
	for _, c := range s.Counts {
		fmt.Fprintf(w, "  %-22s %6d\n", c.Name, c.N)
	}
	if len(s.Projects) > 0 {
		fmt.Fprintln(w, "\nProjects")
		fmt.Fprintln(w, strings.Repeat("-", 40))
		for _, p := range s.Projects {
			portal := "portal /p/" + p.Key
			if !p.PortalVisible {
				portal = "staff only"
			}
			fmt.Fprintf(w, "  %-5s %-14s %-14s %3d tickets  e.g. %s\n", p.Key, p.Name, portal, p.Tickets, strings.Join(p.SampleRefs, ", "))
		}
	}
	fmt.Fprintf(w, "\nPortal (customers): %s\nAgent panel (staff): %s\n", s.PortalURL, s.AgentURL)
	if s.MailpitURL != "" {
		fmt.Fprintf(w, "Mailpit inbox:      %s\n", s.MailpitURL)
	}
	fmt.Fprintln(w, "\nLogins")
	fmt.Fprintln(w, strings.Repeat("-", 40))
	for _, area := range []string{"agent", "portal"} {
		fmt.Fprintf(w, "  %s\n", map[string]string{"agent": "Agent panel", "portal": "Customer portal"}[area])
		for _, a := range s.Accounts {
			if a.Area == area {
				fmt.Fprintf(w, "    %-20s %-34s %-18s %s\n", a.Role, a.Email, a.Password, a.Note)
			}
		}
	}
	if s.APIToken != "" {
		fmt.Fprintf(w, "\nAPI token of the admin (shown once; scopes tickets:read, tickets:write, directory:read, kb:read):\n  %s\n", s.APIToken)
	}
	for _, m := range s.MFA {
		fmt.Fprintf(w, "\nTwo-step verification of %s (shown once):\n", m.Email)
		fmt.Fprintf(w, "  TOTP secret:    %s\n", m.Secret)
		fmt.Fprintf(w, "                  add it to an authenticator app, or print a code with: oathtool --totp -b %s\n", m.Secret)
		fmt.Fprintf(w, "  otpauth URI:    %s\n", m.OTPAuthURI)
		fmt.Fprintf(w, "  Recovery codes: %s\n", strings.Join(m.RecoveryCodes, " "))
	}
	fmt.Fprintln(w)
	for _, n := range s.Notes {
		fmt.Fprintf(w, "Note: %s\n", n)
	}
}

var _ = errors.New
