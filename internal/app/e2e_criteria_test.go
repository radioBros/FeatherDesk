// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"encoding/json"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestInvalidTimezoneRejected (DB-033): the calendar service boundary loads
// the IANA zone; an unknown or path-like zone is a 422 field error on create
// and on update, and nothing is written. Full app, both engines.
func TestInvalidTimezoneRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		sink := newSink(t)
		s := buildStack(t, sink, e2eEnv(t, db, sink, t.TempDir()))
		admin, _ := s.staff(domain.RoleAdmin, 3)
		week := map[string]any{"mon": []map[string]string{{"start": "09:00", "end": "18:00"}}}
		cal := admin.must(201, "POST", "/api/v1/admin/calendars", map[string]any{"name": "Rome office", "timezone": "Europe/Rome", "week": week})
		id, _ := cal["id"].(string)
		if id == "" || cal["timezone"] != "Europe/Rome" {
			t.Fatalf("created %v", cal)
		}
		count := func() int {
			var n int
			if err := db.Read().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM business_calendars`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := count()
		for _, tz := range []string{"Mars/Olympus", "Europe/Atlantis", "../../etc/localtime", "/etc/localtime", "Europe/Rome\x00"} {
			for _, call := range []struct{ method, path string }{
				{"POST", "/api/v1/admin/calendars"},
				{"PATCH", "/api/v1/admin/calendars/" + id},
			} {
				code, body := admin.do(call.method, call.path, map[string]any{"name": "Bad zone", "timezone": tz, "week": week})
				if code != 422 {
					t.Fatalf("%s timezone %q: status %d %s", call.method, tz, code, body)
				}
				var p struct {
					Errors []struct{ Field, Code string } `json:"errors"`
				}
				if err := json.Unmarshal(body, &p); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, e := range p.Errors {
					found = found || (e.Field == "timezone" && e.Code == "timezone")
				}
				if !found {
					t.Fatalf("%s timezone %q: no timezone field error in %s", call.method, tz, body)
				}
			}
		}
		if n := count(); n != before {
			t.Fatalf("calendars %d → %d after rejected writes", before, n)
		}
		got := admin.must(200, "GET", "/api/v1/admin/calendars/"+id, nil)
		if got["timezone"] != "Europe/Rome" || got["name"] != "Rome office" {
			t.Fatalf("calendar changed by a rejected PATCH: %v", got)
		}
	})
}
