// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	"github.com/pb33f/libopenapi/orderedmap"
)

// Example values for the API reference (CON-018). A value comes, in order,
// from the schema's own `example`/`examples`, `const`, `enum`, `default`,
// then a realistic value chosen by property name and format. Every example
// the page shows is validated against its schema by TestAPIDocsExamplesValid;
// when a generated value does not fit, the fix is an `example:` in the spec.

// exMode says which side of the wire an example is for.
type exMode int

const (
	exRequest  exMode = iota // readOnly properties left out
	exResponse               // writeOnly properties left out
)

const exMaxDepth = 7

type exampler struct {
	mode  exMode
	stack []string // $refs being expanded (cycle guard)
}

// docComponents are the spec's component schemas. A `$ref` with sibling
// keywords (usually a description) comes back from libopenapi as the
// siblings only, so references are resolved against this map.
var docComponents *orderedmap.Map[string, *base.SchemaProxy]

// resolved is the schema behind sp: the referenced component when sp is a $ref.
func resolved(sp *base.SchemaProxy) *base.Schema {
	if sp == nil {
		return nil
	}
	if r := sp.GetReference(); r != "" && docComponents != nil {
		if c := docComponents.GetOrZero(r[strings.LastIndex(r, "/")+1:]); c != nil && c != sp {
			if s := c.Schema(); s != nil {
				return s
			}
		}
	}
	return sp.Schema()
}

// obj is a JSON object that keeps its keys in schema order (a Go map would
// print them sorted, which reads badly: `subject` should come first).
type obj struct {
	keys []string
	vals map[string]any
}

func newObj() *obj { return &obj{vals: map[string]any{}} }

// set adds k (first value wins, see allOf) and reports whether it was new.
func (o *obj) set(k string, v any) bool {
	if _, ok := o.vals[k]; ok {
		return false
	}
	o.keys = append(o.keys, k)
	o.vals[k] = v
	return true
}

// MarshalJSON writes the keys in insertion order.
func (o *obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := enc.Encode(k); err != nil {
			return nil, err
		}
		b.Truncate(b.Len() - 1) // Encode's newline
		b.WriteByte(':')
		if err := enc.Encode(o.vals[k]); err != nil {
			return nil, err
		}
		b.Truncate(b.Len() - 1)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// del removes k.
func (o *obj) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	o.keys = slices.DeleteFunc(o.keys, func(x string) bool { return x == k })
}

// objOf builds an ordered object from key, value pairs.
func objOf(kv ...any) *obj {
	o := newObj()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

// decoder is a libopenapi YAML node (no direct dependency on its yaml module).
type decoder interface{ Decode(v any) error }

func nodeValue[N decoder](n N) (any, bool) {
	if any(n) == nil || reflectNil(n) {
		return nil, false
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, false
	}
	return normalize(v), true
}

func reflectNil(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// normalize turns yaml's map[string]any / int into JSON-friendly values.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
		return x
	case map[any]any:
		m := map[string]any{}
		for k, e := range x {
			if ks, ok := k.(string); ok {
				m[ks] = normalize(e)
			}
		}
		return m
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
		return x
	}
	return v
}

// types returns the schema's non-null types and whether null is allowed.
func types(s *base.Schema) (out []string, nullable bool) {
	for _, t := range s.Type {
		if t == "null" {
			nullable = true
			continue
		}
		out = append(out, t)
	}
	if s.Nullable != nil && *s.Nullable {
		nullable = true
	}
	return out, nullable
}

// isNullOnly reports a `{type: "null"}` branch of a oneOf.
func isNullOnly(sp *base.SchemaProxy) bool {
	if sp == nil {
		return false
	}
	s := sp.Schema()
	return s != nil && len(s.Type) == 1 && s.Type[0] == "null"
}

func (e *exampler) value(sp *base.SchemaProxy, name string, depth int) any {
	if sp == nil {
		return nil
	}
	if ref := sp.GetReference(); ref != "" {
		if slices.Contains(e.stack, ref) {
			return nil
		}
		e.stack = append(e.stack, ref)
		defer func() { e.stack = e.stack[:len(e.stack)-1] }()
	}
	s := resolved(sp)
	if s == nil {
		return nil
	}
	if v, ok := nodeValue(s.Example); ok {
		return v
	}
	if len(s.Examples) > 0 {
		if v, ok := nodeValue(s.Examples[0]); ok {
			return v
		}
	}
	if v, ok := nodeValue(s.Const); ok {
		return v
	}
	if len(s.Enum) > 0 {
		for _, n := range s.Enum {
			if v, ok := nodeValue(n); ok && v != nil {
				return v
			}
		}
	}
	if len(s.AllOf) > 0 {
		// First value wins: a base schema's constrained property (enum items)
		// must survive a later, looser redefinition, since allOf needs both.
		merged := newObj()
		add := func(m *obj) {
			for _, k := range m.keys {
				merged.set(k, m.vals[k])
			}
		}
		for _, sub := range s.AllOf {
			if m, ok := e.value(sub, name, depth).(*obj); ok {
				add(m)
			}
		}
		add(e.object(s, depth))
		return merged
	}
	for _, branches := range [][]*base.SchemaProxy{s.OneOf, s.AnyOf} {
		for _, b := range branches {
			if !isNullOnly(b) {
				return e.value(b, name, depth)
			}
		}
		if len(branches) > 0 {
			return nil
		}
	}
	ts, _ := types(s)
	t := ""
	if len(ts) > 0 {
		t = ts[0]
	} else if s.Properties != nil && s.Properties.Len() > 0 {
		t = "object"
	} else if len(s.Type) > 0 { // only "null"
		return nil
	}
	if v, ok := nodeValue(s.Default); ok && t != "object" && t != "array" {
		return v
	}
	switch t {
	case "object":
		if depth >= exMaxDepth {
			return newObj()
		}
		return e.object(s, depth+1)
	case "array":
		var item any
		if s.Items != nil && s.Items.IsA() {
			item = e.value(s.Items.A, singular(name), depth+1)
		}
		n := 1
		if s.MinItems != nil && *s.MinItems > 1 {
			n = int(*s.MinItems)
		}
		if item == nil {
			return []any{}
		}
		out := make([]any, n)
		for i := range out {
			out[i] = item
		}
		if s.UniqueItems != nil && *s.UniqueItems && n > 1 {
			return out[:1]
		}
		return out
	case "integer":
		return intExample(s, name)
	case "number":
		if s.Minimum != nil {
			return *s.Minimum
		}
		return 4.5
	case "boolean":
		return boolExample(name)
	case "string":
		return stringExample(s, name)
	}
	return nil
}

// object builds the example of an object schema's own properties.
func (e *exampler) object(s *base.Schema, depth int) *obj {
	out := newObj()
	if s.Properties != nil {
		for k, p := range s.Properties.FromOldest() {
			ps := p.Schema()
			if ps != nil {
				if e.mode == exRequest && ps.ReadOnly != nil && *ps.ReadOnly {
					continue
				}
				if e.mode == exResponse && ps.WriteOnly != nil && *ps.WriteOnly {
					continue
				}
			}
			out.set(k, e.value(p, k, depth))
		}
	}
	if len(out.keys) == 0 && s.AdditionalProperties != nil && s.AdditionalProperties.IsA() && s.AdditionalProperties.A != nil {
		key := "order_id"
		if s.PropertyNames != nil {
			if k := e.value(s.PropertyNames, "key", depth); k != nil {
				key = fmt.Sprint(k)
			}
		}
		if v := e.value(s.AdditionalProperties.A, key, depth); v != nil {
			out.set(key, v)
		}
	}
	return out
}

func singular(name string) string {
	switch {
	case strings.HasSuffix(name, "ies"):
		return strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "s"):
		return strings.TrimSuffix(name, "s")
	}
	return name
}

func intExample(s *base.Schema, name string) any {
	v := int64(1)
	switch {
	case name == "number" || strings.HasSuffix(name, "_number"):
		v = 1042
	case name == "limit":
		v = 50
	case name == "score" || name == "rating":
		v = 4
	case name == "tier" || strings.HasSuffix(name, "_tier"):
		v = 2
	case strings.HasSuffix(name, "_minutes"):
		v = 240
	case strings.HasSuffix(name, "_ms"):
		v = 12
	case strings.HasSuffix(name, "_bytes") || name == "size":
		v = 48213
	case name == "port":
		v = 587
	case strings.HasSuffix(name, "count") || name == "total" || strings.HasPrefix(name, "n_"):
		v = 3
	}
	if s.Minimum != nil && float64(v) < *s.Minimum {
		v = int64(*s.Minimum)
	}
	if s.Maximum != nil && float64(v) > *s.Maximum {
		v = int64(*s.Maximum)
	}
	return v
}

func boolExample(name string) bool {
	for _, p := range []string{"is_", "has_", "include_", "active", "enabled", "set", "ok", "public", "visible", "threads", "required", "verified"} {
		if strings.HasPrefix(name, p) || strings.HasSuffix(name, p) {
			return true
		}
	}
	return false
}

// byName are realistic values for common property names.
var byName = map[string]string{
	"subject": "VPN drops every 10 minutes", "title": "Reset your password", "name": "Ada Lovelace",
	"display_name": "Ada Lovelace", "email": "ada@example.com", "body_md": "Since this morning the VPN disconnects every ten minutes.",
	"body": "Since this morning the VPN disconnects every ten minutes.", "description": "Customers of the web shop",
	"ref": "HD-1042", "key": "HD", "project_key": "HD", "color": "#3F51B5", "primary_color": "#3F51B5", "locale": "en",
	"timezone": "Europe/Rome", "time_zone": "Europe/Rome", "slug": "reset-your-password", "filename": "screenshot.png",
	"content_type": "image/png", "mime_type": "image/png", "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	"token": "fd_pat_3fQ9…", "password": "correct-horse-battery-staple", "code": "123456", "reason": "Needs a network engineer",
	"message": "Enter a valid email address.", "detail": "The ticket was changed by someone else.", "field": "subject",
	"host": "smtp.example.com", "username": "support@example.com", "address": "support@example.com", "domain": "example.com",
	"channel_name": "support", "channel_id": "C0123ABCD", "client_id": "1234567890.1234567890", "team_name": "Acme",
	"bot_name": "featherdesk", "state": "c2xhY2stc3RhdGU", "query": "vpn", "q": "vpn", "tag": "vpn", "label": "Order number",
	"text": "Thanks, that fixed it!", "comment": "Quick and friendly.", "note": "Checked the firewall logs.",
	"scope": "tickets:read", "folder": "INBOX", "ip": "203.0.113.7", "user_agent": "Mozilla/5.0", "version": "1.1.0",
	"product_name": "Acme Help", "company_name": "Acme Ltd", "action": "ticket.status_changed", "event": "ticket.created",
	"type": "public", "kind": "public", "status": "open", "priority": "normal", "role": "agent", "phase": "V1",
	"en": "Billing", "it": "Fatturazione", "order_id": "A-10293", "value": "A-10293", "via_integration_name": "Shop integration",
	"notes": "Escalated after the second outage.", "reply_md": "Thanks, we are on it.", "portal_title": "Acme Help Center",
	"icon": "briefcase", "from_name": "Acme Support", "assignee": "me", "csrf_token": "8c1d4f2e9a6b3c70d5e1f4a2b9c8d7e6",
	"processed_folder": "Processed", "helo": "help.example.com", "subtitle": "Fast answers from real people",
	"policy_version": "2026-10", "mfa_token": "mfa_7Kq2vW9xYzR3", "disabled_reason": "failures", "custom_css": "/* custom CSS */",
	"prefix": "fd_pat_3fQ9", "otpauth_uri": "otpauth://totp/Acme:ada@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Acme",
	"oidc_label": "Sign in with Authentik", "from_addr": "support@example.com", "target_type": "ticket", "ticket": "HD-1042",
	"secret": "whsec_5f0c2b8e9d1a", "current_password": "old-horse-battery-staple", "commit": "41f2521", "bucket": "featherdesk",
	"ticket_subject": "VPN drops every 10 minutes", "excerpt": "Since this morning the VPN disconnects…", "strategy": "round_robin",
}

// bySuffix are realistic values by name suffix, tried in order.
var bySuffix = []struct{ suffix, value string }{
	{"_md", "Thanks for contacting **Acme** support. We reply within one business day."},
	{"_html", "<p>Thanks for contacting <strong>Acme</strong> support.</p>"},
	{"cursor", "eyJpZCI6IjAxOTJmMGM0In0"},
	{"error", "dial tcp 203.0.113.7:443: i/o timeout"},
	{"_secret", "whsec_5f0c2b8e9d1a"},
	{"_token", "tok_7Kq2vW9xYzR3"},
	{"_by", "Ada Lovelace"},
	{"_name", "Acme Support"},
	{"_title", "Acme Help Center"},
	{"_label", "Order number"},
	{"_folder", "Processed"},
	{"_reason", "Needs a network engineer"},
	{"_version", "2026-10"},
	{"_key", "order_id"},
}

var (
	reUUIDish = regexp.MustCompile(`(^|_)id$|^id$`)
	exUUID    = "0192f0c4-7a1e-7c3e-9a10-4b5f0e2d8a11"
)

func stringExample(s *base.Schema, name string) any {
	cands := []string{}
	switch s.Format {
	case "date-time":
		cands = append(cands, "2026-10-23T09:30:00Z")
	case "date":
		cands = append(cands, "2026-10-23")
	case "time":
		cands = append(cands, "09:30:00")
	case "uuid":
		cands = append(cands, exUUID)
	case "email", "idn-email":
		cands = append(cands, "ada@example.com")
	case "uri", "url", "iri":
		cands = append(cands, uriFor(name))
	case "uri-reference":
		cands = append(cands, "/agent/tickets/HD-1042")
	case "hostname":
		cands = append(cands, "help.example.com")
	case "ipv4":
		cands = append(cands, "203.0.113.7")
	case "binary":
		return "(binary file)"
	case "byte":
		cands = append(cands, "aGVsbG8=")
	case "password":
		cands = append(cands, "correct-horse-battery-staple")
	}
	if v, ok := byName[name]; ok {
		cands = append(cands, v)
	}
	if name == "key" {
		cands = append(cands, "order_id")
	}
	for _, r := range bySuffix {
		if strings.HasSuffix(name, r.suffix) {
			cands = append(cands, r.value)
			break
		}
	}
	switch {
	case reUUIDish.MatchString(name):
		cands = append(cands, exUUID)
	case strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "_date"):
		cands = append(cands, "2026-10-23T09:30:00Z")
	case strings.HasSuffix(name, "_url") || name == "url" || name == "href" || name == "endpoint":
		cands = append(cands, uriFor(name))
	case strings.HasSuffix(name, "email"):
		cands = append(cands, "ada@example.com")
	case strings.HasSuffix(name, "_ref"):
		cands = append(cands, "HD-1042")
	case name == "start" || strings.HasPrefix(name, "open") || strings.HasSuffix(name, "_from"):
		cands = append(cands, "09:00")
	case name == "end" || strings.HasPrefix(name, "close") || strings.HasSuffix(name, "_to"):
		cands = append(cands, "17:30")
	}
	cands = append(cands, "string", "example", "09:00", "HD-1042", "HD", "abc123", "1234567890.1234567890", "C0123ABCD", "#3F51B5", "a")
	var re *regexp.Regexp
	if s.Pattern != "" {
		re, _ = regexp.Compile(s.Pattern)
	}
	for _, c := range cands {
		if re != nil && !re.MatchString(c) {
			continue
		}
		if s.MaxLength != nil && int64(len([]rune(c))) > *s.MaxLength {
			continue
		}
		if s.MinLength != nil && int64(len([]rune(c))) < *s.MinLength {
			c += strings.Repeat("x", int(*s.MinLength)-len([]rune(c)))
			if re != nil && !re.MatchString(c) {
				continue
			}
		}
		return c
	}
	return "string" // the validation test reports it: add an `example:` to the spec
}

func uriFor(name string) string {
	switch {
	case strings.Contains(name, "logo") || strings.Contains(name, "avatar") || strings.Contains(name, "image"):
		return "https://help.example.com/branding/logo.png"
	case strings.Contains(name, "webhook") || name == "url":
		return "https://hooks.example.com/featherdesk"
	}
	return "https://help.example.com/agent/tickets/HD-1042"
}
