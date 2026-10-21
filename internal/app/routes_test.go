// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/apispec"
)

// TestAllSpecOperationsImplemented builds the full app the way
// `simpletickets serve` does (default Registry, embedded SPA) and asserts
// that the router serves exactly the operations of api/openapi.yaml: none
// missing, none undocumented.
func TestAllSpecOperationsImplemented(t *testing.T) {
	a, err := Build(context.Background(), testConfig(t, ""), BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(); err != nil {
			t.Errorf("a.Close: %v", err)
		}
	}()
	r := a.Server.Router()
	missing, err := apispec.Unimplemented(r, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range missing {
		t.Errorf("spec operation %s is not served", op)
	}
	extra, err := apispec.MissingRoutes(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range extra {
		t.Errorf("route %s is not in api/openapi.yaml", op)
	}
	ops, _ := apispec.Operations()
	if len(ops) < 170 {
		t.Fatalf("only %d spec operations: is the spec loaded?", len(ops))
	}
}
