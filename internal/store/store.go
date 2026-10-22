// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns database access for both engines (D-14, D-18).
//
// Queries are written once with `?` placeholders; Rebind turns them into `$n`
// for Postgres. Writes go through DB.Tx, which on SQLite uses the single
// writer connection with BEGIN IMMEDIATE; reads use the reader pool.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Dialect int

const (
	SQLite Dialect = iota
	Postgres
)

func (d Dialect) String() string {
	if d == Postgres {
		return "postgres"
	}
	return "sqlite"
}

// ErrNotFound is returned by single-row lookups that match nothing.
var ErrNotFound = errors.New("store: not found")

// Options tune the pools; zero values take the defaults from CONVENTIONS §10.
type Options struct {
	Readers  int // SQLite reader pool size (default 2)
	PoolSize int // Postgres pool size (default 5)
	CacheKiB int // SQLite cache_size per connection in KiB (default 2000)
	OnCommit func()
}

// DB wraps the writer and reader pools of the active engine.
type DB struct {
	W       *sql.DB
	R       *sql.DB
	Dialect Dialect
	URL     string

	mu       sync.RWMutex
	onCommit []func()

	writeTxs   atomic.Int64 // write transactions begun through Tx
	writeStmts atomic.Int64 // statements run inside those transactions
}

// WriteStats counts the writer work done through Tx since Open: transactions
// and the statements inside them. The counters are two atomic adds per call;
// tests and benchmarks (DB-026, PLT-023) diff two snapshots.
type WriteStats struct {
	Txs        int64
	Statements int64
}

// WriteStats returns the current writer counters.
func (db *DB) WriteStats() WriteStats {
	return WriteStats{Txs: db.writeTxs.Load(), Statements: db.writeStmts.Load()}
}

// Open connects to `url`: empty or `sqlite:///path` / `file:path` selects SQLite,
// `postgres://` or `postgresql://` selects Postgres.
func Open(ctx context.Context, rawURL string, opt Options) (*DB, error) {
	if opt.Readers <= 0 {
		opt.Readers = 2
	}
	if opt.PoolSize <= 0 {
		opt.PoolSize = 5
	}
	if opt.CacheKiB <= 0 {
		opt.CacheKiB = 2000
	}
	db := &DB{URL: rawURL}
	if opt.OnCommit != nil {
		db.onCommit = append(db.onCommit, opt.OnCommit)
	}
	switch {
	case strings.HasPrefix(rawURL, "postgres://"), strings.HasPrefix(rawURL, "postgresql://"):
		db.Dialect = Postgres
		pc, err := pgx.ParseConfig(rawURL)
		if err != nil {
			return nil, fmt.Errorf("store: postgres URL: %w", err)
		}
		// JIT compilation costs more than it saves on FeatherDesk' short
		// OLTP queries (DB-023: 124 ms of compile for a 21 ms list query on
		// 100 k tickets), so it is off per session unless the URL sets it.
		if _, set := pc.RuntimeParams["jit"]; !set {
			pc.RuntimeParams["jit"] = "off"
		}
		p := stdlib.OpenDB(*pc)
		p.SetMaxOpenConns(opt.PoolSize)
		p.SetMaxIdleConns(opt.PoolSize)
		p.SetConnMaxIdleTime(5 * time.Minute)
		db.W, db.R = p, p
	case rawURL != "" && strings.Contains(rawURL, "://") && !strings.HasPrefix(rawURL, "sqlite://") && !strings.HasPrefix(rawURL, "file://"):
		return nil, fmt.Errorf("store: unsupported database URL scheme in %q (want sqlite:// or postgres://)", redactURL(rawURL))
	default:
		db.Dialect = SQLite
		path := sqlitePath(rawURL)
		dsn := sqliteDSN(path, opt.CacheKiB)
		w, err := sql.Open("sqlite", dsn+"&_txlock=immediate")
		if err != nil {
			return nil, err
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		r, err := sql.Open("sqlite", dsn+"&mode=ro")
		if err != nil {
			return nil, closeWith(err, w)
		}
		if path == ":memory:" {
			// An in-memory DB is per-connection; share the writer for reads.
			_ = r.Close() // unused read pool; the writer is shared instead
			r = w
		} else {
			r.SetMaxOpenConns(opt.Readers)
			r.SetMaxIdleConns(opt.Readers)
		}
		db.W, db.R = w, r
	}
	if err := db.W.PingContext(ctx); err != nil {
		return nil, closeWith(fmt.Errorf("store: ping %s: %w", db.Dialect, err), db)
	}
	if db.Dialect == Postgres {
		var v int
		if err := db.W.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&v); err != nil {
			return nil, closeWith(err, db)
		}
		if v < MinPostgresVersion {
			return nil, closeWith(fmt.Errorf("store: Postgres %d.%d is too old; FeatherDesk needs Postgres 16 or newer", v/10000, v%10000), db)
		}
	}
	return db, nil
}

// MinPostgresVersion is server_version_num of the oldest supported Postgres (D-14).
const MinPostgresVersion = 160000

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid url>"
	}
	return u.Redacted()
}

func sqlitePath(u string) string {
	switch {
	case u == "":
		return "/data/featherdesk.db"
	case strings.HasPrefix(u, "sqlite://"):
		p := strings.TrimPrefix(u, "sqlite://")
		if i := strings.IndexByte(p, '?'); i >= 0 {
			p = p[:i]
		}
		return p
	case strings.HasPrefix(u, "file:"):
		p := strings.TrimPrefix(u, "file:")
		if i := strings.IndexByte(p, '?'); i >= 0 {
			p = p[:i]
		}
		return p
	}
	return u
}

func sqliteDSN(path string, cacheKiB int) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "cache_size(-"+strconv.Itoa(cacheKiB)+")")
	q.Add("_time_format", "sqlite")
	return "file:" + path + "?" + q.Encode()
}

func (db *DB) Close() error {
	var err error
	if db.R != nil && db.R != db.W {
		err = db.R.Close()
	}
	if db.W != nil {
		if e := db.W.Close(); e != nil {
			err = e
		}
	}
	return err
}

// OnCommit registers a hook run after every successful write transaction
// (used by the queue dispatcher wake-up, D-11).
func (db *DB) OnCommit(fn func()) {
	db.mu.Lock()
	db.onCommit = append(db.onCommit, fn)
	db.mu.Unlock()
}

func (db *DB) fireCommit() {
	db.mu.RLock()
	hooks := db.onCommit
	db.mu.RUnlock()
	for _, h := range hooks {
		h()
	}
}

// Rebind converts `?` placeholders to `$1..$n` for Postgres. Question marks
// inside single-quoted literals are left alone.
func (db *DB) Rebind(q string) string { return Rebind(db.Dialect, q) }

func Rebind(d Dialect, q string) string {
	if d != Postgres || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	inStr := false
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\'' {
			inStr = !inStr
		}
		if c == '?' && !inStr {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Querier is satisfied by *DB (reads on the reader pool) and *Tx.
type Querier interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	Dialect() Dialect
}

type readQ struct{ db *DB }

// Read returns a Querier on the reader pool. Writes through it fail on SQLite (mode=ro).
func (db *DB) Read() Querier { return readQ{db} }

func (r readQ) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return r.db.R.ExecContext(ctx, r.db.Rebind(q), args...)
}
func (r readQ) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return r.db.R.QueryContext(ctx, r.db.Rebind(q), args...)
}
func (r readQ) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return r.db.R.QueryRowContext(ctx, r.db.Rebind(q), args...)
}
func (r readQ) Dialect() Dialect { return r.db.Dialect }

// Tx is a write transaction with placeholder rebinding.
type Tx struct {
	tx      *sql.Tx
	dialect Dialect
	after   []func()
	stmts   *atomic.Int64
}

func (t *Tx) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	t.count()
	return t.tx.ExecContext(ctx, Rebind(t.dialect, q), args...)
}
func (t *Tx) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	t.count()
	return t.tx.QueryContext(ctx, Rebind(t.dialect, q), args...)
}
func (t *Tx) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	t.count()
	return t.tx.QueryRowContext(ctx, Rebind(t.dialect, q), args...)
}

func (t *Tx) count() {
	if t.stmts != nil {
		t.stmts.Add(1)
	}
}
func (t *Tx) Dialect() Dialect { return t.dialect }

// AfterCommit schedules fn to run only if the transaction commits.
func (t *Tx) AfterCommit(fn func()) { t.after = append(t.after, fn) }

// Tx runs fn in a write transaction. On SQLite the writer pool has one
// connection and `_txlock=immediate`, so writers serialise instead of
// failing with SQLITE_BUSY mid-transaction.
func (db *DB) Tx(ctx context.Context, fn func(tx *Tx) error) error {
	sqlTx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	db.writeTxs.Add(1)
	t := &Tx{tx: sqlTx, dialect: db.Dialect, stmts: &db.writeStmts}
	if err := fn(t); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return err
	}
	for _, f := range t.after {
		f()
	}
	db.fireCommit()
	return nil
}

// clockOverride, when set, replaces the wall clock behind Now. Only the demo
// seeder (internal/demo) sets it, to write a believable history; production
// code never does.
var clockOverride atomic.Pointer[func() time.Time]

// Now is the single time source for persisted timestamps (UTC, µs).
func Now() time.Time {
	if f := clockOverride.Load(); f != nil {
		return (*f)().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

// SetClock replaces the time source behind Now until the returned restore is
// called (nil fn = wall clock). It is process-wide: use it only in offline
// tools such as the demo seeder, never while the server serves requests.
func SetClock(fn func() time.Time) (restore func()) {
	prev := clockOverride.Load()
	if fn == nil {
		clockOverride.Store(nil)
	} else {
		clockOverride.Store(&fn)
	}
	return func() { clockOverride.Store(prev) }
}

// IsUniqueViolation reports a unique-constraint error on either engine.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "SQLSTATE 23505") || strings.Contains(s, "duplicate key value")
}

// NotFound maps sql.ErrNoRows to ErrNotFound.
func NotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// closeWith closes c and returns err unchanged, or joined with the close error
// when closing fails too (the original error stays comparable when it does not).
func closeWith(err error, c io.Closer) error {
	if cerr := c.Close(); cerr != nil {
		return errors.Join(err, cerr)
	}
	return err
}
