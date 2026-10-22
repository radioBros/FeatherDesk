// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/domain"
)

// ---- JSON Schema helpers (2020-12, the MCP default dialect) ----

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strLen(desc string, minLen, maxLen int) map[string]any {
	s := str(desc)
	s["minLength"], s["maxLength"] = minLen, maxLen
	return s
}

func enum[T ~string](desc string, vals []T) map[string]any {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return map[string]any{"type": "string", "enum": out, "description": desc}
}

func enumList[T ~string](desc string, vals []T) map[string]any {
	e := enum("", vals)
	delete(e, "description")
	return map[string]any{"type": "array", "items": e, "uniqueItems": true, "description": desc}
}

func integer(desc string, minV, maxV, def int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minV, "maximum": maxV, "default": def, "description": desc}
}

var (
	ticketProp = str(`Ticket reference ("WEB-12"), ticket number (1042 or "#1042") or ticket id.`)
	limitProp  = integer("Page size.", 1, 100, 20)
	cursorProp = str("next_cursor from a previous page; omit for the first page.")
	bodyProp   = strLen("Message text in Markdown.", 1, 65536)
)

var customerStatuses = []string{"open", "awaiting_reply", "resolved", "closed"}

func strs[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// ---- tools shared by both kinds (the REST routes are principal-aware) ----

var searchKB = &tool{
	Name:  "search_kb",
	Title: "Search the knowledge base",
	Description: "Full-text search of the knowledge base articles you may read (published articles; staff also see internal ones). " +
		"Returns title, slug, category and a snippet per article, best match first. Read a result with get_kb_article.",
	Schema:   object(map[string]any{"query": strLen("Words to search for.", 1, 200), "limit": integer("Maximum results.", 1, 50, 10)}, "query"),
	ReadOnly: true,
	Ops:      []string{"GET /kb/articles"},
	run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		if strings.TrimSpace(a.Query) == "" {
			return nil, badArg("query is required")
		}
		q := url.Values{"q": {a.Query}}
		if err := setLimit(q, a.Limit, 10); err != nil {
			return nil, err
		}
		var page map[string]any
		_, err := c.api.do(ctx, http.MethodGet, "/kb/articles", q, nil, &page)
		return page, err
	},
}

var getKBArticle = &tool{
	Name:        "get_kb_article",
	Title:       "Read a knowledge base article",
	Description: "Read one knowledge base article by its slug (from search_kb): title, Markdown body, category, locale and dates.",
	Schema: object(map[string]any{
		"slug":   strLen("Article slug, e.g. \"reset-your-password\".", 1, 200),
		"locale": enum("Preferred translation; defaults to your language.", domain.Locales),
	}, "slug"),
	ReadOnly: true,
	Ops:      []string{"GET /kb/articles/{slug}"},
	run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
		var a struct {
			Slug   string `json:"slug"`
			Locale string `json:"locale"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		return c.kbArticle(ctx, a.Slug, a.Locale)
	},
}

// kbArticle reads an article, following one slug redirect (renamed article).
func (c *caller) kbArticle(ctx context.Context, slug, locale string) (map[string]any, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, badArg("slug is required")
	}
	var q url.Values
	if locale != "" {
		q = url.Values{"locale": {locale}}
	}
	var art map[string]any
	for range 2 {
		_, err := c.api.do(ctx, http.MethodGet, "/kb/articles/"+url.PathEscape(slug), q, nil, &art)
		var ae *apiError
		if err != nil && asAPIError(err, &ae) && ae.Status == http.StatusMovedPermanently {
			next := strings.TrimPrefix(ae.header.Get("Location"), "/api/v1/kb/articles/")
			if i := strings.IndexByte(next, '?'); i >= 0 {
				next = next[:i]
			}
			if s, err := url.PathUnescape(next); err == nil && s != "" && s != slug {
				slug = s
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		delete(art, "body_html") // the model reads Markdown
		return art, nil
	}
	return nil, &apiError{Status: http.StatusNotFound, Slug: "not-found", Title: "Not found"}
}

// ---- staff tools ----

var staffTools = []*tool{
	{
		Name:  "search_tickets",
		Title: "Search tickets",
		Description: "Search the tickets you can see by free text (subject, replies and internal notes) and/or filters. " +
			"Returns ticket summaries (ref, project, subject, status, priority, tier, team, assignee, requester, SLA due dates) and next_cursor for the next page. " +
			"Use get_ticket to read a conversation, list_my_tickets for your own queue.",
		Schema: object(map[string]any{
			"query":    strLen("Words to search for. Omit to list by filters only.", 1, 200),
			"status":   enumList("Only these statuses.", domain.TicketStatuses),
			"priority": enumList("Only these priorities.", domain.Priorities),
			"assignee": str(`"me", "none" (unassigned) or a staff user id.`),
			"team_id":  str(`Team id (list_teams), or "none" for triage tickets without a team.`),
			"project":  str(`Only tickets of this project: its key (e.g. "WEB") or id.`),
			"sort":     map[string]any{"type": "string", "enum": []string{"attention", "-updated_at", "-created_at", "priority", "due", "-number"}, "description": "Order; default attention (most urgent first)."},
			"limit":    limitProp,
			"cursor":   cursorProp,
		}),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /agent/tickets"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Query    string   `json:"query"`
				Status   []string `json:"status"`
				Priority []string `json:"priority"`
				Assignee string   `json:"assignee"`
				TeamID   string   `json:"team_id"`
				Project  string   `json:"project"`
				Sort     string   `json:"sort"`
				Limit    int      `json:"limit"`
				Cursor   string   `json:"cursor"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			q := url.Values{}
			if a.Query != "" {
				q.Set("q", a.Query)
			}
			if err := staffFilters(q, a.Status, a.Priority); err != nil {
				return nil, err
			}
			for k, v := range map[string]string{"assignee": a.Assignee, "team_id": a.TeamID, "project": a.Project, "sort": a.Sort, "cursor": a.Cursor} {
				if v != "" {
					q.Set(k, v)
				}
			}
			if err := setLimit(q, a.Limit, 20); err != nil {
				return nil, err
			}
			return listTickets(ctx, c, q)
		},
	},
	{
		Name:  "list_my_tickets",
		Title: "List my tickets",
		Description: "List the tickets assigned to you, most urgent first. By default only work in progress " +
			"(new, open, pending, on_hold); pass status to include resolved or closed ones.",
		Schema: object(map[string]any{
			"status": enumList("Only these statuses (default: new, open, pending, on_hold).", domain.TicketStatuses),
			"limit":  limitProp,
			"cursor": cursorProp,
		}),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /agent/tickets"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Status []string `json:"status"`
				Limit  int      `json:"limit"`
				Cursor string   `json:"cursor"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			if len(a.Status) == 0 {
				a.Status = []string{"new", "open", "pending", "on_hold"}
			}
			q := url.Values{"assignee": {"me"}}
			if err := staffFilters(q, a.Status, nil); err != nil {
				return nil, err
			}
			if a.Cursor != "" {
				q.Set("cursor", a.Cursor)
			}
			if err := setLimit(q, a.Limit, 20); err != nil {
				return nil, err
			}
			return listTickets(ctx, c, q)
		},
	},
	{
		Name:  "get_ticket",
		Title: "Get a ticket",
		Description: "Read one ticket with its conversation, oldest message first: public replies, internal notes (kind \"internal\": " +
			"staff only, never shown to the customer) and system messages, plus status, priority, tier, team, assignee, requester, tags, " +
			"watchers, links, SLA dates and allowed_actions (the changes you may make). Only the newest max_messages messages are returned.",
		Schema:   object(map[string]any{"ticket": ticketProp, "max_messages": integer("Newest messages to return.", 1, 500, 50)}, "ticket"),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /agent/tickets/{id}", "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		run:      runGetTicket,
	},
	{
		Name:  "create_ticket",
		Title: "Create a ticket",
		Description: "Open a ticket on behalf of a customer. Identify the customer with requester_email (an unknown address creates the " +
			"customer, named requester_name) or requester_id. body_md becomes the first public message and the customer is notified by email. " +
			"Category, team, assignee and priority follow your permissions; omitted ones use the routing defaults.",
		Schema: object(map[string]any{
			"subject":         strLen("Short summary.", 1, 200),
			"body_md":         bodyProp,
			"requester_email": str("Customer email address."),
			"requester_name":  str("Customer name, used when requester_email creates a new customer."),
			"requester_id":    str("Existing customer user id (instead of requester_email)."),
			"priority":        enum("Default normal.", domain.Priorities),
			"project":         str(`Project key (e.g. "WEB") or id; default: the category's project, else the default project.`),
			"category_id":     str("Category id (list_categories)."),
			"team_id":         str("Team id (list_teams); default from the category."),
			"assignee_id":     str(`Staff user id, or "me".`),
			"tags":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags to set."},
		}, "subject", "body_md"),
		OpenWorld: true,
		Scopes:    []string{domain.ScopeTicketsWrite},
		Ops:       []string{"POST /agent/tickets"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Subject        string   `json:"subject"`
				BodyMD         string   `json:"body_md"`
				RequesterEmail string   `json:"requester_email"`
				RequesterName  string   `json:"requester_name"`
				RequesterID    string   `json:"requester_id"`
				Priority       string   `json:"priority"`
				Project        string   `json:"project"`
				CategoryID     string   `json:"category_id"`
				TeamID         string   `json:"team_id"`
				AssigneeID     string   `json:"assignee_id"`
				Tags           []string `json:"tags"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			body := map[string]any{"subject": a.Subject, "body_md": a.BodyMD}
			for k, v := range map[string]string{"requester_email": a.RequesterEmail, "requester_name": a.RequesterName,
				"requester_id": a.RequesterID, "priority": a.Priority, "project_id": a.Project, "category_id": a.CategoryID, "team_id": a.TeamID} {
				if v != "" {
					body[k] = v
				}
			}
			if a.AssigneeID == "me" {
				a.AssigneeID = c.UserID
			}
			if a.AssigneeID != "" {
				body["assignee_id"] = a.AssigneeID
			}
			if a.Tags != nil {
				body["tags"] = a.Tags
			}
			var out map[string]any
			_, err := c.api.do(ctx, http.MethodPost, "/agent/tickets", nil, body, &out)
			return out, err
		},
	},
	{
		Name:  "reply_to_ticket",
		Title: "Reply to the customer",
		Description: "Post a PUBLIC reply on a ticket: the customer sees it in the portal and receives it by email. " +
			"Optionally set the status in the same step (e.g. pending while you wait for the customer, resolved when done). " +
			"For remarks the customer must not see, use add_internal_note.",
		Schema: object(map[string]any{
			"ticket":     ticketProp,
			"body_md":    bodyProp,
			"set_status": enum("Status to set together with the reply.", domain.TicketStatuses),
		}, "ticket", "body_md"),
		OpenWorld: true,
		Scopes:    []string{domain.ScopeTicketsWrite},
		Ops:       []string{"POST /agent/tickets/{id}/messages", "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket    ticketArg `json:"ticket"`
				BodyMD    string    `json:"body_md"`
				SetStatus string    `json:"set_status"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			body := map[string]any{"kind": "public", "body_md": a.BodyMD}
			if a.SetStatus != "" {
				body["set_status"] = a.SetStatus
			}
			return c.postMessage(ctx, a.Ticket, body)
		},
	},
	{
		Name:  "add_internal_note",
		Title: "Add an internal note",
		Description: "Add an internal note to a ticket. Notes are visible to staff only: they never appear in the customer portal, " +
			"in customer emails or in customer API responses. Use it for findings, hand-over context and questions to colleagues.",
		Schema:    object(map[string]any{"ticket": ticketProp, "body_md": bodyProp}, "ticket", "body_md"),
		Scopes:    []string{domain.ScopeTicketsWrite},
		Ops:       []string{"POST /agent/tickets/{id}/messages", "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		OpenWorld: false,
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket ticketArg `json:"ticket"`
				BodyMD string    `json:"body_md"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			return c.postMessage(ctx, a.Ticket, map[string]any{"kind": "internal", "body_md": a.BodyMD})
		},
	},
	{
		Name:  "update_ticket",
		Title: "Update a ticket",
		Description: "Change a ticket's status, priority, category, team, assignee, subject or tags; only the fields you pass change " +
			"(tags replaces the whole list). Status changes follow the ticket lifecycle: an impossible change fails with invalid-transition " +
			"and lists the allowed targets. Each change must be permitted to you (see allowed_actions in get_ticket), otherwise nothing changes. " +
			"Returns the ticket and visible=false when the change moved it out of your sight.",
		Schema: object(map[string]any{
			"ticket":      ticketProp,
			"status":      enum("New status.", domain.TicketStatuses),
			"priority":    enum("New priority.", domain.Priorities),
			"category_id": str(`Category id (list_categories), or "none" to clear.`),
			"team_id":     str(`Team id (list_teams) to transfer to, or "none" for triage.`),
			"assignee_id": str(`Staff user id, "me", or "none" to unassign.`),
			"subject":     strLen("New subject.", 1, 200),
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The complete new tag list."},
		}, "ticket"),
		Idempotent: true,
		Scopes:     []string{domain.ScopeTicketsWrite},
		Ops:        []string{"PATCH /agent/tickets/{id}", "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket     ticketArg `json:"ticket"`
				Status     string    `json:"status"`
				Priority   string    `json:"priority"`
				CategoryID *string   `json:"category_id"`
				TeamID     *string   `json:"team_id"`
				AssigneeID *string   `json:"assignee_id"`
				Subject    string    `json:"subject"`
				Tags       []string  `json:"tags"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			body := map[string]any{}
			for k, v := range map[string]string{"status": a.Status, "priority": a.Priority, "subject": a.Subject} {
				if v != "" {
					body[k] = v
				}
			}
			for k, v := range map[string]*string{"category_id": a.CategoryID, "team_id": a.TeamID, "assignee_id": a.AssigneeID} {
				if v == nil {
					continue
				}
				if k == "assignee_id" && *v == "me" {
					body[k] = c.UserID
					continue
				}
				body[k] = nullableID(*v)
			}
			if a.Tags != nil {
				body["tags"] = a.Tags
			}
			if len(body) == 0 {
				return nil, badArg("nothing to change: pass at least one of status, priority, category_id, team_id, assignee_id, subject, tags")
			}
			id, err := c.ticketID(ctx, a.Ticket)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			_, err = c.api.do(ctx, http.MethodPatch, "/agent/tickets/"+url.PathEscape(id), nil, body, &out)
			return out, err
		},
	},
	escalation("escalate_ticket", "Escalate a ticket",
		"Escalate a ticket one support tier (L1 → L2 → L3) with a reason recorded on its timeline, optionally moving it to another team. "+
			"If the ticket rises above your tier you may lose sight of it (visible=false).", "/escalate"),
	escalation("deescalate_ticket", "De-escalate a ticket",
		"Move a ticket down one support tier (L3 → L2 → L1) with a reason recorded on its timeline, optionally to another team.", "/deescalate"),
	{
		Name:  "apply_macro",
		Title: "Apply a macro",
		Description: "Apply a saved macro to a ticket in one step: its reply or internal note, then its priority, tag, assignee and status changes. " +
			"Name the macro by id or by its exact name (case-insensitive); an unknown name fails and lists the macros you can use. " +
			"Every part must be permitted to you, otherwise nothing changes.",
		Schema: object(map[string]any{"ticket": ticketProp, "macro": strLen("Macro id or exact macro name.", 1, 200)}, "ticket", "macro"),
		Scopes: []string{domain.ScopeTicketsWrite},
		Ops:    []string{"POST /agent/tickets/{id}/macros/{macro_id}/apply", "GET /agent/macros", "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket ticketArg `json:"ticket"`
				Macro  string    `json:"macro"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			macroID, err := c.macroID(ctx, a.Macro)
			if err != nil {
				return nil, err
			}
			id, err := c.ticketID(ctx, a.Ticket)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			_, err = c.api.do(ctx, http.MethodPost, "/agent/tickets/"+url.PathEscape(id)+"/macros/"+url.PathEscape(macroID)+"/apply", nil, nil, &out)
			return out, err
		},
	},
	searchKB,
	getKBArticle,
	{
		Name:        "list_teams",
		Title:       "List teams",
		Description: "List the support teams (id and name). Use the ids in create_ticket, update_ticket and escalate_ticket.",
		Schema:      object(map[string]any{}),
		ReadOnly:    true,
		Scopes:      []string{domain.ScopeDirectoryRead},
		Ops:         []string{"GET /agent/teams"},
		run:         listRun("/agent/teams", "teams"),
	},
	{
		Name:        "list_categories",
		Title:       "List categories",
		Description: "List the ticket categories (id, name, whether active, default team). Use the ids in create_ticket and update_ticket.",
		Schema:      object(map[string]any{}),
		ReadOnly:    true,
		Scopes:      []string{domain.ScopeDirectoryRead},
		Ops:         []string{"GET /agent/categories"},
		run:         listRun("/agent/categories", "categories"),
	},
}

func staffFilters(q url.Values, status, priority []string) error {
	if len(status) > 0 {
		v, err := joinEnum("status", status, strs(domain.TicketStatuses))
		if err != nil {
			return err
		}
		q.Set("status", v)
	}
	if len(priority) > 0 {
		v, err := joinEnum("priority", priority, strs(domain.Priorities))
		if err != nil {
			return err
		}
		q.Set("priority", v)
	}
	return nil
}

func runGetTicket(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
	var a struct {
		Ticket      ticketArg `json:"ticket"`
		MaxMessages int       `json:"max_messages"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.MaxMessages < 0 || a.MaxMessages > 500 {
		return nil, badArg("max_messages must be between 1 and 500")
	}
	t, err := c.getTicket(ctx, a.Ticket)
	if err != nil {
		return nil, err
	}
	return trimMessages(t, a.MaxMessages), nil
}

func (c *caller) postMessage(ctx context.Context, ref ticketArg, body map[string]any) (any, error) {
	id, err := c.ticketID(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_, err = c.api.do(ctx, http.MethodPost, c.area()+"/tickets/"+url.PathEscape(id)+"/messages", nil, body, &out)
	return out, err
}

func escalation(name, title, desc, suffix string) *tool {
	return &tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Schema: object(map[string]any{
			"ticket":         ticketProp,
			"reason":         strLen("Why; shown to staff on the ticket timeline.", 1, 2000),
			"target_team_id": str("Team id (list_teams) to move the ticket to; omit to keep the team."),
		}, "ticket", "reason"),
		Scopes: []string{domain.ScopeTicketsWrite},
		Ops:    []string{"POST /agent/tickets/{id}" + suffix, "GET /agent/tickets/by-number/{n}", "GET /agent/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket       ticketArg `json:"ticket"`
				Reason       string    `json:"reason"`
				TargetTeamID string    `json:"target_team_id"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			body := map[string]any{"reason": a.Reason}
			if a.TargetTeamID != "" {
				body["target_team_id"] = a.TargetTeamID
			}
			id, err := c.ticketID(ctx, a.Ticket)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			_, err = c.api.do(ctx, http.MethodPost, "/agent/tickets/"+url.PathEscape(id)+suffix, nil, body, &out)
			return out, err
		},
	}
}

// macroID resolves a macro id or name against GET /agent/macros.
func (c *caller) macroID(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", badArg("macro is required")
	}
	var macros []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if _, err := c.api.do(ctx, http.MethodGet, "/agent/macros", nil, nil, &macros); err != nil {
		return "", err
	}
	names := make([]string, 0, len(macros))
	for _, m := range macros {
		if m.ID == ref || strings.EqualFold(m.Name, ref) {
			return m.ID, nil
		}
		names = append(names, strconv.Quote(m.Name))
	}
	if len(names) == 0 {
		return "", badArg("unknown macro %q: you have no macros", ref)
	}
	return "", badArg("unknown macro %q; available: %s", ref, strings.Join(names, ", "))
}

// listRun wraps an array endpoint as {key: [...]} (structuredContent is an object).
func listRun(path, key string) func(context.Context, *caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
		var a struct{}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		var items []any
		if _, err := c.api.do(ctx, http.MethodGet, path, nil, nil, &items); err != nil {
			return nil, err
		}
		if items == nil {
			items = []any{}
		}
		return map[string]any{key: items}, nil
	}
}

// ---- customer tools (portal routes: public messages only, I-2/I-9) ----

var customerTools = []*tool{
	{
		Name:  "search_tickets",
		Title: "Search my tickets",
		Description: "Search your tickets by free text in subjects and replies (organization managers also see their organization's tickets). " +
			"Returns ticket summaries (ref, project, subject, status, priority, dates) and next_cursor for the next page. Use get_ticket to read one.",
		Schema: object(map[string]any{
			"query":  strLen("Words to search for. Omit to list all.", 1, 200),
			"status": map[string]any{"type": "string", "enum": customerStatuses, "description": "Only this status."},
			"limit":  limitProp,
			"cursor": cursorProp,
		}),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /portal/tickets"},
		run:      customerList(false),
	},
	{
		Name:        "list_my_tickets",
		Title:       "List my tickets",
		Description: "List the tickets you opened, most recently updated first; pass status to narrow it (open, awaiting_reply, resolved, closed).",
		Schema: object(map[string]any{
			"status": map[string]any{"type": "string", "enum": customerStatuses, "description": "Only this status."},
			"limit":  limitProp,
			"cursor": cursorProp,
		}),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /portal/tickets"},
		run:      customerList(true),
	},
	{
		Name:  "get_ticket",
		Title: "Get my ticket",
		Description: "Read one of your tickets with its conversation (the replies between you and the support team, oldest first), " +
			"status, priority, dates and allowed_actions. Only the newest max_messages messages are returned.",
		Schema:   object(map[string]any{"ticket": ticketProp, "max_messages": integer("Newest messages to return.", 1, 500, 50)}, "ticket"),
		ReadOnly: true,
		Scopes:   []string{domain.ScopeTicketsRead},
		Ops:      []string{"GET /portal/tickets/{id}", "GET /portal/tickets/by-number/{n}", "GET /portal/tickets/by-ref/{ref}"},
		run:      runGetTicket,
	},
	{
		Name:  "create_ticket",
		Title: "Open a ticket",
		Description: "Open a new support ticket. Describe the problem in body_md (what happened, what you expected, error messages). " +
			"Search the knowledge base first (search_kb): the answer may already be there.",
		Schema: object(map[string]any{
			"subject":     strLen("Short summary.", 1, 200),
			"body_md":     bodyProp,
			"priority":    enum("Default normal.", domain.Priorities),
			"project":     str(`Project key (e.g. "WEB") or id, when you can open tickets in several; default: the category's project.`),
			"category_id": str("Category id (list_categories)."),
		}, "subject", "body_md"),
		Scopes: []string{domain.ScopeTicketsWrite},
		Ops:    []string{"POST /portal/tickets"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Subject    string `json:"subject"`
				BodyMD     string `json:"body_md"`
				Priority   string `json:"priority"`
				Project    string `json:"project"`
				CategoryID string `json:"category_id"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			body := map[string]any{"subject": a.Subject, "body_md": a.BodyMD}
			if a.Priority != "" {
				body["priority"] = a.Priority
			}
			if a.Project != "" {
				body["project_id"] = a.Project
			}
			if a.CategoryID != "" {
				body["category_id"] = a.CategoryID
			}
			var out map[string]any
			_, err := c.api.do(ctx, http.MethodPost, "/portal/tickets", nil, body, &out)
			return out, err
		},
	},
	{
		Name:  "reply_to_ticket",
		Title: "Reply on my ticket",
		Description: "Add a reply to one of your tickets; the support team is notified. Replying to a closed ticket opens a follow-up " +
			"ticket instead, and its id is returned as followup_ticket_id.",
		Schema: object(map[string]any{"ticket": ticketProp, "body_md": bodyProp}, "ticket", "body_md"),
		Scopes: []string{domain.ScopeTicketsWrite},
		Ops:    []string{"POST /portal/tickets/{id}/messages", "GET /portal/tickets/by-number/{n}", "GET /portal/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket ticketArg `json:"ticket"`
				BodyMD string    `json:"body_md"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			out, err := c.postMessage(ctx, a.Ticket, map[string]any{"body_md": a.BodyMD})
			var ae *apiError
			if err != nil && asAPIError(err, &ae) && ae.Status == http.StatusConflict {
				if f, ok := ae.Extra["followup_ticket_id"].(string); ok && f != "" {
					// The ticket was closed: the reply opened a follow-up (lifecycle §2).
					return map[string]any{"followup_ticket_id": f,
						"note": "The ticket was closed, so your reply opened a new follow-up ticket."}, nil
				}
			}
			return out, err
		},
	},
	{
		Name:        "update_ticket",
		Title:       "Resolve or reopen my ticket",
		Description: "Mark one of your tickets resolved (your problem is solved), or reopen a resolved ticket (status open).",
		Schema: object(map[string]any{
			"ticket": ticketProp,
			"status": map[string]any{"type": "string", "enum": []string{"resolved", "open"}, "description": "resolved, or open to reopen."},
		}, "ticket", "status"),
		Idempotent: true,
		Scopes:     []string{domain.ScopeTicketsWrite},
		Ops:        []string{"POST /portal/tickets/{id}/resolve", "POST /portal/tickets/{id}/reopen", "GET /portal/tickets/by-number/{n}", "GET /portal/tickets/by-ref/{ref}"},
		run: func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
			var a struct {
				Ticket ticketArg `json:"ticket"`
				Status string    `json:"status"`
			}
			if err := decodeArgs(raw, &a); err != nil {
				return nil, err
			}
			action := map[string]string{"resolved": "/resolve", "open": "/reopen"}[a.Status]
			if action == "" {
				return nil, badArg(`status must be "resolved" or "open"`)
			}
			id, err := c.ticketID(ctx, a.Ticket)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			_, err = c.api.do(ctx, http.MethodPost, "/portal/tickets/"+url.PathEscape(id)+action, nil, nil, &out)
			return out, err
		},
	},
	searchKB,
	getKBArticle,
	{
		Name:        "list_categories",
		Title:       "List categories",
		Description: "List the categories you can choose when opening a ticket (id and name).",
		Schema:      object(map[string]any{}),
		ReadOnly:    true,
		Scopes:      []string{domain.ScopeTicketsRead},
		Ops:         []string{"GET /portal/categories"},
		run:         listRun("/portal/categories", "categories"),
	},
}

func customerList(mineOnly bool) func(context.Context, *caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, c *caller, raw json.RawMessage) (any, error) {
		var a struct {
			Query  string `json:"query"`
			Status string `json:"status"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		q := url.Values{}
		if mineOnly {
			q.Set("mine_only", "true")
		}
		if a.Query != "" {
			q.Set("q", a.Query)
		}
		if a.Status != "" {
			if _, err := joinEnum("status", []string{a.Status}, customerStatuses); err != nil {
				return nil, err
			}
			q.Set("status", a.Status)
		}
		if a.Cursor != "" {
			q.Set("cursor", a.Cursor)
		}
		if err := setLimit(q, a.Limit, 20); err != nil {
			return nil, err
		}
		return listTickets(ctx, c, q)
	}
}
