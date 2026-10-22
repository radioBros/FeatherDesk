// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"encoding/xml"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// visibilityFixture is the identical fixture for the I-10 parity suite.
// Every article mentions "router"; slugs encode category/article visibility.
func visibilityFixture(e *env) {
	pub := e.category("pub", VisPublic, "")
	cust := e.category("cust", VisCustomers, "")
	in := e.category("int", VisInternal, "")
	e.article(pub, "pub-pub", VisPublic, "published", "Router password reset", "Reset the router password from the admin page.")
	e.article(pub, "pub-cust", VisCustomers, "published", "Router firmware for customers", "Firmware download for the router.")
	e.article(pub, "pub-draft", VisPublic, "draft", "Router draft notes", "Unfinished router notes.")
	e.article(cust, "cust-pub", VisCustomers, "published", "Router warranty", "Warranty terms for every router.")
	e.article(in, "int-pub", VisInternal, "published", "Router escalation runbook", "Internal router runbook secretword.")
	e.article(pub, "pub-archived", VisPublic, "archived", "Router legacy", "Old router model.")
	id := e.article(cust, "cust-draft", VisCustomers, "draft", "Router beta", "Beta router secretword.")
	e.translate(id, "it", "Router beta (IT)", "instradatore beta")
	pp := slugID(e, "pub-pub")
	e.translate(pp, "it", "Reimpostare la password del router", "Reimposta la password dell'instradatore.")
}

func slugID(e *env, slug string) string {
	var id string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT id FROM kb_articles WHERE slug = ?`, slug).Scan(&id); err != nil {
		e.t.Fatalf("slug %s: %v", slug, err)
	}
	return id
}

// TestKBSearch_VisibilityParity is the I-10 parity suite (KB-007): the same
// fixture and queries on SQLite and Postgres; anonymous never sees
// customers/internal/draft hits, customers never internal/draft, staff see
// drafts only with the flag; result sets (ids by slug) are equal across engines.
func TestKBSearch_VisibilityParity(t *testing.T) {
	type key struct{ who, q string }
	results := map[string]map[key][]string{}
	queries := []string{"router", "rout", "password", "secretword", "instradatore", "warranty router", "draft"}
	expect := map[string][]string{
		"anonymous":    {"pub-pub"},
		"customer":     {"cust-pub", "pub-cust", "pub-pub"},
		"staff":        {"cust-pub", "int-pub", "pub-cust", "pub-pub"},
		"staff+drafts": {"cust-draft", "cust-pub", "int-pub", "pub-cust", "pub-draft", "pub-pub"},
	}
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		ctx := context.Background()
		who := map[string]*domain.Principal{"anonymous": nil, "customer": e.p("customer"), "staff": e.p("agent"), "staff+drafts": e.p("agent")}
		got := map[key][]string{}
		for name, p := range who {
			for _, q := range queries {
				hits, err := e.m.Search(ctx, p, q, "en", 50, name == "staff+drafts")
				if err != nil {
					t.Fatalf("%s %q: %v", name, q, err)
				}
				var slugs []string
				for _, h := range hits {
					slugs = append(slugs, h.Slug)
					if name != "staff" && name != "staff+drafts" && strings.Contains(h.Excerpt+h.Title, "secretword") {
						t.Errorf("%s: snippet leaks internal text: %q", name, h.Excerpt)
					}
				}
				sort.Strings(slugs)
				got[key{name, q}] = slugs
			}
			// "router" matches every article: the visible set is exactly the rule.
			if r := got[key{name, "router"}]; !slices.Equal(r, expect[name]) {
				t.Errorf("%s router: got %v want %v", name, r, expect[name])
			}
			if r := got[key{name, "rout"}]; !slices.Equal(r, expect[name]) {
				t.Errorf("%s prefix rout: got %v want %v", name, r, expect[name])
			}
		}
		// Spot checks per rule.
		if r := got[key{"anonymous", "secretword"}]; len(r) != 0 {
			t.Errorf("anonymous found internal/draft text: %v", r)
		}
		if r := got[key{"customer", "secretword"}]; len(r) != 0 {
			t.Errorf("customer found internal/draft text: %v", r)
		}
		if r := got[key{"staff", "secretword"}]; !slices.Equal(r, []string{"int-pub"}) {
			t.Errorf("staff without drafts: %v", r)
		}
		if r := got[key{"staff+drafts", "secretword"}]; !slices.Equal(r, []string{"cust-draft", "int-pub"}) {
			t.Errorf("staff with drafts: %v", r)
		}
		if r := got[key{"anonymous", "instradatore"}]; !slices.Equal(r, []string{"pub-pub"}) {
			t.Errorf("italian translation search: %v", r)
		}
		results[t.Name()] = got
	})
	var names []string
	for n := range results {
		names = append(names, n)
	}
	if len(names) == 2 {
		a, b := results[names[0]], results[names[1]]
		for k, v := range a {
			if !slices.Equal(v, b[k]) {
				t.Errorf("engine parity %v: %s=%v %s=%v", k, names[0], v, names[1], b[k])
			}
		}
	}
}

// ---- KB-008: public reads ----

func TestKBRead_AnonymousCustomersArticle404(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		for _, slug := range []string{"pub-cust", "cust-pub", "int-pub"} {
			e.want(e.do("GET", "/api/v1/kb/articles/"+slug, "", nil), 404)
		}
		e.want(e.do("GET", "/api/v1/kb/articles/pub-cust", "customer", nil), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/pub-pub", "", nil), 200)
		// Lists and categories follow the same predicate.
		var page pageDTO
		e.do("GET", "/api/v1/kb/articles", "", nil).json(t, &page)
		if len(page.Items) != 1 || page.Items[0].Slug != "pub-pub" {
			t.Fatalf("anonymous list %+v", page.Items)
		}
		var cats []categoryDTO
		e.do("GET", "/api/v1/kb/categories", "", nil).json(t, &cats)
		if len(cats) != 1 || cats[0].Slug != "pub" || cats[0].ArticleCount != 1 {
			t.Fatalf("anonymous categories %+v", cats)
		}
		// A PAT without kb:read reads like an anonymous visitor.
		e.want(e.do("GET", "/api/v1/kb/articles/pub-cust", "customer-pat-nokb", nil), 404)
	})
}

func TestKBRead_CustomerInternalArticle404(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		e.want(e.do("GET", "/api/v1/kb/articles/int-pub", "customer", nil), 404)
		e.want(e.do("GET", "/api/v1/kb/articles/int-pub", "agent", nil), 200)
		var page pageDTO
		e.do("GET", "/api/v1/kb/articles?limit=50", "customer", nil).json(t, &page)
		var slugs []string
		for _, it := range page.Items {
			slugs = append(slugs, it.Slug)
		}
		sort.Strings(slugs)
		if !slices.Equal(slugs, []string{"cust-pub", "pub-cust", "pub-pub"}) {
			t.Fatalf("customer list %v", slugs)
		}
		var cats []categoryDTO
		e.do("GET", "/api/v1/kb/categories", "customer", nil).json(t, &cats)
		if len(cats) != 2 {
			t.Fatalf("customer categories %+v", cats)
		}
		// Feedback on an invisible article is a 404 too.
		e.want(e.do("POST", "/api/v1/kb/articles/"+slugID(e, "int-pub")+"/feedback", "customer", map[string]any{"helpful": true}), 404)
	})
}

func TestKBRead_DraftNeverPublic(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		for _, who := range []string{"", "customer", "agent", "admin"} {
			e.want(e.do("GET", "/api/v1/kb/articles/pub-draft", who, nil), 404)
			var page pageDTO
			e.do("GET", "/api/v1/kb/articles?q=draft", who, nil).json(t, &page)
			if len(page.Items) != 0 {
				t.Fatalf("%s: draft in public search %+v", who, page.Items)
			}
		}
		// Staff preview drafts in the editor only.
		var page pageDTO
		e.do("GET", "/api/v1/agent/kb/articles?status=draft", "agent", nil).json(t, &page)
		if len(page.Items) != 2 {
			t.Fatalf("editor drafts %+v", page.Items)
		}
		e.do("GET", "/api/v1/agent/kb/articles?q=secretword", "agent", nil).json(t, &page)
		if len(page.Items) != 2 {
			t.Fatalf("editor search includes drafts: %+v", page.Items)
		}
	})
}

func TestKBRead_PaginationAndCategoryFilter(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		a := e.category("alpha", VisPublic, "")
		b := e.category("beta", VisPublic, "")
		for i := 0; i < 5; i++ {
			e.article(a, "alpha-"+string(rune('a'+i)), VisPublic, "published", "Alpha", "x")
		}
		e.article(b, "beta-a", VisPublic, "published", "Beta", "x")
		seen := map[string]bool{}
		cursor := ""
		for pages := 0; pages < 10; pages++ {
			var page pageDTO
			url := "/api/v1/kb/articles?limit=2"
			if cursor != "" {
				url += "&cursor=" + cursor
			}
			e.do("GET", url, "", nil).json(t, &page)
			for _, it := range page.Items {
				if seen[it.ID] {
					t.Fatalf("duplicate across pages: %s", it.Slug)
				}
				seen[it.ID] = true
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		if len(seen) != 6 {
			t.Fatalf("paged %d of 6", len(seen))
		}
		for _, f := range []string{"beta", b} {
			var page pageDTO
			e.do("GET", "/api/v1/kb/articles?category="+f, "", nil).json(t, &page)
			if len(page.Items) != 1 || page.Items[0].Slug != "beta-a" {
				t.Fatalf("category filter %s: %+v", f, page.Items)
			}
		}
	})
}

// TestKBEditorList_Sort pins the editor list `sort` (title and updated_at,
// both directions) across offset pages, with and without q: an explicit sort
// wins over rank while q still filters. Without sort the order is unchanged.
func TestKBEditorList_Sort(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("fruit", VisPublic, "")
		type art struct {
			id, title string
			match     bool
		}
		var arts []art
		add := func(slug, title, body string, match bool) {
			arts = append(arts, art{e.article(cat, slug, VisPublic, "published", title, body), title, match})
		}
		add("apricot", "Apricot", "A long story about orchards, harvests, seasons and one widget.", true)
		add("apple", "apple", "Nothing to see.", false)
		add("banana-1", "Banana", "A widget for fruit.", true)
		add("banana-2", "banana", "Fruit only.", false)
		add("cherry", "cherry", "Red.", false)
		add("zucchini", "Zucchini widget", "widget widget widget", true)
		// updated_at in an order unrelated to titles; distinct values.
		base := store.Now().Add(-time.Hour)
		upd := map[string]time.Time{}
		for i, k := range []int{3, 0, 5, 1, 4, 2} {
			a := arts[i]
			upd[a.id] = base.Add(time.Duration(k) * time.Minute)
			if err := e.db.Tx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.ExecContext(context.Background(), `UPDATE kb_articles SET updated_at = ? WHERE id = ?`, upd[a.id], a.id)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		all := func(base string) []string {
			t.Helper()
			var ids []string
			cursor := ""
			for pages := 0; pages < 20; pages++ {
				var page pageDTO
				u := base
				if cursor != "" {
					u += "&cursor=" + cursor
				}
				r := e.do("GET", u, "agent", nil)
				e.want(r, 200)
				r.json(t, &page)
				for _, it := range page.Items {
					ids = append(ids, it.ID)
				}
				if page.NextCursor == nil {
					return ids
				}
				cursor = *page.NextCursor
			}
			t.Fatalf("%s: pagination did not end", base)
			return nil
		}
		want := func(sortKey string, onlyMatch bool) []string {
			var xs []art
			for _, a := range arts {
				if !onlyMatch || a.match {
					xs = append(xs, a)
				}
			}
			key, desc := strings.CutPrefix(sortKey, "-")
			slices.SortFunc(xs, func(a, b art) int {
				c := 0
				if key == "title" {
					c = strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
				} else {
					c = upd[a.id].Compare(upd[b.id])
				}
				if c == 0 {
					c = strings.Compare(a.id, b.id)
				}
				if desc {
					c = -c
				}
				return c
			})
			out := make([]string, len(xs))
			for i, a := range xs {
				out[i] = a.id
			}
			return out
		}
		for _, srt := range []string{"title", "-title", "updated_at", "-updated_at"} {
			if got, w := all("/api/v1/agent/kb/articles?limit=2&sort="+srt), want(srt, false); !slices.Equal(got, w) {
				t.Errorf("sort=%s\n got %v\nwant %v", srt, got, w)
			}
			if got, w := all("/api/v1/agent/kb/articles?limit=2&q=widget&sort="+srt), want(srt, true); !slices.Equal(got, w) {
				t.Errorf("q + sort=%s\n got %v\nwant %v", srt, got, w)
			}
		}
		// Default: most recently updated first; with q, rank order (the
		// zucchini article matches best, the others once each).
		if got, w := all("/api/v1/agent/kb/articles?limit=2"), want("-updated_at", false); !slices.Equal(got, w) {
			t.Errorf("default\n got %v\nwant %v", got, w)
		}
		got := all("/api/v1/agent/kb/articles?limit=2&q=widget")
		if len(got) != 3 || got[0] != arts[5].id {
			t.Errorf("default q order %v, want zucchini (%s) first of 3", got, arts[5].id)
		}
		// The title follows the requested locale, as the list shows it.
		e.translate(arts[5].id, "it", "Aaa zucchina", "widget")
		var page pageDTO
		e.do("GET", "/api/v1/agent/kb/articles?sort=title&locale=it", "agent", nil).json(t, &page)
		if len(page.Items) == 0 || page.Items[0].ID != arts[5].id || page.Items[0].Title != "Aaa zucchina" {
			t.Errorf("locale=it title order: first %+v", page.Items)
		}
		r := e.do("GET", "/api/v1/agent/kb/articles?sort=views", "agent", nil)
		e.want(r, 422)
		var p struct {
			Errors []struct {
				Field string `json:"field"`
			} `json:"errors"`
		}
		r.json(t, &p)
		if len(p.Errors) != 1 || p.Errors[0].Field != "sort" {
			t.Errorf("invalid sort problem %s", r.Body.String())
		}
	})
}

// ---- KB-009: caching, sitemap, kill switch ----

func TestKBCache_304OnMatchingETag(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("cache", VisPublic, "")
		id := e.article(cat, "cached", VisPublic, "published", "Cached", "x")
		for _, path := range []string{"/api/v1/kb/articles/cached", "/api/v1/kb/articles", "/api/v1/kb/categories"} {
			r := e.do("GET", path, "", nil)
			e.want(r, 200)
			tag := r.Header().Get("ETag")
			if !strings.HasPrefix(tag, `W/"`) {
				t.Fatalf("%s: weak ETag missing: %q", path, tag)
			}
			r = e.do("GET", path, "", nil, "If-None-Match", tag)
			e.want(r, http.StatusNotModified)
			if r.Body.Len() != 0 || r.Header().Get("ETag") != tag {
				t.Fatalf("%s: 304 must be empty and repeat the ETag", path)
			}
		}
		r := e.do("GET", "/api/v1/kb/articles/cached", "", nil)
		tag := r.Header().Get("ETag")
		// Views alone do not change the public ETag…
		_ = e.m.FlushCounters(context.Background())
		e.want(e.do("GET", "/api/v1/kb/articles/cached", "", nil, "If-None-Match", tag), 304)
		// …an edit does.
		e.translate(id, "en", "Cached v2", "y")
		e.want(e.do("GET", "/api/v1/kb/articles/cached", "", nil, "If-None-Match", tag), 200)
	})
}

func TestKBCache_AuthenticatedIsPrivate(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("priv", VisPublic, "")
		e.article(cat, "shared", VisPublic, "published", "Shared", "x")
		r := e.do("GET", "/api/v1/kb/articles/shared", "", nil)
		if cc := r.Header().Get("Cache-Control"); cc != "public, max-age=300" {
			t.Fatalf("anonymous Cache-Control %q", cc)
		}
		for _, who := range []string{"customer", "agent"} {
			r = e.do("GET", "/api/v1/kb/articles/shared", who, nil)
			if cc := r.Header().Get("Cache-Control"); cc != "private, no-cache" || r.Header().Get("ETag") == "" {
				t.Fatalf("%s Cache-Control %q ETag %q", who, cc, r.Header().Get("ETag"))
			}
		}
		// The ETag differs per audience, so a shared cache never crosses them.
		anon := e.do("GET", "/api/v1/kb/articles", "", nil).Header().Get("ETag")
		cust := e.do("GET", "/api/v1/kb/articles", "customer", nil).Header().Get("ETag")
		if anon == cust {
			t.Fatal("anonymous and customer lists share an ETag")
		}
		// robots flag: public → index; customers-only → noindex.
		var a articleDTO
		r = e.do("GET", "/api/v1/kb/articles/shared", "", nil)
		r.json(t, &a)
		if a.Robots != "index" || a.Views != nil {
			t.Fatalf("public article robots=%q views=%v (counters are staff-only)", a.Robots, a.Views)
		}
		c2 := e.category("cust-only", VisCustomers, "")
		e.article(c2, "cust-art", VisCustomers, "published", "C", "x")
		e.do("GET", "/api/v1/kb/articles/cust-art", "customer", nil).json(t, &a)
		if a.Robots != "noindex" {
			t.Fatalf("customers article robots=%q", a.Robots)
		}
	})
}

type sitemapDoc struct {
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

func TestKBSitemap_OnlyPublicPublished(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		visibilityFixture(e)
		e.setSupport(func(s *settingsSupport) { s.KBSitemapEnabled = true })
		r := e.do("GET", "/kb/sitemap.xml", "", nil)
		e.want(r, 200)
		if ct := r.Header().Get("Content-Type"); ct != "application/xml" {
			t.Fatalf("Content-Type %q", ct)
		}
		var doc sitemapDoc
		if err := xml.Unmarshal(r.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.URLs) != 1 || doc.URLs[0].Loc != "https://help.example.com/kb/pub-pub" {
			t.Fatalf("sitemap %+v", doc.URLs)
		}
	})
}

func TestKBSitemap_DisabledReturns404(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		e.category("x", VisPublic, "")
		e.want(e.do("GET", "/kb/sitemap.xml", "", nil), 404) // kb_sitemap_enabled defaults to false in settings
		e.setSupport(func(s *settingsSupport) { s.KBSitemapEnabled = true })
		e.want(e.do("GET", "/kb/sitemap.xml", "", nil), 200)
		e.setSupport(func(s *settingsSupport) { s.KBEnabled = false })
		e.want(e.do("GET", "/kb/sitemap.xml", "", nil), 404)
	})
}

// TestKB_DisabledHidesPublicRoutes: kb_enabled=false hides every public and
// portal KB route (404) and keeps the data and the staff editor.
func TestKB_DisabledHidesPublicRoutes(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("off", VisPublic, "")
		id := e.article(cat, "dormant", VisPublic, "published", "Dormant", "x")
		e.setSupport(func(s *settingsSupport) { s.KBEnabled = false })
		// The spec lists no 404 on these list operations; the kill switch is the exception.
		e.guard("GET", "/api/v1/kb/categories", "", nil, 404)
		e.guard("GET", "/api/v1/kb/articles", "customer", nil, 404)
		e.want(e.do("GET", "/api/v1/kb/articles/dormant", "", nil), 404)
		e.want(e.do("POST", "/api/v1/kb/articles/"+id+"/feedback", "", map[string]any{"helpful": true}), 404)
		e.guard("GET", "/api/v1/portal/kb/suggest?q=dormant", "customer", nil, 404)
		e.want(e.do("GET", "/api/v1/agent/kb/articles/"+id, "agent", nil), 200)
		e.setSupport(func(s *settingsSupport) { s.KBEnabled = true })
		e.want(e.do("GET", "/api/v1/kb/articles/dormant", "", nil), 200)
	})
}

// TestKB_RoutesMatchSpec: every KB route is documented and every documented
// KB operation is served (CON-010 contract check).
func TestKB_RoutesMatchSpec(t *testing.T) {
	db := storeOpen(t)
	e := newEnv(t, db)
	apispec.AssertRoutes(t, e.srv.Router())
	for _, prefix := range []string{"/api/v1/kb", "/api/v1/agent/kb", "/api/v1/admin/kb", "/api/v1/portal/kb", "/kb/"} {
		missing, err := apispec.Unimplemented(e.srv.Router(), prefix)
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) > 0 {
			t.Errorf("unimplemented %s operations: %v", prefix, missing)
		}
	}
}

func storeOpen(t *testing.T) *store.DB {
	t.Helper()
	return storetest.Open(t, store.SQLite)
}
