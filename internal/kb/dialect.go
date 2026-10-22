// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"strings"

	"github.com/radioBros/FeatherDesk/internal/store"
)

// titleMatch is store.KBMatch restricted to the title column (SQLite FTS5
// column filter; Postgres weight A, which IndexKB gives the title). Suggest
// tries it first: titles are what the panel shows, bm25/ts_rank already
// weight titles 10:1, and it keeps "matches nearly everything" prefixes
// (3 letters while typing) cheap. Terms come from store.SearchTerms, so only
// letters and digits reach the query syntax.
func titleMatch(d store.Dialect, query string) (sub string, args []any, ok bool) {
	terms := store.SearchTerms(query)
	if len(terms) == 0 {
		return "", nil, false
	}
	parts := make([]string, len(terms))
	if d == store.Postgres {
		for i, t := range terms {
			parts[i] = t + ":*A"
		}
		q := strings.Join(parts, " & ")
		return `SELECT article_id, locale, -ts_rank(tsv, to_tsquery('simple', ?)) AS rank FROM kb_search WHERE tsv @@ to_tsquery('simple', ?)`,
			[]any{q, q}, true
	}
	for i, t := range terms {
		parts[i] = `"` + t + `"*`
	}
	return `SELECT article_id, locale, bm25(kb_fts, 0, 0, 10.0, 1.0) AS rank FROM kb_fts WHERE kb_fts MATCH ?`,
		[]any{"title : (" + strings.Join(parts, " AND ") + ")"}, true
}
