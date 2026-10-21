// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// errAppRunning is what offline commands (restore, migrate-engine) report when
// the server, or another offline command, holds the single-instance lock.
var errAppRunning = errors.New("stop the server first: this command runs offline only")

// localBlobRoot is the directory of the local blob backend ("" for S3): the
// data directory by default, or the path of a file:// ST_BLOB_URL. backup and
// restore use the same root, so a restore puts files back where backup read them.
func localBlobRoot(cfg *config.Config) string {
	switch {
	case cfg.BlobURL == "":
		return cfg.DataDir
	case strings.HasPrefix(cfg.BlobURL, "file://"):
		return strings.TrimPrefix(cfg.BlobURL, "file://")
	}
	return ""
}

// lockURL takes the single-instance lock of the database at rawURL the way
// `serve` does (store.LockInstance): a flock on "<file>.lock" for SQLite,
// without opening (and so creating) the file, or the advisory lock on Postgres.
func lockURL(ctx context.Context, rawURL string) (release func(), err error) {
	isPG := strings.HasPrefix(rawURL, "postgres://") || strings.HasPrefix(rawURL, "postgresql://")
	if !isPG {
		if path := store.SQLiteFile(rawURL); path != ":memory:" {
			release, err = store.LockSQLiteFile(path)
		} else {
			release = func() {}
		}
	} else {
		var db *store.DB
		db, err = store.Open(ctx, rawURL, store.Options{PoolSize: 2})
		if err != nil {
			return nil, err
		}
		var unlock func()
		unlock, err = db.LockInstance(ctx)
		if err != nil {
			err = errors.Join(err, db.Close())
		} else {
			release = func() {
				unlock()
				_ = db.Close() // release has no error path; the session lock ends with the connection
			}
		}
	}
	if errors.Is(err, store.ErrInstanceRunning) {
		return nil, fmt.Errorf("%w (%v)", errAppRunning, err)
	}
	return release, err
}
