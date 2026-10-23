// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi-validator/schema_validation"

	"github.com/radioBros/FeatherDesk/api"
)

var update = flag.Bool("update", false, "rewrite api/docs.html from api/openapi.yaml")

// TestAPIDocsGenerated: the committed API reference matches the spec.
// Regenerate with `go test ./internal/apispec -run TestAPIDocsGenerated -update`.
func TestAPIDocsGenerated(t *testing.T) {
	got, _, err := BuildDocs()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("../../api/docs.html", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !bytes.Equal(got, api.Docs) {
		t.Fatal("api/docs.html is stale: run `go test ./internal/apispec -run TestAPIDocsGenerated -update`")
	}
}

// TestAPIDocsExamplesValid: every example on the page is valid for its schema.
// A generated value that does not fit means the schema needs an `example:`.
func TestAPIDocsExamplesValid(t *testing.T) {
	_, examples, err := BuildDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) < 200 {
		t.Fatalf("only %d examples", len(examples))
	}
	v := schema_validation.NewSchemaValidator()
	bad := 0
	for _, ex := range examples {
		ok, errs := v.ValidateSchemaBytes(ex.Schema, ex.JSON)
		if ok {
			continue
		}
		bad++
		var msgs []string
		for _, e := range errs {
			m := e.Message
			for _, se := range e.SchemaValidationErrors {
				m += "; " + se.FieldPath + ": " + se.Reason
			}
			msgs = append(msgs, m)
		}
		t.Errorf("%s: %s\n  example: %.300s", ex.Where, strings.Join(msgs, " | "), ex.JSON)
	}
	t.Logf("%d examples, %d invalid", len(examples), bad)
}

// TestAPIDocsPage: the page covers every operation, links the spec and keeps
// planning references out.
func TestAPIDocsPage(t *testing.T) {
	page, _, err := BuildDocs()
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	ops, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(s, `<article class="op" id="`) - strings.Count(s, `<span class="mb POST">EVENT</span>`); n != len(ops) {
		t.Errorf("%d operation cards, spec has %d operations", n, len(ops))
	}
	for _, want := range []string{`href="/api/v1/openapi.yaml" download=`, `id="errors"`, `id="webhooks"`, `id="authentication"`, "X-FeatherDesk-Signature"} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, leak := range []string{"Phase: V1", "Phase: CORE", "<script"} {
		if strings.Contains(s, leak) {
			t.Errorf("page contains %q", leak)
		}
	}
}
