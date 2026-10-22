// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"syscall"
)

// ErrInstanceRunning is returned when another FeatherDesk process already
// holds the instance lock: the queue, WebSocket hub and write-behind buffers are
// in-process, so exactly one app process may own a database (D-11, D-14).
var ErrInstanceRunning = errors.New("store: another FeatherDesk instance is already running against this database")

// LockInstance takes the single-instance lock and returns its release func.
// SQLite: an exclusive flock on "<db file>.lock". Postgres: a session-level
// advisory lock held on a dedicated connection.
func (db *DB) LockInstance(ctx context.Context) (release func(), err error) {
	if db.Dialect == Postgres {
		conn, err := db.W.Conn(ctx)
		if err != nil {
			return nil, err
		}
		var schema string
		_ = conn.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema)
		// The SimpleTickets 1.0.0 key is held too, so a not-yet-upgraded
		// SimpleTickets process and FeatherDesk never run on one database.
		var keys []int64
		for _, name := range []string{"featherdesk-instance", "simpletickets-instance"} {
			h := fnv.New64a()
			h.Write([]byte(name))
			h.Write([]byte(schema))
			keys = append(keys, int64(h.Sum64()>>1))
		}
		var ok bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1) AND pg_try_advisory_lock($2)`, keys[0], keys[1]).Scan(&ok); err != nil {
			return nil, closeWith(err, conn)
		}
		if !ok {
			return nil, closeWith(ErrInstanceRunning, conn) // closing the session drops a half-taken lock
		}
		return func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1), pg_advisory_unlock($2)`, keys[0], keys[1])
			_ = conn.Close() // release func has no error path; closing ends the session lock anyway
		}, nil
	}
	path := sqlitePath(db.URL)
	if path == ":memory:" {
		return func() {}, nil
	}
	return LockSQLiteFile(path)
}

// LockSQLiteFile takes the SQLite single-instance lock (an exclusive flock on
// "<path>.lock") without opening the database, so offline commands such as
// `restore` can refuse while the app runs without creating the database file.
// It returns ErrInstanceRunning when the lock is held.
func LockSQLiteFile(path string) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrInstanceRunning
		}
		return nil, fmt.Errorf("store: lock %s: %w", path+".lock", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
