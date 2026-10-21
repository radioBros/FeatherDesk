// SPDX-License-Identifier: AGPL-3.0-only

package store

import "strings"

// Both engines' migrations are rendered from one template in
// migrations/_templates (directories starting with "_" are not embedded), so the
// schemas cannot drift. Regenerate with:
//
//	go test ./internal/store -run TestMigrationsGenerated -update
//
// Template syntax:
//
//	{TS}    TIMESTAMP (sqlite) / TIMESTAMPTZ (postgres)
//	{JSON}  JSONTEXT (sqlite: TEXT affinity) / JSONB (postgres)
//	@@sqlite … @@end, @@postgres … @@end   engine-only lines
func renderMigrationTemplate(tpl string, d Dialect) string {
	var b strings.Builder
	b.WriteString("-- Code generated from migrations/_templates for both engines (D-18). DO NOT EDIT. Engine: " + d.String() + ".\n")
	block := ""
	for _, line := range strings.Split(tpl, "\n") {
		switch strings.TrimSpace(line) {
		case "@@sqlite", "@@postgres":
			block = strings.TrimPrefix(strings.TrimSpace(line), "@@")
			continue
		case "@@end":
			block = ""
			continue
		}
		if block != "" && block != d.String() {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := strings.TrimRight(b.String(), "\n") + "\n"
	ts, js := "TIMESTAMP", "JSONTEXT"
	if d == Postgres {
		ts, js = "TIMESTAMPTZ", "JSONB"
	}
	return strings.NewReplacer("{TS}", ts, "{JSON}", js).Replace(out)
}
