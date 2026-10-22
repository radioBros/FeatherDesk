// SPDX-License-Identifier: AGPL-3.0-only

package config_test

// DEP-006: .env.example must document every FD_* variable the binary reads,
// and nothing else. Sources of truth:
//   - the core Config struct tags (reflection);
//   - module variables found by scanning non-test Go source under internal/
//     and cmd/: "FD_…" literals, settings.Overlay(…, "FD_X_", …) prefixes
//     combined with `env:"NAME"` struct tags of the same package, and the
//     OIDC "FD_OIDC_" + AUDIENCE + "_" + "NAME" pattern;
//   - every FD_* name mentioned in docs/configuration.md.
// The scan is regex based on purpose: importing the module packages here
// would create import cycles with internal/config.

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/config"
)

const repoRoot = "../.."

type exampleVar struct {
	value    string
	required bool
	set      bool // uncommented
}

var (
	reExampleVar = regexp.MustCompile(`^(#\s*)?(FD_[A-Z0-9_]+)=(.*)$`)
	reLiteral    = regexp.MustCompile(`"(FD_[A-Z0-9_]*[A-Z0-9])"`)
	reOverlay    = regexp.MustCompile(`Overlay\([^,]+,\s*"(FD_[A-Z0-9_]+_)"`)
	reEnvTag     = regexp.MustCompile("env:\"([A-Z0-9_]+)\"")
	reAudPrefix  = regexp.MustCompile(`"(FD_[A-Z0-9_]+_)"\s*\+\s*strings\.ToUpper`)
	rePrefixUse  = regexp.MustCompile(`prefix\s*\+\s*"([A-Z0-9_]+)"`)
	reDocVar     = regexp.MustCompile("`(FD_[A-Z0-9_]*[A-Z0-9])`")
)

// Names read only by tests or test helpers; never part of an operator's .env.
func testOnly(name string) bool {
	return strings.HasPrefix(name, "FD_TEST_") || strings.HasPrefix(name, "FD_WS_HELPER_") || name == "FD_PRINT_CONFIG_MD"
}

// audiences for the OIDC prefix; the customer audience has no role mapping
// (AUTH-025), so only the staff audience must document every suffix.
var oidcAudiences = []string{"STAFF", "CUSTOMER"}

func parseEnvExample(t *testing.T) map[string]exampleVar {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]exampleVar{}
	var comment []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := reExampleVar.FindStringSubmatch(line); m != nil {
			if _, dup := out[m[2]]; dup {
				t.Errorf(".env.example: %s documented twice", m[2])
			}
			out[m[2]] = exampleVar{
				value:    m[3],
				set:      m[1] == "",
				required: strings.Contains(strings.Join(comment, " "), "[required]"),
			}
			comment = nil
			continue
		}
		if strings.HasPrefix(line, "#") {
			comment = append(comment, line)
		} else {
			comment = nil
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func coreVars() map[string]string { // name → default tag
	out := map[string]string{}
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if n := f.Tag.Get("env"); n != "" && n != "-" {
			out[n] = f.Tag.Get("default")
		}
	}
	return out
}

// sourceVars returns the variables that must be documented and the wider set
// of names that may be documented.
func sourceVars(t *testing.T) (required, known map[string]bool) {
	t.Helper()
	required, known = map[string]bool{}, map[string]bool{}
	add := func(name string, must bool) {
		if testOnly(name) {
			return
		}
		known[name] = true
		if must {
			required[name] = true
		}
	}
	type pkg struct {
		prefixes map[string]bool
		tags     map[string]bool
	}
	pkgs := map[string]*pkg{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			src := string(b)
			for _, m := range reLiteral.FindAllStringSubmatch(src, -1) {
				add(m[1], true)
			}
			pk := pkgs[filepath.Dir(p)]
			if pk == nil {
				pk = &pkg{prefixes: map[string]bool{}, tags: map[string]bool{}}
				pkgs[filepath.Dir(p)] = pk
			}
			for _, m := range reOverlay.FindAllStringSubmatch(src, -1) {
				pk.prefixes[m[1]] = true
			}
			for _, line := range strings.Split(src, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue // a doc comment showing the tag syntax is not a field
				}
				for _, m := range reEnvTag.FindAllStringSubmatch(line, -1) {
					if !strings.HasPrefix(m[1], "FD_") {
						pk.tags[m[1]] = true
					}
				}
			}
			if am := reAudPrefix.FindStringSubmatch(src); am != nil {
				for _, sm := range rePrefixUse.FindAllStringSubmatch(src, -1) {
					for _, aud := range oidcAudiences {
						add(am[1]+aud+"_"+sm[1], aud == "STAFF")
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, pk := range pkgs {
		for prefix := range pk.prefixes {
			for tag := range pk.tags {
				add(prefix+tag, true)
			}
		}
	}
	if len(required) < 40 {
		t.Fatalf("source scan found only %d variables; the scanner is broken", len(required))
	}
	return required, known
}

func TestEnvExampleMatchesConfig(t *testing.T) {
	ex := parseEnvExample(t)
	core := coreVars()
	required, known := sourceVars(t)
	for n := range core {
		required[n], known[n] = true, true
	}

	var missing, unknown []string
	for n := range required {
		if _, ok := ex[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range ex {
		if !known[n] {
			unknown = append(unknown, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	if len(missing) > 0 {
		t.Errorf(".env.example is missing %d variable(s) the code reads:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(unknown) > 0 {
		t.Errorf(".env.example documents %d variable(s) no code reads:\n  %s", len(unknown), strings.Join(unknown, "\n  "))
	}

	// Commented core entries show the struct default.
	for n, def := range core {
		v, ok := ex[n]
		if !ok || v.set || def == "" {
			continue
		}
		if v.value != def {
			t.Errorf(".env.example: %s shows %q, the default is %q", n, v.value, def)
		}
	}
}

func TestEnvExampleCoversDocs(t *testing.T) {
	ex := parseEnvExample(t)
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range reDocVar.FindAllStringSubmatch(string(b), -1) {
		n := m[1]
		if seen[n] || testOnly(n) {
			continue
		}
		seen[n] = true
		if _, ok := ex[n]; !ok {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("docs/configuration.md mentions variables absent from .env.example: %s", strings.Join(missing, ", "))
	}
}

// Booting with .env.example as shipped (required values still empty) must
// fail and name every [required] variable in one error.
func TestEnvExampleRequiredKeysReported(t *testing.T) {
	ex := parseEnvExample(t)
	vals := map[string]string{}
	var req []string
	for n, v := range ex {
		if v.set {
			vals[n] = v.value
		}
		if v.required {
			if !v.set {
				t.Errorf(".env.example: required %s must be uncommented", n)
			}
			req = append(req, n)
		}
	}
	if len(req) == 0 {
		t.Fatal("no [required] variable in .env.example")
	}
	_, err := config.Load(func(k string) string { return vals[k] })
	var le *config.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("Load with empty required values: want LoadError, got %v", err)
	}
	for _, n := range req {
		if !strings.Contains(err.Error(), n) {
			t.Errorf("LoadError does not mention required %s:\n%v", n, err)
		}
	}
}
