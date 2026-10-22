// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// visPredicate is THE I-10 filter (KB-007): every list, detail, search,
// suggest, sitemap and asset query goes through it. It expects
// `kb_articles a JOIN kb_categories c ON c.id = a.category_id`. The category
// join makes M-10 a second line of defence: an article never leaks through a
// hidden category even if the trigger were bypassed. Only constants are
// interpolated; user input is always bound.
func visPredicate(aud audience, statuses []string) string {
	vis := inList(aud.visibilities())
	st := inList(statuses)
	if aud != audStaff {
		st = "'published'" // drafts and archived are never readable outside staff
		if contains(statuses, "archived") {
			st = "'published', 'archived'" // detail lookups return 410 for archived
		}
	}
	return "a.status IN (" + st + ") AND a.visibility IN (" + vis + ") AND c.visibility IN (" + vis + ")"
}

func inList(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = "'" + strings.ReplaceAll(v, "'", "") + "'"
	}
	return strings.Join(q, ", ")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// listQuery describes one article listing or search.
type listQuery struct {
	Aud      audience
	Statuses []string
	Q        string
	Category string // id or slug
	Project  string // D-20: project id; keeps its categories and the global ones
	Locale   string
	Limit    int
	Offset   int
	Order    string // "published" (public) or "updated" (editor)
	// Sort is an explicit editor order (editorSorts); "" keeps Order, or
	// rank order when Q is set.
	Sort string
	// TitleFirst (suggest) ranks title matches first and falls back to the
	// full-text query when they do not fill the page.
	TitleFirst bool
}

// ids runs the listing: FTS match (when Q has terms) joined with the
// visibility predicate in one SQL statement on both engines.
//
// Search first ranks only the best `window` index rows: an article's best
// row is inside any rank-ordered prefix that holds any of its rows, so when
// that prefix yields enough visible articles the page is exactly the one the
// full query would return. Otherwise the full query runs. This keeps broad
// prefixes ("pas…" while typing) off the slow path (KB-010 p95).
func (m *Module) ids(ctx context.Context, lq listQuery) ([]string, bool, error) {
	need := lq.Offset + lq.Limit + 1
	if strings.TrimSpace(lq.Q) == "" {
		return m.runIDs(ctx, lq, 0)
	}
	if len(store.SearchTerms(lq.Q)) == 0 {
		return nil, false, nil // no searchable terms
	}
	if lq.Sort != "" {
		// An explicit order is not rank order, so a rank-ordered window
		// cannot stand in for it: order every match.
		sub, sargs, _ := store.KBMatch(m.d.DB.Dialect, lq.Q)
		out, more, _, err := m.runSearch(ctx, lq, sub, sargs, 0)
		return out, more, err
	}
	window := max(250, need*10)
	if lq.TitleFirst {
		sub, sargs, _ := titleMatch(m.d.DB.Dialect, lq.Q)
		out, more, n, err := m.runSearch(ctx, lq, sub, sargs, window)
		if err != nil || n >= need {
			return out, more, err
		}
	}
	sub, sargs, _ := store.KBMatch(m.d.DB.Dialect, lq.Q)
	out, more, n, err := m.runSearch(ctx, lq, sub, sargs, window)
	if err != nil || n >= need {
		return out, more, err
	}
	out, more, _, err = m.runSearch(ctx, lq, sub, sargs, 0)
	return out, more, err
}

func (m *Module) runSearch(ctx context.Context, lq listQuery, sub string, sargs []any, window int) ([]string, bool, int, error) {
	var sb strings.Builder
	var args []any
	// MATERIALIZED keeps SQLite from flattening bm25() into the aggregate
	// (bm25 is only valid in the FTS query itself).
	// The window is ordered inside the FTS query itself (top-N without
	// materialising every match); KBMatch returns a plain SELECT … WHERE ….
	sb.WriteString(`WITH mm AS MATERIALIZED (` + sub)
	args = append(args, sargs...)
	if window > 0 {
		sb.WriteString(` ORDER BY rank LIMIT ?`)
		args = append(args, window)
	}
	sb.WriteString(`) SELECT a.id, MIN(mm.rank) AS r FROM mm JOIN kb_articles a ON a.id = mm.article_id JOIN kb_categories c ON c.id = a.category_id WHERE ` +
		visPredicate(lq.Aud, lq.Statuses))
	if lq.Category != "" {
		sb.WriteString(` AND (c.id = ? OR c.slug = ?)`)
		args = append(args, lq.Category, lq.Category)
	}
	if lq.Project != "" {
		sb.WriteString(` AND (c.project_id IS NULL OR c.project_id = ?)`)
		args = append(args, lq.Project)
	}
	// Fetch from the start so the caller can tell whether the window held
	// enough articles; the offset is applied below.
	order := "r, a.id"
	if lq.Sort != "" {
		var oargs []any
		order, oargs = m.sortOrder(ctx, lq)
		args = append(args, oargs...)
	}
	sb.WriteString(` GROUP BY a.id ORDER BY ` + order + ` LIMIT ?`)
	args = append(args, lq.Offset+lq.Limit+1)
	all, err := m.scanIDs(ctx, sb.String(), args)
	if err != nil {
		return nil, false, 0, err
	}
	n := len(all)
	if lq.Offset >= len(all) {
		return nil, false, n, nil
	}
	all = all[lq.Offset:]
	more := len(all) > lq.Limit
	if more {
		all = all[:lq.Limit]
	}
	return all, more, n, nil
}

func (m *Module) runIDs(ctx context.Context, lq listQuery, _ int) ([]string, bool, error) {
	var sb strings.Builder
	var args []any
	col := "a.published_at"
	if lq.Order == "updated" {
		col = "a.updated_at"
	}
	sb.WriteString(`SELECT a.id, 0 FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id WHERE ` + visPredicate(lq.Aud, lq.Statuses))
	if lq.Category != "" {
		sb.WriteString(` AND (c.id = ? OR c.slug = ?)`)
		args = append(args, lq.Category, lq.Category)
	}
	if lq.Project != "" {
		sb.WriteString(` AND (c.project_id IS NULL OR c.project_id = ?)`)
		args = append(args, lq.Project)
	}
	order := col + ` DESC, a.id DESC`
	if lq.Sort != "" {
		var oargs []any
		order, oargs = m.sortOrder(ctx, lq)
		args = append(args, oargs...)
	}
	sb.WriteString(` ORDER BY ` + order + ` LIMIT ? OFFSET ?`)
	args = append(args, lq.Limit+1, lq.Offset)
	out, err := m.scanIDs(ctx, sb.String(), args)
	if err != nil {
		return nil, false, err
	}
	more := len(out) > lq.Limit
	if more {
		out = out[:lq.Limit]
	}
	return out, more, nil
}

// editorSorts are the explicit orders of the editor list (`-` = descending).
var editorSorts = map[string]bool{"title": true, "-title": true, "updated_at": true, "-updated_at": true}

// titleKeySQL is the lower-cased title the list shows (summaries/pickLocale:
// requested locale, default locale, then Locales order, else the slug). It
// binds the requested and the default locale.
var titleKeySQL = func() string {
	var ranks strings.Builder
	for i, l := range Locales {
		fmt.Fprintf(&ranks, " WHEN t.locale = %s THEN %d", inList([]string{l}), i+2)
	}
	return `COALESCE((SELECT lower(t.title) FROM kb_article_translations t WHERE t.article_id = a.id AND (t.locale IN (?, ?) OR t.locale IN (` +
		inList(Locales) + `)) ORDER BY CASE WHEN t.locale = ? THEN 0 WHEN t.locale = ? THEN 1` + ranks.String() + ` END LIMIT 1), lower(a.slug))`
}()

// sortOrder is the ORDER BY for lq.Sort (ties by id in the same direction)
// and its bound arguments.
func (m *Module) sortOrder(ctx context.Context, lq listQuery) (string, []any) {
	key, desc := strings.CutPrefix(lq.Sort, "-")
	dir := ""
	if desc {
		dir = " DESC"
	}
	if key == "title" {
		def := m.defaultLocale(ctx)
		return titleKeySQL + dir + ", a.id" + dir, []any{lq.Locale, def, lq.Locale, def}
	}
	return "a.updated_at" + dir + ", a.id" + dir, nil
}

func (m *Module) scanIDs(ctx context.Context, q string, args []any) ([]string, error) {
	rows, err := m.d.DB.Read().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		var rank float64
		if err := rows.Scan(&id, &rank); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// summaries loads the list items for ids (keeping their order), with the
// title in the best locale and an excerpt near the first search term.
func (m *Module) summaries(ctx context.Context, ids []string, locale string, terms []string) ([]summaryDTO, error) {
	out := make([]summaryDTO, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	db := m.d.DB.Read()
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	byID := map[string]*summaryDTO{}
	rows, err := db.QueryContext(ctx, `SELECT id, category_id, slug, status, visibility, updated_at FROM kb_articles WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s summaryDTO
		if err := rows.Scan(&s.ID, &s.CategoryID, &s.Slug, &s.Status, &s.Visibility, &s.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.UpdatedAt = s.UpdatedAt.UTC()
		byID[s.ID] = &s
	}
	rows.Close()
	type tr struct{ title, body string }
	trs := map[string]map[string]tr{}
	rows, err = db.QueryContext(ctx, `SELECT article_id, locale, title, substr(body_md, 1, 2000) FROM kb_article_translations WHERE article_id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, l, title, body string
		if err := rows.Scan(&id, &l, &title, &body); err != nil {
			rows.Close()
			return nil, err
		}
		if trs[id] == nil {
			trs[id] = map[string]tr{}
		}
		trs[id][l] = tr{title, body}
	}
	rows.Close()
	def := m.defaultLocale(ctx)
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			continue
		}
		have := trs[id]
		l, _ := pickLocale(func(l string) bool { _, ok := have[l]; return ok }, locale, def)
		if l == "" {
			s.Locale = def
			s.Title = s.Slug
		} else {
			s.Locale = l
			s.Title = have[l].title
			s.Excerpt = excerpt(have[l].body, terms, excerptLen)
		}
		out = append(out, *s)
	}
	return out, nil
}

// list returns one page for lq; the cursor is an opaque offset.
func (m *Module) list(ctx context.Context, lq listQuery) (pageDTO, error) {
	ids, more, err := m.ids(ctx, lq)
	if err != nil {
		return pageDTO{}, err
	}
	items, err := m.summaries(ctx, ids, lq.Locale, store.SearchTerms(lq.Q))
	if err != nil {
		return pageDTO{}, err
	}
	page := pageDTO{Items: items}
	if more {
		c := encodeCursor(lq.Offset + lq.Limit)
		page.NextCursor = &c
	}
	return page, nil
}

// Search is the KB-007 entry point: articles matching q that p may read, in
// rank order. Staff see drafts only with includeDrafts.
func (m *Module) Search(ctx context.Context, p *domain.Principal, q, locale string, limit int, includeDrafts bool) ([]summaryDTO, error) {
	aud := audienceOf(p)
	statuses := []string{"published"}
	if includeDrafts && aud == audStaff {
		statuses = append(statuses, "draft")
	}
	page, err := m.list(ctx, listQuery{Aud: aud, Statuses: statuses, Q: q, Locale: locale, Limit: limit})
	return page.Items, err
}

// Suggest returns up to 5 articles for the new-ticket form (KB-010), cached
// per (KB generation, audience, locale, query) for 30 s in a 2 MB LRU.
func (m *Module) Suggest(ctx context.Context, p *domain.Principal, q, locale string, useCache bool) ([]summaryDTO, error) {
	norm := strings.Join(store.SearchTerms(q), " ")
	if len([]rune(strings.TrimSpace(q))) < 3 || norm == "" {
		return []summaryDTO{}, nil
	}
	aud := audienceOf(p)
	key := fmt.Sprintf("%d|%s|%s|%s", m.gen.Load(), aud, locale, norm)
	if useCache {
		if v, ok := m.suggest.Get(key); ok {
			return v, nil
		}
	}
	page, err := m.list(ctx, listQuery{Aud: aud, Statuses: []string{"published"}, Q: norm, Locale: locale, Limit: suggestLimit, TitleFirst: true})
	if err != nil {
		return nil, err
	}
	items := page.Items
	if useCache {
		size := 0
		for _, it := range items {
			size += it.size()
		}
		m.suggest.Put(key, items, size, suggestTTL)
	}
	return items, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(offset)))
}

func decodeCursor(c string) int {
	if c == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.HasPrefix(string(b), "o:") {
		return 0
	}
	n, err := strconv.Atoi(string(b[2:]))
	if err != nil || n < 0 || n > 1_000_000 {
		return 0
	}
	return n
}

// reindex refreshes the search entries of one article inside tx: archived
// articles are removed; drafts are indexed too (staff search them with
// include_drafts) and filtered by status in SQL.
func reindex(ctx context.Context, tx *store.Tx, articleID string) error {
	if err := store.UnindexKB(ctx, tx, articleID); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM kb_articles WHERE id = ?`, articleID).Scan(&status); err != nil {
		return store.NotFound(err)
	}
	if status == "archived" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT locale, title, body_md FROM kb_article_translations WHERE article_id = ?`, articleID)
	if err != nil {
		return err
	}
	var docs []store.KBDoc
	for rows.Next() {
		var d store.KBDoc
		if err := rows.Scan(&d.Locale, &d.Title, &d.Body); err != nil {
			rows.Close()
			return err
		}
		d.ArticleID = articleID
		d.Body = plainText(d.Body)
		docs = append(docs, d)
	}
	rows.Close()
	for _, d := range docs {
		if err := store.IndexKB(ctx, tx, d); err != nil {
			return err
		}
	}
	return nil
}
