// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// PLT-017 dependency budget gate (CONVENTIONS §10, runtime_stack_decision).
// It runs in the CI go job with every `go test ./...`; scripts/depgate.sh
// (hygiene job) separately counts the modules linked into the binary.

// maxDirect is the hard cap on direct requirements of go.mod.
const maxDirect = 15

// allowedDirect is the allowlist of direct modules (runtime_stack_decision:
// 14 modules; libopenapi-validator and yaml.v3 are test-only). Adding one is a
// decision record first, then this list.
var allowedDirect = map[string]bool{
	"github.com/coder/websocket":            true,
	"github.com/coreos/go-oidc/v3":          true,
	"github.com/emersion/go-imap/v2":        true,
	"github.com/go-chi/chi/v5":              true,
	"github.com/google/uuid":                true,
	"github.com/jackc/pgx/v5":               true,
	"github.com/microcosm-cc/bluemonday":    true,
	"github.com/pb33f/libopenapi-validator": true,
	"github.com/wneessen/go-mail":           true,
	"github.com/yuin/goldmark":              true,
	"golang.org/x/crypto":                   true,
	"golang.org/x/oauth2":                   true,
	"gopkg.in/yaml.v3":                      true,
	"modernc.org/sqlite":                    true,
}

// pinned modules must stay at exactly this version (go-imap v2 is a beta:
// any drift is a reviewed decision, D-16).
var pinned = map[string]string{"github.com/emersion/go-imap/v2": "v2.0.0-beta.8"}

// directRequires parses go.mod and returns module → version of the direct
// (non-`// indirect`) requirements.
func directRequires(gomod string) map[string]string {
	out := map[string]string{}
	inBlock := false
	sc := bufio.NewScanner(strings.NewReader(gomod))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !inBlock:
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") || strings.Contains(line, "// indirect") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 {
			out[f[0]] = f[1]
		}
	}
	return out
}

// depGate returns every violation of the budget, the allowlist, the pins and
// the no-AWS-SDK rule (blob_storage_decision) for a go.mod / go.sum pair.
func depGate(gomod, gosum string) []string {
	var bad []string
	direct := directRequires(gomod)
	if len(direct) > maxDirect {
		bad = append(bad, fmt.Sprintf("%d direct modules in go.mod, limit %d", len(direct), maxDirect))
	}
	var names []string
	for m := range direct {
		names = append(names, m)
	}
	sort.Strings(names)
	for _, m := range names {
		if !allowedDirect[m] {
			bad = append(bad, "module not in the allowlist: "+m)
		}
	}
	for m, want := range pinned {
		if got := direct[m]; got != want {
			bad = append(bad, fmt.Sprintf("%s drifted from its pin: %q, want %q", m, got, want))
		}
	}
	for _, line := range strings.Split(gosum+"\n"+gomod, "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(strings.ToLower(f[0]), "github.com/aws/") {
			bad = append(bad, "AWS SDK module in the graph: "+f[0])
			break
		}
	}
	return bad
}

// TestDependencyGate: the real go.mod / go.sum pass the gate.
func TestDependencyGate(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("../../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if bad := depGate(string(mod), string(sum)); len(bad) > 0 {
		t.Fatalf("dependency gate (PLT-017):\n  %s", strings.Join(bad, "\n  "))
	}
	if n := len(directRequires(string(mod))); n != len(allowedDirect) {
		t.Logf("direct modules: %d (allowlist %d)", n, len(allowedDirect))
	}
}

// TestDependencyGateCatchesViolations stands in for the "throwaway branch":
// a 16th direct module, an unlisted module, go-imap drift and an AWS SDK
// module each fail the gate.
func TestDependencyGateCatchesViolations(t *testing.T) {
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	base := string(mod)
	addDirect := func(lines ...string) string {
		return strings.Replace(base, "require (\n", "require (\n\t"+strings.Join(lines, "\n\t")+"\n", 1)
	}
	cases := map[string]struct {
		mod, sum, want string
	}{
		"unlisted module":     {addDirect("github.com/sirupsen/logrus v1.9.3"), "", "not in the allowlist: github.com/sirupsen/logrus"},
		"16th direct module":  {addDirect("example.com/a v1.0.0", "example.com/b v1.0.0"), "", "direct modules in go.mod, limit 15"},
		"single-line require": {base + "\nrequire example.com/c v1.0.0\n", "", "not in the allowlist: example.com/c"},
		"go-imap drift":       {strings.Replace(base, "go-imap/v2 v2.0.0-beta.8", "go-imap/v2 v2.0.0-beta.9", 1), "", "drifted from its pin"},
		"aws sdk":             {base, "github.com/aws/aws-sdk-go-v2 v1.30.0 h1:x=\n", "AWS SDK module"},
	}
	for name, c := range cases {
		bad := depGate(c.mod, c.sum)
		if !strings.Contains(strings.Join(bad, "\n"), c.want) {
			t.Errorf("%s: gate reported %v, want %q", name, bad, c.want)
		}
	}
	if bad := depGate(base, ""); len(bad) != 0 {
		t.Fatalf("clean go.mod reported %v", bad)
	}
}
