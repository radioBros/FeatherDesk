// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// cgroupRoot is overridden in tests.
var cgroupRoot = "/sys/fs/cgroup"

// ApplyMemoryLimit implements PLT-025: when GOMEMLIMIT is unset, set the Go
// soft memory limit to 75 % of the container's cgroup memory limit so the GC
// tightens before an OOM kill without thrashing under the normal working set.
// It returns the limit applied (0 = none: env set, or no cgroup limit).
func ApplyMemoryLimit(getenv func(string) string) int64 {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	limit := cgroupMemoryLimit()
	if limit <= 0 {
		return 0
	}
	soft := limit * 3 / 4
	debug.SetMemoryLimit(soft)
	return soft
}

// cgroupMemoryLimit reads cgroup v2 memory.max, falling back to v1.
func cgroupMemoryLimit() int64 {
	for _, p := range []string{filepath.Join(cgroupRoot, "memory.max"), filepath.Join(cgroupRoot, "memory", "memory.limit_in_bytes")} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		if s == "max" || s == "" {
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 || n >= 1<<62 { // v1 reports a huge number when unlimited
			return 0
		}
		return n
	}
	return 0
}
