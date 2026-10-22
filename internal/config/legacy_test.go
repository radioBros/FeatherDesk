// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyLegacyEnv(t *testing.T) {
	env := map[string]string{"ST_PUBLIC_URL": "https://old.test", "ST_LOG_LEVEL": "debug", "FD_LOG_LEVEL": "warn", "ST_": "x", "PATH": "/bin"}
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	set := func(k, v string) error { env[k] = v; return nil }
	got := ApplyLegacyEnv(environ, lookup, set)
	if want := []string{"FD_PUBLIC_URL"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied %v, want %v", got, want)
	}
	if env["FD_PUBLIC_URL"] != "https://old.test" {
		t.Fatalf("FD_PUBLIC_URL = %q", env["FD_PUBLIC_URL"])
	}
	if env["FD_LOG_LEVEL"] != "warn" {
		t.Fatalf("an explicit FD_ variable was overwritten: %q", env["FD_LOG_LEVEL"])
	}
}

func TestSQLitePathKeepsSimpleTicketsFile(t *testing.T) {
	dir := t.TempDir()
	c := &Config{DataDir: dir}
	if got := c.SQLitePath(); got != filepath.Join(dir, "featherdesk.db") {
		t.Fatalf("fresh data dir: %s", got)
	}
	legacy := filepath.Join(dir, "simpletickets.db")
	if err := os.WriteFile(legacy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := c.SQLitePath(); got != legacy {
		t.Fatalf("upgraded data dir: %s, want %s", got, legacy)
	}
	if err := os.WriteFile(filepath.Join(dir, "featherdesk.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := c.SQLitePath(); got != filepath.Join(dir, "featherdesk.db") {
		t.Fatalf("featherdesk.db must win once it exists: %s", got)
	}
}
