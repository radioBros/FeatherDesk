// SPDX-License-Identifier: AGPL-3.0-only

// Package proofdeps is a deliberate CI proof (DEP-011): it links four more
// modules into the binary (16 runtime modules > 15) so the dependency gate
// fails. Never merged.
package proofdeps

import (
	"golang.org/x/net/idna"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/cpu"
	"golang.org/x/text/language"
)

var (
	_ = idna.ToASCII
	_ singleflight.Group
	_ = cpu.X86
	_ = language.English
)
