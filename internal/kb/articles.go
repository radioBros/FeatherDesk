// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ArticleInput is KbArticleCreate.
type ArticleInput struct {
	CategoryID string `json:"category_id"`
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
	Locale     string `json:"locale"`
	Title      string `json:"title"`
	BodyMD     string `json:"body_md"`
}

// ArticlePatch is KbArticlePatch.
type ArticlePatch struct {
	CategoryID *string `json:"category_id"`
	Slug       *string `json:"slug"`
	Visibility *string `json:"visibility"`
	Status     *string `json:"status"`
}

// TranslationInput is KbTranslationPut.
type TranslationInput struct {
	Title  *string `json:"title"`
	BodyMD *string `json:"body_md"`
}

func validateText(title, body string) error {
	var fields []problems.FieldError
	if n := utf8.RuneCountInString(strings.TrimSpace(title)); n == 0 {
		fields = append(fields, problems.Field("title", "required", "title is required"))
	} else if utf8.RuneCountInString(title) > maxTitle {
		fields = append(fields, problems.Field("title", "max_length", "at most 200 characters"))
	}
	if len(body) > maxBodyMD {
		fields = append(fields, problems.Field("body_md", "max_length", "at most 256 KB"))
	}
	if len(fields) > 0 {
		return problems.Validation(fields...)
	}
	return nil
}

// fold maps common Latin letters with diacritics to ASCII for slugs.
var fold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ä", "ae", "ã", "a", "å", "a", "æ", "ae", "ç", "c",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ì", "i", "í", "i", "î", "i", "ï", "i",
	"ñ", "n", "ò", "o", "ó", "o", "ô", "o", "ö", "oe", "õ", "o", "ø", "o", "œ", "oe",
	"ù", "u", "ú", "u", "û", "u", "ü", "ue", "ý", "y", "ÿ", "y", "ß", "ss",
)

// slugify derives a slug from a title: ASCII-folded, lowercase, dashes.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range fold.Replace(strings.ToLower(s)) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 100 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "article"
	}
	return out
}

// slugFree checks that slug can be used by articleID (KB-005): no other
// article has it, and no other article redirected from it within 90 days.
// A stale (≥ 90 d) or own history row is released.
func (m *Module) slugFree(ctx context.Context, tx *store.Tx, slug, articleID string) error {
	var other string
	err := tx.QueryRowContext(ctx, `SELECT id FROM kb_articles WHERE slug = ?`, slug).Scan(&other)
	if err == nil && other != articleID {
		return problems.Conflict("kb-slug-taken: another article uses this slug")
	}
	if err != nil && !notFoundIs(store.NotFound(err)) {
		return err
	}
	var owner string
	var created time.Time
	err = tx.QueryRowContext(ctx, `SELECT article_id, created_at FROM kb_slug_history WHERE old_slug = ?`, slug).Scan(&owner, &created)
	if err != nil {
		if notFoundIs(store.NotFound(err)) {
			return nil
		}
		return err
	}
	if owner != articleID && m.now().Sub(created) < slugReuseAfter {
		return problems.Conflict("kb-slug-taken: the slug redirects to another article (released after 90 days)")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM kb_slug_history WHERE old_slug = ?`, slug)
	return err
}

// uniqueSlug appends -2, -3… to a derived slug until it is free.
func (m *Module) uniqueSlug(ctx context.Context, tx *store.Tx, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		s := base
		if i > 1 {
			suffix := "-" + strconv.Itoa(i)
			s = strings.TrimRight(base[:min(len(base), 120-len(suffix))], "-") + suffix
		}
		err := m.slugFree(ctx, tx, s, "")
		if err == nil {
			return s, nil
		}
		if _, ok := err.(*problems.Error); !ok {
			return "", err
		}
	}
	return "", problems.Conflict("kb-slug-taken: no free slug")
}

func (m *Module) loadCategoryVis(ctx context.Context, q store.Querier, id string) (string, error) {
	if !validID(id) {
		return "", fieldErr("category_id", "not_found", "category does not exist")
	}
	var v string
	err := q.QueryRowContext(ctx, `SELECT visibility FROM kb_categories WHERE id = ?`, id).Scan(&v)
	if notFoundIs(store.NotFound(err)) {
		return "", fieldErr("category_id", "not_found", "category does not exist")
	}
	return v, err
}

// CreateArticle stores a new draft with its first translation (KB-003).
func (m *Module) CreateArticle(ctx context.Context, p *domain.Principal, in ArticleInput) (articleDTO, error) {
	if err := authorize(p, ActionDraft, ""); err != nil {
		return articleDTO{}, err
	}
	if !m.enabledLocale(ctx, in.Locale) {
		return articleDTO{}, fieldErr("locale", "locale", "unknown or disabled locale")
	}
	if err := validateText(in.Title, in.BodyMD); err != nil {
		return articleDTO{}, err
	}
	if in.Slug != "" && !slugRe.MatchString(in.Slug) {
		return articleDTO{}, fieldErr("slug", "slug", "lowercase letters, digits and dashes")
	}
	if in.Visibility != "" && visRank(in.Visibility) < 0 {
		return articleDTO{}, fieldErr("visibility", "enum", "public, customers or internal")
	}
	id := store.NewID()
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		catVis, err := m.loadCategoryVis(ctx, tx, in.CategoryID)
		if err != nil {
			return err
		}
		vis := in.Visibility
		if vis == "" {
			vis = catVis
		}
		if visRank(vis) < visRank(catVis) {
			return fieldErr("visibility", "kb_visibility_wider", "an article cannot be more visible than its category (M-10)")
		}
		slug := in.Slug
		if slug == "" {
			if slug, err = m.uniqueSlug(ctx, tx, slugify(in.Title)); err != nil {
				return err
			}
		} else if err := m.slugFree(ctx, tx, slug, id); err != nil {
			return err
		}
		now := store.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, status, visibility, author_id, created_at, updated_at) VALUES (?, ?, ?, 'draft', ?, ?, ?, ?)`,
			id, in.CategoryID, slug, vis, store.Nullable(p.UserID), now, now); err != nil {
			if store.IsUniqueViolation(err) {
				return problems.Conflict("kb-slug-taken: another article uses this slug")
			}
			if isM10(err) {
				return fieldErr("visibility", "kb_visibility_wider", err.Error())
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO kb_article_translations (article_id, locale, title, body_md, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, in.Locale, in.Title, in.BodyMD, store.Nullable(p.UserID), now); err != nil {
			return err
		}
		if err := linkAssets(ctx, tx, id, in.BodyMD); err != nil {
			return err
		}
		if err := reindex(ctx, tx, id); err != nil {
			return err
		}
		tx.AfterCommit(func() {
			m.committed(updateEvent{Action: "article.created", ArticleID: id, Status: "draft", ActorID: p.UserID})
		})
		return audit(ctx, tx, p, "kb.article.create", "kb_article", id, map[string]any{"slug": slug, "locale": in.Locale})
	})
	if err != nil {
		return articleDTO{}, err
	}
	return m.EditorArticle(ctx, p, id, in.Locale)
}

// PatchArticle changes category, slug, visibility or status (KB-003/005).
func (m *Module) PatchArticle(ctx context.Context, p *domain.Principal, id string, in ArticlePatch) (articleDTO, error) {
	if !p.IsStaff() {
		return articleDTO{}, problems.NotFound()
	}
	if in.CategoryID == nil && in.Slug == nil && in.Visibility == nil && in.Status == nil {
		return articleDTO{}, problems.Validation(problems.Field("", "min_properties", "nothing to change"))
	}
	if in.Slug != nil && !slugRe.MatchString(*in.Slug) {
		return articleDTO{}, fieldErr("slug", "slug", "lowercase letters, digits and dashes")
	}
	if in.Visibility != nil && visRank(*in.Visibility) < 0 {
		return articleDTO{}, fieldErr("visibility", "enum", "public, customers or internal")
	}
	if in.Status != nil && !contains([]string{"draft", "published", "archived"}, *in.Status) {
		return articleDTO{}, fieldErr("status", "enum", "draft, published or archived")
	}
	var evStatus string
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		a, err := loadArticle(ctx, tx, id)
		if err != nil {
			return err
		}
		statusChange := in.Status != nil && *in.Status != a.Status
		contentChange := in.CategoryID != nil || in.Slug != nil || in.Visibility != nil
		if contentChange {
			if err := authorize(p, ActionDraft, a.Status); err != nil {
				return err
			}
		}
		if statusChange {
			if err := authorize(p, statusAction(*in.Status), a.Status); err != nil {
				return err
			}
		}
		if !contentChange && !statusChange {
			if err := authorize(p, ActionDraft, a.Status); err != nil {
				return err
			}
		}
		catID, vis, slug, status := a.CategoryID, a.Visibility, a.Slug, a.Status
		if in.CategoryID != nil {
			catID = *in.CategoryID
		}
		if in.Visibility != nil {
			vis = *in.Visibility
		}
		if in.Slug != nil {
			slug = *in.Slug
		}
		if in.Status != nil {
			status = *in.Status
		}
		catVis, err := m.loadCategoryVis(ctx, tx, catID)
		if err != nil {
			return err
		}
		if visRank(vis) < visRank(catVis) {
			return fieldErr("visibility", "kb_visibility_wider", "an article cannot be more visible than its category (M-10)")
		}
		if slug != a.Slug {
			if err := m.slugFree(ctx, tx, slug, id); err != nil {
				return err
			}
			// One-hop redirects: every older history row already points at
			// this article id, so it resolves straight to the new slug.
			if _, err := tx.ExecContext(ctx, `INSERT INTO kb_slug_history (old_slug, article_id, created_at) VALUES (?, ?, ?)`, a.Slug, id, store.Now()); err != nil {
				return err
			}
		}
		publishedAt := any(nil)
		if a.PublishedAt != nil {
			publishedAt = *a.PublishedAt
		}
		if statusChange && status == "published" {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_article_translations WHERE article_id = ? AND title <> ''`, id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fieldErr("status", "kb_translation_required", "at least one translation is required to publish")
			}
			if a.PublishedAt == nil {
				publishedAt = store.Now()
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE kb_articles SET category_id = ?, slug = ?, visibility = ?, status = ?, published_at = ?, updated_at = ? WHERE id = ?`,
			catID, slug, vis, status, publishedAt, store.Now(), id)
		switch {
		case store.IsUniqueViolation(err):
			return problems.Conflict("kb-slug-taken: another article uses this slug")
		case isM10(err):
			return fieldErr("visibility", "kb_visibility_wider", err.Error())
		case err != nil:
			return err
		}
		if err := reindex(ctx, tx, id); err != nil {
			return err
		}
		evStatus = status
		action := "kb.article.update"
		if statusChange {
			action = map[string]string{"published": "kb.article.publish", "archived": "kb.article.archive", "draft": "kb.article.unpublish"}[status]
		}
		tx.AfterCommit(func() {
			m.committed(updateEvent{Action: strings.TrimPrefix(action, "kb."), ArticleID: id, Status: evStatus, ActorID: p.UserID})
		})
		return audit(ctx, tx, p, action, "kb_article", id, map[string]any{"from_status": a.Status, "status": status, "slug": slug, "visibility": vis, "category_id": catID})
	})
	if err != nil {
		return articleDTO{}, err
	}
	return m.EditorArticle(ctx, p, id, "")
}

// DeleteArticle hard-deletes a draft (admin only); published or archived
// articles are archived, never deleted (403, the spec lists no 409 here).
func (m *Module) DeleteArticle(ctx context.Context, p *domain.Principal, id string) error {
	if !p.IsStaff() {
		return problems.NotFound()
	}
	return m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		a, err := loadArticle(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authorize(p, ActionDelete, a.Status); err != nil {
			return err
		}
		if a.Status != "draft" {
			return problems.Forbidden("kb-article-not-draft: only drafts can be deleted; archive published articles")
		}
		if err := store.UnindexKB(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM kb_articles WHERE id = ?`, id); err != nil {
			return err
		}
		tx.AfterCommit(func() {
			m.committed(updateEvent{Action: "article.deleted", ArticleID: id, Status: "deleted", ActorID: p.UserID})
		})
		return audit(ctx, tx, p, "kb.article.delete", "kb_article", id, map[string]any{"slug": a.Slug})
	})
}

// PutTranslation creates or replaces one locale (KB-004).
func (m *Module) PutTranslation(ctx context.Context, p *domain.Principal, id, locale string, in TranslationInput) (translationDTO, error) {
	if !p.IsStaff() {
		return translationDTO{}, problems.NotFound()
	}
	if !m.enabledLocale(ctx, locale) {
		return translationDTO{}, fieldErr("locale", "locale", "unknown or disabled locale")
	}
	if in.Title == nil || in.BodyMD == nil {
		return translationDTO{}, problems.Validation(problems.Field("title", "required", ""), problems.Field("body_md", "required", ""))
	}
	if err := validateText(*in.Title, *in.BodyMD); err != nil {
		return translationDTO{}, err
	}
	var out translationDTO
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		a, err := loadArticle(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authorize(p, ActionDraft, a.Status); err != nil {
			return err
		}
		now := store.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO kb_article_translations (article_id, locale, title, body_md, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (article_id, locale) DO UPDATE SET title = excluded.title, body_md = excluded.body_md, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			id, locale, *in.Title, *in.BodyMD, store.Nullable(p.UserID), now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kb_articles SET updated_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
		if err := linkAssets(ctx, tx, id, *in.BodyMD); err != nil {
			return err
		}
		if err := reindex(ctx, tx, id); err != nil {
			return err
		}
		var name string
		_ = tx.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, p.UserID).Scan(&name)
		out = translationDTO{ArticleID: id, Locale: locale, Title: *in.Title, BodyMD: *in.BodyMD, UpdatedBy: ref(p.UserID, name), UpdatedAt: &now}
		tx.AfterCommit(func() {
			m.committed(updateEvent{Action: "translation.updated", ArticleID: id, Status: a.Status, Locale: locale, ActorID: p.UserID})
		})
		return audit(ctx, tx, p, "kb.translation.put", "kb_article", id, map[string]any{"locale": locale})
	})
	return out, err
}

// DeleteTranslation removes a locale; the default locale, and the last
// translation of a published article, are refused (409).
func (m *Module) DeleteTranslation(ctx context.Context, p *domain.Principal, id, locale string) error {
	if !p.IsStaff() {
		return problems.NotFound()
	}
	if !isLocale(locale) {
		return problems.NotFound()
	}
	def := m.defaultLocale(ctx)
	return m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		a, err := loadArticle(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authorize(p, ActionDraft, a.Status); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_article_translations WHERE article_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_article_translations WHERE article_id = ? AND locale = ?`, id, locale).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return problems.NotFound()
		}
		if locale == def {
			return problems.Conflict("kb-default-locale: the default-locale translation cannot be deleted")
		}
		if n <= 1 && a.Status != "draft" {
			return problems.Conflict("kb-last-translation: a published article needs at least one translation")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM kb_article_translations WHERE article_id = ? AND locale = ?`, id, locale); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kb_articles SET updated_at = ? WHERE id = ?`, store.Now(), id); err != nil {
			return err
		}
		if err := reindex(ctx, tx, id); err != nil {
			return err
		}
		tx.AfterCommit(func() {
			m.committed(updateEvent{Action: "translation.deleted", ArticleID: id, Status: a.Status, Locale: locale, ActorID: p.UserID})
		})
		return audit(ctx, tx, p, "kb.translation.delete", "kb_article", id, map[string]any{"locale": locale})
	})
}

// EditorArticle returns an article with every translation (staff).
func (m *Module) EditorArticle(ctx context.Context, p *domain.Principal, id, locale string) (articleDTO, error) {
	if !p.IsStaff() {
		return articleDTO{}, problems.NotFound()
	}
	db := m.d.DB.Read()
	a, err := loadArticle(ctx, db, id)
	if err != nil {
		return articleDTO{}, err
	}
	trs, err := loadTranslations(ctx, db, id)
	if err != nil {
		return articleDTO{}, err
	}
	dto := m.articleDTO(ctx, p, a, trs, locale, true)
	dto.Translations = make([]translationDTO, 0, len(trs))
	for _, l := range Locales {
		if t, ok := trs[l]; ok {
			ts := t.UpdatedAt
			dto.Translations = append(dto.Translations, translationDTO{ArticleID: id, Locale: l, Title: t.Title, BodyMD: t.BodyMD, UpdatedBy: ref(t.UpdatedBy, t.UpdaterNm), UpdatedAt: &ts})
		}
	}
	return dto, nil
}

// articleDTO renders an article in the best locale for the reader.
func (m *Module) articleDTO(ctx context.Context, p *domain.Principal, a *article, trs map[string]translation, locale string, staff bool) articleDTO {
	def := m.defaultLocale(ctx)
	l, fallback := pickLocale(func(l string) bool { _, ok := trs[l]; return ok }, locale, def)
	dto := articleDTO{
		ID: a.ID, CategoryID: a.CategoryID, Slug: a.Slug, Status: a.Status, Visibility: a.Visibility,
		AvailableLocales: availableLocales(trs), Author: ref(a.AuthorID, a.AuthorName), PublishedAt: a.PublishedAt,
		UpdatedAt: a.UpdatedAt, Fallback: fallback, Robots: "index", AllowedActions: []string{},
	}
	if staff {
		dto.AllowedActions = AllowedActions(p, a.Status)
	}
	if a.Visibility != VisPublic || a.CatVisibility != VisPublic || a.Status != "published" {
		dto.Robots = "noindex"
	}
	if l == "" {
		dto.Locale = def
		dto.Title = a.Slug
	} else {
		t := trs[l]
		dto.Locale, dto.Title, dto.BodyMD = l, t.Title, t.BodyMD
		dto.BodyHTML = m.render.Render(a.ID, l, t.UpdatedAt, t.BodyMD)
	}
	if staff {
		v, y, n := a.Views, a.Yes, a.No
		pv, py, pn := m.counters.Pending(a.ID)
		v, y, n = v+pv, y+py, n+pn
		dto.Views, dto.HelpfulYes, dto.HelpfulNo = &v, &y, &n
	}
	return dto
}

// ---- editor handlers ----

func (m *Module) handleEditorList(w http.ResponseWriter, r *http.Request) {
	if err := authorize(server.PrincipalFrom(r.Context()), ActionRead, ""); err != nil {
		problems.Write(w, r, err)
		return
	}
	q := r.URL.Query()
	statuses := []string{"draft", "published", "archived"}
	if s := q.Get("status"); s != "" {
		if !contains(statuses, s) {
			problems.Write(w, r, fieldErr("status", "enum", "draft, published or archived"))
			return
		}
		statuses = []string{s}
	}
	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	page, err := m.list(r.Context(), listQuery{
		Aud: audStaff, Statuses: statuses, Q: q.Get("q"), Category: q.Get("category_id"),
		Locale: q.Get("locale"), Limit: limit, Offset: decodeCursor(q.Get("cursor")), Order: "updated",
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseLimit(s string) (int, error) {
	if s == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 200 {
		return 0, fieldErr("limit", "range", "1 to 200")
	}
	return n, nil
}

func reqCtx(r *http.Request) (context.Context, *domain.Principal) {
	return withIP(r.Context(), server.ClientIP(r)), server.PrincipalFrom(r.Context())
}

func (m *Module) handleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in ArticleInput
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	ctx, p := reqCtx(r)
	out, err := m.CreateArticle(ctx, p, in)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (m *Module) handleEditorGet(w http.ResponseWriter, r *http.Request) {
	ctx, p := reqCtx(r)
	if err := authorize(p, ActionRead, ""); err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := m.EditorArticle(ctx, p, param(r, "id"), r.URL.Query().Get("locale"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handlePatchArticle(w http.ResponseWriter, r *http.Request) {
	var in ArticlePatch
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	ctx, p := reqCtx(r)
	out, err := m.PatchArticle(ctx, p, param(r, "id"), in)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	ctx, p := reqCtx(r)
	if err := m.DeleteArticle(ctx, p, param(r, "id")); err != nil {
		problems.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleGetTranslation(w http.ResponseWriter, r *http.Request) {
	ctx, p := reqCtx(r)
	if err := authorize(p, ActionRead, ""); err != nil {
		problems.Write(w, r, err)
		return
	}
	id, locale := param(r, "id"), param(r, "locale")
	db := m.d.DB.Read()
	if _, err := loadArticle(ctx, db, id); err != nil {
		problems.Write(w, r, err)
		return
	}
	trs, err := loadTranslations(ctx, db, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	t, ok := trs[locale]
	if !ok {
		problems.Write(w, r, problems.NotFound())
		return
	}
	ts := t.UpdatedAt
	writeJSON(w, http.StatusOK, translationDTO{ArticleID: id, Locale: locale, Title: t.Title, BodyMD: t.BodyMD, UpdatedBy: ref(t.UpdatedBy, t.UpdaterNm), UpdatedAt: &ts})
}

func (m *Module) handlePutTranslation(w http.ResponseWriter, r *http.Request) {
	var in TranslationInput
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	ctx, p := reqCtx(r)
	out, err := m.PutTranslation(ctx, p, param(r, "id"), param(r, "locale"), in)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleDeleteTranslation(w http.ResponseWriter, r *http.Request) {
	ctx, p := reqCtx(r)
	if err := m.DeleteTranslation(ctx, p, param(r, "id"), param(r, "locale")); err != nil {
		problems.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
