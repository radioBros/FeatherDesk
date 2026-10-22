// SPDX-License-Identifier: AGPL-3.0-only

//go:build !linux && !darwin

package main

// lockDataDir is a no-op where flock is unavailable (unsupported platforms).
func lockDataDir(dir string) (func(), error) { return func() {}, nil }
