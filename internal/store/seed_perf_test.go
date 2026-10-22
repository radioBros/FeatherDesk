// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestSeedDemo(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		res, err := store.SeedDemo(ctx, db, store.SeedOptions{Tickets: 30, PasswordHash: "$argon2id$v=19$m=19456,t=2,p=1$x$y"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Tickets != 30 || len(res.Accounts) != 8 {
			t.Fatalf("%+v", res)
		}
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM ticket_events`).Scan(&n); err != nil {
			t.Fatalf("db.Read: %v", err)
		}
		if n != 30 {
			t.Fatalf("events %d", n)
		}
		if _, err := store.SeedDemo(ctx, db, store.SeedOptions{}); !errors.Is(err, store.ErrAlreadySeeded) {
			t.Fatalf("second seed: %v", err)
		}
	})
}

// TestListPerformance50k (DB-023): an agent queue page over 50k tickets stays
// under 50 ms p95 on SQLite. Skipped with -short.
// raceEnabled is set by race_test.go: timings are meaningless under -race.
var raceEnabled bool

func TestListPerformance50k(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("-short or -race")
	}
	db, err := store.Open(ctx, "sqlite://"+filepath.Join(t.TempDir(), "perf.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	}()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	teams := []string{storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")}
	cust := storetest.CreateCustomer(t, db, "")
	statuses := []string{"new", "open", "pending", "on_hold", "resolved", "closed", "closed", "closed"}
	base := time.Now().UTC().Add(-365 * 24 * time.Hour)
	const total = 50000
	for chunk := 0; chunk < total; chunk += 5000 {
		err := db.Tx(ctx, func(tx *store.Tx) error {
			for i := chunk; i < chunk+5000; i++ {
				at := base.Add(time.Duration(i) * 10 * time.Minute)
				if _, err := tx.ExecContext(ctx, `INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, requester_id, channel, last_customer_activity_at, created_at, updated_at)
					VALUES (?, ?, ?, ?, 'normal', ?, ?, ?, 'portal', ?, ?, ?)`,
					store.NewID(), 2000+i, "Perf ticket", statuses[i%len(statuses)], 1+i%3, teams[i%len(teams)], cust, at, at, at); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.W.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}
	q := `SELECT id, number, subject, status, priority, tier, updated_at FROM tickets
		WHERE team_id IN (?, ?) AND tier <= ? AND status IN ('new', 'open', 'pending', 'on_hold')
		ORDER BY updated_at DESC, id DESC LIMIT 50`
	var durs []time.Duration
	for i := 0; i < 60; i++ {
		start := time.Now()
		rows, err := db.Read().QueryContext(ctx, q, teams[0], teams[1], 2)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		durs = append(durs, time.Since(start))
		if n != 50 {
			t.Fatalf("got %d rows", n)
		}
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	p95 := durs[len(durs)*95/100]
	t.Logf("agent queue page over %d tickets: p50=%v p95=%v", total, durs[len(durs)/2], p95)
	if p95 > 50*time.Millisecond {
		t.Fatalf("p95 %v > 50ms", p95)
	}
}
