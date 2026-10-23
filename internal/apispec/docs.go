// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"

	"github.com/radioBros/FeatherDesk/api"
)

// The API reference served at /api/docs (CON-018) is generated here, at test
// time, because the OpenAPI libraries are test-only (D-01): the runtime serves
// the committed api/docs.html. Regenerate after changing api/openapi.yaml:
//
//	go test ./internal/apispec -run TestAPIDocsGenerated -update
//
// TestAPIDocsGenerated fails when the committed page is stale, and
// TestAPIDocsExamplesValid validates every example on the page against its
// schema.

// DocsExample is one example shown on the page, for validation.
type DocsExample struct {
	Where  string
	Schema *base.Schema
	JSON   []byte
}

type docParam struct {
	Name, In, Type, Constraints string
	Required                    bool
	Desc                        template.HTML
}

type docField struct {
	Name, Type, Constraints string
	Depth                   int
	Required                bool
	Desc                    template.HTML
}

type docBody struct {
	ContentType string
	Required    bool
	Desc        template.HTML
	Fields      []docField
	Example     string
}

type docResp struct {
	Code, ContentType, Schema, ErrorAnchor string
	Desc                                   template.HTML
	Headers                                []string
}

type docOp struct {
	ID, Anchor, Method, Path, SummaryText string
	Summary                               template.HTML
	Desc                                  template.HTML
	Public, Mutation                      bool
	Audience                              string
	Scopes                                []string
	Params                                []docParam
	Body                                  *docBody
	Responses                             []docResp
	Curl                                  string
	OKStatus, OKExample, OKType           string
	WebSocket                             bool
}

type docTag struct {
	Name, Anchor string
	Desc         template.HTML
	Ops          []*docOp
}

type docWebhook struct {
	Name, Anchor        string
	Summary, Desc, When template.HTML
	Example             string
}

// webhookEvents: when each event fires and the shape of its `data.changes`
// (internal/events payload structs, events_spec §1).
var webhookEvents = map[string]struct {
	when    string
	changes *obj
	message bool
}{
	"ticket.created":           {"A ticket is opened (portal, agent panel, API or email).", objOf("channel", "portal", "priority", "normal", "team", "", "category", "", "message_id", exUUID), false},
	"ticket.status_changed":    {"The status changes.", objOf("from", "new", "to", "open"), false},
	"ticket.priority_changed":  {"The priority changes.", objOf("from", "normal", "to", "urgent"), false},
	"ticket.assigned":          {"The assignee changes (`to` empty = unassigned).", objOf("from", "", "to", exUUID), false},
	"ticket.team_changed":      {"The team changes.", objOf("from", exUUID, "to", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12"), false},
	"ticket.escalated":         {"The ticket moves up a support level.", objOf("from_tier", 1, "to_tier", 2, "from_team", nil, "to_team", exUUID, "unassigned", exUUID), false},
	"ticket.deescalated":       {"The ticket moves down a support level.", objOf("from_tier", 2, "to_tier", 1, "from_team", exUUID, "to_team", nil), false},
	"ticket.category_changed":  {"The category changes.", objOf("from", nil, "to", exUUID), false},
	"ticket.tags_changed":      {"Tags are added or removed.", objOf("from", []any{"vpn"}, "to", []any{"vpn", "network"}), false},
	"ticket.fields_changed":    {"Custom field values change (customer-visible keys only, unless the webhook includes internal data).", objOf("keys", []any{"order_id"}), false},
	"message.created":          {"A public message is added by the customer, an agent, email or an integration.", objOf("message_id", exUUID, "kind", "public", "channel", "portal"), true},
	"note.created":             {"An internal note is added. Sent only to webhooks with *Include internal notes*.", objOf("message_id", exUUID, "kind", "internal", "mentioned", []any{exUUID}), true},
	"sla.warning":              {"An SLA target is about to be missed.", objOf("which", "first_response", "due_at", "2026-10-23T14:00:00Z"), false},
	"sla.breached":             {"An SLA target was missed.", objOf("which", "resolution", "due_at", "2026-10-23T18:00:00Z"), false},
	"ticket.csat":              {"The customer rates the ticket.", objOf("score", 4), false},
	"ticket.followup_created":  {"A reply to a closed ticket opens a follow-up (sent for both tickets).", objOf("new_ticket_id", exUUID, "original_ticket_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12"), false},
	"ticket.merged":            {"A ticket is merged into another.", objOf("source_id", exUUID, "target_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12"), false},
	"ticket.split":             {"Messages are split off into a new ticket.", objOf("parent_id", exUUID, "child_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12", "message_ids", []any{exUUID}), false},
	"ticket.linked":            {"Two tickets are linked.", objOf("to_ticket_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12", "type", "related"), false},
	"ticket.unlinked":          {"A link between tickets is removed.", objOf("to_ticket_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12", "type", "related"), false},
	"ticket.children_resolved": {"The last open child of a parent ticket is resolved.", objOf("parent_id", exUUID, "child_id", "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a12"), false},
}

type docProblem struct {
	Status          int
	Anchor, Name    string
	Desc            template.HTML
	Example, Header string
}

type docPage struct {
	Title, Version, Summary, Base, Host string
	Intro                               template.HTML
	Tags                                []*docTag
	Webhooks                            []docWebhook
	Problems                            []docProblem
	FieldCodes, ProblemSlugs            []string
	OpCount                             int
}

const docHost = "https://help.example.com"

var reNonAnchor = regexp.MustCompile(`[^a-z0-9]+`)

func anchor(prefix, s string) string {
	return prefix + strings.Trim(reNonAnchor.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// schemaName is the component name of a $ref, or "".
func schemaName(sp *base.SchemaProxy) string {
	if sp == nil {
		return ""
	}
	if r := sp.GetReference(); r != "" {
		return r[strings.LastIndex(r, "/")+1:]
	}
	return ""
}

// typeLabel is the short type shown in the tables.
func typeLabel(sp *base.SchemaProxy) string {
	if sp == nil {
		return ""
	}
	s := sp.Schema()
	if s == nil {
		return ""
	}
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		var parts []string
		nullable := false
		for _, b := range append(slices.Clone(s.OneOf), s.AnyOf...) {
			if isNullOnly(b) {
				nullable = true
				continue
			}
			parts = append(parts, typeLabel(b))
		}
		l := strings.Join(parts, " | ")
		if nullable {
			l += " | null"
		}
		return l
	}
	name := schemaName(sp)
	ts, nullable := types(s)
	t := strings.Join(ts, " | ")
	switch {
	case t == "array" && s.Items != nil && s.Items.IsA():
		t = typeLabel(s.Items.A) + "[]"
	case name != "" && (t == "object" || t == "" || len(s.Enum) > 0):
		t = name
	case t == "" && len(s.AllOf) > 0:
		t = typeLabel(s.AllOf[0])
	case t == "":
		t = "object"
	}
	if s.Format != "" && name == "" && !strings.HasSuffix(t, "[]") {
		t += " (" + s.Format + ")"
	}
	if nullable {
		t += " | null"
	}
	return t
}

func enumValues(s *base.Schema) []string {
	var out []string
	for _, n := range s.Enum {
		if v, ok := nodeValue(n); ok && v != nil {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}

// constraints summarises enum, bounds, pattern and default.
func constraints(sp *base.SchemaProxy) string {
	if sp == nil {
		return ""
	}
	s := sp.Schema()
	if s == nil {
		return ""
	}
	var c []string
	if ev := enumValues(s); len(ev) > 0 {
		if len(ev) > 12 {
			ev = append(ev[:12], "…")
		}
		c = append(c, "one of: "+strings.Join(ev, ", "))
	}
	if s.MinLength != nil && *s.MinLength > 0 {
		c = append(c, "min length "+strconv.FormatInt(*s.MinLength, 10))
	}
	if s.MaxLength != nil {
		c = append(c, "max length "+strconv.FormatInt(*s.MaxLength, 10))
	}
	if s.Minimum != nil {
		c = append(c, "≥ "+strconv.FormatFloat(*s.Minimum, 'f', -1, 64))
	}
	if s.Maximum != nil {
		c = append(c, "≤ "+strconv.FormatFloat(*s.Maximum, 'f', -1, 64))
	}
	if s.MinItems != nil && *s.MinItems > 0 {
		c = append(c, "min items "+strconv.FormatInt(*s.MinItems, 10))
	}
	if s.MaxItems != nil {
		c = append(c, "max items "+strconv.FormatInt(*s.MaxItems, 10))
	}
	if s.Pattern != "" {
		c = append(c, "pattern "+s.Pattern)
	}
	if v, ok := nodeValue(s.Default); ok && v != nil {
		b, _ := json.Marshal(v)
		c = append(c, "default "+string(b))
	}
	return strings.Join(c, " · ")
}

func descOf(sp *base.SchemaProxy) string {
	if sp == nil || sp.Schema() == nil {
		return ""
	}
	return sp.Schema().Description
}

// objectSchema returns the schema whose properties describe sp (following
// allOf and a nullable oneOf).
func objectSchemas(sp *base.SchemaProxy) []*base.Schema {
	if sp == nil {
		return nil
	}
	s := sp.Schema()
	if s == nil {
		return nil
	}
	out := []*base.Schema{}
	for _, sub := range s.AllOf {
		out = append(out, objectSchemas(sub)...)
	}
	for _, b := range s.OneOf {
		if !isNullOnly(b) {
			out = append(out, objectSchemas(b)...)
			break
		}
	}
	if s.Properties != nil && s.Properties.Len() > 0 {
		out = append(out, s)
	}
	return out
}

// fields flattens a request schema's properties (nested objects and arrays of
// objects with dotted names, two levels deep).
func fields(sp *base.SchemaProxy, prefix string, depth int, out *[]docField) {
	if depth > 2 || len(*out) > 80 {
		return
	}
	for _, s := range objectSchemas(sp) {
		for k, p := range s.Properties.FromOldest() {
			ps := p.Schema()
			if ps == nil || (ps.ReadOnly != nil && *ps.ReadOnly) {
				continue
			}
			name := prefix + k
			*out = append(*out, docField{Name: name, Type: typeLabel(p), Required: slices.Contains(s.Required, k),
				Desc: md(descOf(p)), Constraints: constraints(p), Depth: depth})
			if ps.Items != nil && ps.Items.IsA() && len(objectSchemas(ps.Items.A)) > 0 && depth < 2 {
				fields(ps.Items.A, name+"[].", depth+1, out)
			} else if len(objectSchemas(p)) > 0 && depth < 2 && schemaName(p) == "" {
				fields(p, name+".", depth+1, out)
			}
		}
	}
}

func prettyJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

func compactJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

// pickMedia prefers JSON, then problem+json, then the first content type.
func pickMedia(all map[string]*v3.MediaType, order []string) (string, *v3.MediaType) {
	for _, want := range []string{"application/json", "application/problem+json"} {
		if m, ok := all[want]; ok {
			return want, m
		}
	}
	if len(order) > 0 {
		return order[0], all[order[0]]
	}
	return "", nil
}

func mediaMap(content *orderedmap.Map[string, *v3.MediaType]) (map[string]*v3.MediaType, []string) {
	m := map[string]*v3.MediaType{}
	var order []string
	if content == nil {
		return m, nil
	}
	for k, v := range content.FromOldest() {
		m[k] = v
		order = append(order, k)
	}
	return m, order
}

// mediaExample is the media type's own example, else one generated from the schema.
func mediaExample(m *v3.MediaType, mode exMode) (any, bool) {
	if m == nil {
		return nil, false
	}
	if v, ok := nodeValue(m.Example); ok {
		return v, true
	}
	if m.Examples != nil {
		for _, ex := range m.Examples.FromOldest() {
			if v, ok := nodeValue(ex.Value); ok {
				return v, true
			}
		}
	}
	if m.Schema == nil {
		return nil, false
	}
	e := &exampler{mode: mode}
	return e.value(m.Schema, "", 0), true
}

var methodOrder = []string{"get", "post", "put", "patch", "delete"}

// BuildDocs renders the API reference page and returns every example on it.
func BuildDocs() ([]byte, []DocsExample, error) {
	doc, err := Document()
	if err != nil {
		return nil, nil, err
	}
	model, err := doc.BuildV3Model()
	if err != nil || model == nil {
		return nil, nil, fmt.Errorf("apispec: build model: %w", err)
	}
	m := model.Model
	docComponents = m.Components.Schemas
	page := &docPage{Title: strings.TrimSuffix(m.Info.Title, " API"), Version: m.Info.Version, Summary: m.Info.Summary, Base: api.BasePath, Host: docHost}
	intro := m.Info.Description
	if i := strings.Index(intro, "`x-phase`"); i >= 0 {
		intro = strings.TrimSpace(intro[:i])
	}
	page.Intro = md(intro)
	var examples []DocsExample
	addEx := func(where string, sp *base.SchemaProxy, v any) string {
		b, _ := json.Marshal(v)
		if sp != nil && sp.Schema() != nil {
			examples = append(examples, DocsExample{Where: where, Schema: sp.Schema(), JSON: b})
		}
		return prettyJSON(v)
	}

	tagIndex := map[string]*docTag{}
	for _, t := range m.Tags {
		dt := &docTag{Name: t.Name, Anchor: anchor("tag-", t.Name), Desc: md(t.Description)}
		tagIndex[t.Name] = dt
		page.Tags = append(page.Tags, dt)
	}
	for path, item := range m.Paths.PathItems.FromOldest() {
		base := api.BasePath
		if len(item.Servers) > 0 {
			base = strings.TrimSuffix(item.Servers[0].URL, "/")
		}
		ops := item.GetOperations()
		for _, method := range methodOrder {
			op, ok := ops.Get(method)
			if !ok || op == nil {
				continue
			}
			o := buildOp(strings.ToUpper(method), base+path, item, op, addEx)
			tag := "Other"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			dt := tagIndex[tag]
			if dt == nil {
				dt = &docTag{Name: tag, Anchor: anchor("tag-", tag)}
				tagIndex[tag] = dt
				page.Tags = append(page.Tags, dt)
			}
			dt.Ops = append(dt.Ops, o)
			page.OpCount++
		}
	}
	page.Tags = slices.DeleteFunc(page.Tags, func(t *docTag) bool { return len(t.Ops) == 0 })

	// Webhooks: one entry per event, payload example from the request body.
	if m.Webhooks != nil {
		for name, item := range m.Webhooks.FromOldest() {
			op := item.Post
			if op == nil || op.RequestBody == nil || op.RequestBody.Content == nil {
				continue
			}
			all, order := mediaMap(op.RequestBody.Content)
			_, media := pickMedia(all, order)
			v, ok := mediaExample(media, exResponse)
			if !ok {
				continue
			}
			w := docWebhook{Name: name, Anchor: anchor("webhook-", name), Summary: mdInline(op.Summary), Desc: md(op.Description)}
			if info, ok := webhookEvents[name]; ok {
				w.When = mdInline(info.when)
				if env, ok := v.(*obj); ok {
					env.vals["type"] = name
					if data, ok := env.vals["data"].(*obj); ok {
						data.vals["changes"] = info.changes
						if !info.message {
							data.del("message")
						}
					}
				}
			}
			w.Example = addEx("webhook "+name, media.Schema, v)
			page.Webhooks = append(page.Webhooks, w)
		}
	}

	// Shared errors: one problem example per status.
	problemSP := m.Components.Schemas.GetOrZero("Problem")
	for _, p := range problemCatalog {
		ex := objOf("type", "https://featherdesk.dev/problems/"+p.slug, "title", p.title, "status", p.status)
		if p.detail != "" {
			ex.set("detail", p.detail)
		}
		if p.status == 422 {
			ex.set("errors", []any{objOf("field", "subject", "code", "required", "message", "This field is required."),
				objOf("field", "requester_email", "code", "email", "message", "Enter a valid email address.")})
		}
		if p.slug == "invalid-transition" {
			ex.set("allowed", []any{"open", "pending"})
		}
		desc := ""
		if r := m.Components.Responses.GetOrZero(p.name); r != nil {
			desc = r.Description
		}
		page.Problems = append(page.Problems, docProblem{Status: p.status, Anchor: anchor("error-", strconv.Itoa(p.status)), Name: p.name,
			Desc: md(desc), Example: addEx("problem "+p.slug, problemSP, ex), Header: p.header})
	}
	if fe := m.Components.Schemas.GetOrZero("FieldErrorCode"); fe != nil && fe.Schema() != nil {
		page.FieldCodes = enumValues(fe.Schema())
	}
	if ps := m.Components.Schemas.GetOrZero("ProblemSlug"); ps != nil && ps.Schema() != nil {
		page.ProblemSlugs = enumValues(ps.Schema())
	}

	var b bytes.Buffer
	if err := docsTpl.Execute(&b, page); err != nil {
		return nil, nil, err
	}
	return b.Bytes(), examples, nil
}

var problemCatalog = []struct {
	status                    int
	name, slug, title, detail string
	header                    string
}{
	{400, "BadRequest", "bad-request", "Bad request", "invalid JSON body", ""},
	{401, "Unauthenticated", "unauthenticated", "Authentication required", "", ""},
	{403, "Forbidden", "forbidden", "Forbidden", "", ""},
	{404, "NotFound", "not-found", "Not found", "", ""},
	{409, "Conflict", "invalid-transition", "Invalid status transition", "", ""},
	{412, "PreconditionFailed", "precondition-failed", "Precondition failed", "The ticket was changed by someone else.", ""},
	{413, "TooLarge", "payload-too-large", "Payload too large", "", ""},
	{422, "Validation", "validation", "Validation failed", "", ""},
	{429, "RateLimited", "rate-limited", "Too many requests", "", "Retry-After: 30"},
	{503, "Unavailable", "unavailable", "Service unavailable", "", "Retry-After: 5"},
}

func problemAnchor(code string) string {
	for _, p := range problemCatalog {
		if strconv.Itoa(p.status) == code {
			return anchor("error-", code)
		}
	}
	return ""
}

func buildOp(method, path string, item *v3.PathItem, op *v3.Operation, addEx func(string, *base.SchemaProxy, any) string) *docOp {
	o := &docOp{ID: op.OperationId, Anchor: op.OperationId, Method: method, Path: path, Summary: mdInline(op.Summary), SummaryText: op.Summary, Desc: md(op.Description),
		Mutation: method != "GET"}
	if o.Anchor == "" {
		o.Anchor = anchor("op-", method+" "+path)
	}
	o.Public = op.Security != nil && len(op.Security) == 0
	switch {
	case strings.HasPrefix(path, api.BasePath+"/admin"):
		o.Audience = "Admins (some admin routes also allow supervisors or project admins — see the description)"
	case strings.HasPrefix(path, api.BasePath+"/agent"):
		o.Audience = "Staff"
	case strings.HasPrefix(path, api.BasePath+"/portal"):
		o.Audience = "Customers"
	}
	if op.Extensions != nil {
		if n, ok := op.Extensions.Get("x-scopes"); ok && n != nil {
			if v, ok := nodeValue(n); ok {
				if l, ok := v.([]any); ok {
					for _, s := range l {
						o.Scopes = append(o.Scopes, fmt.Sprint(s))
					}
				}
			}
		}
		if _, ok := op.Extensions.Get("x-websocket"); ok {
			o.WebSocket = true
		}
	}

	params := append(slices.Clone(item.Parameters), op.Parameters...)
	pathVals := map[string]string{}
	query := url.Values{}
	var headers []string
	for _, p := range params {
		if p == nil || p.Name == "X-CSRF-Token" {
			continue
		}
		req := p.Required != nil && *p.Required
		dp := docParam{Name: p.Name, In: p.In, Type: typeLabel(p.Schema), Required: req || p.In == "path", Desc: md(p.Description + "\n\n" + descOf(p.Schema)),
			Constraints: constraints(p.Schema)}
		o.Params = append(o.Params, dp)
		ex := (&exampler{mode: exRequest}).value(p.Schema, p.Name, 0)
		val := fmt.Sprint(ex)
		if ex == nil {
			val = "…"
		}
		switch p.In {
		case "path":
			pathVals[p.Name] = val
		case "query":
			if req {
				query.Set(p.Name, val)
			}
		case "header":
			if req || p.Name == "If-Match" && method != "GET" {
				headers = append(headers, p.Name+": "+val)
			}
		}
	}

	// Request body.
	var bodyArg string
	if op.RequestBody != nil && op.RequestBody.Content != nil {
		all, order := mediaMap(op.RequestBody.Content)
		ct, media := pickMedia(all, order)
		b := &docBody{ContentType: ct, Required: op.RequestBody.Required != nil && *op.RequestBody.Required, Desc: md(op.RequestBody.Description)}
		if media != nil && media.Schema != nil {
			fields(media.Schema, "", 0, &b.Fields)
		}
		switch {
		case strings.Contains(ct, "json"):
			if v, ok := mediaExample(media, exRequest); ok {
				b.Example = addEx(method+" "+path+" request", media.Schema, v)
				bodyArg = "  -H 'Content-Type: application/json' \\\n  -d '" + strings.ReplaceAll(compactJSON(v), "'", `'\''`) + "'"
			}
		case ct == "multipart/form-data":
			var parts []string
			for _, f := range b.Fields {
				if f.Depth > 0 {
					continue
				}
				if strings.Contains(f.Type, "binary") {
					parts = append(parts, "  -F '"+f.Name+"=@./screenshot.png'")
				} else if f.Required {
					parts = append(parts, "  -F '"+f.Name+"=…'")
				}
			}
			bodyArg = strings.Join(parts, " \\\n")
		default:
			bodyArg = "  -H 'Content-Type: " + ct + "' \\\n  --data-binary @./message.eml"
		}
		o.Body = b
	}

	// Responses.
	if op.Responses != nil && op.Responses.Codes != nil {
		for code, r := range op.Responses.Codes.FromOldest() {
			if r == nil {
				continue
			}
			dr := docResp{Code: code, Desc: md(r.Description), ErrorAnchor: problemAnchor(code)}
			if r.Headers != nil {
				for h := range r.Headers.KeysFromOldest() {
					dr.Headers = append(dr.Headers, h)
				}
			}
			if r.Content != nil {
				all, order := mediaMap(r.Content)
				ct, media := pickMedia(all, order)
				dr.ContentType = ct
				if media != nil {
					dr.Schema = typeLabel(media.Schema)
				}
				if o.OKStatus == "" && strings.HasPrefix(code, "2") {
					o.OKStatus, o.OKType = code, ct
					if strings.Contains(ct, "json") {
						if v, ok := mediaExample(media, exResponse); ok {
							o.OKExample = addEx(method+" "+path+" "+code, media.Schema, v)
						}
					}
				}
			} else if o.OKStatus == "" && strings.HasPrefix(code, "2") {
				o.OKStatus = code
			}
			o.Responses = append(o.Responses, dr)
		}
	}

	// curl
	p := path
	for k, v := range pathVals {
		p = strings.ReplaceAll(p, "{"+k+"}", url.PathEscape(v))
	}
	u := docHost + p
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var c strings.Builder
	if method == "GET" {
		c.WriteString("curl '" + u + "'")
	} else {
		c.WriteString("curl -X " + method + " '" + u + "'")
	}
	if !o.Public && !strings.HasPrefix(path, "/healthz") && !strings.HasPrefix(path, "/readyz") {
		c.WriteString(" \\\n  -H 'Authorization: Bearer fd_pat_…'")
	}
	sort.Strings(headers)
	for _, h := range headers {
		c.WriteString(" \\\n  -H '" + h + "'")
	}
	if bodyArg != "" {
		c.WriteString(" \\\n" + bodyArg)
	}
	o.Curl = c.String()
	return o
}
