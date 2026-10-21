// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// tool is one MCP tool. Staff and customers get different definitions of the
// same name (different schemas and REST routes); staff-only tools have no
// customer definition, so they are absent from a customer's tools/list and
// unknown to a customer's tools/call.
type tool struct {
	Name        string
	Title       string
	Description string
	Schema      map[string]any
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
	// Scopes: the PAT needs any one of them (mirrors the x-scopes of Ops,
	// checked by TestToolScopesMatchSpec); empty = none.
	Scopes []string
	// Ops are the REST operations the tool calls ("GET /agent/tickets").
	Ops []string
	run func(ctx context.Context, c *caller, args json.RawMessage) (any, error)
}

func (t *tool) definition() map[string]any {
	return map[string]any{
		"name":        t.Name,
		"title":       t.Title,
		"description": t.Description,
		"inputSchema": t.Schema,
		"annotations": map[string]any{
			"title":           t.Title,
			"readOnlyHint":    t.ReadOnly,
			"destructiveHint": t.Destructive,
			"idempotentHint":  t.Idempotent || t.ReadOnly,
			"openWorldHint":   t.OpenWorld,
		},
	}
}

// toolsFor returns the tool set of a principal kind, in a fixed order.
func toolsFor(kind domain.UserKind) []*tool {
	if kind == domain.KindStaff {
		return staffTools
	}
	return customerTools
}

func findTool(kind domain.UserKind, name string) *tool {
	for _, t := range toolsFor(kind) {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// listTools: the caller's kind, filtered by the token's scopes (the list
// may vary by authorisation; it never varies per session).
func listTools(c *caller) []map[string]any {
	out := []map[string]any{}
	for _, t := range toolsFor(c.Kind) {
		if c.can(t.Scopes) {
			out = append(out, t.definition())
		}
	}
	return out
}

// ---- tools/call ----

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// argError is an invalid tool argument (a tool execution error the model
// can correct, not a protocol error).
type argError struct{ msg string }

func (e *argError) Error() string { return e.msg }

func badArg(format string, a ...any) error { return &argError{msg: fmt.Sprintf(format, a...)} }

func (h *Handler) callTool(r *http.Request, c *caller, version string, m *message) (map[string]any, *rpcError) {
	var p callParams
	if e := decodeParams(m.Params, &p); e != nil {
		return nil, e
	}
	t := findTool(c.Kind, p.Name)
	if t == nil {
		return nil, newError(codeInvalidParams, "Unknown tool: "+p.Name)
	}
	start := time.Now()
	out, err := t.run(r.Context(), c, p.Arguments)
	log := server.Logger(r.Context()).With("tool", t.Name, "user_id", c.UserID, "duration_ms", time.Since(start).Milliseconds())
	if err != nil {
		res, slug := toolError(err)
		if slug == problems.SlugInternal {
			log.Error("mcp tool call failed", "err", err)
		} else {
			log.Info("mcp tool call", "outcome", slug)
		}
		return res, nil
	}
	log.Info("mcp tool call", "outcome", "ok")
	return toolResult(out, version), nil
}

func toolResult(out any, version string) map[string]any {
	text, _ := json.MarshalIndent(out, "", "  ")
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
	if version != Version20250326 { // structuredContent arrived in 2025-06-18
		res["structuredContent"] = out
	}
	return res
}

// toolError turns a failure into an MCP tool execution error carrying the
// problem slug, so the model can see what went wrong and correct itself.
func toolError(err error) (map[string]any, string) {
	var e map[string]any
	var ae *apiError
	var arg *argError
	switch {
	case errors.As(err, &arg):
		e = map[string]any{"slug": problems.SlugValidation, "status": http.StatusUnprocessableEntity, "title": "Invalid arguments", "detail": arg.msg}
	case errors.As(err, &ae):
		e = map[string]any{"slug": ae.Slug, "status": ae.Status, "title": ae.Title}
		if ae.Detail != "" {
			e["detail"] = ae.Detail
		}
		if len(ae.Fields) > 0 {
			e["errors"] = ae.Fields
		}
		for k, v := range ae.Extra {
			e[k] = v
		}
	default:
		e = map[string]any{"slug": problems.SlugInternal, "status": http.StatusInternalServerError, "title": "Internal error"}
	}
	text := fmt.Sprintf("Error [%s] (HTTP %v): %v", e["slug"], e["status"], e["title"])
	if d, ok := e["detail"].(string); ok && d != "" {
		text += ": " + d
	}
	if fe, ok := e["errors"].([]problems.FieldError); ok {
		for _, f := range fe {
			text += fmt.Sprintf("\n- %s: %s %s", f.Field, f.Code, f.Message)
		}
	}
	if a, ok := e["allowed"]; ok {
		text += fmt.Sprintf("\nAllowed: %v", a)
	}
	slug, _ := e["slug"].(string)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": text}},
		"structuredContent": map[string]any{"error": e},
		"isError":           true,
	}, slug
}

// ---- argument helpers ----

func decodeArgs(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return badArg("invalid arguments: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

// ticketArg accepts a ticket reference ("WEB-12", D-20), a ticket number
// (1042, "1042", "#1042") or a ticket id.
type ticketArg string

func (t *ticketArg) UnmarshalJSON(b []byte) error {
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*t = ticketArg(n.String())
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("ticket must be a ticket reference (KEY-123), number or id")
	}
	*t = ticketArg(s)
	return nil
}

// number returns the ticket number when the reference is one.
func (t ticketArg) number() (int64, bool) {
	s := strings.TrimPrefix(strings.TrimSpace(string(t)), "#")
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// ref returns the KEY-123 reference (upper-cased key) when the argument is one.
func (t ticketArg) ref() (string, bool) {
	key, seq, ok := domain.ParseTicketRef(strings.Trim(strings.TrimSpace(string(t)), "[]"))
	if !ok {
		return "", false
	}
	return domain.TicketRef(key, seq), true
}

func (c *caller) area() string {
	if c.staff() {
		return "/agent"
	}
	return "/portal"
}

// getTicket loads the ticket detail (staff or customer view) by number or id.
func (c *caller) getTicket(ctx context.Context, ref ticketArg) (map[string]any, error) {
	if strings.TrimSpace(string(ref)) == "" {
		return nil, badArg("ticket is required")
	}
	path := c.area() + "/tickets/" + url.PathEscape(string(ref))
	if r, ok := ref.ref(); ok {
		path = c.area() + "/tickets/by-ref/" + url.PathEscape(r)
	} else if n, ok := ref.number(); ok {
		path = c.area() + "/tickets/by-number/" + strconv.FormatInt(n, 10)
	}
	var out map[string]any
	_, err := c.api.do(ctx, http.MethodGet, path, nil, nil, &out)
	return out, err
}

// ticketID resolves a reference to the ticket id (one read for a ref or a
// number).
func (c *caller) ticketID(ctx context.Context, ref ticketArg) (string, error) {
	_, isRef := ref.ref()
	if _, ok := ref.number(); !ok && !isRef {
		if strings.TrimSpace(string(ref)) == "" {
			return "", badArg("ticket is required")
		}
		return strings.TrimSpace(string(ref)), nil
	}
	t, err := c.getTicket(ctx, ref)
	if err != nil {
		return "", err
	}
	id, _ := t["id"].(string)
	return id, nil
}

// trimMessages keeps the newest max messages of a ticket detail.
func trimMessages(t map[string]any, maxMessages int) map[string]any {
	msgs, _ := t["messages"].([]any)
	if maxMessages <= 0 {
		maxMessages = 50
	}
	t["messages_total"] = len(msgs)
	if len(msgs) > maxMessages {
		t["messages"] = msgs[len(msgs)-maxMessages:]
		t["messages_omitted"] = len(msgs) - maxMessages
	}
	return t
}

func setLimit(q url.Values, limit int, def int) error {
	if limit == 0 {
		limit = def
	}
	if limit < 1 || limit > 100 {
		return badArg("limit must be between 1 and 100")
	}
	q.Set("limit", strconv.Itoa(limit))
	return nil
}

func listTickets(ctx context.Context, c *caller, q url.Values) (any, error) {
	var page map[string]any
	_, err := c.api.do(ctx, http.MethodGet, c.area()+"/tickets", q, nil, &page)
	return page, err
}

// nullableID: "none" or "" clears the field (JSON null), anything else is an id.
func nullableID(v string) any {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "none", "null":
		return nil
	}
	return strings.TrimSpace(v)
}

func joinEnum(field string, vals []string, allowed []string) (string, error) {
	for _, v := range vals {
		ok := false
		for _, a := range allowed {
			ok = ok || v == a
		}
		if !ok {
			return "", badArg("%s: %q is not one of %s", field, v, strings.Join(allowed, ", "))
		}
	}
	return strings.Join(vals, ","), nil
}
