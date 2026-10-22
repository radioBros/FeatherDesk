// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
)

func TestFixtures(t *testing.T) {
	Engines(t, func(t *testing.T, db *store.DB) {
		org := CreateOrg(t, db, "")
		team := CreateTeam(t, db, "")
		agent := CreateStaff(t, db, domain.RoleAgent, 2, team)
		cust := CreateCustomer(t, db, org)
		cat := CreateCategory(t, db, "", team)
		id := CreateTicket(t, db, Ticket{Requester: cust, OrgID: org, TeamID: team, CategoryID: cat, AssigneeID: agent})
		var n int64
		if err := db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n <= 1000 {
			t.Fatalf("ticket number %d", n)
		}
	})
}
