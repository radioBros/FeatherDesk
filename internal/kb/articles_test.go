// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/store"
)

// ---- KB-001: M-10 on both engines ----

func TestKB_M10_ArticleWiderThanCategoryRefused(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("internal-docs", VisInternal, "")
		// Trigger (second line of defence): raw insert refused on both engines.
		err := e.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, status, visibility, created_at, updated_at) VALUES (?, ?, 'leak', 'draft', 'public', ?, ?)`,
				store.NewID(), cat, store.Now(), store.Now())
			return err
		})
		if !isM10(err) {
			t.Fatalf("trigger must refuse a public article in an internal category, got %v", err)
		}
		// Service (first line): clean 422.
		r := e.do("POST", "/api/v1/agent/kb/articles", "agent", map[string]any{"category_id": cat, "locale": "en", "title": "Leak", "visibility": "public"})
		e.want(r, 422)
		// Moving a public article into an internal category is refused too.
		pub := e.category("open", VisPublic, "")
		id := e.article(pub, "movable", VisPublic, "draft", "Movable", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "agent", map[string]any{"category_id": cat}), 422)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "agent", map[string]any{"category_id": cat, "visibility": "internal"}), 200)
	})
}

func TestKB_M10_CategoryNarrowingRefused(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("faq", VisPublic, "")
		e.article(cat, "faq-1", VisPublic, "published", "FAQ", "x")
		err := e.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE kb_categories SET visibility = 'customers' WHERE id = ?`, cat)
			return err
		})
		if !isM10(err) {
			t.Fatalf("trigger must refuse narrowing below a contained article, got %v", err)
		}
		r := e.do("PATCH", "/api/v1/admin/kb/categories/"+cat, "supervisor", map[string]any{"slug": "faq", "title": map[string]string{"en": "FAQ"}, "visibility": "customers"})
		e.want(r, 409)
		// Narrowing a parent below a child category is refused as well.
		parent := e.category("parent", VisPublic, "")
		e.category("child", VisPublic, parent)
		e.want(e.do("PATCH", "/api/v1/admin/kb/categories/"+parent, "supervisor", map[string]any{"slug": "parent", "title": map[string]string{"en": "P"}, "visibility": "internal"}), 409)
		// A child can never be wider than its parent.
		in := e.category("inside", VisInternal, "")
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "supervisor", map[string]any{"slug": "wide-child", "parent_id": in, "title": map[string]string{"en": "W"}, "visibility": "public"}), 422)
	})
}

// ---- KB-002: categories ----

func TestKBCategory_DepthLimit(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		a := e.category("level-1", VisPublic, "")
		b := e.category("level-2", VisPublic, a)
		c := e.category("level-3", VisPublic, b)
		r := e.do("POST", "/api/v1/admin/kb/categories", "supervisor", map[string]any{"slug": "level-4", "parent_id": c, "title": map[string]string{"en": "L4"}, "visibility": "public"})
		e.want(r, 422)
		// Moving a 2-level subtree under level-2 would make depth 4.
		x := e.category("x-root", VisPublic, "")
		e.category("x-child", VisPublic, x)
		e.want(e.do("PATCH", "/api/v1/admin/kb/categories/"+x, "supervisor", map[string]any{"slug": "x-root", "parent_id": b, "title": map[string]string{"en": "X"}, "visibility": "public"}), 422)
		// Slug format and translated titles are validated.
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "supervisor", map[string]any{"slug": "Bad Slug", "title": map[string]string{"en": "B"}, "visibility": "public"}), 422)
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "supervisor", map[string]any{"slug": "dup", "title": map[string]string{"en": "D"}, "visibility": "public"}), 201)
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "supervisor", map[string]any{"slug": "dup", "title": map[string]string{"en": "D"}, "visibility": "public"}), 409)
	})
}

func TestKBCategory_NoCycles(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		a := e.category("a", VisPublic, "")
		b := e.category("b", VisPublic, a)
		e.want(e.do("PATCH", "/api/v1/admin/kb/categories/"+a, "supervisor", map[string]any{"slug": "a", "parent_id": b, "title": map[string]string{"en": "A"}, "visibility": "public"}), 409)
		e.want(e.do("PATCH", "/api/v1/admin/kb/categories/"+a, "supervisor", map[string]any{"slug": "a", "parent_id": a, "title": map[string]string{"en": "A"}, "visibility": "public"}), 409)
		// A legal move and a reorder succeed.
		c := e.category("c", VisPublic, "")
		r := e.do("PATCH", "/api/v1/admin/kb/categories/"+b, "supervisor", map[string]any{"slug": "b", "parent_id": c, "sort": 5, "title": map[string]string{"en": "B", "it": "Bi"}, "visibility": "public"})
		e.want(r, 200)
		var got categoryDTO
		r.json(t, &got)
		if got.ParentID == nil || *got.ParentID != c || got.Sort != 5 || got.Title["it"] != "Bi" {
			t.Fatalf("moved category %+v", got)
		}
	})
}

func TestKBCategory_DeleteNonEmpty409(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("full", VisPublic, "")
		id := e.article(cat, "in-full", VisPublic, "draft", "In", "x")
		r := e.do("DELETE", "/api/v1/admin/kb/categories/"+cat, "supervisor", nil)
		e.want(r, 409)
		if !strings.Contains(r.Body.String(), "kb-category-not-empty") {
			t.Fatalf("409 must name kb-category-not-empty: %s", r.Body.String())
		}
		parent := e.category("has-child", VisPublic, "")
		e.category("the-child", VisPublic, parent)
		e.want(e.do("DELETE", "/api/v1/admin/kb/categories/"+parent, "supervisor", nil), 409)
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+id, "admin", nil), 204)
		e.want(e.do("DELETE", "/api/v1/admin/kb/categories/"+cat, "supervisor", nil), 204)
		e.want(e.do("DELETE", "/api/v1/admin/kb/categories/"+cat, "supervisor", nil), 404)
	})
}

func TestKBCategory_AgentForbidden(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("guarded", VisPublic, "")
		body := map[string]any{"slug": "new-cat", "title": map[string]string{"en": "N"}, "visibility": "public"}
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "agent", body), 403)
		e.guard("GET", "/api/v1/admin/kb/categories", "agent", nil, 403)
		e.want(e.do("DELETE", "/api/v1/admin/kb/categories/"+cat, "agent", nil), 403)
		// Customers never learn the admin area exists (I-1).
		e.guard("POST", "/api/v1/admin/kb/categories", "customer", body, 404)
		e.guard("GET", "/api/v1/admin/kb/categories", "customer", nil, 404)
		// Supervisor and admin manage categories.
		e.want(e.do("POST", "/api/v1/admin/kb/categories", "supervisor", body), 201)
		r := e.do("GET", "/api/v1/admin/kb/categories", "admin", nil)
		e.want(r, 200)
		var list []categoryDTO
		r.json(t, &list)
		if len(list) != 2 {
			t.Fatalf("admin list %d", len(list))
		}
	})
}

// ---- KB-003: lifecycle ----

func TestKBArticle_AgentCanDraftNotPublish(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("help", VisPublic, "")
		r := e.do("POST", "/api/v1/agent/kb/articles", "agent", map[string]any{"category_id": cat, "locale": "en", "title": "How to reset", "body_md": "Steps"})
		e.want(r, 201)
		var a articleDTO
		r.json(t, &a)
		if a.Status != "draft" || a.Slug != "how-to-reset" || a.Author == nil || a.Author.ID != e.p("agent").UserID {
			t.Fatalf("draft %+v", a)
		}
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "agent", map[string]any{"slug": "reset-howto"}), 200)
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+a.ID+"/translations/it", "agent", map[string]any{"title": "Come reimpostare", "body_md": "Passi"}), 200)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "agent", map[string]any{"status": "published"}), 403)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "agent", map[string]any{"status": "archived"}), 403)
		// Agents cannot hard-delete either (admin only).
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+a.ID, "agent", nil), 403)
		// Once published, agents can no longer edit it.
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "supervisor", map[string]any{"status": "published"}), 200)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "agent", map[string]any{"slug": "another"}), 403)
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+a.ID+"/translations/en", "agent", map[string]any{"title": "x", "body_md": "y"}), 403)
		// Customers never reach the editor (I-1).
		e.guard("GET", "/api/v1/agent/kb/articles", "customer", nil, 404)
	})
}

func TestKBArticle_SupervisorPublishes(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("pub", VisPublic, "")
		id := e.article(cat, "to-publish", VisPublic, "draft", "To publish", "Body")
		r := e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "published"})
		e.want(r, 200)
		var a articleDTO
		r.json(t, &a)
		if a.Status != "published" || a.PublishedAt == nil {
			t.Fatalf("published %+v", a)
		}
		first := *a.PublishedAt
		// Unpublish + republish keeps the first published_at.
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "draft"}), 200)
		r = e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "admin", map[string]any{"status": "published"})
		r.json(t, &a)
		if !a.PublishedAt.Equal(first) {
			t.Fatalf("published_at changed: %v → %v", first, a.PublishedAt)
		}
		// Every change is audited.
		var n int
		if err := e.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE target_id = ? AND action LIKE 'kb.%'`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < 4 {
			t.Fatalf("audit rows %d, want ≥ 4 (create, publish, unpublish, publish)", n)
		}
		// Editor responses carry translations and staff counters.
		r = e.do("GET", "/api/v1/agent/kb/articles/"+id, "agent", nil)
		e.want(r, 200)
		r.json(t, &a)
		if len(a.Translations) != 1 || a.Views == nil {
			t.Fatalf("editor view %+v", a)
		}
	})
}

func TestKBArticle_ArchivedReturns410(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("old", VisPublic, "")
		id := e.article(cat, "old-news", VisPublic, "published", "Old news", "Body")
		e.want(e.do("GET", "/api/v1/kb/articles/old-news", "", nil), 200)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "archived"}), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/old-news", "", nil), 410)
		e.want(e.do("GET", "/api/v1/kb/articles/old-news", "customer", nil), 410)
		// Archived = not listed and not searchable.
		var page pageDTO
		r := e.do("GET", "/api/v1/kb/articles", "", nil)
		r.json(t, &page)
		if len(page.Items) != 0 {
			t.Fatalf("archived article listed: %+v", page.Items)
		}
		r = e.do("GET", "/api/v1/kb/articles?q=old", "", nil)
		r.json(t, &page)
		if len(page.Items) != 0 {
			t.Fatalf("archived article found by search")
		}
		// An internal archived article stays 404 for outsiders (no existence leak).
		in := e.category("in", VisInternal, "")
		iid := e.article(in, "secret-old", VisInternal, "published", "Secret", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+iid, "supervisor", map[string]any{"status": "archived"}), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/secret-old", "", nil), 404)
	})
}

func TestKBArticle_DeletePublishedRefused(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("del", VisPublic, "")
		pub := e.article(cat, "live", VisPublic, "published", "Live", "x")
		r := e.do("DELETE", "/api/v1/agent/kb/articles/"+pub, "admin", nil)
		e.want(r, 403) // spec: 403 (no 409 on this operation)
		if !strings.Contains(r.Body.String(), "kb-article-not-draft") {
			t.Fatalf("refusal must say why: %s", r.Body.String())
		}
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+pub, "supervisor", nil), 403)
		draft := e.article(cat, "scratch", VisPublic, "draft", "Scratch", "x")
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+draft, "admin", nil), 204)
		e.want(e.do("GET", "/api/v1/agent/kb/articles/"+draft, "admin", nil), 404)
	})
}

// ---- KB-004: translations ----

func TestKBLocale_FallbackToDefault(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("loc", VisPublic, "")
		id := e.article(cat, "multi", VisPublic, "published", "English title", "English body")
		e.translate(id, "it", "Titolo italiano", "Corpo")
		var a articleDTO
		r := e.do("GET", "/api/v1/kb/articles/multi?locale=it", "", nil)
		e.want(r, 200)
		r.json(t, &a)
		if a.Locale != "it" || a.Title != "Titolo italiano" || a.Fallback {
			t.Fatalf("it: %+v", a)
		}
		r = e.do("GET", "/api/v1/kb/articles/multi?locale=de", "", nil)
		r.json(t, &a)
		if a.Locale != "en" || a.Title != "English title" {
			t.Fatalf("de must fall back to default en: %+v", a)
		}
		if strings.Join(a.AvailableLocales, ",") != "en,it" {
			t.Fatalf("available %v", a.AvailableLocales)
		}
		// With default = it, a missing locale falls back to Italian.
		e.setSupport(func(s *settingsSupport) { s.DefaultLocale = "it" })
		r = e.do("GET", "/api/v1/kb/articles/multi?locale=fr", "", nil)
		r.json(t, &a)
		if a.Locale != "it" {
			t.Fatalf("fallback to instance default it: %+v", a)
		}
		// The default-locale translation cannot be deleted.
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+id+"/translations/it", "supervisor", nil), 409)
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+id+"/translations/en", "supervisor", nil), 204)
		e.want(e.do("GET", "/api/v1/agent/kb/articles/"+id+"/translations/en", "supervisor", nil), 404)
		e.want(e.do("GET", "/api/v1/agent/kb/articles/"+id+"/translations/it", "agent", nil), 200)
	})
}

func TestKBLocale_FallbackFlagSet(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("flag", VisPublic, "")
		e.article(cat, "only-en", VisPublic, "published", "Only English", "x")
		var a articleDTO
		r := e.do("GET", "/api/v1/kb/articles/only-en?locale=es", "", nil)
		r.json(t, &a)
		if !a.Fallback || a.Locale != "en" {
			t.Fatalf("fallback flag: %+v", a)
		}
		r = e.do("GET", "/api/v1/kb/articles/only-en?locale=en", "", nil)
		r.json(t, &a)
		if a.Fallback {
			t.Fatalf("no fallback expected: %+v", a)
		}
		// Any existing translation is used when neither requested nor default exist.
		id := e.article(cat, "only-it", VisPublic, "draft", "Draft", "x")
		e.translate(id, "it", "Solo italiano", "y")
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+id+"/translations/en", "agent", nil), 409) // default locale
		e.setSupport(func(s *settingsSupport) { s.DefaultLocale = "de" })
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+id+"/translations/en", "agent", nil), 204)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "published"}), 200)
		r = e.do("GET", "/api/v1/kb/articles/only-it?locale=fr", "", nil)
		r.json(t, &a)
		if a.Locale != "it" || !a.Fallback {
			t.Fatalf("any-translation fallback: %+v", a)
		}
	})
}

func TestKBLocale_UnknownLocale422(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("unk", VisPublic, "")
		id := e.article(cat, "unk-1", VisPublic, "draft", "T", "x")
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+id+"/translations/xx", "agent", map[string]any{"title": "x", "body_md": "y"}), 422)
		// A locale disabled in settings is refused too.
		e.setSupport(func(s *settingsSupport) { s.Locales = []string{"en", "it"} })
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+id+"/translations/de", "agent", map[string]any{"title": "x", "body_md": "y"}), 422)
		e.want(e.do("POST", "/api/v1/agent/kb/articles", "agent", map[string]any{"category_id": cat, "locale": "fr", "title": "T"}), 422)
		// Body and title limits.
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+id+"/translations/it", "agent", map[string]any{"title": "", "body_md": "y"}), 422)
		e.want(e.do("PUT", "/api/v1/agent/kb/articles/"+id+"/translations/it", "agent", map[string]any{"title": "t", "body_md": strings.Repeat("a", maxBodyMD+1)}), 422)
	})
}

func TestKBPublish_NoTranslation422(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("empty", VisPublic, "")
		a, err := e.m.CreateArticle(context.Background(), e.p("agent"), ArticleInput{CategoryID: cat, Locale: "it", Title: "Solo", BodyMD: "x"})
		if err != nil {
			t.Fatal(err)
		}
		e.want(e.do("DELETE", "/api/v1/agent/kb/articles/"+a.ID+"/translations/it", "agent", nil), 204)
		r := e.do("PATCH", "/api/v1/agent/kb/articles/"+a.ID, "supervisor", map[string]any{"status": "published"})
		e.want(r, 422)
		if !strings.Contains(r.Body.String(), "kb_translation_required") {
			t.Fatalf("want kb_translation_required: %s", r.Body.String())
		}
	})
}

// ---- KB-005: slugs ----

func TestKBSlug_OldSlugRedirects301(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("slugs", VisPublic, "")
		id := e.article(cat, "first-name", VisPublic, "published", "Article", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"slug": "second-name"}), 200)
		r := e.do("GET", "/api/v1/kb/articles/first-name?locale=en", "", nil)
		e.want(r, 301)
		if loc := r.Header().Get("Location"); loc != "/api/v1/kb/articles/second-name?locale=en" {
			t.Fatalf("Location %q", loc)
		}
		var body map[string]string
		r.json(t, &body)
		if body["redirect"] != "/kb/articles/second-name" {
			t.Fatalf("redirect body %v", body)
		}
		e.want(e.do("GET", "/api/v1/kb/articles/second-name", "", nil), 200)
		// A redirect never discloses an article the reader cannot see.
		in := e.category("hidden", VisInternal, "")
		hid := e.article(in, "hidden-a", VisInternal, "published", "Hidden", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+hid, "supervisor", map[string]any{"slug": "hidden-b"}), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/hidden-a", "", nil), 404)
		e.want(e.do("GET", "/api/v1/kb/articles/hidden-a", "agent", nil), 301)
	})
}

func TestKBSlug_ChainResolvesInOneHop(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("chain", VisPublic, "")
		id := e.article(cat, "slug-a", VisPublic, "published", "Chain", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"slug": "slug-b"}), 200)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"slug": "slug-c"}), 200)
		for _, old := range []string{"slug-a", "slug-b"} {
			r := e.do("GET", "/api/v1/kb/articles/"+old, "", nil)
			e.want(r, 301)
			if loc := r.Header().Get("Location"); loc != "/api/v1/kb/articles/slug-c" {
				t.Fatalf("%s → %q, want one hop to slug-c", old, loc)
			}
		}
		// Renaming back to an own old slug is allowed and drops that redirect.
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"slug": "slug-a"}), 200)
		e.want(e.do("GET", "/api/v1/kb/articles/slug-a", "", nil), 200)
		r := e.do("GET", "/api/v1/kb/articles/slug-c", "", nil)
		e.want(r, 301)
	})
}

func TestKBSlug_Collision409(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		cat := e.category("coll", VisPublic, "")
		a := e.article(cat, "taken", VisPublic, "draft", "A", "x")
		b := e.article(cat, "other", VisPublic, "draft", "B", "x")
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+b, "agent", map[string]any{"slug": "taken"}), 409)
		e.want(e.do("POST", "/api/v1/agent/kb/articles", "agent", map[string]any{"category_id": cat, "slug": "taken", "locale": "en", "title": "C"}), 409)
		// A fresh old slug of another article stays reserved for 90 days…
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+a, "agent", map[string]any{"slug": "renamed"}), 200)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+b, "agent", map[string]any{"slug": "taken"}), 409)
		// …and is released afterwards.
		if err := e.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE kb_slug_history SET created_at = ? WHERE old_slug = 'taken'`, store.Now().AddDate(0, 0, -91))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+b, "agent", map[string]any{"slug": "taken"}), 200)
		// Derived slugs get a numeric suffix instead of colliding.
		c, err := e.m.CreateArticle(ctx, e.p("agent"), ArticleInput{CategoryID: cat, Locale: "en", Title: "Taken"})
		if err != nil || c.Slug != "taken-2" {
			t.Fatalf("derived slug %q, %v", c.Slug, err)
		}
	})
}

// ---- KB-013: authorisation matrix ----

// TestKB_PermissionMatrix has one row per KB cell of permissions_spec §3
// ("KB: write drafts / publish / categories") plus I-10 reads and §5 scopes.
func TestKB_PermissionMatrix(t *testing.T) {
	type row struct {
		who                                       string
		read, draft, editPublished, publish, cats bool
		archive, deleteDraft                      bool
		readAud                                   audience
	}
	envP := testPrincipals()
	rows := []row{
		{"anonymous", true, false, false, false, false, false, false, audAnonymous},
		{"customer", true, false, false, false, false, false, false, audCustomer},
		{"agent", true, true, false, false, false, false, false, audStaff},
		{"supervisor", true, true, true, true, true, true, false, audStaff},
		{"admin", true, true, true, true, true, true, true, audStaff},
		{"agent-pat-read", true, false, false, false, false, false, false, audStaff},      // tickets:read ⇒ kb:read
		{"supervisor-pat-read", true, false, false, false, false, false, false, audStaff}, // kb:read only
		{"agent-pat-write", false, true, false, false, false, false, false, audAnonymous}, // kb:write without kb:read
		{"customer-pat-nokb", true, false, false, false, false, false, false, audAnonymous},
	}
	for _, r := range rows {
		p := envP[r.who]
		check := func(name string, got, want bool) {
			if got != want {
				t.Errorf("%s %s: got %v want %v", r.who, name, got, want)
			}
		}
		check("read", Can(p, ActionRead, ""), r.read)
		check("draft", Can(p, ActionDraft, "draft"), r.draft)
		check("edit published", Can(p, ActionDraft, "published"), r.editPublished)
		check("publish", Can(p, ActionPublish, "draft"), r.publish)
		check("archive", Can(p, ActionArchive, "published"), r.archive)
		check("categories", Can(p, ActionCategoryManage, ""), r.cats)
		check("delete draft", Can(p, ActionDelete, "draft"), r.deleteDraft)
		if got := audienceOf(p); got != r.readAud {
			t.Errorf("%s audience %v want %v", r.who, got, r.readAud)
		}
	}
}

func TestKB_PATWithoutWriteScopeCannotDraft(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("pat", VisPublic, "")
		body := map[string]any{"category_id": cat, "locale": "en", "title": "Via token"}
		e.want(e.do("POST", "/api/v1/agent/kb/articles", "agent-pat-read", body), 403)
		e.want(e.do("POST", "/api/v1/agent/kb/articles", "supervisor-pat-read", body), 403)
		e.want(e.do("POST", "/api/v1/agent/kb/articles", "agent-pat-write", body), 201)
		// Read-only token can still read the editor list.
		e.want(e.do("GET", "/api/v1/agent/kb/articles", "agent-pat-read", nil), 200)
	})
}

// TestKBArticle_AllowedActions: KbArticle.allowed_actions follows kb.Can per
// role and status (KB-013), so the editor never decides permissions; readers
// outside staff always get [].
func TestKBArticle_AllowedActions(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		cat := e.category("acts", VisPublic, "")
		draft := e.article(cat, "acts-draft", VisPublic, "draft", "Draft", "x")
		pub := e.article(cat, "acts-pub", VisPublic, "published", "Pub", "x")
		arch := e.article(cat, "acts-arch", VisPublic, "archived", "Arch", "x")
		actions := func(who, id string) []string {
			var a struct {
				AllowedActions []string `json:"allowed_actions"`
			}
			r := e.do("GET", "/api/v1/agent/kb/articles/"+id, who, nil)
			e.want(r, 200)
			r.json(t, &a)
			return a.AllowedActions
		}
		cases := []struct {
			who, id string
			want    []string
		}{
			{"agent", draft, []string{"edit"}},
			{"agent", pub, []string{}},
			{"supervisor", draft, []string{"edit", "publish", "archive"}},
			{"supervisor", pub, []string{"edit", "unpublish", "archive"}},
			{"supervisor", arch, []string{"edit", "publish"}},
			{"admin", draft, []string{"edit", "publish", "archive", "delete"}},
			{"supervisor-pat-read", draft, []string{}},
		}
		for _, c := range cases {
			if got := actions(c.who, c.id); !slices.Equal(got, c.want) {
				t.Errorf("%s on %s: %v, want %v", c.who, c.id, got, c.want)
			}
		}
		var a map[string]any
		r := e.do("GET", "/api/v1/kb/articles/acts-pub", "customer", nil)
		e.want(r, 200)
		r.json(t, &a)
		if acts, ok := a["allowed_actions"].([]any); !ok || len(acts) != 0 {
			t.Fatalf("customer allowed_actions %v", a["allowed_actions"])
		}
	})
}
