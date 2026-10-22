// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"image/color"
	"time"
)

type cannedDef struct{ team, title, body, project string }

var cannedDefs = []cannedDef{
	{"", "Greeting", "Hi {{customer.name}},\n\nthank you for contacting us. My name is {{agent.name}} and I am looking into ticket #{{ticket.number}} right now.", ""},
	{"", "Need more information", "Hi {{customer.name}},\n\nto move forward with ticket #{{ticket.number}} I need a little more detail:\n\n- What exactly did you do right before the problem appeared?\n- Does it happen every time?\n- A screenshot or the exact error text would help a lot.\n\nThanks,\n{{agent.name}}", ""},
	{"", "Resolved, anything else?", "Hi {{customer.name}},\n\nwe believe ticket #{{ticket.number}} is resolved. Please reply if something is still not working; you can reopen it within seven days.\n\nBest regards,\n{{agent.name}}", ""},
	{"", "Closing: no response", "Hi {{customer.name}},\n\nwe have not heard back about ticket #{{ticket.number}}, so we are marking it as solved. Reply any time to continue.\n\n{{agent.name}}", ""},
	{"front", "Password reset steps", "Hi {{requester.name}},\n\nplease use **Forgot password** on the sign-in page. The link is valid for one hour. If the email does not arrive, check your spam folder and tell me which address you used.\n\n{{agent.name}}", ""},
	{"front", "Where to find the portal", "Hi {{customer.name}},\n\nyou can follow every request in the customer portal. Sign in with the email address you used to contact us.\n\n{{agent.name}}", ""},
	{"tech", "Please send logs", "Hi {{customer.name}},\n\ncould you attach the application log from the time of the incident (ticket #{{ticket.number}})? On desktop it is under *Help > Diagnostics > Export logs*.\n\n{{agent.name}}", ""},
	{"tech", "Escalated to engineering", "Hi {{customer.name}},\n\nI have escalated ticket #{{ticket.number}} to our engineers. You do not need to do anything; we will update you here as soon as we know more.\n\n{{agent.name}}", ""},
	{"billing", "Invoice copy", "Hi {{customer.name}},\n\nattached is a copy of the invoice you asked for (ticket #{{ticket.number}}). Let me know if you need it addressed differently.\n\n{{agent.name}}", ""},
	{"billing", "Refund issued", "Hi {{customer.name}},\n\nwe have issued the refund. It usually shows up on your statement within 5-7 business days.\n\nKind regards,\n{{agent.name}}", ""},
	{"", "Outside business hours", "Hi {{customer.name}},\n\nthank you for your message. Our team is offline right now; we will pick up ticket #{{ticket.number}} first thing on the next business day.\n\nFeatherDesk Demo Support", ""},
	// D-20: offered only on Mobile tickets.
	{"", "Mobile: update the app", "Hi {{customer.name}},\n\nplease update the app to the latest version from the App Store or Google Play and try again (ticket {{ticket.ref}}). The fix is included in version 3.9.\n\n{{agent.name}}", "mob"},
}

func (s *seeder) setupCanned() error {
	for _, c := range cannedDefs {
		body := map[string]any{"title": c.title, "body_md": c.body}
		if c.team != "" {
			body["team_id"] = s.teams[c.team].ID
		}
		if c.project != "" {
			body["project_id"] = s.projects[c.project].ID
		}
		var out idResp
		if err := s.admin.post("/admin/canned-responses", body, &out); err != nil {
			return fmt.Errorf("canned %q: %w", c.title, err)
		}
		s.canned[c.title] = out.ID
		s.advance(time.Minute)
	}
	// A supervisor-made response of their own team through the agent API.
	return s.staff["bi2"].cl.post("/agent/canned-responses", map[string]any{
		"title": "Payment reminder (friendly)", "team_id": s.teams["billing"].ID,
		"body_md": "Hi {{customer.name}},\n\na friendly reminder that the invoice from ticket #{{ticket.number}} is still open. If you already paid, please ignore this message.\n\n{{agent.name}}"}, nil)
}

func (s *seeder) setupBranding() error {
	a := s.admin
	uploads := []struct {
		slot, name string
		data       []byte
	}{
		{"logo_light", "logo-light.png", brandPNG(360, 80, nil, hex("#1B5E8C"), hex("#F59E0B"))},
		{"logo_dark", "logo-dark.png", brandPNG(360, 80, nil, hex("#E8F1F8"), hex("#F59E0B"))},
		{"icon", "icon.png", brandPNG(128, 128, bgPtr("#1B5E8C"), hex("#FFFFFF"), hex("#F59E0B"))},
		{"favicon", "favicon.png", brandPNG(64, 64, bgPtr("#1B5E8C"), hex("#FFFFFF"), hex("#F59E0B"))},
		{"email_logo", "email-logo.png", brandPNG(320, 72, bgPtr("#1B5E8C"), hex("#FFFFFF"), hex("#F59E0B"))},
		{"login_background", "login-background.png", backgroundPNG(640, 360, hex("#0F2A43"), hex("#1B5E8C"))},
	}
	for _, u := range uploads {
		if err := a.upload("/admin/branding/assets/"+u.slot, u.name, "image/png", u.data, nil); err != nil {
			return fmt.Errorf("branding asset %s: %w", u.slot, err)
		}
	}
	var cur map[string]any
	if err := a.get("/admin/branding", &cur); err != nil {
		return err
	}
	settings, _ := cur["settings"].(map[string]any)
	if settings == nil {
		settings = cur
	}
	settings["product_name"] = "FeatherDesk Demo"
	settings["company_name"] = "RadioBros"
	settings["colors"] = map[string]any{"primary": "#1B5E8C", "secondary": "#37474F", "accent": "#F59E0B"}
	settings["default_locale"] = "en"
	settings["available_locales"] = []string{"en", "it", "de", "fr", "es"}
	settings["default_theme"] = "system"
	settings["font"] = "inter"
	settings["radius"] = "rounded"
	settings["powered_by"] = true
	settings["support_url"] = "https://radiobros.com/"
	settings["legal"] = map[string]any{"imprint_url": "https://demo.example.com/imprint", "privacy_url": "https://demo.example.com/privacy", "terms_url": "https://demo.example.com/terms"}
	settings["login"] = map[string]any{"title": "Welcome to the FeatherDesk demo", "subtitle": "Sign in with one of the demo accounts", "background_color": "#0F2A43"}
	settings["portal"] = map[string]any{
		"welcome_md":          "## How can we help?\nSearch the knowledge base or open a ticket. This is a **demo** instance filled with sample data.",
		"new_ticket_intro_md": "Describe the problem as precisely as you can. Screenshots and logs help us a lot.",
	}
	settings["email"] = map[string]any{"header_color": "#1B5E8C", "footer_md": "FeatherDesk Demo - this message was sent by the demo instance."}
	return a.put("/admin/branding", settings, nil)
}

func bgPtr(c string) *color.RGBA { v := hex(c); return &v }

func (s *seeder) setupIntegrations() error {
	a := s.admin
	// API token of the admin: shown once at the end; also drives the "api" channel.
	var tok struct {
		Token string `json:"token"`
	}
	exp := s.now.AddDate(1, 0, 0).Format(time.RFC3339)
	if err := a.post("/me/tokens", map[string]any{"name": "Demo integration", "scopes": []string{"tickets:read", "tickets:write", "directory:read", "kb:read"}, "expires_at": exp}, &tok); err != nil {
		return fmt.Errorf("api token: %w", err)
	}
	s.patToken = tok.Token
	s.pat = s.newClient()
	s.pat.bearer = tok.Token
	// Webhook: disabled, aimed at a closed port, so nothing is ever sent.
	var wh idResp
	if err := a.post("/admin/webhooks", map[string]any{
		"url": "http://localhost:9/demo", "active": false, "include_internal": false,
		"events": []string{"ticket.created", "ticket.status_changed", "ticket.escalated", "message.created", "sla.breached", "ticket.csat"},
	}, &wh); err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	s.webhookID = wh.ID
	// Inbound mailbox: IMAP, inactive (never connects).
	var mb idResp
	if err := a.post("/admin/inbound/mailboxes", map[string]any{
		"name": "Support inbox", "kind": "imap", "address": "support@demo.test", "team_id": s.teams["front"].ID, "active": false,
		"imap": map[string]any{"host": "imap.demo.invalid", "port": 993, "security": "tls", "username": "support@demo.test", "password": "not-a-real-password",
			"folder": "INBOX", "processed_action": "move", "processed_folder": "Processed", "auth": "password"},
	}, &mb); err != nil {
		return fmt.Errorf("inbound mailbox: %w", err)
	}
	s.mailboxID = mb.ID
	return nil
}
