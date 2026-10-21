// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// scenarios are the scripted stories. Together they cover every status,
// priority, channel, tier, link type and the special cases (merge, split,
// follow-up, SLA warning and breach, CSAT, mentions, watchers, attachments).
// Each story starts `age` before now and runs on the simulated clock.
func (s *seeder) scenarios() []scenario {
	st := func(k string) *person { return s.staff[k] }
	cu := func(k string) *person { return s.custs[k] }
	min := time.Minute
	var relA, relB, fuOrig *tk

	return []scenario{
		{"new ticket with attachments", 2 * time.Hour, 1, func() error {
			_, err := s.portalOpen(cu("ben"), spec{subject: "Export to CSV fails with a timeout", cat: "tech", prio: "normal",
				body:   "Exporting more than 5,000 rows to CSV ends with *Request timed out*. The log and a screenshot of the error are attached.",
				cf:     map[string]any{"os": "windows", "affected_users": 4, "error_message": "Request timed out after 60 s", "affected_systems": []string{"crm"}, "blocking_work": true},
				attach: []attach{{"error-screenshot.png", "image/png", screenshotPNG(1)}, {"export.log", "text/plain", logText(1, "export")}}})
			return err
		}},
		{"triage: portal, no category", 5 * time.Hour, 1, func() error {
			_, err := s.portalOpen(cu("hank"), spec{subject: "Quick question about our licences", prio: "normal",
				body: "We are not sure which team to ask: how many seats does our current plan include?"})
			return err
		}},
		{"triage: email", 26 * time.Hour, 1, func() error {
			_, err := s.emailOpen(cu("jack"), spec{subject: "Contract renewal and new office address",
				body: "Hello,\n\nour contract renews next month and we are moving offices. Who should we talk to?\n\nJack Jones\nGlobex"}, "")
			return err
		}},
		{"email ticket, emailed reply", 3 * day, 1, func() error {
			t, err := s.emailOpen(cu("chloe"), spec{subject: "Cannot sign in to the portal",
				body: "Hi,\n\nI cannot sign in since this morning. The page just says *invalid credentials*.\n\nChloe"}, "front")
			if err != nil {
				return err
			}
			s.wait(20*min, 2*time.Hour)
			if err := s.patch(t, st("fd1"), map[string]any{"category_id": s.cats["access"].ID, "assignee_id": st("fd1").id}); err != nil {
				return err
			}
			t.cat = "access"
			if err := s.reply(t, st("fd1"), "Hi Chloe, I have checked your account and it is active. Could you try the **Forgot password** link and tell me whether the email arrives?", "pending"); err != nil {
				return err
			}
			s.wait(3*time.Hour, 20*time.Hour)
			if err := s.emailReply(t, "Thanks Fiona, the reset email arrived and I could sign in again.\n\n> Hi Chloe, I have checked your account ..."); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			return s.reply(t, st("fd1"), "Great, glad it works. I will leave the ticket open for a day in case it comes back.", "pending")
		}},
		{"agent channel: phone call, on hold", 4 * day, 1, func() error {
			t, err := s.agentOpen(st("fd2").cl, st("fd2"), cu("ida"), spec{subject: "Phone call: invoices do not print correctly",
				body: "Ida called: the PDF invoices print with a cut-off right margin. She will check with her accountant which printer driver they use.", cat: "billing", prio: "normal"}, st("bi1"))
			if err != nil {
				return err
			}
			s.wait(time.Hour, 5*time.Hour)
			if err := s.note(t, st("bi1"), "Waiting for Ida's accountant to confirm the driver version."); err != nil {
				return err
			}
			return s.patch(t, st("bi1"), map[string]any{"status": "on_hold", "tags": []string{"phone", "printing"}})
		}},
		{"api channel", 2 * day, 1, func() error {
			t, err := s.agentOpen(s.pat, st("admin"), cu("ivan"), spec{subject: "Monitoring alert: webhook latency above threshold",
				body: "**Automated alert** from the status monitor.\n\n- check: webhook delivery latency\n- value: 8.2 s (threshold 3 s)\n- since: 06:40 UTC", cat: "tech", prio: "high",
				tags: []string{"monitoring", "api"}}, st("te2"))
			if err != nil {
				return err
			}
			s.wait(10*min, time.Hour)
			return s.note(t, st("te2"), "Latency correlates with the nightly import. Raising it with the platform team.")
		}},
		{"urgent outage, escalated twice", 20 * time.Hour, 1, func() error {
			t, err := s.portalOpen(cu("hank"), spec{subject: "Production outage: orders cannot be submitted", cat: "tech", prio: "urgent",
				body: "Since 08:15 nobody on our team can submit orders. The ERP shows **HTTP 500** on every submit. About 140 people are blocked.",
				cf:   map[string]any{"os": "linux", "affected_users": 140, "affected_systems": []string{"erp", "crm"}, "blocking_work": true, "error_message": "HTTP 500 on POST /orders"}})
			if err != nil {
				return err
			}
			s.wait(3*min, 8*min)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			s.wait(5*min, 10*min)
			if err := s.reply(t, st("te1"), "Hi Hank, I see it and I am on it right now. I will update you every 30 minutes.", ""); err != nil {
				return err
			}
			s.wait(10*min, 20*min)
			if err := s.note(t, st("te1"), "Order service returns 500 after the 08:10 deploy. @tina.torres can you check the config rollout?"); err != nil {
				return err
			}
			if err := s.escalate(t, st("te1"), "Production outage after a deploy; needs access to the production configuration.", ""); err != nil {
				return err
			}
			s.wait(5*min, 15*min)
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			s.wait(15*min, 30*min)
			if err := s.note(t, st("te2"), fmt.Sprintf("Config is fine, the failure is in the queue consumer. %s %s please join.", s.mention(st("te3")), s.mention(st("sup")))); err != nil {
				return err
			}
			if err := s.escalate(t, st("te2"), "Suspected defect in the queue consumer; engineering has to read the production logs.", ""); err != nil {
				return err
			}
			s.wait(5*min, 15*min)
			if err := s.assign(t, st("te3"), st("te3")); err != nil {
				return err
			}
			if err := s.setFields(t, st("te3"), map[string]any{"assigned_engineer": "Tomas Tran"}); err != nil {
				return err
			}
			if err := s.watch(t, st("te3"), cu("ines")); err != nil {
				return err
			}
			if err := s.watch(t, st("te3"), st("sup")); err != nil {
				return err
			}
			s.wait(20*min, 40*min)
			if err := s.reply(t, st("te3"), "Update: we found a defect in the queue consumer and are preparing a hotfix. Next update in 30 minutes.", ""); err != nil {
				return err
			}
			s.wait(10*min, 30*min)
			return s.reply(t, cu("hank"), "Thank you, please keep us posted. We are still fully blocked.", "")
		}},
		{"de-escalated, resolved, CSAT 5", 84 * time.Hour, 1, func() error {
			t, err := s.portalOpen(cu("chloe"), spec{subject: "Single sign-on loops back to the sign-in page", cat: "tech", prio: "high",
				body: "After entering my credentials at the identity provider I land on the sign-in page again. My colleagues can sign in.",
				cf:   map[string]any{"os": "macos", "affected_users": 1, "affected_systems": []string{"vpn"}}})
			if err != nil {
				return err
			}
			s.wait(10*min, time.Hour)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			if err := s.reply(t, st("te1"), "Hi Chloe, thanks. I am checking your account and the identity provider settings.", ""); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.escalate(t, st("te1"), "Account looks correct; the loop points at the SSO configuration.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.escalate(t, st("te2"), "Needs the identity provider admin console, which only L3 can open.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("te3"), st("te3")); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.note(t, st("te3"), "Root cause: the redirect URI of the portal was registered with a trailing slash."); err != nil {
				return err
			}
			if err := s.deescalate(t, st("te3"), "Root cause found (redirect URI); rollout is routine, back to L2."); err != nil {
				return err
			}
			if err := s.assign(t, st("te3"), st("te2")); err != nil {
				return err
			}
			s.wait(time.Hour, 2*time.Hour)
			if err := s.resolve(t, st("te2"), "We corrected the redirect URI of the single sign-on application. Please try signing in again; I am marking this as resolved."); err != nil {
				return err
			}
			return s.rate(t, 5)
		}},
		{"SLA warning (first response at 87 %)", 105 * min, 1, func() error {
			_, err := s.portalOpen(cu("jack"), spec{subject: "Where can I find the user manual?", cat: "general", prio: "high",
				body: "A PDF version of the manual would be great: we are onboarding three new people this afternoon."})
			return err
		}},
		{"SLA breached (first response)", 7 * time.Hour, 1, func() error {
			_, err := s.portalOpen(cu("ines"), spec{subject: "Opening hours over the holidays", cat: "general", prio: "high",
				body: "Will support be available between Christmas and New Year? We have a release planned for the 28th."})
			return err
		}},
		{"SLA breached (resolution), open", 8 * day, 1, func() error {
			t, err := s.portalOpen(cu("ben"), spec{subject: "Dashboard is very slow when filtering by date", cat: "tech", prio: "high",
				body: "Filtering by *last 90 days* takes more than a minute. Shorter ranges are fine.", cf: s.customerCF("tech")})
			if err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			if err := s.reply(t, st("te1"), "Hi Ben, I can reproduce the slowdown. I am looking at the query plan.", ""); err != nil {
				return err
			}
			s.wait(day, 2*day)
			if err := s.escalate(t, st("te1"), "Needs a query optimisation in the reporting service.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			s.wait(day, 2*day)
			return s.reply(t, cu("ben"), "Any news? The dashboard is still slow for our whole team.", "")
		}},
		{"on hold (vendor)", 8 * day, 1, func() error {
			t, err := s.portalOpen(cu("uma"), spec{subject: "Email sync stopped on Android", cat: "tech", prio: "normal",
				body: "The mail app on my phone has not synced since yesterday. Webmail works.", cf: map[string]any{"os": "android", "affected_systems": []string{"email"}}})
			if err != nil {
				return err
			}
			s.wait(time.Hour, 4*time.Hour)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			if err := s.reply(t, st("te1"), "Hi Uma, thanks. This looks like a problem with the mobile gateway of our mail provider. I opened a case with them.", ""); err != nil {
				return err
			}
			s.wait(time.Hour, 5*time.Hour)
			if err := s.note(t, st("te1"), "Vendor case #88217 opened. They promise an answer within 3 business days."); err != nil {
				return err
			}
			return s.patch(t, st("te1"), map[string]any{"status": "on_hold"})
		}},
		{"pending customer reply (billing)", 2 * day, 1, func() error {
			t, err := s.portalOpen(cu("gus"), spec{subject: "Charged twice for the October subscription", cat: "billing", prio: "high",
				body: "Our card was charged twice on 3 October. Please refund the duplicate payment.", cf: s.customerCF("billing")})
			if err != nil {
				return err
			}
			s.wait(20*min, time.Hour)
			if err := s.assign(t, st("bi1"), st("bi1")); err != nil {
				return err
			}
			s.wait(10*min, time.Hour)
			return s.reply(t, st("bi1"), "Hi Gus, I see two charges on the account. Could you send me a screenshot of the card statement so I can match the payment references?", "pending")
		}},
		{"resolved, refund, CSAT 5", 3 * day, 1, func() error {
			t, err := s.portalOpen(cu("ida"), spec{subject: "Refund request for unused licences", cat: "billing", prio: "normal",
				body: "We cancelled 12 licences in September and were still billed for them. Can you credit us?", cf: s.customerCF("billing")})
			if err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.assign(t, st("bi1"), st("bi1")); err != nil {
				return err
			}
			if err := s.reply(t, st("bi1"), "Hi Ida, you are right. I am preparing the credit note now.", ""); err != nil {
				return err
			}
			s.wait(2*time.Hour, 6*time.Hour)
			if err := s.setFields(t, st("bi1"), map[string]any{"refund_amount": 1188.0, "internal_ref": "CN-2026-0173"}); err != nil {
				return err
			}
			if err := s.resolve(t, st("bi1"), "The credit note over EUR 1,188.00 is issued and will appear on your next invoice. I am marking this as resolved."); err != nil {
				return err
			}
			return s.rate(t, 5)
		}},
		{"resolved, CSAT 2", 5 * day, 1, func() error {
			t, err := s.portalOpen(cu("ivan"), spec{subject: "Change the company name in our profile", cat: "general", prio: "low",
				body: "We were renamed after a merger. How can we update the company name shown on tickets?"})
			if err != nil {
				return err
			}
			s.wait(18*time.Hour, 22*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.resolve(t, st("fd1"), "Hi Ivan, I have updated the organization name. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			return s.csat(t, 2, "Took too long to get a first answer.")
		}},
		{"closed with attachments, CSAT 4", 36 * day, 1, func() error {
			t, err := s.portalOpen(cu("uma"), spec{subject: "App crashes on start after update 4.2", cat: "tech", prio: "high",
				body:   "Since version 4.2 the desktop app closes right after the splash screen. Reinstalling did not help. Crash report and screenshot attached.",
				cf:     map[string]any{"os": "windows", "affected_users": 3, "error_message": "Faulting module: ntdll.dll"},
				attach: []attach{{"crash-screenshot.png", "image/png", screenshotPNG(2)}, {"crash-report.txt", "text/plain", logText(2, "desktop")}}})
			if err != nil {
				return err
			}
			s.wait(20*min, 2*time.Hour)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			if err := s.reply(t, st("te1"), "Hi Uma, thanks for the crash report. I can see the faulting module and am checking it with a colleague.", ""); err != nil {
				return err
			}
			s.wait(time.Hour, 5*time.Hour)
			if err := s.escalate(t, st("te1"), "Crash in a native module; needs a specialist.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			s.wait(5*time.Hour, 20*time.Hour)
			if err := s.resolve(t, st("te2"), "This is fixed in version 4.2.2. Please update and tell me if the crash is gone. Marking this as resolved."); err != nil {
				return err
			}
			if err := s.rate(t, 4); err != nil {
				return err
			}
			s.wait(2*day, 3*day)
			return s.closeTicket(t)
		}},
		{"closed, CSAT 3", 29 * day, 1, func() error {
			t, err := s.portalOpen(cu("gus"), spec{subject: "Invoice shows the wrong VAT number", cat: "billing", prio: "normal",
				body: "The VAT number on invoice INV-2026-0412 is outdated. Please reissue it with the number from our profile.", cf: s.customerCF("billing")})
			if err != nil {
				return err
			}
			s.wait(30*min, 3*time.Hour)
			if err := s.assign(t, st("bi1"), st("bi1")); err != nil {
				return err
			}
			if err := s.reply(t, st("bi1"), "Hi Gus, I will reissue the invoice today.", ""); err != nil {
				return err
			}
			s.wait(day, 2*day)
			if err := s.resolve(t, st("bi1"), "The corrected invoice has been sent. Marking this as resolved."); err != nil {
				return err
			}
			if err := s.csat(t, 3, "It worked out in the end."); err != nil {
				return err
			}
			s.wait(2*day, 4*day)
			return s.closeTicket(t)
		}},
		{"merged duplicates", 6 * day, 2, func() error {
			a, err := s.portalOpen(cu("ben"), spec{subject: "VPN is down", cat: "tech", prio: "normal",
				body: "The VPN does not connect since this morning.", cf: map[string]any{"os": "windows", "affected_systems": []string{"vpn"}}})
			if err != nil {
				return err
			}
			s.wait(20*min, 30*min)
			b, err := s.portalOpen(cu("mia"), spec{subject: "Cannot connect to the VPN", cat: "tech", prio: "normal",
				body: "Is the VPN down? I cannot connect from home.", cf: map[string]any{"os": "macos", "affected_systems": []string{"vpn"}}})
			if err != nil {
				return err
			}
			s.wait(10*min, time.Hour)
			if err := s.assign(a, st("te1"), st("te1")); err != nil {
				return err
			}
			if err := s.reply(a, st("te1"), "Hi Ben, we know about the VPN problem and are working on it.", ""); err != nil {
				return err
			}
			s.wait(10*min, 30*min)
			if err := s.note(b, st("sup"), "Duplicate of the VPN outage ticket; merging."); err != nil {
				return err
			}
			var out outcomeResp
			if err := st("sup").cl.post("/agent/tickets/"+b.id+"/merge", map[string]any{"into_ticket_id": a.id}, &out); err != nil {
				return fmt.Errorf("merge: %w", err)
			}
			s.track(b, out.Ticket)
			s.wait(2*time.Hour, 6*time.Hour)
			return s.reply(a, st("te1"), "The VPN gateway was restarted. Please reconnect and let me know if it works.", "pending")
		}},
		{"split into a child ticket", 11 * day, 2, func() error {
			t, err := s.portalOpen(cu("uma"), spec{subject: "Printer offline and a question about invoices", cat: "general", prio: "normal",
				body: "The printer on the 2nd floor shows as offline for everyone."})
			if err != nil {
				return err
			}
			s.wait(10*min, 20*min)
			if err := s.reply(t, cu("uma"), "Also: could you send me a copy of the September invoice?", ""); err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.reply(t, st("fd1"), "Hi Uma, I will look at the printer first. For the invoice I am opening a separate ticket so it does not get lost.", ""); err != nil {
				return err
			}
			var detail ticketResp
			if err := st("sup").cl.get("/agent/tickets/"+t.id, &detail); err != nil {
				return err
			}
			var invoiceMsg string
			for _, m := range detail.Messages {
				if m.Kind == "public" && len(m.Body) > 0 && m.Body[:4] == "Also" {
					invoiceMsg = m.ID
				}
			}
			if invoiceMsg == "" {
				return fmt.Errorf("split: invoice message not found")
			}
			var child ticketResp
			if err := st("sup").cl.post("/agent/tickets/"+t.id+"/split", map[string]any{"message_ids": []string{invoiceMsg}, "subject": "Copy of the September invoice"}, &child); err != nil {
				return fmt.Errorf("split: %w", err)
			}
			c := &tk{subject: "Copy of the September invoice", req: cu("uma"), created: s.cur()}
			s.track(c, child)
			s.tickets = append(s.tickets, c)
			s.wait(20*min, time.Hour)
			if err := s.patch(c, st("sup"), map[string]any{"category_id": s.cats["billing"].ID, "team_id": s.teams["billing"].ID}); err != nil {
				return err
			}
			c.cat = "billing"
			s.wait(20*min, time.Hour)
			if err := s.assign(c, st("bi1"), st("bi1")); err != nil {
				return err
			}
			if err := s.resolve(c, st("bi1"), "Hi Uma, the September invoice is attached to this thread by email. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(3*time.Hour, 8*time.Hour)
			if err := s.resolve(t, st("fd1"), "The printer is back online (the print server needed a restart). Marking this as resolved."); err != nil {
				return err
			}
			s.wait(2*day, 3*day)
			if err := s.closeTicket(t); err != nil {
				return err
			}
			return s.closeTicket(c)
		}},
		{"related: earlier incident (closed)", 20 * day, 1, func() error {
			t, err := s.portalOpen(cu("ivan"), spec{subject: "Export to CSV fails", cat: "tech", prio: "normal",
				body: "Exports with more than 5,000 rows end with an error.", cf: s.customerCF("tech")})
			if err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.assign(t, st("te1"), st("te1")); err != nil {
				return err
			}
			s.wait(time.Hour, 5*time.Hour)
			if err := s.resolve(t, st("te1"), "We raised the export limit; please retry. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(3*day, 5*day)
			if err := s.closeTicket(t); err != nil {
				return err
			}
			relA = t
			return nil
		}},
		{"related: the same issue again", 4 * day, 1, func() error {
			t, err := s.portalOpen(cu("ivan"), spec{subject: "CSV export fails again", cat: "tech", prio: "high",
				body: "The CSV export is failing again, this time already at 3,000 rows.", cf: s.customerCF("tech")})
			if err != nil {
				return err
			}
			s.wait(20*min, time.Hour)
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			if err := s.link(t, relA, st("te2"), "related_to"); err != nil {
				return err
			}
			if err := s.note(t, st("te2"), fmt.Sprintf("Same symptom as #%d. The earlier fix only raised the limit.", relA.number)); err != nil {
				return err
			}
			relB = t
			return s.reply(t, st("te2"), "Hi Ivan, this is related to your earlier report. I am looking into the root cause now.", "")
		}},
		{"duplicate_of link", 90 * time.Hour, 1, func() error {
			t, err := s.portalOpen(cu("hank"), spec{subject: "CSV export is not working", cat: "tech", prio: "normal",
				body: "We cannot export reports to CSV since yesterday.", cf: s.customerCF("tech")})
			if err != nil {
				return err
			}
			s.wait(20*min, time.Hour)
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			if err := s.resolve(t, st("te2"), "Hi Hank, this is the same problem as in another open ticket; we handle it there and will update you by email. Closing this one as a duplicate."); err != nil {
				return err
			}
			s.wait(10*min, time.Hour)
			if err := s.closeTicket(t); err != nil {
				return err
			}
			return s.link(t, relB, st("sup"), "duplicate_of")
		}},
		{"closed ticket, later follow-up (1/2)", 48 * day, 1, func() error {
			t, err := s.portalOpen(cu("chloe"), spec{subject: "Where can I download the data processing agreement?", cat: "general", prio: "low",
				body: "Our legal team asks for the signed data processing agreement as PDF."})
			if err != nil {
				return err
			}
			s.wait(2*time.Hour, 6*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.resolve(t, st("fd1"), "The signed agreement is attached to the email I just sent you. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(day, 3*day)
			if err := s.closeTicket(t); err != nil {
				return err
			}
			fuOrig = t
			return nil
		}},
		{"closed ticket, later follow-up (2/2)", 30 * day, 1, func() error {
			// A reply to a closed ticket opens a follow-up ticket; the API
			// answers 409 and names it (followup_ticket_id).
			err := cu("chloe").cl.post("/portal/tickets/"+fuOrig.id+"/messages",
				map[string]any{"body_md": "Hello again: legal also needs the sub-processor list that goes with the agreement. Can you send that as well?"}, nil)
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != http.StatusConflict {
				return fmt.Errorf("reply to the closed ticket should answer 409 with a follow-up: %v", err)
			}
			var id string
			if err := s.db.Read().QueryRowContext(s.ctx, `SELECT id FROM tickets WHERE followup_of_id = ?`, fuOrig.id).Scan(&id); err != nil {
				return fmt.Errorf("follow-up of #%d not found: %w", fuOrig.number, err)
			}
			var r ticketResp
			if err := st("sup").cl.get("/agent/tickets/"+id, &r); err != nil {
				return err
			}
			f := &tk{subject: "follow-up", req: cu("chloe"), cat: "general", created: s.cur()}
			s.track(f, r)
			s.tickets = append(s.tickets, f)
			s.wait(time.Hour, 5*time.Hour)
			if f.team == "" {
				f.team = "front"
			}
			if err := s.assign(f, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.resolve(f, st("fd1"), "Sent. Marking this follow-up as resolved."); err != nil {
				return err
			}
			s.wait(day, 3*day)
			return s.closeTicket(f)
		}},
		{"CC watchers", 7 * day, 1, func() error {
			t, err := s.portalOpen(cu("mia"), spec{subject: "Need access for a new employee", cat: "access", prio: "normal",
				body: "Please create an account for our new colleague. Her work address is on the same domain."})
			if err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.watch(t, st("fd1"), cu("uma")); err != nil {
				return err
			}
			if err := s.watch(t, st("fd1"), cu("ben")); err != nil {
				return err
			}
			if err := s.watch(t, st("fd1"), st("sup")); err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			return s.reply(t, st("fd1"), "Hi Mia, I have copied Uma and Ben on this ticket as you asked. Please send me the new colleague's name and I will create the account.", "")
		}},
		{"internal notes with mentions, CSAT 5", 12 * day, 1, func() error {
			t, err := s.portalOpen(cu("hank"), spec{subject: "API returns 502 on /v2/orders", cat: "tech", prio: "high",
				body: "Our integration gets a **502 Bad Gateway** from `/v2/orders` about every tenth call, mostly around 09:00 CET.",
				cf:   map[string]any{"os": "linux", "affected_users": 6, "affected_systems": []string{"erp"}, "error_message": "502 Bad Gateway"}})
			if err != nil {
				return err
			}
			s.wait(20*min, time.Hour)
			if err := s.patch(t, st("te1"), map[string]any{"assignee_id": st("te1").id, "tags": []string{"api", "502", "production"}}); err != nil {
				return err
			}
			if err := s.reply(t, st("te1"), "Hi Hank, thanks for the details. I am checking the proxy logs for the times you mention.", ""); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.note(t, st("te1"), "Repeated 502 from the upstream proxy between 08:55 and 09:10. @tina.torres have you seen this before?"); err != nil {
				return err
			}
			if err := s.escalate(t, st("te1"), "Proxy behaviour needs a configuration review by L2.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("te2"), st("te2")); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			if err := s.note(t, st("te2"), fmt.Sprintf("The upstream pool is too small at 09:00 peak. %s FYI, we should raise this in the capacity review.", s.mention(st("sup")))); err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.note(t, st("sup"), "Agreed, adding it to the Thursday review. @tina.torres please go ahead with the pool change."); err != nil {
				return err
			}
			s.wait(time.Hour, 5*time.Hour)
			if err := s.resolve(t, st("te2"), "We increased the upstream pool; since then no 502 appeared at the morning peak. Marking this as resolved."); err != nil {
				return err
			}
			if err := s.rate(t, 5); err != nil {
				return err
			}
			s.wait(2*day, 3*day)
			return s.closeTicket(t)
		}},
		{"low priority, pending", 15 * day, 1, func() error {
			t, err := s.portalOpen(cu("ida"), spec{subject: "Dark mode for the portal", cat: "feature", prio: "low",
				body: "Many of us work late. A dark theme for the portal would be great."})
			if err != nil {
				return err
			}
			s.wait(5*time.Hour, 20*time.Hour)
			if err := s.patch(t, st("te1"), map[string]any{"assignee_id": st("te1").id, "tags": []string{"feature-request", "ui"}}); err != nil {
				return err
			}
			return s.reply(t, st("te1"), "Hi Ida, thanks for the idea! The portal already follows your system theme; do you see the light theme even though your system is set to dark?", "pending")
		}},
		{"customer reopens a resolved ticket", 5 * day, 1, func() error {
			t, err := s.portalOpen(cu("jack"), spec{subject: "Verification code is not accepted", cat: "access", prio: "normal",
				body: "The six-digit code from the authenticator app is rejected every time, although my phone time is correct."})
			if err != nil {
				return err
			}
			s.wait(20*min, 2*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.resolve(t, st("fd1"), "I reset your authenticator binding. Please scan the new QR code on your profile page. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(2*time.Hour, 6*time.Hour)
			if err := cu("jack").cl.post("/portal/tickets/"+t.id+"/reopen", nil, nil); err != nil {
				return fmt.Errorf("reopen: %w", err)
			}
			t.status = "open"
			s.wait(10*min, 40*min)
			if err := s.reply(t, cu("jack"), "Reopening: the new QR code does not work either, the code is still rejected.", ""); err != nil {
				return err
			}
			s.wait(20*min, 2*time.Hour)
			if err := s.escalate(t, st("fd1"), "The binding reset did not help; needs a second pair of eyes with access to the identity logs.", ""); err != nil {
				return err
			}
			if err := s.assign(t, st("fd2"), st("fd2")); err != nil {
				return err
			}
			s.wait(time.Hour, 3*time.Hour)
			return s.resolve(t, st("fd2"), "The server clock of the authenticator service was off by 40 seconds; fixed. Marking this as resolved.")
		}},
		{"ticket of a customer who left", 50 * day, 1, func() error {
			t, err := s.portalOpen(cu("dan"), spec{subject: "Locked out after too many attempts", cat: "access", prio: "normal",
				body: "I mistyped my password a few times and now the portal says my account is locked."})
			if err != nil {
				return err
			}
			s.wait(30*min, 2*time.Hour)
			if err := s.assign(t, st("fd1"), st("fd1")); err != nil {
				return err
			}
			if err := s.resolve(t, st("fd1"), "I unlocked your account. Marking this as resolved."); err != nil {
				return err
			}
			s.wait(2*day, 4*day)
			return s.closeTicket(t)
		}},
	}
}
