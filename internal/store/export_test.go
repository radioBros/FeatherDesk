// SPDX-License-Identifier: AGPL-3.0-only

package store

// SetPruneTxHook exposes the prune transaction observer to external tests.
func SetPruneTxHook(fn func(table string, n int64)) { pruneTxHook = fn }
