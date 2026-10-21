// SPDX-License-Identifier: AGPL-3.0-only

package queue

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestRestoreReplaysFromCursor (DB-015): the in-memory queue is not in a
// backup. After a restore, the dispatcher resumes from the dispatch_cursor
// saved in the backup: every event after it is dispatched again, none before.
// (SQLite only: a Postgres restore is pg_restore, outside the binary.)
func TestRestoreReplaysFromCursor(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t, store.SQLite)
	ids := writeEvents(t, db, 6)
	// The backup is taken while the dispatcher had handled the first three
	// events only (the cursor is persisted at most once per CursorEvery).
	if err := db.Tx(ctx, func(tx *store.Tx) error { return store.SetCursor(ctx, tx, cursorName, ids[2]) }); err != nil {
		t.Fatal(err)
	}
	bk, err := store.Backup(ctx, db, store.BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Writes after the backup are lost with the restore (they are not replayed).
	writeEvents(t, db, 2)

	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := store.Restore(ctx, store.RestoreOptions{BackupDir: bk.Dir, DBPath: target}); err != nil {
		t.Fatal(err)
	}
	rdb, err := store.Open(ctx, "sqlite://"+target, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	rec := newRecorder()
	q := New(rdb, fastOpts())
	q.Subscribe("notify", SubscribeOptions{Durable: true}, nil, func(ctx context.Context, e events.Event) error { rec.add(e.ID); return nil })
	if err := q.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer stop(t, q)
	waitFor(t, 5*time.Second, func() bool { return rec.count() >= 3 })
	time.Sleep(200 * time.Millisecond) // a few more dispatcher rounds: nothing else may arrive
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.ids) != 3 {
		t.Fatalf("replayed %d events, want exactly the 3 after the restored cursor: %v", len(rec.ids), rec.ids)
	}
	for _, id := range ids[:3] {
		if rec.ids[id] != 0 {
			t.Errorf("event %s (at or before the restored cursor) was dispatched again", id)
		}
	}
	for _, id := range ids[3:] {
		if rec.ids[id] != 1 {
			t.Errorf("event %s (after the restored cursor) dispatched %d times, want 1", id, rec.ids[id])
		}
	}
}
