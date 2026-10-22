// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FeatherDesk was called SimpleTickets up to 1.0.0. The shims below keep an
// install configured for SimpleTickets running unchanged after the upgrade
// (docs/DOCUMENTATION.md, "Upgrading from SimpleTickets"). They only read old
// names; nothing new is ever written under them.

// LegacyEnvPrefix is the environment prefix of SimpleTickets 1.0.0.
const LegacyEnvPrefix = "ST_"

// SQLiteFile is the default database file in the data directory.
const SQLiteFile = "featherdesk.db"

// LegacySQLiteFile is the database file name of SimpleTickets 1.0.0.
const LegacySQLiteFile = "simpletickets.db"

// ApplyLegacyEnv sets FD_X from ST_X for every ST_X in environ whose FD_X is
// unset (an explicit FD_X always wins) and returns the FD_ names it set,
// sorted. Call it once at startup, before anything reads the environment.
func ApplyLegacyEnv(environ []string, lookup func(string) (string, bool), set func(key, value string) error) []string {
	var applied []string
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, LegacyEnvPrefix) || len(k) == len(LegacyEnvPrefix) {
			continue
		}
		nk := "FD_" + strings.TrimPrefix(k, LegacyEnvPrefix)
		if _, exists := lookup(nk); exists {
			continue
		}
		if set(nk, v) == nil {
			applied = append(applied, nk)
		}
	}
	sort.Strings(applied)
	return applied
}

// sqliteFileIn returns the database path in dir: featherdesk.db, or the
// SimpleTickets file when only that one exists, so an upgraded install keeps
// its data. A fresh data directory gets featherdesk.db.
func sqliteFileIn(dir string) string {
	p := filepath.Join(dir, SQLiteFile)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	legacy := filepath.Join(dir, LegacySQLiteFile)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return p
}
