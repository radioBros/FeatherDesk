// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"strings"
	"unicode"

	"github.com/radioBros/FeatherDesk/internal/domain"
)

// Search indexes are engine-specific (FTS5 on SQLite, tsvector on Postgres) and
// are written by the asynchronous index worker, never inside a ticket write
// transaction (D-11). Customer search matches public text only (I-2, I-9).

// TicketDoc is the indexed text of one ticket.
type TicketDoc struct {
	TicketID    string
	Public      string // subject + public messages + customer-visible field values
	Internal    string // internal notes
	StaffFields string // staff-only custom field values (I-9)
}

// IndexTicket replaces the index entry of one ticket.
func IndexTicket(ctx context.Context, q Querier, doc TicketDoc) error {
	if q.Dialect() == Postgres {
		// staff_tsv is public ∪ internal ∪ staff fields, stored (DB-023): staff
		// search matches one plain GIN column, and the wide row is toasted out
		// of the heap (toast_tuple_target, migration 0131).
		_, err := q.ExecContext(ctx, `INSERT INTO ticket_search (ticket_id, public_tsv, staff_tsv, updated_at)
			SELECT ?, p.tsv, p.tsv || to_tsvector('simple', ?) || to_tsvector('simple', ?), ?
			FROM (SELECT to_tsvector('simple', ?) AS tsv) p
			ON CONFLICT (ticket_id) DO UPDATE SET public_tsv = excluded.public_tsv, staff_tsv = excluded.staff_tsv,
				updated_at = excluded.updated_at`,
			doc.TicketID, FoldDiacritics(doc.Internal), FoldDiacritics(doc.StaffFields), Now(), FoldDiacritics(doc.Public))
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM tickets_fts WHERE ticket_id = ?`, doc.TicketID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO tickets_fts (ticket_id, public_text, internal_text, staff_fields_text) VALUES (?, ?, ?, ?)`,
		doc.TicketID, doc.Public, doc.Internal, doc.StaffFields)
	return err
}

// UnindexTicket removes a ticket from the index (hard delete).
func UnindexTicket(ctx context.Context, q Querier, ticketID string) error {
	tbl := "tickets_fts"
	if q.Dialect() == Postgres {
		tbl = "ticket_search"
	}
	_, err := q.ExecContext(ctx, `DELETE FROM `+tbl+` WHERE ticket_id = ?`, ticketID)
	return err
}

// TicketMatch returns a subquery selecting the IDs of tickets matching a user
// query, for use as `t.id IN (<sub>)`. With staff=false only public text is
// searched. ok is false when the query has no searchable terms.
func TicketMatch(d Dialect, query string, staff bool) (sub string, args []any, ok bool) {
	// D-20: ticket references (KEY-123) in the query match their ticket. A
	// query made only of references matches nothing else; otherwise the
	// reference matches are a UNION inside the subquery, so the text match
	// keeps its plan (DB-023).
	refSub, refArgs, onlyRefs := ticketRefMatch(query)
	if onlyRefs {
		return refSub, refArgs, true
	}
	sub, args, ok = ticketTextMatch(d, query, staff)
	if refSub != "" {
		if !ok {
			return refSub, refArgs, true
		}
		return sub + ` UNION ` + refSub, append(args, refArgs...), true
	}
	return sub, args, ok
}

// ticketRefMatch builds the subquery of the KEY-123 tokens of query (""
// when there are none); onlyRefs reports that every token is a reference.
func ticketRefMatch(query string) (sub string, args []any, onlyRefs bool) {
	fields := strings.Fields(query)
	var parts []string
	for _, f := range fields {
		f = strings.Trim(f, "[]()#,;.")
		key, seq, ok := domain.ParseTicketRef(f)
		if !ok {
			continue
		}
		parts = append(parts, `SELECT r.id FROM tickets r WHERE r.project_id = (SELECT p.id FROM projects p WHERE p.key = ?) AND r.project_seq = ?`)
		args = append(args, key, seq)
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	return strings.Join(parts, ` UNION `), args, len(parts) == len(fields)
}

func ticketTextMatch(d Dialect, query string, staff bool) (sub string, args []any, ok bool) {
	terms := SearchTerms(query)
	if len(terms) == 0 {
		return "", nil, false
	}
	if d == Postgres {
		col := "public_tsv"
		if staff {
			col = "staff_tsv"
		}
		// The tsquery sits in an InitPlan on purpose (DB-023): the planner then
		// costs the match like a generic plan (a selective GIN bitmap scan) on
		// every execution. Seeing the real term frequency in a custom plan, it
		// picks per-row `@@` filters (a backward walk of tickets_updated_idx,
		// or a seq scan of ticket_search) and cannot cost the detoast each row
		// then pays: 4-50x slower at 100k tickets. The bitmap path never
		// detoasts, so its cost stays proportional to the number of matches.
		return `SELECT ticket_id FROM ticket_search WHERE ` + col + ` @@ (SELECT to_tsquery('simple', ?))`, []any{pgFoldedQuery(terms)}, true
	}
	expr := ftsQuery(terms)
	if !staff {
		expr = "public_text : (" + expr + ")"
	}
	return `SELECT ticket_id FROM tickets_fts WHERE tickets_fts MATCH ?`, []any{expr}, true
}

// KBDoc is the indexed text of one article translation.
type KBDoc struct {
	ArticleID string
	Locale    string
	Title     string
	Body      string
}

// IndexKB replaces the index entry of one article translation.
func IndexKB(ctx context.Context, q Querier, doc KBDoc) error {
	if q.Dialect() == Postgres {
		_, err := q.ExecContext(ctx, `INSERT INTO kb_search (article_id, locale, title, tsv)
			VALUES (?, ?, ?, setweight(to_tsvector('simple', ?), 'A') || setweight(to_tsvector('simple', ?), 'B'))
			ON CONFLICT (article_id, locale) DO UPDATE SET title = excluded.title, tsv = excluded.tsv`,
			doc.ArticleID, doc.Locale, doc.Title, doc.Title, doc.Body)
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM kb_fts WHERE article_id = ? AND locale = ?`, doc.ArticleID, doc.Locale); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO kb_fts (article_id, locale, title, body) VALUES (?, ?, ?, ?)`,
		doc.ArticleID, doc.Locale, doc.Title, doc.Body)
	return err
}

// UnindexKB removes all translations of an article from the index.
func UnindexKB(ctx context.Context, q Querier, articleID string) error {
	tbl := "kb_fts"
	if q.Dialect() == Postgres {
		tbl = "kb_search"
	}
	_, err := q.ExecContext(ctx, `DELETE FROM `+tbl+` WHERE article_id = ?`, articleID)
	return err
}

// KBMatch returns a subquery selecting (article_id, locale, rank) for a user
// query; lower rank is better on both engines. Visibility is the caller's job:
// join kb_articles and filter in SQL (I-10).
func KBMatch(d Dialect, query string) (sub string, args []any, ok bool) {
	terms := SearchTerms(query)
	if len(terms) == 0 {
		return "", nil, false
	}
	if d == Postgres {
		return `SELECT article_id, locale, -ts_rank(tsv, to_tsquery('simple', ?)) AS rank FROM kb_search WHERE tsv @@ to_tsquery('simple', ?)`,
			[]any{pgQuery(terms), pgQuery(terms)}, true
	}
	return `SELECT article_id, locale, bm25(kb_fts, 0, 0, 10.0, 1.0) AS rank FROM kb_fts WHERE kb_fts MATCH ?`, []any{ftsQuery(terms)}, true
}

// SearchTerms splits user input into at most 10 lower-cased letter/digit terms,
// so no query syntax from the user ever reaches FTS5 or to_tsquery.
func SearchTerms(q string) []string {
	f := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(f) > 10 {
		f = f[:10]
	}
	return f
}

func ftsQuery(terms []string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = `"` + t + `"*`
	}
	return strings.Join(parts, " AND ")
}

func pgQuery(terms []string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = t + ":*"
	}
	return strings.Join(parts, " & ")
}

// pgFoldedQuery is pgQuery with diacritics folded (DB-029): `perché` and
// `perche` both become `perche:*`. A term whose folded form differs also keeps
// its original spelling as an alternative, so rows indexed before folding
// existed still match until the next reindex.
func pgFoldedQuery(terms []string) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		f := FoldDiacritics(t)
		if f == t {
			parts[i] = t + ":*"
		} else {
			parts[i] = "(" + f + ":* | " + t + ":*)"
		}
	}
	return strings.Join(parts, " & ")
}

// FoldDiacritics lower-cases s and strips the accents of Latin letters that
// have a canonical decomposition (é → e, ñ → n, ș → s), the same set SQLite's
// `unicode61 remove_diacritics 2` tokenizer folds. Postgres ticket search
// applies it on both the index and the query side in Go, so diacritic-
// insensitive matching needs no `unaccent` extension (managed Postgres often
// lacks it) and behaves the same on both engines. Letters without a
// decomposition (ß, æ, ø, ł, đ) are kept, as SQLite keeps them.
func FoldDiacritics(s string) string {
	s = strings.ToLower(s)
	for _, r := range s {
		if r >= 0xC0 {
			return foldReplacer.Replace(s)
		}
	}
	return s
}

var foldReplacer = func() *strings.Replacer {
	groups := map[string]string{
		"a": "àáâãäåāăąǎȁȃȧạảấầẩẫậắằẳẵặ",
		"c": "çćĉċč",
		"d": "ďḍ",
		"e": "èéêëēĕėęěȅȇȩẹẻẽếềểễệ",
		"g": "ĝğġģǧ",
		"h": "ĥȟḥ",
		"i": "ìíîïĩīĭįǐȉȋịỉ",
		"j": "ĵ",
		"k": "ķǩ",
		"l": "ĺļľ",
		"n": "ñńņňǹ",
		"o": "òóôõöōŏőơǒȍȏȯọỏốồổỗộớờởỡợ",
		"r": "ŕŗřȑȓ",
		"s": "śŝşšș",
		"t": "ţťț",
		"u": "ùúûüũūŭůűųưǔȕȗụủứừửữự",
		"w": "ŵẁẃẅ",
		"y": "ýÿŷỳỵỷỹ",
		"z": "źżžẓ",
	}
	var pairs []string
	for base, accented := range groups {
		for _, r := range accented {
			pairs = append(pairs, string(r), base)
		}
	}
	return strings.NewReplacer(pairs...)
}()
