// SPDX-License-Identifier: AGPL-3.0-only

package automation

import (
	"encoding/json"
	"strings"
	"testing"
)

func facts() Facts {
	return Facts{
		Subject: "Printer OFFLINE on floor 3", Body: "Order #12345 is late, please refund", Channel: "email", Priority: "high",
		Status: "pending", Tier: 2, CategoryID: "cat-1", TeamID: "team-1", OrganizationID: "org-1", RequesterDomain: "acme.test",
		Tags: []string{"vip", "billing"}, Fields: map[string]any{"plan": "Enterprise", "seats": 25.0, "urgent": true, "regions": []string{"eu", "us"}},
		BusinessHours: true, HoursSinceUpdate: 80, HoursSinceStatus: 100,
	}
}

func cond(t *testing.T, c Condition, event string) Condition {
	t.Helper()
	r := Rule{Name: "x", Event: event, Conditions: []Condition{c}, Actions: []Action{{Type: ActEscalate}}}
	if errs := r.Validate(); len(errs) > 0 {
		t.Fatalf("validate %+v: %v", c, errs)
	}
	return r.Conditions[0]
}

// TestConditions_Each: every condition field with each of its operators,
// matching and not matching.
func TestConditions_Each(t *testing.T) {
	f := facts()
	cases := []struct {
		c     Condition
		event string
		want  bool
	}{
		{Condition{Field: FieldSubject, Op: OpContains, Value: "printer offline"}, EventTicketCreated, true},
		{Condition{Field: FieldSubject, Op: OpContains, Value: "scanner"}, EventTicketCreated, false},
		{Condition{Field: FieldSubject, Op: OpNotContains, Value: "scanner"}, EventTicketCreated, true},
		{Condition{Field: FieldSubject, Op: OpRegex, Value: `floor \d+$`}, EventTicketCreated, true},
		{Condition{Field: FieldSubject, Op: OpRegex, Value: `^floor`}, EventTicketCreated, false},
		{Condition{Field: FieldBody, Op: OpRegex, Value: `order #\d{5}`}, EventTicketCreated, true},
		{Condition{Field: FieldBody, Op: OpContains, Value: "REFUND"}, EventTicketCreated, true},
		{Condition{Field: FieldBody, Op: OpNotContains, Value: "refund"}, EventTicketCreated, false},
		{Condition{Field: FieldChannel, Op: OpIs, Value: "email"}, EventTicketCreated, true},
		{Condition{Field: FieldChannel, Op: OpIs, Value: []any{"portal", "api"}}, EventTicketCreated, false},
		{Condition{Field: FieldChannel, Op: OpIsNot, Value: "portal"}, EventTicketCreated, true},
		{Condition{Field: FieldPriority, Op: OpIs, Value: []any{"high", "urgent"}}, EventTicketCreated, true},
		{Condition{Field: FieldPriority, Op: OpIsNot, Value: "high"}, EventTicketCreated, false},
		{Condition{Field: FieldStatus, Op: OpIs, Value: "pending"}, EventTicketUpdated, true},
		{Condition{Field: FieldStatus, Op: OpIsNot, Value: "pending"}, EventTicketUpdated, false},
		{Condition{Field: FieldTier, Op: OpIs, Value: 2.0}, EventTicketCreated, true},
		{Condition{Field: FieldTier, Op: OpIsNot, Value: 2.0}, EventTicketCreated, false},
		{Condition{Field: FieldTier, Op: OpGte, Value: 2.0}, EventTicketCreated, true},
		{Condition{Field: FieldTier, Op: OpLte, Value: 1.0}, EventTicketCreated, false},
		{Condition{Field: FieldCategory, Op: OpIs, Value: "cat-1"}, EventTicketCreated, true},
		{Condition{Field: FieldCategory, Op: OpIsNot, Value: "cat-1"}, EventTicketCreated, false},
		{Condition{Field: FieldTeam, Op: OpIs, Value: "team-2"}, EventTicketCreated, false},
		{Condition{Field: FieldTeam, Op: OpIsNot, Value: "team-2"}, EventTicketCreated, true},
		{Condition{Field: FieldOrganization, Op: OpIs, Value: "org-1"}, EventTicketCreated, true},
		{Condition{Field: FieldOrganization, Op: OpIsNot, Value: "org-1"}, EventTicketCreated, false},
		{Condition{Field: FieldRequesterDomain, Op: OpIs, Value: "@ACME.test"}, EventTicketCreated, true},
		{Condition{Field: FieldRequesterDomain, Op: OpIsNot, Value: "acme.test"}, EventTicketCreated, false},
		{Condition{Field: FieldTag, Op: OpPresent, Value: "vip"}, EventTicketCreated, true},
		{Condition{Field: FieldTag, Op: OpPresent, Value: "spam"}, EventTicketCreated, false},
		{Condition{Field: FieldTag, Op: OpAbsent, Value: "spam"}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpEquals, Key: "plan", Value: "enterprise"}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpContains, Key: "plan", Value: "ERPR"}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpEquals, Key: "plan", Value: "free"}, EventTicketCreated, false},
		{Condition{Field: FieldCustomField, Op: OpEquals, Key: "seats", Value: 25.0}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpEquals, Key: "urgent", Value: true}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpContains, Key: "regions", Value: "EU"}, EventTicketCreated, true},
		{Condition{Field: FieldCustomField, Op: OpEquals, Key: "missing", Value: "x"}, EventTicketCreated, false},
		{Condition{Field: FieldBusinessHours, Op: OpIs, Value: true}, EventTicketCreated, true},
		{Condition{Field: FieldBusinessHours, Op: OpIs, Value: false}, EventTicketCreated, false},
		{Condition{Field: FieldHoursSinceUpdate, Op: OpGte, Value: 72.0}, EventTime, true},
		{Condition{Field: FieldHoursSinceUpdate, Op: OpGte, Value: 96.0}, EventTime, false},
		{Condition{Field: FieldHoursSinceStatus, Op: OpGte, Value: 96.0}, EventTime, true},
		{Condition{Field: FieldHoursSinceStatus, Op: OpGte, Value: 120.0}, EventTime, false},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		c := cond(t, tc.c, tc.event)
		if got := c.Match(f); got != tc.want {
			t.Errorf("%s %s %v: got %v, want %v", tc.c.Field, tc.c.Op, tc.c.Value, got, tc.want)
		}
		seen[tc.c.Field+"/"+tc.c.Op] = true
	}
	for field, ops := range fieldOps {
		for _, op := range ops {
			if !seen[field+"/"+op] {
				t.Errorf("no case for %s %s", field, op)
			}
		}
	}
}

// TestRule_AllAny: match all needs every condition, any needs one; no
// conditions always match.
func TestRule_AllAny(t *testing.T) {
	yes := Condition{Field: FieldTag, Op: OpPresent, Value: "vip"}
	no := Condition{Field: FieldTag, Op: OpPresent, Value: "spam"}
	for _, tc := range []struct {
		match string
		conds []Condition
		want  bool
	}{
		{"all", []Condition{yes, yes}, true},
		{"all", []Condition{yes, no}, false},
		{"any", []Condition{no, yes}, true},
		{"any", []Condition{no, no}, false},
		{"all", nil, true},
	} {
		r := Rule{Name: "r", Event: EventTicketCreated, Match: tc.match, Conditions: tc.conds, Actions: []Action{{Type: ActEscalate}}}
		if errs := r.Validate(); len(errs) > 0 {
			t.Fatal(errs)
		}
		got, per := r.Evaluate(facts())
		if got != tc.want || len(per) != len(tc.conds) {
			t.Errorf("%s %v: got %v (%v)", tc.match, tc.conds, got, per)
		}
	}
}

// TestRule_Validation: structural errors carry the JSON path and a
// FieldErrorCode.
func TestRule_Validation(t *testing.T) {
	long := strings.Repeat("a", MaxRegexLen+1)
	cases := []struct {
		r    Rule
		path string
		code string
	}{
		{Rule{Event: EventTicketCreated, Actions: []Action{{Type: ActEscalate}}}, "name", "length"},
		{Rule{Name: "x", Event: "ticket.deleted", Actions: []Action{{Type: ActEscalate}}}, "event", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated}, "actions", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: make([]Action, 11)}, "actions", "max_items"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldSubject, Op: OpRegex, Value: "(a"}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].value", "pattern"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldSubject, Op: OpRegex, Value: long}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].value", "max_length"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldTier, Op: OpContains, Value: 1.0}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].op", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldPriority, Op: OpIs, Value: "huge"}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].value", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldHoursSinceUpdate, Op: OpGte, Value: 3.0}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].field", "not_applicable"},
		{Rule{Name: "x", Event: EventTime, Conditions: []Condition{{Field: FieldStatus, Op: OpIs, Value: "pending"}}, Actions: []Action{{Type: ActEscalate}}}, "conditions", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Conditions: []Condition{{Field: FieldCustomField, Op: OpEquals, Value: "a"}}, Actions: []Action{{Type: ActEscalate}}}, "conditions[0].key", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActSetPriority, Value: "huge"}}}, "actions[0].value", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActSetStatus, Value: "new"}}}, "actions[0].value", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActSetTier, Value: 4.0}}}, "actions[0].value", "range"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActAddTag, Value: "Not A Tag!"}}}, "actions[0].value", "pattern"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActAssign}}}, "actions[0].user_id", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActAssign, Strategy: "random"}}}, "actions[0].strategy", "enum"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActAddNote, BodyMD: "  "}}}, "actions[0].body_md", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: ActSendReply}}}, "actions[0].canned_response_id", "required"},
		{Rule{Name: "x", Event: EventTicketCreated, Actions: []Action{{Type: "delete_ticket"}}}, "actions[0].type", "enum"},
	}
	for _, tc := range cases {
		errs := tc.r.Validate()
		found := false
		for _, e := range errs {
			if e.Field == tc.path && e.Code == tc.code {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s %s, got %+v", tc.r.Name, tc.path, tc.code, errs)
		}
	}
}

// TestRule_JSONRoundTrip: a stored rule decodes and prepares to the same
// behaviour (regexes are compiled by Prepare).
func TestRule_JSONRoundTrip(t *testing.T) {
	r := Rule{Name: "r", Event: EventTicketCreated, Match: "all",
		Conditions: []Condition{{Field: FieldSubject, Op: OpRegex, Value: "printer"}, {Field: FieldBusinessHours, Op: OpIs, Value: false}},
		Actions:    []Action{{Type: ActSetTier, Value: 2.0}, {Type: ActAddTag, Value: "Hardware"}}}
	if errs := r.Validate(); len(errs) > 0 {
		t.Fatal(errs)
	}
	b, _ := json.Marshal(r)
	var back Rule
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back.Prepare()
	f := facts()
	f.BusinessHours = false
	if ok, _ := back.Evaluate(f); !ok {
		t.Fatalf("round-tripped rule does not match: %s", b)
	}
	if back.Actions[1].Str() != "hardware" {
		t.Fatalf("tag not normalised: %v", back.Actions[1].Value)
	}
	if n, ok := back.Actions[0].Int(); !ok || n != 2 {
		t.Fatalf("tier: %v", back.Actions[0].Value)
	}
}

// TestRegex_InputCapped: matching never looks past MaxMatchInput bytes.
func TestRegex_InputCapped(t *testing.T) {
	c := cond(t, Condition{Field: FieldBody, Op: OpRegex, Value: "needle"}, EventTicketCreated)
	f := facts()
	f.Body = strings.Repeat("x", MaxMatchInput) + "needle"
	if c.Match(f) {
		t.Fatal("matched beyond the input cap")
	}
}
