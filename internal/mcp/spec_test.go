// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/domain"
)

// TestToolScopesMatchSpec keeps tools/list filtering honest: a tool's
// declared scopes are exactly the x-scopes of the REST operation it performs
// (Ops[0]), and every operation it calls exists in api/openapi.yaml. The REST
// layer enforces the scopes anyway; this guards the list against drift.
func TestToolScopesMatchSpec(t *testing.T) {
	spec, err := apispec.Load()
	if err != nil {
		t.Fatal(err)
	}
	xScopes := func(op string) ([]string, bool) {
		method, path, _ := strings.Cut(op, " ")
		item, ok := spec.Paths[path]
		if !ok {
			return nil, false
		}
		node, ok := item[strings.ToLower(method)]
		if !ok {
			return nil, false
		}
		var o struct {
			Scopes []string `yaml:"x-scopes"`
		}
		if err := node.Decode(&o); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return o.Scopes, true
	}
	for kind, tools := range map[string][]*tool{"staff": staffTools, "customer": customerTools} {
		for _, tl := range tools {
			for i, op := range tl.Ops {
				got, ok := xScopes(op)
				if !ok {
					t.Errorf("%s/%s: operation %s is not in the spec", kind, tl.Name, op)
					continue
				}
				// I-12: MCP tools act as the token owner, never on a customer's
				// behalf, so tickets:act_as_customer is not a tool scope.
				got = slices.DeleteFunc(got, func(s string) bool { return s == domain.ScopeActAsCustomer })
				if i == 0 && !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(tl.Scopes))) {
					t.Errorf("%s/%s: scopes %v, but %s declares x-scopes %v", kind, tl.Name, tl.Scopes, op, got)
				}
			}
		}
	}
}
