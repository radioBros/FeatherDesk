// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"testing"
)

// TestCLIExitCodes: unknown command 2, help 0, version 0, config error 2.
func TestCLIExitCodes(t *testing.T) {
	ctx := context.Background()
	if c := run(ctx, []string{"no-such-command"}); c != 2 {
		t.Errorf("unknown: %d", c)
	}
	if c := run(ctx, []string{"help"}); c != 0 {
		t.Errorf("help: %d", c)
	}
	if c := run(ctx, []string{"version"}); c != 0 {
		t.Errorf("version: %d", c)
	}
	t.Setenv("ST_SECRET_KEY", "")
	t.Setenv("ST_DEV", "")
	t.Setenv("ST_LOG_LEVEL", "loud")
	if c := run(ctx, []string{"config"}); c != 2 {
		t.Errorf("config error: %d", c)
	}
	_ = os.Unsetenv("ST_LOG_LEVEL")
}
