// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"os"
	"testing"
)

// TestPrintMarkdown writes the core table to $FD_PRINT_CONFIG_MD (helper, skipped normally).
func TestPrintMarkdown(t *testing.T) {
	p := os.Getenv("FD_PRINT_CONFIG_MD")
	if p == "" {
		t.Skip()
	}
	_ = os.WriteFile(p, []byte(Markdown()), 0o644)
}
