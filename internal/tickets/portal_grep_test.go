// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestPortalQueries_AllFilterPublic (I-2): every SQL string in the portal
// query file that touches ticket_messages filters kind = 'public' in the SQL
// text itself.
func TestPortalQueries_AllFilterPublic(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "portal_queries.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, _ := strconv.Unquote(lit.Value)
		if strings.Contains(s, "ticket_messages") {
			n++
			if !strings.Contains(s, "kind = 'public'") {
				t.Errorf("portal query without kind = 'public': %s", s)
			}
		}
		return true
	})
	if n < 4 {
		t.Fatalf("only %d portal message queries found", n)
	}
}
