// SPDX-License-Identifier: AGPL-3.0-only

package demo

import "fmt"

type catDef struct {
	key, name, team, priority string
	inactive                  bool
	project                   string // project key (D-20)
}

var catDefs = []catDef{
	{"general", "General question", "front", "", false, "web"},
	{"access", "Account access", "front", "high", false, "web"},
	{"tech", "Technical issue", "tech", "", false, "web"},
	{"feature", "Feature request", "tech", "low", false, "web"},
	{"billing", "Billing & invoices", "billing", "", false, "web"},
	{"legacy", "Legacy hardware (retired)", "tech", "", true, "web"},
	{"mobile", "App crash or bug", "tech", "", false, "mob"},
	{"store", "App store & subscriptions", "billing", "", false, "mob"},
	{"hardware", "Laptop & hardware", "tech", "", false, "it"},
	{"onboarding", "New hire onboarding", "front", "low", false, "it"},
}

type optDef struct{ value, en, it string }

type fieldDef struct {
	key, typ, en, it string
	options          []optDef
	cats             []string // category keys; nil = every category
	projects         []string // project keys; nil = every project (D-20)
	visible, edit    bool
	required         bool
}

var fieldDefs = []fieldDef{
	{key: "os", typ: "select", en: "Operating system", it: "Sistema operativo", cats: []string{"tech", "access", "mobile"}, visible: true, edit: true,
		options: []optDef{{"windows", "Windows", "Windows"}, {"macos", "macOS", "macOS"}, {"linux", "Linux", "Linux"}, {"ios", "iOS", "iOS"}, {"android", "Android", "Android"}}},
	{key: "affected_users", typ: "number", en: "Affected users", it: "Utenti coinvolti", cats: []string{"tech"}, visible: true, edit: true},
	{key: "error_message", typ: "textarea", en: "Error message", it: "Messaggio di errore", cats: []string{"tech", "access"}, visible: true, edit: true},
	{key: "affected_systems", typ: "multiselect", en: "Affected systems", it: "Sistemi coinvolti", cats: []string{"tech"}, visible: true, edit: true,
		options: []optDef{{"vpn", "VPN", "VPN"}, {"email", "Email", "Email"}, {"erp", "ERP", "ERP"}, {"crm", "CRM", "CRM"}, {"wifi", "Wi-Fi", "Wi-Fi"}}},
	{key: "blocking_work", typ: "checkbox", en: "Blocks my work", it: "Mi impedisce di lavorare", visible: true, edit: true},
	{key: "order_number", typ: "text", en: "Order or invoice number", it: "Numero ordine o fattura", cats: []string{"billing"}, visible: true, edit: true, required: true},
	{key: "invoice_date", typ: "date", en: "Invoice date", it: "Data fattura", cats: []string{"billing"}, visible: true, edit: true},
	{key: "assigned_engineer", typ: "text", en: "Engineer in charge", it: "Tecnico incaricato", cats: []string{"tech"}, visible: true, edit: false},
	{key: "root_cause", typ: "select", en: "Root cause", it: "Causa radice", cats: []string{"tech"}, required: true,
		options: []optDef{{"bug", "Product bug", "Bug del prodotto"}, {"config", "Configuration", "Configurazione"}, {"user_error", "User error", "Errore dell'utente"}, {"hardware", "Hardware", "Hardware"}, {"third_party", "Third party", "Terze parti"}}},
	{key: "refund_amount", typ: "number", en: "Refund amount (EUR)", it: "Importo rimborso (EUR)", cats: []string{"billing"}},
	{key: "internal_ref", typ: "text", en: "Internal reference", it: "Riferimento interno"},
	{key: "app_version", typ: "text", en: "App version", it: "Versione dell'app", projects: []string{"mob"}, visible: true, edit: true},
}

func (s *seeder) setupSupport() error {
	a := s.admin
	for i, d := range catDefs {
		body := map[string]any{"name": d.name, "default_team_id": s.teams[d.team].ID, "sort": i, "project_id": s.projects[d.project].ID}
		if d.priority != "" {
			body["default_priority"] = d.priority
		}
		var out idResp
		if err := a.post("/admin/categories", body, &out); err != nil {
			return fmt.Errorf("category %s: %w", d.key, err)
		}
		if d.inactive {
			if err := a.patch("/admin/categories/"+out.ID, map[string]any{"name": d.name, "default_team_id": s.teams[d.team].ID, "sort": i, "active": false}, nil); err != nil {
				return err
			}
		}
		s.cats[d.key] = &catRef{ID: out.ID, Key: d.key, Name: d.name, Team: d.team, Project: d.project}
	}
	for i, d := range fieldDefs {
		body := map[string]any{"key": d.key, "type": d.typ, "label": map[string]string{"en": d.en, "it": d.it},
			"customer_visible": d.visible, "customer_editable": d.edit, "required": d.required, "sort": i}
		if d.cats != nil {
			var ids []string
			for _, k := range d.cats {
				ids = append(ids, s.cats[k].ID)
			}
			body["category_ids"] = ids
		}
		if d.projects != nil {
			var ids []string
			for _, k := range d.projects {
				ids = append(ids, s.projects[k].ID)
			}
			body["project_ids"] = ids
		}
		if len(d.options) > 0 {
			var opts []map[string]any
			for _, o := range d.options {
				opts = append(opts, map[string]any{"value": o.value, "label": map[string]string{"en": o.en, "it": o.it}})
			}
			body["options"] = opts
		}
		var out idResp
		if err := a.post("/admin/custom-fields", body, &out); err != nil {
			return fmt.Errorf("custom field %s: %w", d.key, err)
		}
		s.fields[d.key] = &fieldRef{ID: out.ID, Key: d.key, Type: d.typ}
	}
	policies := []map[string]any{
		{"priority": "urgent", "first_response_minutes": 30, "resolution_minutes": 240, "calendar_mode": "24x7"},
		{"priority": "high", "first_response_minutes": 120, "resolution_minutes": 1440, "calendar_mode": "team"},
		{"priority": "normal", "first_response_minutes": 480, "resolution_minutes": 2880, "calendar_mode": "team"},
		{"priority": "low", "first_response_minutes": 1440, "resolution_minutes": 7200, "calendar_mode": "team"},
	}
	if err := a.put("/admin/sla-policies", map[string]any{"policies": policies}, nil); err != nil {
		return fmt.Errorf("sla policies: %w", err)
	}
	// D-20: Mobile answers urgent and high tickets faster; the other
	// priorities inherit the global defaults.
	mobSLA := []map[string]any{
		{"priority": "urgent", "first_response_minutes": 15, "resolution_minutes": 180, "calendar_mode": "24x7"},
		{"priority": "high", "first_response_minutes": 60, "resolution_minutes": 720, "calendar_mode": "24x7"},
	}
	if err := a.put("/admin/projects/"+s.projects["mob"].ID+"/sla-policies", map[string]any{"policies": mobSLA}, nil); err != nil {
		return fmt.Errorf("mobile sla policies: %w", err)
	}
	return a.patch("/admin/settings", map[string]any{
		"tier_labels":                map[string]string{"1": "L1 Front line", "2": "L2 Specialist", "3": "L3 Expert"},
		"auto_close_days":            7,
		"reopen_window_days":         7,
		"customer_urgent_allowed":    true,
		"urgent_requires_supervisor": true,
		"triage_visible_to_agents":   true,
		"portal_enabled":             true,
		"kb_enabled":                 true,
		"kb_sitemap_enabled":         true,
		"self_registration":          true,
		"default_locale":             "en",
	}, nil)
}
