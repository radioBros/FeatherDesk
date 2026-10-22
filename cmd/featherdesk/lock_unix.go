// SPDX-License-Identifier: AGPL-3.0-only

//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDataDir takes an exclusive, non-blocking flock on $FD_DATA_DIR/.lock so
// two instances can never share one volume (SQLite single writer, in-process
// queue). The kernel releases it if the process dies.
func lockDataDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another featherdesk instance is using %s (lock held): %w", dir, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
