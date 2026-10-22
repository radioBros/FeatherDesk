// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// TestPerf_List50k (TKT-004: p95 < 50 ms) and TestPerf_Reports50k
// (TKT-028: < 300 ms) on a 50 000-ticket SQLite fixture. Skipped with -short
// and under the race detector (latency budgets are measured without it).
func seed50k(t *testing.T) (*env, []string, *domain.Principal) {
	if testing.Short() {
		t.Skip("-short")
	}
	if raceEnabled {
		t.Skip("latency budgets are not meaningful under -race")
	}
	db := storetest.Open(t, store.SQLite)
	e := newEnv(t, db)
	ctx := context.Background()
	teams := []string{storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, ""), storetest.CreateTeam(t, db, "")}
	agent := e.staff(domain.RoleAgent, 2, teams[0], teams[1])
	cust := storetest.CreateCustomer(t, db, "")
	statuses := []string{"new", "open", "pending", "on_hold", "resolved", "closed", "closed", "closed"}
	prios := []string{"low", "normal", "high", "urgent"}
	base := time.Now().UTC().Add(-365 * 24 * time.Hour)
	for chunk := 0; chunk < 50000; chunk += 5000 {
		err := db.Tx(ctx, func(tx *store.Tx) error {
			for i := chunk; i < chunk+5000; i++ {
				at := base.Add(time.Duration(i) * 10 * time.Minute)
				var resolved any
				if statuses[i%8] == "resolved" || statuses[i%8] == "closed" {
					resolved = at.Add(3 * time.Hour)
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO tickets (id, number, subject, status, priority, tier, team_id, requester_id, channel,
					first_response_due_at, resolution_due_at, first_responded_at, resolved_at, last_customer_activity_at, created_at, updated_at)
					VALUES (?, ?, 'Perf ticket', ?, ?, ?, ?, ?, 'portal', ?, ?, ?, ?, ?, ?, ?)`,
					store.NewID(), 2000+i, statuses[i%8], prios[i%4], 1+i%3, teams[i%4], cust, at.Add(8*time.Hour), at.Add(48*time.Hour),
					at.Add(time.Hour), resolved, at, at, at); err != nil {
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
	return e, teams, agent
}

func p95(t *testing.T, n int, fn func()) time.Duration {
	var d []time.Duration
	for i := 0; i < n; i++ {
		s := time.Now()
		fn()
		d = append(d, time.Since(s))
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)*95/100]
}

func TestPerf_List50k(t *testing.T) {
	e, _, agent := seed50k(t)
	ctx := context.Background()
	for _, sort := range []string{"attention", "-updated_at"} {
		q, _ := ParseAgentListQuery(url.Values{"sort": {sort}})
		var next *string
		d := p95(t, 40, func() {
			page, err := e.svc.ListStaff(ctx, agent, q)
			if err != nil || len(page.Items) != 50 {
				t.Fatalf("list: %v", err)
			}
			next = page.NextCursor
		})
		t.Logf("sort=%s p95=%v", sort, d)
		if d > 50*time.Millisecond {
			t.Errorf("sort=%s p95 %v > 50ms", sort, d)
		}
		q.Cursor = *next
		if _, err := e.svc.ListStaff(ctx, agent, q); err != nil {
			t.Fatal(err)
		}
	}
	d := p95(t, 40, func() {
		if _, err := e.svc.Counts(ctx, agent); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("counts p95=%v", d)
}

func TestPerf_Reports50k(t *testing.T) {
	e, _, _ := seed50k(t)
	sup := e.staff(domain.RoleSupervisor, 1)
	ctx := context.Background()
	now := time.Now().UTC()
	d := p95(t, 10, func() {
		if _, err := e.svc.Summary(ctx, sup, ReportQuery{From: now.Add(-30 * 24 * time.Hour), To: now}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("30-day report p95=%v", d)
	if d > 300*time.Millisecond {
		t.Errorf("report p95 %v > 300ms", d)
	}
}
