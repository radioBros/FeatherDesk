// SPDX-License-Identifier: AGPL-3.0-only

// Package apispec checks the HTTP surface against api/openapi.yaml (D-18):
// every chi route must be documented, and handler tests validate requests and
// responses with libopenapi-validator. Test-only: never import from runtime code.
package apispec

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/schema_validation"
	v3high "github.com/pb33f/libopenapi/datamodel/high/v3"
	"gopkg.in/yaml.v3"

	"github.com/radioBros/FeatherDesk/api"
)

var methods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// Spec is the minimal YAML view needed for route inventory and enum checks.
type Spec struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Enum []string `yaml:"enum"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

var (
	loadOnce sync.Once
	spec     *Spec
	loadErr  error

	valOnce sync.Once
	val     validator.Validator
	valErr  error
)

// Load parses the embedded spec (cached).
func Load() (*Spec, error) {
	loadOnce.Do(func() {
		var s Spec
		loadErr = yaml.Unmarshal(api.Spec, &s)
		spec = &s
	})
	return spec, loadErr
}

// Document returns a fresh libopenapi document of the embedded spec.
func Document() (libopenapi.Document, error) { return libopenapi.NewDocument(api.Spec) }

// Validator returns the shared request/response validator.
func Validator() (validator.Validator, error) {
	valOnce.Do(func() {
		doc, err := Document()
		if err != nil {
			valErr = err
			return
		}
		v, errs := validator.NewValidator(doc)
		if len(errs) > 0 {
			valErr = fmt.Errorf("apispec: build validator: %v", errs)
			return
		}
		val = v
	})
	return val, valErr
}

var paramRe = regexp.MustCompile(`\{[^}]*\}`)

// Normalize turns `/api/v1/tickets/{id:[0-9]+}` into `/api/v1/tickets/{}`.
func Normalize(path string) string {
	path = paramRe.ReplaceAllString(path, "{}")
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

// Operations returns the set of "METHOD /full/path" in the spec, with paths
// resolved against the document server (/api/v1) or a path-level server override.
func Operations() (map[string]bool, error) {
	s, err := Load()
	if err != nil {
		return nil, err
	}
	base := api.BasePath
	if len(s.Servers) > 0 {
		base = s.Servers[0].URL
	}
	out := map[string]bool{}
	for p, item := range s.Paths {
		prefix := base
		if n, ok := item["servers"]; ok {
			var srv []struct {
				URL string `yaml:"url"`
			}
			if err := n.Decode(&srv); err == nil && len(srv) > 0 {
				prefix = srv[0].URL
			}
		}
		full := strings.TrimSuffix(prefix, "/") + p
		for _, m := range methods {
			if _, ok := item[m]; ok {
				out[strings.ToUpper(m)+" "+Normalize(full)] = true
			}
		}
	}
	return out, nil
}

// Ignored routes are served by the router but are not API operations
// (SPA fallback, static files, chi's automatic HEAD/OPTIONS).
var Ignored = []*regexp.Regexp{
	regexp.MustCompile(`^(HEAD|OPTIONS) `),
	regexp.MustCompile(`^GET /\*$`),
	regexp.MustCompile(`^GET /$`),
	regexp.MustCompile(`^GET /assets/\*$`),
	regexp.MustCompile(`^GET /fonts/\*$`),
	regexp.MustCompile(` /debug/pprof`),
}

func ignored(route string) bool {
	for _, re := range Ignored {
		if re.MatchString(route) {
			return true
		}
	}
	return false
}

// RouterOperations lists "METHOD /path" for every route registered in r.
func RouterOperations(r chi.Routes) ([]string, error) {
	var out []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.ReplaceAll(route, "/*/", "/")
		op := method + " " + Normalize(route)
		if !ignored(op) {
			out = append(out, op)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// MissingRoutes returns router operations that are not documented in the spec.
func MissingRoutes(r chi.Routes) ([]string, error) {
	ops, err := Operations()
	if err != nil {
		return nil, err
	}
	routes, err := RouterOperations(r)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, op := range routes {
		if !ops[op] {
			missing = append(missing, op)
		}
	}
	return missing, nil
}

// Unimplemented returns spec operations under prefix that the
// router does not serve (useful once every module is mounted).
func Unimplemented(r chi.Routes, prefix string) ([]string, error) {
	ops, err := Operations()
	if err != nil {
		return nil, err
	}
	routes, err := RouterOperations(r)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, op := range routes {
		have[op] = true
	}
	var out []string
	for op := range ops {
		_, path, _ := strings.Cut(op, " ")
		if strings.HasPrefix(path, prefix) && !have[op] {
			out = append(out, op)
		}
	}
	sort.Strings(out)
	return out, nil
}

// AssertRoutes fails the test for every registered route missing from the spec.
func AssertRoutes(t testing.TB, r chi.Routes) {
	t.Helper()
	missing, err := MissingRoutes(r)
	if err != nil {
		t.Fatalf("apispec: %v", err)
	}
	for _, m := range missing {
		t.Errorf("route %s is not documented in api/openapi.yaml", m)
	}
}

// RequestErrors validates req against the spec. The body is restored afterwards.
func RequestErrors(req *http.Request) ([]string, error) {
	v, err := Validator()
	if err != nil {
		return nil, err
	}
	restoreBody(req)
	ok, errs := v.ValidateHttpRequestSync(req)
	restoreBody(req)
	if ok {
		return nil, nil
	}
	return flatten(errs), nil
}

// ResponseErrors validates resp (for req) against the spec. Bodies are restored.
func ResponseErrors(req *http.Request, resp *http.Response) ([]string, error) {
	v, err := Validator()
	if err != nil {
		return nil, err
	}
	restoreBody(req)
	var body []byte
	if resp.Body != nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	ok, errs := v.ValidateHttpResponse(req, resp)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if ok {
		return nil, nil
	}
	return flatten(errs), nil
}

// ValidateRequest fails the test when req does not match the spec.
func ValidateRequest(t testing.TB, req *http.Request) {
	t.Helper()
	errs, err := RequestErrors(req)
	if err != nil {
		t.Fatalf("apispec: %v", err)
	}
	for _, e := range errs {
		t.Errorf("request %s %s: %s", req.Method, req.URL.Path, e)
	}
}

// ValidateResponse fails the test when resp does not match the spec for req.
func ValidateResponse(t testing.TB, req *http.Request, resp *http.Response) {
	t.Helper()
	errs, err := ResponseErrors(req, resp)
	if err != nil {
		t.Fatalf("apispec: %v", err)
	}
	for _, e := range errs {
		t.Errorf("response %d for %s %s: %s", resp.StatusCode, req.Method, req.URL.Path, e)
	}
}

var (
	modelOnce sync.Once
	model     *v3high.Document
	modelErr  error
)

// SchemaErrors validates the JSON document doc against the component schema
// name (components.schemas.<name>), for payloads that travel outside an HTTP
// operation: WebSocket frames, webhook envelopes.
func SchemaErrors(name string, doc []byte) ([]string, error) {
	modelOnce.Do(func() {
		d, err := Document()
		if err != nil {
			modelErr = err
			return
		}
		m, err := d.BuildV3Model()
		if err != nil || m == nil {
			modelErr = fmt.Errorf("apispec: build model: %v", err)
			return
		}
		model = &m.Model
	})
	if modelErr != nil {
		return nil, modelErr
	}
	sp := model.Components.Schemas.GetOrZero(name)
	if sp == nil {
		return nil, fmt.Errorf("apispec: no component schema %q", name)
	}
	ok, errs := schema_validation.NewSchemaValidator().ValidateSchemaBytes(sp.Schema(), doc)
	if ok {
		return nil, nil
	}
	return flatten(errs), nil
}

// ValidateSchema fails the test when doc does not match component schema name.
func ValidateSchema(t testing.TB, name string, doc []byte) {
	t.Helper()
	errs, err := SchemaErrors(name, doc)
	if err != nil {
		t.Fatalf("apispec: %v", err)
	}
	for _, e := range errs {
		t.Errorf("%s does not match %s: %s", doc, name, e)
	}
}

// bodies is keyed by request so validation can read the body repeatedly.
var bodies sync.Map

func restoreBody(req *http.Request) {
	if req.Body == nil || req.Body == http.NoBody {
		if b, ok := bodies.Load(req); ok {
			req.Body = io.NopCloser(bytes.NewReader(b.([]byte)))
		}
		return
	}
	b, ok := bodies.Load(req)
	if !ok {
		data, _ := io.ReadAll(req.Body)
		req.Body.Close()
		bodies.Store(req, data)
		b = data
	}
	req.Body = io.NopCloser(bytes.NewReader(b.([]byte)))
}

func flatten(errs []*vErr) []string {
	var out []string
	for _, e := range errs {
		msg := e.Message
		if e.Reason != "" {
			msg += ": " + e.Reason
		}
		for _, se := range e.SchemaValidationErrors {
			msg += fmt.Sprintf(" [%s: %s]", se.FieldPath, se.Reason)
		}
		out = append(out, msg)
	}
	return out
}
