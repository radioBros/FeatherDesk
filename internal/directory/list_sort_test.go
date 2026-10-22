// SPDX-License-Identifier: AGPL-3.0-only

package directory_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/app"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// dirModule mounts the directory routes (in the app they come with the
// tickets module).
type dirModule struct{ *directory.Directory }

func (dirModule) Name() string { return "directory.test" }

func dirEnv(t *testing.T, db *store.DB) (*authntest.Env, *authntest.Client) {
	e := authntest.New(t, db, authntest.Options{Extra: []app.Factory{func(d *app.Deps) (app.Module, error) {
		return dirModule{directory.NewDirectory(directory.DirDeps{DB: d.DB, Log: d.Log, Settings: d.Settings, Hub: d.Hub, Blob: d.Blob})}, nil
	}}})
	return e, e.Login(e.StaffUser(domain.RoleAdmin, 3, true))
}

// sortRow is a fixture row; names and e-mails are letters only, so Go's
// lower-case byte order equals both engines' lower() and collation.
type sortRow struct{ id, name, email string }

// expectOrder is rows ordered by lower(field) then id; desc reverses both.
func expectOrder(rows []sortRow, sort string) []string {
	field := strings.TrimPrefix(sort, "-")
	key := func(r sortRow) string {
		switch field {
		case "name":
			return strings.ToLower(r.name)
		case "email":
			return strings.ToLower(r.email)
		}
		return ""
	}
	rs := slices.Clone(rows)
	slices.SortFunc(rs, func(a, b sortRow) int {
		if c := strings.Compare(key(a), key(b)); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	if field != sort {
		slices.Reverse(rs)
	}
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.id
	}
	return ids
}

type listPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func getPage(t *testing.T, c *authntest.Client, path string) listPage {
	t.Helper()
	r, b := c.Do("GET", path, nil)
	if r.StatusCode != 200 {
		t.Fatalf("GET %s: %d %s", path, r.StatusCode, b)
	}
	var p listPage
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// pageAll follows next_cursor two rows at a time and returns every id seen.
func pageAll(t *testing.T, c *authntest.Client, path string) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for range 20 {
		p := getPage(t, c, path+"&limit=2"+cursor)
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		if p.NextCursor == nil {
			return ids
		}
		cursor = "&cursor=" + *p.NextCursor
	}
	t.Fatalf("%s: no last page", path)
	return nil
}

// checkSorts pages base (which already has a "?") with each sort and without
// one (creation order), comparing against rows (inserted in id order).
func checkSorts(t *testing.T, c *authntest.Client, base string, rows []sortRow, sorts ...string) {
	t.Helper()
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	if got := pageAll(t, c, base); !slices.Equal(got, ids) {
		t.Errorf("%s default order:\n got %v\nwant %v", base, got, ids)
	}
	for _, s := range sorts {
		want := expectOrder(rows, s)
		if got := pageAll(t, c, base+"&sort="+s); !slices.Equal(got, want) {
			t.Errorf("%s sort=%s:\n got %v\nwant %v", base, s, got, want)
		}
	}
}

// checkBadSortAndCursors: an unknown sort and a cursor of another order are 422.
// The unknown sort is outside the spec's enum, so that one request is not
// validated against it.
func checkBadSortAndCursors(t *testing.T, c *authntest.Client, base, badSort string) {
	t.Helper()
	nc := *c
	nc.NoCheck = true
	r, b := nc.Do("GET", base+"&sort="+badSort, nil)
	if r.StatusCode != 422 || !strings.Contains(string(b), `"field":"sort"`) {
		t.Fatalf("sort=%s: %d %s", badSort, r.StatusCode, b)
	}
	byName := *getPage(t, c, base+"&sort=name&limit=2").NextCursor
	byID := *getPage(t, c, base+"&limit=2").NextCursor
	for _, q := range []string{"&sort=-name&cursor=" + byName, "&cursor=" + byName, "&sort=name&cursor=" + byID, "&sort=name&cursor=bm9wZQ"} {
		r, b := c.Do("GET", base+q, nil)
		if r.StatusCode != 422 || !strings.Contains(string(b), `"field":"cursor"`) {
			t.Errorf("%s: %d %s", q, r.StatusCode, b)
		}
	}
}

// Mixed-case names with case-insensitive ties (broken by id) and e-mails in
// yet another order; the "zq" prefix keeps the admin and fixtures out via ?q=.
var sortNames = []string{"zqDelta", "ZQalpha", "zqcharlie", "zqALPHA", "zqbravo", "zqalpha", "ZQecho"}
var sortMails = []string{"zqkilo", "zqmike", "zqhotel", "zqjuliet", "zqlima", "zqgolf", "zqindia"}

func TestListCustomers_Sort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, c := dirEnv(t, db)
		var rows []sortRow
		for i, n := range sortNames {
			id := storetest.CreateCustomer(t, e.DB, "")
			e.Exec(`UPDATE users SET name = ?, email = ? WHERE id = ?`, n, sortMails[i]+"@example.test", id)
			rows = append(rows, sortRow{id, n, sortMails[i] + "@example.test"})
		}
		checkSorts(t, c, "/api/v1/agent/customers?q=zq", rows, "name", "-name", "email", "-email")
		checkBadSortAndCursors(t, c, "/api/v1/agent/customers?q=zq", "created_at")
	})
}

func TestListOrganizations_Sort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, c := dirEnv(t, db)
		var rows []sortRow
		// Organization names are unique case-insensitively: no ties here.
		for _, n := range []string{"zqDelta", "ZQalpha", "zqCharlie", "zqbravo", "ZQecho", "zqfoxtrot", "zqGolf"} {
			rows = append(rows, sortRow{id: storetest.CreateOrg(t, e.DB, n), name: n})
		}
		checkSorts(t, c, "/api/v1/agent/organizations?q=zq", rows, "name", "-name")
		checkBadSortAndCursors(t, c, "/api/v1/agent/organizations?q=zq", "email")
	})
}

func TestAdminStaff_ListSort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e, _, c := adminEnv(t, db)
		var rows []sortRow
		for _, n := range sortNames {
			id := storetest.CreateStaff(t, e.DB, domain.RoleAgent, 1)
			e.Exec(`UPDATE users SET name = ? WHERE id = ?`, n, id)
			rows = append(rows, sortRow{id: id, name: n})
		}
		checkSorts(t, c, "/api/v1/admin/staff?q=zq", rows, "name", "-name")
		checkBadSortAndCursors(t, c, "/api/v1/admin/staff?q=zq", "email")
	})
}
