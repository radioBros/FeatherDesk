// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/radioBros/FeatherDesk/api"
	"github.com/radioBros/FeatherDesk/internal/domain"
)

func TestSpecIsValidOpenAPI31(t *testing.T) {
	doc, err := Document()
	if err != nil {
		t.Fatal(err)
	}
	if v := doc.GetVersion(); v != "3.1.0" {
		t.Fatalf("spec version %q, want 3.1.0", v)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		t.Fatalf("build model: %v", err)
	}
	if model.Model.Paths.PathItems.Len() < 100 {
		t.Fatalf("suspiciously few paths: %d", model.Model.Paths.PathItems.Len())
	}
	v, err := Validator()
	if err != nil {
		t.Fatal(err)
	}
	if ok, errs := v.ValidateDocument(); !ok {
		for _, e := range errs {
			t.Errorf("schema: %s %s", e.Message, e.Reason)
		}
	}
}

// Every operation has a unique operationId and a phase marker.
func TestOperationIDs(t *testing.T) {
	var raw struct {
		Paths    map[string]map[string]yaml.Node `yaml:"paths"`
		Webhooks map[string]map[string]yaml.Node `yaml:"webhooks"`
	}
	if err := yaml.Unmarshal(api.Spec, &raw); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for p, item := range raw.Paths {
		for _, m := range methods {
			n, ok := item[m]
			if !ok {
				continue
			}
			var op struct {
				OperationID string `yaml:"operationId"`
				Phase       string `yaml:"x-phase"`
			}
			if err := n.Decode(&op); err != nil {
				t.Fatal(err)
			}
			where := strings.ToUpper(m) + " " + p
			if op.OperationID == "" {
				t.Errorf("%s: missing operationId", where)
			}
			if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %s used by %s and %s", op.OperationID, prev, where)
			}
			seen[op.OperationID] = where
			if op.Phase != "CORE" && op.Phase != "V1" {
				t.Errorf("%s: x-phase %q, want CORE or V1", where, op.Phase)
			}
		}
	}
	if len(raw.Webhooks) == 0 {
		t.Error("top-level webhooks section is empty")
	}
}

func TestRoutesInventory(t *testing.T) {
	r := chi.NewRouter()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	r.Get("/healthz", h)
	r.Get("/branding/{file}", h)
	r.Get("/*", h) // SPA fallback is ignored
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/auth/config", h)
		r.Route("/agent", func(r chi.Router) {
			r.Get("/tickets/{ticketID:[0-9a-f-]+}", h)
			r.Patch("/tickets/{id}", h)
		})
		r.Post("/not-in-spec", h)
	})
	missing, err := MissingRoutes(r)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(missing, []string{"POST /api/v1/not-in-spec"}) {
		t.Fatalf("missing = %v", missing)
	}
	unimpl, err := Unimplemented(r, "/api/v1/auth/")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(unimpl, "GET /api/v1/auth/config") || !slices.Contains(unimpl, "POST /api/v1/auth/login") {
		t.Fatalf("unimplemented = %v", unimpl)
	}
}

func roundTrip(method, path, body, status, ctype, respBody string) (*http.Request, *http.Response) {
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", ctype)
	rec.WriteHeader(atoi(status))
	rec.Body.WriteString(respBody)
	return req, rec.Result()
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

const authConfig = `{"staff":{"oidc":true,"local":false},"customer":{"local":true,"oidc":false,"self_registration":true},
"branding":{"product_name":"Acme Help","colors":{"primary":"#3355ff"},"default_theme":"system","default_locale":"en"},
"email_enabled":true,"setup_pending":false,"portal_enabled":true}`

func TestValidateResponse(t *testing.T) {
	req, resp := roundTrip("GET", "/api/v1/auth/config", "", "200", "application/json", authConfig)
	if errs, err := ResponseErrors(req, resp); err != nil || len(errs) > 0 {
		t.Fatalf("valid auth config rejected: %v %v", err, errs)
	}
	req, resp = roundTrip("GET", "/api/v1/auth/config", "", "200", "application/json", `{"staff":{}}`)
	if errs, _ := ResponseErrors(req, resp); len(errs) == 0 {
		t.Fatal("invalid auth config accepted")
	}
	// Problem responses validate too.
	req, resp = roundTrip("GET", "/api/v1/agent/tickets/0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f", "", "404", "application/problem+json",
		`{"type":"https://featherdesk.dev/problems/not-found","title":"Not found","status":404}`)
	if errs, err := ResponseErrors(req, resp); err != nil || len(errs) > 0 {
		t.Fatalf("problem rejected: %v %v", err, errs)
	}
}

// I-9 / I-2 at the contract level: the customer ticket schema rejects staff-only fields.
func TestCustomerTicketSchemaHasNoStaffFields(t *testing.T) {
	base := `{"id":"0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f","number":1042,"ref":"HD-1042","project_id":"00000000-0000-7000-8000-000000000001",
"project":{"id":"00000000-0000-7000-8000-000000000001","key":"HD","name":"Help Desk","color":""},"subject":"x","status":"open","priority":"normal",
"requester":{"id":"0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e60","name":"Ada","kind":"customer"},"messages":[],"allowed_actions":["reply"],
"created_at":"2026-10-20T10:00:00Z","updated_at":"2026-10-20T10:00:00Z"%s}`
	path := "/api/v1/portal/tickets/0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f"
	req, resp := roundTrip("GET", path, "", "200", "application/json", strings.Replace(base, "%s", "", 1))
	if errs, err := ResponseErrors(req, resp); err != nil || len(errs) > 0 {
		t.Fatalf("valid customer ticket rejected: %v %v", err, errs)
	}
	for _, staffField := range []string{`,"tier":2`, `,"team_id":null`, `,"assignee":null`} {
		req, resp = roundTrip("GET", path, "", "200", "application/json", strings.Replace(base, "%s", staffField, 1))
		if errs, _ := ResponseErrors(req, resp); len(errs) == 0 {
			t.Errorf("customer ticket with %s accepted", staffField)
		}
	}
	internal := strings.Replace(base, `"messages":[]`, `"messages":[{"id":"0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e61","ticket_id":"0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f","kind":"internal","body_md":"secret","attachments":[],"created_at":"2026-10-20T10:00:00Z"}]`, 1)
	req, resp = roundTrip("GET", path, "", "200", "application/json", strings.Replace(internal, "%s", "", 1))
	if errs, _ := ResponseErrors(req, resp); len(errs) == 0 {
		t.Error("customer ticket with an internal message accepted")
	}
}

func TestValidateRequest(t *testing.T) {
	req := httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", strings.NewReader(`{"audience":"customer","email":"a@b.co","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	if errs, err := RequestErrors(req); err != nil || len(errs) > 0 {
		t.Fatalf("valid login rejected: %v %v", err, errs)
	}
	req = httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", strings.NewReader(`{"audience":"robot","email":"a@b.co"}`))
	req.Header.Set("Content-Type", "application/json")
	if errs, _ := RequestErrors(req); len(errs) == 0 {
		t.Fatal("invalid login accepted")
	}
}

func TestRootPathsResolve(t *testing.T) {
	ops, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GET /healthz", "GET /readyz", "GET /metrics", "GET /branding/{}", "GET /kb/sitemap.xml",
		"GET /api/v1/kb/articles", "GET /api/v1/ws", "POST /api/v1/inbound/mime/{}", "GET /api/v1/openapi.yaml"} {
		if !ops[want] {
			t.Errorf("spec lacks %s", want)
		}
	}
}

// CON-005 (OpenAPI part): spec enums equal internal/domain.
func TestEnumsMatchOpenAPI(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	str := func(vs ...any) []string {
		var out []string
		for _, v := range vs {
			switch x := v.(type) {
			case string:
				out = append(out, x)
			}
		}
		return out
	}
	conv := func(n int, f func(i int) string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	cases := map[string][]string{
		"TicketStatus": conv(len(domain.TicketStatuses), func(i int) string { return string(domain.TicketStatuses[i]) }),
		"Priority":     conv(len(domain.Priorities), func(i int) string { return string(domain.Priorities[i]) }),
		"Channel":      conv(len(domain.Channels), func(i int) string { return string(domain.Channels[i]) }),
		"MessageKind":  conv(len(domain.MessageKinds), func(i int) string { return string(domain.MessageKinds[i]) }),
		"LinkType":     conv(len(domain.LinkTypes), func(i int) string { return string(domain.LinkTypes[i]) }),
		"FieldType":    conv(len(domain.FieldTypes), func(i int) string { return string(domain.FieldTypes[i]) }),
		"StaffRole":    conv(len(domain.StaffRoles), func(i int) string { return string(domain.StaffRoles[i]) }),
		"CustomerRole": conv(len(domain.CustomerRoles), func(i int) string { return string(domain.CustomerRoles[i]) }),
		"Locale":       domain.Locales,
		"Scope":        domain.Scopes,
		"UserKind":     str(string(domain.KindStaff), string(domain.KindCustomer)),
		"UserStatus":   str(string(domain.UserActive), string(domain.UserPendingVerification), string(domain.UserDisabled)),
		"Audience":     str(string(domain.AudienceStaff), string(domain.AudienceCustomer)),
		"KbVisibility": str(string(domain.KBPublic), string(domain.KBCustomers), string(domain.KBInternal)),
		"KbStatus":     str(string(domain.KBDraft), string(domain.KBPublished), string(domain.KBArchived)),
		"ProjectRole":  conv(len(domain.ProjectRoles), func(i int) string { return string(domain.ProjectRoles[i]) }),
		"Role": append(conv(len(domain.StaffRoles), func(i int) string { return string(domain.StaffRoles[i]) }),
			conv(len(domain.CustomerRoles), func(i int) string { return string(domain.CustomerRoles[i]) })...),
	}
	for name, want := range cases {
		got := s.Components.Schemas[name].Enum
		a, b := slices.Clone(got), slices.Clone(want)
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			t.Errorf("enum %s: spec %v, domain %v", name, got, want)
		}
	}
}

// TestSchemaErrors_WsServerFrame: SchemaErrors really validates component
// schemas (CON-013 relies on it for WebSocket frames).
func TestSchemaErrors_WsServerFrame(t *testing.T) {
	valid := []string{
		`{"type":"ack","id":"s1","ok":true,"denied":["ticket:x"]}`,
		`{"type":"event","topic":"tickets","event_id":"e","event":"message.created","at":"2026-10-21T10:00:00Z","data":{}}`,
		`{"type":"resync","reason":"permissions_changed"}`,
		`{"type":"presence","topic":"ticket:x","users":[]}`,
		`{"type":"pong","id":"p"}`,
	}
	for _, doc := range valid {
		if errs, err := SchemaErrors("WsServerFrame", []byte(doc)); err != nil || len(errs) > 0 {
			t.Errorf("valid frame rejected %s: %v %v", doc, err, errs)
		}
	}
	invalid := []string{
		`{"type":"event","topic":"tickets","event":"message.created"}`, // no at
		`{"type":"resync","reason":"encode_error"}`,                    // reason not in the enum
		`{"type":"presence","topic":"ticket:x"}`,                       // no users
		`{"type":"ack"}`,                                               // no ok
		`{"type":"bogus"}`,
	}
	for _, doc := range invalid {
		if errs, err := SchemaErrors("WsServerFrame", []byte(doc)); err != nil || len(errs) == 0 {
			t.Errorf("invalid frame accepted %s: %v", doc, err)
		}
	}
	if _, err := SchemaErrors("NoSuchSchema", []byte(`{}`)); err == nil {
		t.Error("unknown schema accepted")
	}
}
