// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// The spec's descriptions use a small Markdown subset: paragraphs, `-`/`*`
// and `1.` lists, `code`, **bold**, *italic* and [links](url). md renders
// exactly that; everything else is escaped text, so no raw HTML ever reaches
// the page. Internal planning references (D-20, I-12, MCP-011, "Phase: V1.")
// mean nothing to an API user and are dropped.

var (
	reMdLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reMdCode     = regexp.MustCompile("`([^`]+)`")
	reMdBold     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reMdItalic   = regexp.MustCompile(`(^|[\s(])\*([^*\s][^*]*)\*`)
	rePhase      = regexp.MustCompile(`(?m)[ \t]*Phase: (?:CORE|V1)\.?[ \t]*`)
	rePlanParen  = regexp.MustCompile(`\s*\((?:[A-Z]{1,4}-\d+[A-Za-z0-9 §.,/;:-]*|[A-Z]{1,4}-\d+(?:, ?[A-Z]{1,4}-\d+)*)\)`)
	rePlanPrefix = regexp.MustCompile(`(?m)^(?:[A-Z]{1,4}-\d+(?: §[A-Z])?(?:, ?[A-Z]{1,4}-\d+)*):\s*`)
	reListItem   = regexp.MustCompile(`^(\s*)(?:[-*]|\d+\.)\s+(.*)$`)
	// rePlanID: planning ids by their fixed prefixes (never ticket refs such as HD-1042).
	rePlanID   = regexp.MustCompile(`(?:[;,]\s*|\s*\b(?:per|see)\s+)?\b(?:D|I|M|MCP|PRJ|TKT|AUTH|NOT|PRV|FE|DB|PLT|DEP|CON|KB|INT)-\d+\b(?: §[A-Z])?(?:\s*(?:,|and)\s*(?:D|I|M|MCP|PRJ|TKT|AUTH|NOT|PRV|FE|DB|PLT|DEP|CON|KB|INT)-\d+\b)*`)
	reEmptyPar = regexp.MustCompile(`\(\s*[;,]?\s*\)`)
)

// cleanDesc removes internal planning references from a description.
func cleanDesc(s string) string {
	s = rePhase.ReplaceAllString(s, "")
	s = rePlanParen.ReplaceAllString(s, "")
	s = rePlanPrefix.ReplaceAllString(s, "")
	s = rePlanID.ReplaceAllString(s, "")
	s = reEmptyPar.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " .", ".")
	return strings.TrimSpace(s)
}

func safeURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "/") || strings.HasPrefix(u, "#")
}

// inline renders one line of text (already split from block structure).
func inline(s string) string {
	// Code spans first, so their content is never formatted further.
	var codes []string
	s = reMdCode.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(m[1:len(m)-1])+"</code>")
		return "\x00" + string(rune('A'+len(codes)-1)) + "\x00"
	})
	var links []string
	s = reMdLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reMdLink.FindStringSubmatch(m)
		text := html.EscapeString(p[1])
		if !safeURL(p[2]) {
			links = append(links, text)
		} else {
			links = append(links, `<a href="`+html.EscapeString(p[2])+`">`+text+`</a>`)
		}
		return "\x01" + string(rune('A'+len(links)-1)) + "\x01"
	})
	s = html.EscapeString(s)
	s = reMdBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reMdItalic.ReplaceAllString(s, "$1<em>$2</em>")
	for i, c := range codes {
		s = strings.Replace(s, "\x00"+string(rune('A'+i))+"\x00", c, 1)
	}
	for i, l := range links {
		s = strings.Replace(s, "\x01"+string(rune('A'+i))+"\x01", l, 1)
	}
	return s
}

// mdInline renders a one-line text (a summary) to safe HTML.
func mdInline(s string) template.HTML {
	return template.HTML(inline(cleanDesc(s))) //nolint:gosec // built from escaped text only
}

// md renders a description to safe HTML.
func md(src string) template.HTML {
	src = cleanDesc(src)
	if src == "" {
		return ""
	}
	var b strings.Builder
	var para []string
	list := "" // "ul" or "ol" while inside a list
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</li></" + list + ">")
			list = ""
		}
	}
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			flushPara()
			closeList()
			continue
		}
		if m := reListItem.FindStringSubmatch(line); m != nil && len(m[1]) <= 2 {
			flushPara()
			kind := "ul"
			if t[0] >= '0' && t[0] <= '9' {
				kind = "ol"
			}
			if list != kind {
				closeList()
				b.WriteString("<" + kind + "><li>")
				list = kind
			} else {
				b.WriteString("</li><li>")
			}
			b.WriteString(inline(m[2]))
			continue
		}
		if list != "" { // continuation of a list item
			b.WriteString(" " + inline(t))
			continue
		}
		para = append(para, t)
	}
	flushPara()
	closeList()
	return template.HTML(b.String()) //nolint:gosec // built from escaped text only
}
