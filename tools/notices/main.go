// SPDX-License-Identifier: AGPL-3.0-only

// Command notices generates THIRD_PARTY_NOTICES (DEP-026) from what actually
// ships: the Go modules linked into ./cmd/featherdesk (`go list -deps`, so
// test-only and tool modules are excluded), the Go standard library, and the
// npm production tree of web/ (`pnpm licenses list --prod`, which is bundled into
// the embedded SPA). Platform-specific npm binaries (packages that declare `os`
// or `cpu`) are skipped so the output is identical on every host.
//
// Every licence must be on the AGPL-3.0-compatible allowlist below.
//
//	go run ./tools/notices            # rewrite THIRD_PARTY_NOTICES
//	go run ./tools/notices -check     # CI: fail when the file is stale or a licence is not allowed
//
// It needs `web/node_modules` (pnpm install --frozen-lockfile) and is not linked
// into the app binary.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// allowed lists licences that may be combined with AGPL-3.0-only in one
// distributed work (FSF "GPL-compatible" list; Apache-2.0 is compatible with
// GPLv3/AGPLv3; MPL-2.0 §3.3 permits it; PSF-2.0 is GPL-compatible).
var allowed = map[string]bool{
	"MIT": true, "ISC": true, "BSD-2-Clause": true, "BSD-3-Clause": true, "0BSD": true,
	"Apache-2.0": true, "MPL-2.0": true, "Unlicense": true, "CC0-1.0": true, "Zlib": true,
	"PSF-2.0": true, "Python-2.0": true, "BlueOak-1.0.0": true,
}

type component struct {
	Ecosystem string // "go" or "npm"
	Name      string
	Version   string
	License   string // SPDX expression
	Text      string // licence file text
}

func main() {
	check := flag.Bool("check", false, "fail if THIRD_PARTY_NOTICES is stale or a licence is not allowed")
	out := flag.String("out", "THIRD_PARTY_NOTICES", "output file (relative to the repo root)")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	comps, err := goComponents(root)
	if err != nil {
		fatal(err)
	}
	npm, err := npmComponents(filepath.Join(root, "web"))
	if err != nil {
		fatal(err)
	}
	comps = append(comps, npm...)

	var bad []string
	for _, c := range comps {
		if !licenseAllowed(c.License) {
			bad = append(bad, fmt.Sprintf("%s %s@%s: %q", c.Ecosystem, c.Name, c.Version, c.License))
		}
	}
	doc := render(comps)
	path := filepath.Join(root, *out)
	if *check {
		cur, err := os.ReadFile(path)
		stale := err != nil || !bytes.Equal(cur, doc)
		if stale {
			fmt.Fprintf(os.Stderr, "::error::%s is missing or stale; run `go run ./tools/notices` and commit it\n", *out)
		}
		for _, b := range bad {
			fmt.Fprintf(os.Stderr, "::error::licence not on the AGPL-3.0 compatibility allowlist: %s\n", b)
		}
		if stale || len(bad) > 0 {
			os.Exit(1)
		}
		fmt.Printf("ok: %s lists %d components (%d Go incl. stdlib, %d npm), all licences AGPL-3.0-compatible\n",
			*out, len(comps), len(comps)-len(npm), len(npm))
		return
	}
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: %d components\n", *out, len(comps))
	if len(bad) > 0 {
		for _, b := range bad {
			fmt.Fprintf(os.Stderr, "licence not allowed: %s\n", b)
		}
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "notices:", err)
	os.Exit(2)
}

func repoRoot() (string, error) {
	b, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return "", fmt.Errorf("go list -m: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func goComponents(root string) ([]component, error) {
	cmd := exec.Command("go", "list", "-deps", "-f",
		`{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}`, "./cmd/featherdesk")
	cmd.Dir = root
	// The image ships linux/amd64 and linux/arm64 binaries (same module set);
	// a darwin build would add terminal helpers that never ship.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps: %w", err)
	}
	seen := map[string]bool{}
	var out []component
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		text, primary, err := licenseText(f[2])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f[0], err)
		}
		out = append(out, component{"go", f[0], f[1], classify(primary), text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	// The standard library is linked into the binary too.
	std, err := goLicense()
	if err != nil {
		return nil, fmt.Errorf("licence of the Go standard library: %w", err)
	}
	stdlib := component{"go", "Go standard library", "go1.x (toolchain used for the build)", classify(string(std)), normalize(string(std))}
	return append([]component{stdlib}, out...), nil
}

// goLicense reads the toolchain's LICENSE. Homebrew keeps it one level above
// GOROOT (Cellar/go/<v>/LICENSE), so look there too.
func goLicense() ([]byte, error) {
	b, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return nil, err
	}
	goroot := strings.TrimSpace(string(b))
	for _, p := range []string{filepath.Join(goroot, "LICENSE"), filepath.Join(goroot, "..", "LICENSE")} {
		if t, err := os.ReadFile(p); err == nil {
			return t, nil
		}
	}
	return nil, fmt.Errorf("no LICENSE in or above %s", goroot)
}

type pnpmPkg struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
	Paths    []string `json:"paths"`
	License  string   `json:"license"`
}

func npmComponents(web string) ([]component, error) {
	cmd := exec.Command("pnpm", "licenses", "list", "--prod", "--json")
	if _, err := exec.LookPath("pnpm"); err != nil {
		cmd = exec.Command("corepack", "pnpm", "licenses", "list", "--prod", "--json")
	}
	cmd.Dir = web
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pnpm licenses list --prod (run pnpm install first): %w: %s", err, stderr.String())
	}
	var byLicense map[string][]pnpmPkg
	if err := json.Unmarshal(b, &byLicense); err != nil {
		return nil, fmt.Errorf("pnpm licenses json: %w", err)
	}
	var out []component
	for lic, pkgs := range byLicense {
		for _, p := range pkgs {
			for i, v := range p.Versions {
				if i >= len(p.Paths) {
					break
				}
				dir := p.Paths[i]
				if platformSpecific(dir) {
					continue
				}
				text, _, err := licenseText(dir)
				if err != nil {
					// A few packages only declare the licence in package.json.
					text = "(no licence file in the package; package.json declares " + lic + ")\n"
				}
				out = append(out, component{"npm", p.Name, v, lic, text})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// platformSpecific reports packages that only install on some OS/CPU (native
// binaries of build tools); they differ between a Mac and the Linux CI runner.
func platformSpecific(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pj struct {
		OS  []string `json:"os"`
		CPU []string `json:"cpu"`
	}
	_ = json.Unmarshal(b, &pj)
	return len(pj.OS) > 0 || len(pj.CPU) > 0
}

var (
	licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying)([.-].*)?$`)
	primaryFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying)(\.(md|txt))?$`)
)

// licenseText returns every licence/notice file of a package (for the notice)
// and the primary one (LICENSE, LICENSE.md/.txt or COPYING), which alone decides
// the SPDX id: secondary files such as LICENSE-3RD-PARTY.md list other licences.
func licenseText(dir string) (all, primary string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && licenseFile.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", "", fmt.Errorf("no LICENSE/COPYING file in %s", dir)
	}
	sort.SliceStable(names, func(i, j int) bool {
		pi, pj := primaryFile.MatchString(names[i]), primaryFile.MatchString(names[j])
		if pi != pj {
			return pi
		}
		return names[i] < names[j]
	})
	var buf strings.Builder
	for i, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", "", err
		}
		t := normalize(string(b))
		if i == 0 {
			primary = t
		} else {
			buf.WriteString("\n--- " + n + " ---\n")
		}
		buf.WriteString(t)
	}
	return buf.String(), primary, nil
}

func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

// classify maps a licence text to an SPDX id. Unknown texts return "UNKNOWN",
// which the allowlist rejects, so a new dependency with an odd licence fails CI
// until someone checks it.
func classify(t string) string {
	l := strings.ToLower(strings.Join(strings.Fields(t), " "))
	switch {
	case strings.Contains(l, "mozilla public license version 2.0") || strings.Contains(l, "mozilla public license, version 2.0"):
		return "MPL-2.0"
	case strings.Contains(l, "apache license") && strings.Contains(l, "version 2.0"):
		return "Apache-2.0"
	case strings.Contains(l, "permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(l, "permission to use, copy, modify, and/or distribute this software for any purpose"),
		strings.Contains(l, "permission to use, copy, modify, and distribute this software for any purpose"):
		return "ISC"
	case strings.Contains(l, "redistribution and use in source and binary forms"):
		if strings.Contains(l, "neither the name") || strings.Contains(l, "may not be used to endorse") || strings.Contains(l, "may be used to endorse") {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	case strings.Contains(l, "this is free and unencumbered software released into the public domain"):
		return "Unlicense"
	}
	return "UNKNOWN"
}

var spdxSplit = regexp.MustCompile(`\s+(?:OR|AND)\s+`)

// licenseAllowed accepts an SPDX expression when every alternative of an OR is
// allowed or at least one is (we may choose the compatible one), and every term
// of an AND is allowed.
func licenseAllowed(expr string) bool {
	e := strings.Trim(strings.TrimSpace(expr), "()")
	if strings.Contains(e, " OR ") {
		for _, alt := range strings.Split(e, " OR ") {
			if licenseAllowed(alt) {
				return true
			}
		}
		return false
	}
	for _, term := range spdxSplit.Split(e, -1) {
		if !allowed[strings.Trim(term, "() ")] {
			return false
		}
	}
	return true
}

func render(comps []component) []byte {
	var b bytes.Buffer
	b.WriteString(`THIRD-PARTY SOFTWARE NOTICES AND INFORMATION

FeatherDesk is licensed under the GNU Affero General Public License v3.0 only
(AGPL-3.0-only); see LICENSE. The FeatherDesk binary and container image also
contain the third-party components listed below, each under its own licence.
All of them are compatible with AGPL-3.0-only.

This file is generated by ` + "`go run ./tools/notices`" + ` from the Go modules linked
into ./cmd/featherdesk and the npm production dependencies of web/ (bundled
into the embedded web app). CI fails when it is out of date
(` + "`go run ./tools/notices -check`" + `). Do not edit it by hand.

`)
	fmt.Fprintf(&b, "SUMMARY (%d components)\n\n", len(comps))
	w := 0
	for _, c := range comps {
		if n := len(c.Name) + len(c.Version) + 1; n > w {
			w = n
		}
	}
	for _, c := range comps {
		id := c.Name + "@" + c.Version
		if c.Ecosystem == "go" && c.Name == "Go standard library" {
			id = c.Name
		}
		fmt.Fprintf(&b, "  %-3s  %-*s  %s\n", c.Ecosystem, w, id, c.License)
	}
	// Licence texts, grouped when several components carry the identical file.
	type group struct {
		text  string
		names []string
	}
	idx := map[[32]byte]int{}
	var groups []group
	for _, c := range comps {
		k := sha256.Sum256([]byte(c.Text))
		id := c.Ecosystem + " " + c.Name + "@" + c.Version
		if c.Name == "Go standard library" {
			id = "go " + c.Name
		}
		if i, ok := idx[k]; ok {
			groups[i].names = append(groups[i].names, id)
			continue
		}
		idx[k] = len(groups)
		groups = append(groups, group{c.Text, []string{id}})
	}
	b.WriteString("\nLICENCE TEXTS\n")
	for _, g := range groups {
		b.WriteString("\n" + strings.Repeat("=", 78) + "\n")
		for _, n := range g.names {
			b.WriteString(n + "\n")
		}
		b.WriteString(strings.Repeat("-", 78) + "\n")
		b.WriteString(g.text)
	}
	return b.Bytes()
}
