// SPDX-License-Identifier: AGPL-3.0-only

// Package automation is the rule model of D-19 §A: trigger and time-based
// rules (conditions ALL/ANY → actions), their structural validation and their
// evaluation against a snapshot of facts about one ticket.
//
// It is pure: no store, no HTTP. The ticket service (internal/tickets) loads
// the facts, checks references (teams, categories, users, canned responses,
// custom fields) and executes the actions under the system actor
// events.ActorAutomation.
package automation

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/problems"
)

// Rule events.
const (
	EventTicketCreated  = "ticket.created"
	EventMessageCreated = "message.created" // customer public replies only
	EventTicketUpdated  = "ticket.updated"
	EventTime           = "time"
)

// Events lists the valid rule events.
var Events = []string{EventTicketCreated, EventMessageCreated, EventTicketUpdated, EventTime}

// Condition fields.
const (
	FieldSubject          = "subject"
	FieldBody             = "body"
	FieldChannel          = "channel"
	FieldPriority         = "priority"
	FieldStatus           = "status"
	FieldTier             = "tier"
	FieldCategory         = "category_id"
	FieldTeam             = "team_id"
	FieldOrganization     = "organization_id"
	FieldRequesterDomain  = "requester_domain"
	FieldTag              = "tag"
	FieldCustomField      = "custom_field"
	FieldBusinessHours    = "business_hours"
	FieldHoursSinceUpdate = "hours_since_update"
	FieldHoursSinceStatus = "hours_since_status"
)

// Operators.
const (
	OpIs          = "is"
	OpIsNot       = "is_not"
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpRegex       = "regex"
	OpGte         = "gte"
	OpLte         = "lte"
	OpPresent     = "present"
	OpAbsent      = "absent"
	OpEquals      = "equals"
)

// Action types.
const (
	ActSetPriority = "set_priority"
	ActSetTeam     = "set_team"
	ActSetCategory = "set_category"
	ActSetStatus   = "set_status"
	ActSetTier     = "set_tier"
	ActAddTag      = "add_tag"
	ActRemoveTag   = "remove_tag"
	ActAssign      = "assign"
	ActAddNote     = "add_note"
	ActSendReply   = "send_reply"
	ActEscalate    = "escalate"
	ActAddWatcher  = "add_watcher"
)

// ActionTypes lists every action type.
var ActionTypes = []string{ActSetPriority, ActSetTeam, ActSetCategory, ActSetStatus, ActSetTier, ActAddTag, ActRemoveTag,
	ActAssign, ActAddNote, ActSendReply, ActEscalate, ActAddWatcher}

// Limits.
const (
	MaxConditions     = 20
	MaxActions        = 10 // per rule, and per event across all rules
	MaxNameLen        = 120
	MaxRegexLen       = 500
	MaxNoteLen        = 10000
	MaxMatchInput     = 64 << 10 // bytes of subject/body a condition looks at
	MaxHours          = 24 * 365
	MaxValueListItems = 50
)

// fieldOps are the operators each field accepts.
var fieldOps = map[string][]string{
	FieldSubject:          {OpContains, OpNotContains, OpRegex},
	FieldBody:             {OpContains, OpNotContains, OpRegex},
	FieldChannel:          {OpIs, OpIsNot},
	FieldPriority:         {OpIs, OpIsNot},
	FieldStatus:           {OpIs, OpIsNot},
	FieldTier:             {OpIs, OpIsNot, OpGte, OpLte},
	FieldCategory:         {OpIs, OpIsNot},
	FieldTeam:             {OpIs, OpIsNot},
	FieldOrganization:     {OpIs, OpIsNot},
	FieldRequesterDomain:  {OpIs, OpIsNot},
	FieldTag:              {OpPresent, OpAbsent},
	FieldCustomField:      {OpEquals, OpContains},
	FieldBusinessHours:    {OpIs},
	FieldHoursSinceUpdate: {OpGte},
	FieldHoursSinceStatus: {OpGte},
}

// Enumerations a condition value or action value must belong to.
var (
	Channels   = []string{"portal", "agent", "api", "email"}
	Priorities = []string{"low", "normal", "high", "urgent"}
	Statuses   = []string{"new", "open", "pending", "on_hold", "resolved", "closed"}
	Strategies = []string{"round_robin", "least_busy"}
)

// Condition is one AutomationCondition.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Key   string `json:"key,omitempty"`
	Value any    `json:"value,omitempty"`

	// Prepared by Rule.Prepare.
	list  []string
	num   float64
	flag  bool
	re    *regexp.Regexp
	ready bool
}

// Action is one AutomationAction.
type Action struct {
	Type             string `json:"type"`
	Value            any    `json:"value,omitempty"`
	Strategy         string `json:"strategy,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	BodyMD           string `json:"body_md,omitempty"`
	CannedResponseID string `json:"canned_response_id,omitempty"`
}

// Str returns the action value as a string ("" when absent).
func (a Action) Str() string {
	switch v := a.Value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

// Int returns the action value as an integer (tier); ok false when it is not one.
func (a Action) Int() (int, bool) {
	switch v := a.Value.(type) {
	case float64:
		if v == math.Trunc(v) {
			return int(v), true
		}
	case int:
		return v, true
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	return 0, false
}

// Rule is one AutomationRule.
type Rule struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Event          string      `json:"event"`
	Match          string      `json:"match"`
	Conditions     []Condition `json:"conditions"`
	Actions        []Action    `json:"actions"`
	Enabled        bool        `json:"enabled"`
	StopProcessing bool        `json:"stop_processing"`
	Position       int         `json:"position"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

// Facts is what conditions see about one ticket.
type Facts struct {
	Subject          string
	Body             string
	Channel          string
	Priority         string
	Status           string
	Tier             int
	CategoryID       string
	TeamID           string
	OrganizationID   string
	RequesterDomain  string
	Tags             []string
	Fields           map[string]any // custom field key → string | float64 | bool | []string
	BusinessHours    bool
	HoursSinceUpdate float64
	HoursSinceStatus float64
}

// Validate checks the structure of r (not references) and prepares its
// conditions. Field paths are `name`, `conditions[i].value`, `actions[i].type`, …
func (r *Rule) Validate() []problems.FieldError {
	var errs []problems.FieldError
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || utf8.RuneCountInString(r.Name) > MaxNameLen {
		errs = append(errs, problems.Field("name", "length", "1..120 characters"))
	}
	if !slices.Contains(Events, r.Event) {
		errs = append(errs, problems.Field("event", "enum", "ticket.created, message.created, ticket.updated or time"))
	}
	if r.Match == "" {
		r.Match = "all"
	}
	if r.Match != "all" && r.Match != "any" {
		errs = append(errs, problems.Field("match", "enum", "all or any"))
	}
	if r.Conditions == nil {
		r.Conditions = []Condition{}
	}
	if len(r.Conditions) > MaxConditions {
		errs = append(errs, problems.Field("conditions", "max_items", "at most 20 conditions"))
	}
	timed := false
	for i := range r.Conditions {
		c := &r.Conditions[i]
		if c.Field == FieldHoursSinceUpdate || c.Field == FieldHoursSinceStatus {
			timed = true
			if r.Event != EventTime {
				errs = append(errs, problems.Field(fmt.Sprintf("conditions[%d].field", i), "not_applicable", "only time rules can use hours since"))
				continue
			}
		}
		errs = append(errs, c.prepare(fmt.Sprintf("conditions[%d]", i))...)
	}
	if r.Event == EventTime && !timed {
		errs = append(errs, problems.Field("conditions", "required", "a time rule needs hours_since_update or hours_since_status"))
	}
	if len(r.Actions) == 0 {
		errs = append(errs, problems.Field("actions", "required", "at least one action"))
	}
	if len(r.Actions) > MaxActions {
		errs = append(errs, problems.Field("actions", "max_items", "at most 10 actions"))
	}
	for i := range r.Actions {
		errs = append(errs, r.Actions[i].validate(fmt.Sprintf("actions[%d]", i))...)
	}
	return errs
}

// Prepare compiles the conditions of a rule loaded from the store. Invalid
// conditions (cannot happen after Validate) never match.
func (r *Rule) Prepare() {
	for i := range r.Conditions {
		_ = r.Conditions[i].prepare("")
	}
}

func (c *Condition) prepare(path string) []problems.FieldError {
	c.ready = false
	fieldErr := func(sub, code, msg string) []problems.FieldError {
		return []problems.FieldError{problems.Field(path+"."+sub, code, msg)}
	}
	ops, ok := fieldOps[c.Field]
	if !ok {
		return fieldErr("field", "enum", "unknown condition field")
	}
	if !slices.Contains(ops, c.Op) {
		return fieldErr("op", "enum", "operator not allowed for "+c.Field+": "+strings.Join(ops, ", "))
	}
	switch c.Field {
	case FieldSubject, FieldBody:
		s, ok := c.Value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return fieldErr("value", "required", "a text value")
		}
		if c.Op == OpRegex {
			if utf8.RuneCountInString(s) > MaxRegexLen {
				return fieldErr("value", "max_length", "at most 500 characters")
			}
			re, err := regexp.Compile("(?i)" + s)
			if err != nil {
				return fieldErr("value", "pattern", "not a valid RE2 regular expression")
			}
			c.re = re
		} else {
			if utf8.RuneCountInString(s) > MaxRegexLen {
				return fieldErr("value", "max_length", "at most 500 characters")
			}
			c.list = []string{strings.ToLower(s)}
		}
	case FieldTier:
		n, ok := number(c.Value)
		if !ok || n != math.Trunc(n) || n < 1 || n > 3 {
			return fieldErr("value", "range", "a tier 1..3")
		}
		c.num = n
	case FieldBusinessHours:
		b, ok := c.Value.(bool)
		if !ok {
			return fieldErr("value", "boolean", "true or false")
		}
		c.flag = b
	case FieldHoursSinceUpdate, FieldHoursSinceStatus:
		n, ok := number(c.Value)
		if !ok || n <= 0 || n > MaxHours {
			return fieldErr("value", "range", "hours, 1..8760")
		}
		c.num = n
	case FieldTag:
		s, ok := c.Value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return fieldErr("value", "required", "a tag name")
		}
		c.list = []string{strings.ToLower(strings.TrimSpace(s))}
	case FieldCustomField:
		if strings.TrimSpace(c.Key) == "" {
			return fieldErr("key", "required", "a custom field key")
		}
		switch v := c.Value.(type) {
		case string:
			c.list = []string{strings.ToLower(v)}
		case bool:
			c.flag, c.list = v, []string{strconv.FormatBool(v)}
		case float64:
			c.num, c.list = v, []string{strconv.FormatFloat(v, 'f', -1, 64)}
		default:
			return fieldErr("value", "required", "a text, number or boolean value")
		}
	default: // is / is_not over an enum or an id: one value or a list
		list, ok := stringList(c.Value)
		if !ok || len(list) == 0 {
			return fieldErr("value", "required", "a value or a list of values")
		}
		if len(list) > MaxValueListItems {
			return fieldErr("value", "max_items", "at most 50 values")
		}
		var enum []string
		switch c.Field {
		case FieldChannel:
			enum = Channels
		case FieldPriority:
			enum = Priorities
		case FieldStatus:
			enum = Statuses
		}
		for i, v := range list {
			list[i] = strings.TrimSpace(v)
			if c.Field == FieldRequesterDomain {
				list[i] = strings.ToLower(strings.TrimPrefix(list[i], "@"))
			}
			if enum != nil && !slices.Contains(enum, list[i]) {
				return fieldErr("value", "enum", "one of "+strings.Join(enum, ", "))
			}
		}
		c.list = list
	}
	c.ready = true
	return nil
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

func stringList(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []string:
		return append([]string(nil), x...), true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

func (a *Action) validate(path string) []problems.FieldError {
	fieldErr := func(sub, code, msg string) []problems.FieldError {
		return []problems.FieldError{problems.Field(path+"."+sub, code, msg)}
	}
	switch a.Type {
	case ActSetPriority:
		if !slices.Contains(Priorities, a.Str()) {
			return fieldErr("value", "enum", "low, normal, high or urgent")
		}
	case ActSetStatus:
		if !slices.Contains(Statuses, a.Str()) || a.Str() == "new" {
			return fieldErr("value", "enum", "open, pending, on_hold, resolved or closed")
		}
	case ActSetTier:
		n, ok := a.Int()
		if !ok || n < 1 || n > 3 {
			return fieldErr("value", "range", "a tier 1..3")
		}
		a.Value = n
	case ActSetTeam, ActSetCategory:
		if _, ok := a.Value.(string); !ok && a.Value != nil {
			return fieldErr("value", "type", "an id, or empty for none")
		}
	case ActAddTag, ActRemoveTag:
		s := strings.ToLower(strings.TrimSpace(a.Str()))
		if !tagRe.MatchString(s) {
			return fieldErr("value", "pattern", "1..40 characters of a-z, 0-9 and -")
		}
		a.Value = s
	case ActAssign:
		switch {
		case a.Strategy != "" && a.UserID != "":
			return fieldErr("strategy", "invalid", "a strategy or a user, not both")
		case a.Strategy != "":
			if !slices.Contains(Strategies, a.Strategy) {
				return fieldErr("strategy", "enum", "round_robin or least_busy")
			}
		case a.UserID == "":
			return fieldErr("user_id", "required", "a strategy or a user")
		}
	case ActAddNote:
		b := strings.TrimSpace(a.BodyMD)
		if b == "" {
			return fieldErr("body_md", "required", "the note text")
		}
		if utf8.RuneCountInString(a.BodyMD) > MaxNoteLen {
			return fieldErr("body_md", "max_length", "at most 10000 characters")
		}
	case ActSendReply:
		if a.CannedResponseID == "" {
			return fieldErr("canned_response_id", "required", "a canned response")
		}
	case ActAddWatcher:
		if a.UserID == "" {
			return fieldErr("user_id", "required", "a staff user")
		}
	case ActEscalate:
	default:
		return fieldErr("type", "enum", "unknown action type")
	}
	return nil
}

var tagRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// Evaluate reports whether r matches f, and the result of each condition.
// A rule without conditions always matches.
func (r *Rule) Evaluate(f Facts) (bool, []bool) {
	res := make([]bool, len(r.Conditions))
	if len(r.Conditions) == 0 {
		return true, res
	}
	anyOK, allOK := false, true
	for i := range r.Conditions {
		res[i] = r.Conditions[i].Match(f)
		anyOK = anyOK || res[i]
		allOK = allOK && res[i]
	}
	if r.Match == "any" {
		return anyOK, res
	}
	return allOK, res
}

// Match evaluates one prepared condition.
func (c *Condition) Match(f Facts) bool {
	if !c.ready {
		return false
	}
	switch c.Field {
	case FieldSubject:
		return c.text(f.Subject)
	case FieldBody:
		return c.text(f.Body)
	case FieldChannel:
		return c.in(f.Channel)
	case FieldPriority:
		return c.in(f.Priority)
	case FieldStatus:
		return c.in(f.Status)
	case FieldCategory:
		return c.in(f.CategoryID)
	case FieldTeam:
		return c.in(f.TeamID)
	case FieldOrganization:
		return c.in(f.OrganizationID)
	case FieldRequesterDomain:
		return c.in(strings.ToLower(f.RequesterDomain))
	case FieldTier:
		t := float64(f.Tier)
		switch c.Op {
		case OpIs:
			return t == c.num
		case OpIsNot:
			return t != c.num
		case OpGte:
			return t >= c.num
		case OpLte:
			return t <= c.num
		}
	case FieldTag:
		has := slices.Contains(f.Tags, c.list[0])
		return has == (c.Op == OpPresent)
	case FieldCustomField:
		return c.field(f.Fields[c.Key])
	case FieldBusinessHours:
		return f.BusinessHours == c.flag
	case FieldHoursSinceUpdate:
		return f.HoursSinceUpdate >= c.num
	case FieldHoursSinceStatus:
		return f.HoursSinceStatus >= c.num
	}
	return false
}

func (c *Condition) text(s string) bool {
	if len(s) > MaxMatchInput {
		s = s[:MaxMatchInput]
	}
	switch c.Op {
	case OpRegex:
		return c.re.MatchString(s)
	case OpContains:
		return strings.Contains(strings.ToLower(s), c.list[0])
	case OpNotContains:
		return !strings.Contains(strings.ToLower(s), c.list[0])
	}
	return false
}

func (c *Condition) in(v string) bool {
	found := slices.Contains(c.list, v)
	if c.Op == OpIsNot {
		return !found
	}
	return found
}

// field compares a custom field value: equals is exact (case-insensitive for
// text; any element of a multiselect), contains is a substring of the text or
// membership in a multiselect.
func (c *Condition) field(v any) bool {
	want := c.list[0]
	switch x := v.(type) {
	case nil:
		return false
	case string:
		s := strings.ToLower(x)
		if c.Op == OpContains {
			return strings.Contains(s, want)
		}
		return s == want
	case bool:
		return strconv.FormatBool(x) == want
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64) == want
	case []string:
		for _, e := range x {
			if strings.ToLower(e) == want {
				return true
			}
		}
	}
	return false
}
