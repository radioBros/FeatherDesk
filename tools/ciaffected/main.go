// SPDX-License-Identifier: AGPL-3.0-only

// Command ciaffected prints the Go packages a change can break: the packages
// whose files changed, plus every package that imports one of them (directly
// or not, test imports included). CI runs `go test` on that list only.
//
//	scripts/ci-changed-files.sh | go run ./tools/ciaffected
//
// Input: changed files, one per line (repository-relative). Output: import
// paths, one per line, sorted; `./...` when everything must run (the line
// ALL, go.mod/go.sum, the OpenAPI spec that handler tests validate against);
// nothing when no Go package is touched. A changed non-Go file (an embedded
// migration, template or YAML) counts for the nearest package directory above
// it; the SPA sources under web/ do not count.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type pkg struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ciaffected:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	var files []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if f := strings.TrimSpace(sc.Text()); f != "" {
			files = append(files, f)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for _, f := range files {
		if f == "ALL" || f == "go.mod" || f == "go.sum" || strings.HasPrefix(f, "api/") {
			_, err := fmt.Fprintln(out, "./...")
			return err
		}
	}

	pkgs, err := list()
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	byDir := map[string]string{}
	for _, p := range pkgs {
		byDir[p.Dir] = p.ImportPath
	}

	affected := map[string]bool{}
	for _, f := range files {
		// The SPA sources are no Go input (web/ is a package only for its embed.go).
		if strings.HasPrefix(f, "web/") && !strings.HasSuffix(f, ".go") {
			continue
		}
		// The nearest directory above the file that is a package.
		for dir := filepath.Dir(filepath.Join(root, f)); strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
			if ip, ok := byDir[dir]; ok {
				affected[ip] = true
				break
			}
		}
	}

	// Importers of affected packages are affected, until nothing changes.
	for grew := len(affected) > 0; grew; {
		grew = false
		for _, p := range pkgs {
			if affected[p.ImportPath] {
				continue
			}
			for _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
				if affected[imp] {
					affected[p.ImportPath] = true
					grew = true
					break
				}
			}
		}
	}

	paths := make([]string, 0, len(affected))
	for ip := range affected {
		paths = append(paths, ip)
	}
	slices.Sort(paths)
	for _, ip := range paths {
		if _, err := fmt.Fprintln(out, ip); err != nil {
			return err
		}
	}
	return nil
}

func list() ([]pkg, error) {
	cmd := exec.Command("go", "list", "-json=ImportPath,Dir,Imports,TestImports,XTestImports", "./...")
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(b)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
