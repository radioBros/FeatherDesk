// SPDX-License-Identifier: AGPL-3.0-only

//go:build race

package app

// raceEnabled: CPU and latency budgets are meaningless under the race detector.
const raceEnabled = true
