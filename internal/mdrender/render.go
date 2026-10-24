// SPDX-License-Identifier: AGPL-3.0-only

// Package mdrender turns Markdown into sanitised HTML (goldmark GFM + bluemonday UGC).
// Knowledge-base articles and ticket message bodies share this renderer.
package mdrender

import (
	"bytes"
	"regexp"

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

var defaultEngine = newMarkdownEngine()

func newMarkdownEngine() *markdownEngine {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(imageFilter{}, 100)),
		),
	)
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("src").Matching(assetSrc).OnElements("img")
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return &markdownEngine{md: md, policy: p}
}

type markdownEngine struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
}

// Render converts Markdown to sanitised HTML.
func Render(body string) string {
	var buf bytes.Buffer
	if err := defaultEngine.md.Convert([]byte(body), &buf); err != nil {
		return ""
	}
	return defaultEngine.policy.Sanitize(buf.String())
}

// imageFilter replaces images whose destination is not a KB asset with their alt text.
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
