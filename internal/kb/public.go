// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// requestLocale: ?locale= when valid, else the reader's locale, else "" (the
// default applies downstream).
func requestLocale(r *http.Request, p *domain.Principal) string {
	if l := r.URL.Query().Get("locale"); isLocale(l) {
		return l
	}
	if p != nil && isLocale(p.Locale) {
		return p.Locale
	}
	return ""
}

// ---- caching (KB-009) ----

// listStamp changes whenever any article or category changes (updated_at
// max + row counts, so deletes count too). Counter flushes do not touch it.
func (m *Module) listStamp(ctx context.Context) (string, error) {
	var a, c sql.NullString
	var na, nc int64
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT (SELECT MAX(updated_at) FROM kb_articles), (SELECT COUNT(*) FROM kb_articles),
		(SELECT MAX(updated_at) FROM kb_categories), (SELECT COUNT(*) FROM kb_categories)`).Scan(&a, &na, &c, &nc)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%s|%d", a.String, na, c.String, nc), nil
}

func weakETag(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return `W/"` + hex.EncodeToString(h[:12]) + `"`
}

// cacheHeaders sets ETag + Cache-Control and reports whether the request's
// If-None-Match matches (the caller then answers 304).
func cacheHeaders(w http.ResponseWriter, r *http.Request, aud audience, etag string) bool {
	h := w.Header()
	h.Set("ETag", etag)
	h.Add("Vary", "Authorization, Cookie")
	if aud == audAnonymous && server.PrincipalFrom(r.Context()) == nil {
		h.Set("Cache-Control", "public, max-age=300")
	} else {
		h.Set("Cache-Control", "private, no-cache")
	}
	for _, v := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		v = strings.TrimSpace(v)
		if v == "*" || v == etag || strings.TrimPrefix(v, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

func notModified(w http.ResponseWriter) { w.WriteHeader(http.StatusNotModified) }

// writeGone answers 410 for an archived article the reader could otherwise see.
func writeGone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusGone)
	_ = json.NewEncoder(w).Encode(problems.Problem{Type: problems.BaseURI + "not-found", Title: "Gone", Status: http.StatusGone,
		Detail: "the article was archived", Instance: r.URL.Path})
}

// ---- handlers ----

func (m *Module) handlePublicCategories(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	aud := audienceOf(p)
	locale := requestLocale(r, p)
	stamp, err := m.listStamp(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if cacheHeaders(w, r, aud, weakETag("categories", stamp, aud.String(), locale)) {
		notModified(w)
		return
	}
	out, err := m.listCategories(r.Context(), aud, locale, false)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handlePublicList(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	aud := audienceOf(p)
	q := r.URL.Query()
	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	locale := requestLocale(r, p)
	stamp, err := m.listStamp(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if cacheHeaders(w, r, aud, weakETag("list", stamp, aud.String(), locale, m.defaultLocale(r.Context()), r.URL.RawQuery)) {
		notModified(w)
		return
	}
	page, err := m.list(r.Context(), listQuery{
		Aud: aud, Statuses: []string{"published"}, Q: q.Get("q"), Category: q.Get("category"),
		Locale: locale, Limit: limit, Offset: decodeCursor(q.Get("cursor")), Order: "published",
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// findPublic resolves a slug (current, else historical) for the reader
// through the visibility predicate. redirect is set for historical slugs.
func (m *Module) findPublic(ctx context.Context, aud audience, slug string) (a *article, redirect bool, err error) {
	db := m.d.DB.Read()
	pred := visPredicate(aud, []string{"published", "archived"})
	a, err = scanArticle(db.QueryRowContext(ctx, `SELECT `+articleCols+articleFrom+`WHERE a.slug = ? AND `+pred, slug))
	if err == nil || !notFoundIs(err) {
		return a, false, err
	}
	a, err = scanArticle(db.QueryRowContext(ctx, `SELECT `+articleCols+articleFrom+
		`JOIN kb_slug_history h ON h.article_id = a.id WHERE h.old_slug = ? AND `+pred, slug))
	return a, err == nil, err
}

func (m *Module) handlePublicArticle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := server.PrincipalFrom(ctx)
	aud := audienceOf(p)
	a, redirect, err := m.findPublic(ctx, aud, param(r, "slug"))
	if err != nil {
		problems.Write(w, r, err) // invisible or missing → 404, never 403
		return
	}
	if a.Status == "archived" {
		writeGone(w, r)
		return
	}
	if redirect {
		loc := "/kb/articles/" + url.PathEscape(a.Slug)
		target := "/api/v1" + loc
		if rq := r.URL.RawQuery; rq != "" {
			target += "?" + rq
		}
		w.Header().Set("Location", target)
		w.Header().Set("Cache-Control", "no-cache")
		writeJSON(w, http.StatusMovedPermanently, map[string]string{"redirect": loc})
		return
	}
	trs, err := loadTranslations(ctx, m.d.DB.Read(), a.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	locale := requestLocale(r, p)
	staff := aud == audStaff
	dto := m.articleDTO(ctx, p, a, trs, locale, staff)
	tag := []string{"article", a.ID, a.UpdatedAt.Format(time.RFC3339Nano), aud.String(), locale, dto.Locale}
	if staff {
		tag = append(tag, fmt.Sprint(*dto.Views, *dto.HelpfulYes, *dto.HelpfulNo), strings.Join(dto.AllowedActions, ","))
	}
	if cacheHeaders(w, r, aud, weakETag(tag...)) {
		notModified(w)
		return
	}
	if !staff {
		m.counters.View(a.ID) // KB-012: staff previews do not count
	}
	writeJSON(w, http.StatusOK, dto)
}

func (m *Module) handleFeedback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	aud := audienceOf(server.PrincipalFrom(ctx))
	id := param(r, "id")
	if !validID(id) {
		problems.Write(w, r, problems.NotFound())
		return
	}
	var n int
	if err := m.d.DB.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id WHERE a.id = ? AND `+
		visPredicate(aud, []string{"published"}), id).Scan(&n); err != nil {
		problems.Write(w, r, err)
		return
	}
	if n == 0 {
		problems.Write(w, r, problems.NotFound())
		return
	}
	var in struct {
		Helpful *bool `json:"helpful"`
	}
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.Helpful == nil {
		problems.Write(w, r, fieldErr("helpful", "required", "helpful is required"))
		return
	}
	// One vote per article per IP per 24 h (best effort, bounded LRU).
	if m.votes.Add(server.ClientIP(r)+"|"+id, struct{}{}, 0, voteWindow) {
		m.counters.Vote(id, *in.Helpful)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleSuggest(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	q := r.URL.Query().Get("q")
	if len(q) > 200 {
		problems.Write(w, r, fieldErr("q", "max_length", "at most 200 characters"))
		return
	}
	items, err := m.Suggest(r.Context(), p, q, requestLocale(r, p), true)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=30")
	writeJSON(w, http.StatusOK, items)
}

// ---- sitemap (KB-009) ----

type urlset struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

func (m *Module) handleSitemap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s := m.support(ctx)
	if !s.KBEnabled || !s.KBSitemapEnabled {
		problems.Write(w, r, problems.NotFound())
		return
	}
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT a.slug, a.updated_at FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id WHERE `+
		visPredicate(audAnonymous, []string{"published"})+` ORDER BY a.updated_at DESC LIMIT 50000`)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	base := ""
	if m.d.Cfg != nil {
		base = strings.TrimRight(m.d.Cfg.PortalURL, "/")
		if base == "" {
			base = strings.TrimRight(m.d.Cfg.PublicURL, "/")
		}
	}
	set := urlset{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for rows.Next() {
		var slug string
		var t time.Time
		if err := rows.Scan(&slug, &t); err != nil {
			problems.Write(w, r, err)
			return
		}
		set.URLs = append(set.URLs, sitemapURL{Loc: base + "/kb/" + url.PathEscape(slug), LastMod: t.UTC().Format("2006-01-02")})
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(set)
}
