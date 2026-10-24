// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVersionFilesMatch: repo-root VERSION is the single source of truth; every
// stamped consumer must carry the same SemVer.
func TestVersionFilesMatch(t *testing.T) {
	want := strings.TrimSpace(mustRead(t, "../../VERSION"))
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(want) {
		t.Fatalf("VERSION is not SemVer X.Y.Z: %q", want)
	}

	readme := mustRead(t, "../../README.md")
	m := regexp.MustCompile(`\*\*Version ([0-9]+\.[0-9]+\.[0-9]+)\*\*`).FindStringSubmatch(readme)
	if m == nil {
		t.Fatal("README.md: no **Version X.Y.Z** line")
	}
	assertEq(t, "README.md", m[1], want)

	pkg := mustRead(t, "../../web/package.json")
	m = regexp.MustCompile(`"version": "([^"]+)"`).FindStringSubmatch(pkg)
	if m == nil {
		t.Fatal("web/package.json: no version field")
	}
	assertEq(t, "web/package.json", m[1], want)

	openapi := mustRead(t, "../../api/openapi.yaml")
	m = regexp.MustCompile(`(?m)^  version: "([^"]+)"`).FindStringSubmatch(openapi)
	if m == nil {
		t.Fatal("api/openapi.yaml: no info.version")
	}
	assertEq(t, "api/openapi.yaml", m[1], want)

	chart := mustRead(t, "../../deploy/helm/featherdesk/Chart.yaml")
	m = regexp.MustCompile(`(?m)^version: (.+)$`).FindStringSubmatch(chart)
	if m == nil {
		t.Fatal("Chart.yaml: no version")
	}
	assertEq(t, "Chart.yaml version", strings.TrimSpace(m[1]), want)
	m = regexp.MustCompile(`(?m)^appVersion: "([^"]+)"`).FindStringSubmatch(chart)
	if m == nil {
		t.Fatal("Chart.yaml: no appVersion")
	}
	assertEq(t, "Chart.yaml appVersion", m[1], want)
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertEq(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q want %q", label, got, want)
	}
}
