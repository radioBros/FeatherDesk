// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The operator manual (docs/DOCUMENTATION.md, which replaces the per-topic
// docs/admin/*.md files) must keep the restore and upgrade facts that
// operators rely on (DEP-013, DEP-014).
func readManual(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/DOCUMENTATION.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDocs_RestoreExplainsInFlightQueue (DEP-013): the restore guide says that
// queue work in flight is not in the backup, what may be sent again, how
// receivers deduplicate, and which mails are lost.
func TestDocs_RestoreExplainsInFlightQueue(t *testing.T) {
	doc := readManual(t)
	for _, want := range []string{
		"**In-flight queue items after a restore.**",
		"may be **sent again**",
		"deterministic `Message-ID`s",
		"stable event `id`s",
		"queued when the backup was taken are lost",
		"pg_dump",
		"pg_restore",
		"scripts/restore-drill.sh",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/DOCUMENTATION.md lacks %q", want)
		}
	}
}

// TestDocs_UpgradeRefusalMessageMatchesCode (DEP-014): the documented refusal
// of an older binary is the message the store really prints.
func TestDocs_UpgradeRefusalMessageMatchesCode(t *testing.T) {
	doc := readManual(t)
	src, err := os.ReadFile("../../internal/store/migrate.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"store: (database schema version %d is newer than this binary \(%d\); refusing to start)"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("refusal message not found in internal/store/migrate.go")
	}
	documented := strings.Replace(strings.Replace(string(m[1]), "%d", "X", 1), "%d", "Y", 1)
	if !strings.Contains(doc, "`"+documented+"`") {
		t.Errorf("docs/DOCUMENTATION.md should quote the refusal as `%s`", documented)
	}
	for _, want := range []string{"**Take a backup**", "**Rolling back:** start the **previous image** and **restore the pre-upgrade backup**", "scripts/upgrade-drill.sh"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/DOCUMENTATION.md lacks %q", want)
		}
	}
}

// TestDocs_DemoSeedDocumented: the demo seeder is described where people look
// for it (README "Demo data", the manual's CLI table and Development
// chapter, and `make demo`).
func TestDocs_DemoSeedDocumented(t *testing.T) {
	for file, wants := range map[string][]string{
		"../../README.md":             {"## Demo data", "seed --demo", "--smtp-mailpit", "--force", "demo-password-123", "make demo"},
		"../../docs/DOCUMENTATION.md": {"`seed --demo`", "**Demo data.**", "--smtp-mailpit", "--force", "demo-password-123", "FD_AUTH_STAFF_LOCAL=true"},
		"../../Makefile":              {"\ndemo:", "seed --demo"},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s lacks %q", file, want)
			}
		}
	}
}
