// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/problems"
)

func enumOf(t *testing.T, name string) []string {
	t.Helper()
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	e := s.Components.Schemas[name].Enum
	if len(e) == 0 {
		t.Fatalf("schema %s has no enum", name)
	}
	return e
}

// TestProblemSlugsMatchRegistry: ProblemSlug lists exactly the slugs of
// internal/problems (CON-006).
func TestProblemSlugsMatchRegistry(t *testing.T) {
	var reg []string
	for slug := range problems.Registry {
		reg = append(reg, slug)
	}
	spec := slices.Clone(enumOf(t, "ProblemSlug"))
	sort.Strings(reg)
	sort.Strings(spec)
	if !slices.Equal(reg, spec) {
		t.Fatalf("ProblemSlug %v\nregistry   %v", spec, reg)
	}
}

// fieldCodeCallers are the helpers whose argument at the given index is a
// field error code (problems.Field and the package-local wrappers).
var fieldCodeCallers = map[string]int{"Field": 1, "fieldErr": 1, "bad": 0}

// dynamicFieldCodes are codes computed at runtime and passed as variables:
// authn.PasswordProblem and the webhooks SSRF guard (Guard.CheckURL).
var dynamicFieldCodes = []string{"password_too_short", "password_too_long", "password_too_common",
	"invalid_url", "insecure_scheme", "private_address", "unresolvable"}

// TestFieldErrorCodesDocumented: every field error code the server can emit
// is in the FieldErrorCode enum, and the enum lists nothing else (CON-006).
// Literal codes are read from the Go sources; dynamic ones are listed above.
func TestFieldErrorCodesDocumented(t *testing.T) {
	found := map[string]string{}
	for _, c := range dynamicFieldCodes {
		found[c] = "dynamic"
	}
	fset := token.NewFileSet()
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch fn := c.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			idx, ok := fieldCodeCallers[name]
			if !ok || len(c.Args) <= idx {
				return true
			}
			if bl, ok := c.Args[idx].(*ast.BasicLit); ok && bl.Kind == token.STRING {
				s, _ := strconv.Unquote(bl.Value)
				found[s] = fset.Position(c.Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	enum := enumOf(t, "FieldErrorCode")
	for code, where := range found {
		if !slices.Contains(enum, code) {
			t.Errorf("field error code %q (%s) is missing from FieldErrorCode", code, where)
		}
	}
	for _, code := range enum {
		if _, ok := found[code]; !ok {
			t.Errorf("FieldErrorCode lists %q, which no handler emits", code)
		}
	}
}

// TestNoAdHocHTTPError: handlers answer errors with problems.Write (RFC 9457,
// registered slugs only), never http.Error (CON-006, internals.md).
func TestNoAdHocHTTPError(t *testing.T) {
	fset := token.NewFileSet()
	for _, root := range []string{"..", filepath.Join("..", "..", "cmd")} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, p, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" {
						t.Errorf("%s: http.Error; use problems.Write", fset.Position(sel.Pos()))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
