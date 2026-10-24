// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/mdrender"
)

// AssetPathPrefix is re-exported for KB callers and tests.
const AssetPathPrefix = mdrender.AssetPathPrefix

// renderer turns article Markdown into sanitised HTML, cached per article revision.
type renderer struct {
	cache *lru[string]
}

func newRenderer(cacheBytes int) *renderer {
	return &renderer{cache: newLRU[string](cacheBytes)}
}

// Render returns the sanitised HTML of body; key identifies the cache slot.
func (r *renderer) Render(articleID, locale string, updatedAt time.Time, body string) string {
	key := articleID + "|" + locale + "|" + updatedAt.UTC().Format(time.RFC3339Nano)
	if html, ok := r.cache.Get(key); ok {
		return html
	}
	html := r.RenderString(body)
	r.cache.Put(key, html, len(html), 0)
	return html
}

// RenderString renders without caching.
func (r *renderer) RenderString(body string) string {
	return mdrender.Render(body)
}

var (
	mdImage   = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdSyntax  = regexp.MustCompile("(?m)^\\s{0,3}(#{1,6}|>|[-*+]|\\d+\\.)\\s+|[*_`~|]+")
	mdSpace   = regexp.MustCompile(`\s+`)
	assetRefs = regexp.MustCompile(`/kb/assets/([0-9a-fA-F-]{36})`)
)

// plainText strips Markdown syntax (excerpts, index text).
func plainText(md string) string {
	s := mdImage.ReplaceAllString(md, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdSyntax.ReplaceAllString(s, " ")
	return strings.TrimSpace(mdSpace.ReplaceAllString(s, " "))
}

// excerpt returns at most n runes of plain text, preferring the region around
// the first matching search term.
func excerpt(md string, terms []string, n int) string {
	s := plainText(md)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	start := 0
	lower := strings.ToLower(s)
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 {
			start = max(0, i-n/4)
			break
		}
	}
	// align to a rune boundary
	for start > 0 && !utf8.RuneStart(s[start]) {
		start--
	}
	rs := []rune(s[start:])
	out := string(rs[:min(n, len(rs))])
	if start > 0 {
		out = "…" + string([]rune(out)[1:])
	}
	if len(rs) > n {
		out = string([]rune(out)[:n-1]) + "…"
	}
	return out
}

// referencedAssets lists the KB asset IDs a body links to.
func referencedAssets(md string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range assetRefs.FindAllStringSubmatch(md, -1) {
		id := strings.ToLower(m[1])
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
