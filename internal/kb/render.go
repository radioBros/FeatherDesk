// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"bytes"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// AssetPathPrefix is the only image source the renderer keeps (KB-006).
const AssetPathPrefix = "/api/v1/kb/assets/"

var assetSrc = regexp.MustCompile(`^/api/v1/kb/assets/[0-9a-fA-F-]{36}$`)

// renderer turns article Markdown into sanitised HTML: goldmark (GFM tables,
// autolinks, strikethrough, task lists, heading anchors; raw HTML dropped)
// then bluemonday's UGC policy with images limited to KB assets. Output is
// cached per (article, locale, updated_at) in a byte-capped LRU, so editing
// an article invalidates its entry implicitly.
type renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
	cache  *lru[string]
}

func newRenderer(cacheBytes int) *renderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(imageFilter{}, 100)),
		),
	)
	p := bluemonday.UGCPolicy()
	// Overrides UGC's "any URL" image source: only uploaded KB assets.
	p.AllowAttrs("src").Matching(assetSrc).OnElements("img")
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return &renderer{md: md, policy: p, cache: newLRU[string](cacheBytes)}
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
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(body), &buf); err != nil {
		return ""
	}
	return r.policy.Sanitize(buf.String())
}

// imageFilter replaces images whose destination is not a KB asset with their
// alt text, so external trackers and data: URLs never render.
type imageFilter struct{}

func (imageFilter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	var bad []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering && !assetSrc.Match(img.Destination) {
			bad = append(bad, img)
		}
		return ast.WalkContinue, nil
	})
	for _, img := range bad {
		parent := img.Parent()
		if parent == nil {
			continue
		}
		for c := img.FirstChild(); c != nil; {
			next := c.NextSibling()
			img.RemoveChild(img, c)
			parent.InsertBefore(parent, img, c)
			c = next
		}
		parent.RemoveChild(parent, img)
	}
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
