// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ---- tokenizer (NOT-025: hand-written, no golang.org/x/net/html) ----

type tokKind int

const (
	tText tokKind = iota
	tStart
	tEnd
)

type token struct {
	kind  tokKind
	name  string // lowercase tag name
	text  string // unescaped text
	attrs map[string]string
	self  bool
}

// rawText elements whose content is skipped verbatim up to the end tag.
var rawText = map[string]bool{"script": true, "style": true, "title": true, "textarea": true, "xmp": true, "noembed": true, "noframes": true}

func isNameByte(c byte) bool {
	return c == '-' || c == ':' || c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

// tokenize splits s into text, start and end tokens. Comments, doctypes and
// processing instructions are dropped; raw-text elements lose their content.
func tokenize(s string) []token {
	var out []token
	i, n := 0, len(s)
	textStart := 0
	flushText := func(end int) {
		if end > textStart {
			out = append(out, token{kind: tText, text: html.UnescapeString(s[textStart:end])})
		}
	}
	for i < n {
		if s[i] != '<' || i+1 >= n {
			i++
			continue
		}
		c := s[i+1]
		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			flushText(i)
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				i = n
			} else {
				i = i + 4 + end + 3
			}
			textStart = i
		case c == '!' || c == '?':
			flushText(i)
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				i = n
			} else {
				i += end + 1
			}
			textStart = i
		case c == '/' && i+2 < n && isLetter(s[i+2]):
			flushText(i)
			j := i + 2
			for j < n && isNameByte(s[j]) {
				j++
			}
			name := strings.ToLower(s[i+2 : j])
			end := strings.IndexByte(s[j:], '>')
			if end < 0 {
				i = n
			} else {
				i = j + end + 1
			}
			out = append(out, token{kind: tEnd, name: name})
			textStart = i
		case isLetter(c):
			flushText(i)
			tok, next := parseStartTag(s, i)
			out = append(out, tok)
			i = next
			if rawText[tok.name] && !tok.self {
				// Skip to the matching end tag (case-insensitive).
				lower := strings.ToLower(s[i:])
				end := strings.Index(lower, "</"+tok.name)
				if end < 0 {
					i = n
				} else {
					i += end
					gt := strings.IndexByte(s[i:], '>')
					if gt < 0 {
						i = n
					} else {
						i += gt + 1
					}
				}
				out = append(out, token{kind: tEnd, name: tok.name})
			}
			textStart = i
		default:
			i++
		}
	}
	flushText(n)
	return out
}

// parseStartTag parses "<name attr=value …>" starting at s[i] == '<'.
func parseStartTag(s string, i int) (token, int) {
	n := len(s)
	j := i + 1
	for j < n && isNameByte(s[j]) {
		j++
	}
	tok := token{kind: tStart, name: strings.ToLower(s[i+1 : j]), attrs: map[string]string{}}
	for j < n {
		for j < n && (isSpace(s[j]) || s[j] == '/') {
			if s[j] == '/' && j+1 < n && s[j+1] == '>' {
				tok.self = true
			}
			j++
		}
		if j >= n {
			return tok, n
		}
		if s[j] == '>' {
			return tok, j + 1
		}
		k := j
		for k < n && !isSpace(s[k]) && s[k] != '=' && s[k] != '>' && s[k] != '/' {
			k++
		}
		if k == j { // stray character
			j++
			continue
		}
		name := strings.ToLower(s[j:k])
		j = k
		for j < n && isSpace(s[j]) {
			j++
		}
		val := ""
		if j < n && s[j] == '=' {
			j++
			for j < n && isSpace(s[j]) {
				j++
			}
			if j < n && (s[j] == '"' || s[j] == '\'') {
				q := s[j]
				end := strings.IndexByte(s[j+1:], q)
				if end < 0 {
					val = s[j+1:]
					j = n
				} else {
					val = s[j+1 : j+1+end]
					j = j + 1 + end + 1
				}
			} else {
				k := j
				for k < n && !isSpace(s[k]) && s[k] != '>' {
					k++
				}
				val = s[j:k]
				j = k
			}
		}
		if _, dup := tok.attrs[name]; !dup {
			tok.attrs[name] = html.UnescapeString(val)
		}
	}
	return tok, n
}

// ---- converter ----

var voidElems = map[string]bool{"br": true, "hr": true, "img": true, "meta": true, "link": true, "input": true, "wbr": true, "area": true, "base": true, "col": true, "embed": true, "source": true, "track": true, "param": true}

// dropElems lose all their content.
var dropElems = map[string]bool{"script": true, "style": true, "head": true, "title": true, "template": true, "noscript": true, "svg": true, "math": true, "object": true, "iframe": true, "textarea": true, "select": true, "button": true, "xmp": true, "noembed": true, "noframes": true}

type listCtx struct {
	ordered bool
	n       int
}

type mdWriter struct {
	sb         strings.Builder
	nl         int // pending newlines before next content (0..2)
	lineStart  bool
	space      bool // pending collapsed space
	quote      int
	pre        int
	lists      []listCtx
	marker     string // pending list marker for the next line start
	inLI       bool
	linkStack  []linkState
	table      *tableState
	tableDepth int
	drop       int
	lastQuote  int
}

type linkState struct {
	href  string
	start int
}

type tableState struct {
	rows [][]string
	row  []string
	cell *strings.Builder
}

func (w *mdWriter) linePrefix() string {
	var p strings.Builder
	for i := 0; i < w.quote; i++ {
		p.WriteString("> ")
	}
	if len(w.lists) > 0 {
		p.WriteString(strings.Repeat("   ", len(w.lists)-1))
		if w.marker != "" {
			p.WriteString(w.marker)
			w.marker = ""
		} else if w.inLI {
			p.WriteString("   ")
		}
	}
	return p.String()
}

// ensure requests at least k newlines before the next content.
func (w *mdWriter) ensure(k int) {
	if w.sb.Len() == 0 {
		return
	}
	if k > w.nl {
		w.nl = k
	}
	w.space = false
}

// content writes s (already formatted) at the current position.
func (w *mdWriter) content(s string) {
	if s == "" {
		return
	}
	if w.nl > 0 && w.sb.Len() > 0 {
		for i := 0; i < w.nl; i++ {
			if q := min(w.quote, w.lastQuote); i > 0 && q > 0 {
				w.sb.WriteString(strings.TrimRight(strings.Repeat("> ", q), " "))
			}
			w.sb.WriteByte('\n')
		}
		w.lineStart = true
	}
	w.nl = 0
	if w.lineStart || w.sb.Len() == 0 {
		w.sb.WriteString(w.linePrefix())
		w.lineStart = false
		w.space = false
	} else if w.space {
		w.sb.WriteByte(' ')
	}
	w.space = false
	w.sb.WriteString(s)
	w.lastQuote = w.quote
}

func (w *mdWriter) text(s string) {
	if w.drop > 0 {
		return
	}
	if w.table != nil {
		if w.table.cell != nil {
			w.table.cell.WriteString(s)
		}
		return
	}
	if w.pre > 0 {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			if i > 0 {
				w.ensure(1)
				w.nl = 1
			}
			if l != "" {
				w.content(l)
			} else if i > 0 {
				w.content("\u200b") // keep blank lines inside pre; stripped below
			}
		}
		return
	}
	// Collapse whitespace (including NBSP) to single spaces.
	var b strings.Builder
	lead := false
	trail := false
	inSpace := false
	for idx, r := range s {
		if unicode.IsSpace(r) || r == ' ' {
			if idx == 0 {
				lead = true
			}
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		if r == '<' {
			b.WriteString("&lt;")
			continue
		}
		b.WriteRune(r)
	}
	trail = inSpace
	if b.Len() == 0 {
		if lead || trail {
			w.space = true
		}
		return
	}
	if lead {
		w.space = true
	}
	w.content(b.String())
	if trail {
		w.space = true
	}
}

var schemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*):`)

// safeHref returns a cleaned http/https/mailto URL or "".
func safeHref(h string) string {
	h = strings.Map(func(r rune) rune {
		if r <= 0x20 || r == 0x7f || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, h)
	m := schemeRe.FindStringSubmatch(h)
	if m == nil {
		return ""
	}
	switch strings.ToLower(m[1]) {
	case "http", "https", "mailto":
	default:
		return ""
	}
	r := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\"", "%22")
	return r.Replace(h)
}

// HTMLToMarkdown converts an HTML mail body to Markdown with an allowlist:
// headings, paragraphs, br, lists, links (http/https/mailto only), emphasis,
// code/pre, blockquote, hr and simple tables. Everything else degrades to
// text; script/style/head content is dropped and no attribute other than a
// sanitized href ever reaches the output.
func HTMLToMarkdown(src string) string {
	w := &mdWriter{}
	toks := tokenize(src)
	for i, t := range toks {
		if t.kind == tStart && t.name == "table" && w.table == nil && w.drop == 0 {
			w.ensure(2)
			if simpleTable(toks, i) {
				w.table = &tableState{}
				w.tableDepth = 1
			}
			continue
		}
		switch t.kind {
		case tText:
			w.text(t.text)
		case tStart:
			w.start(t)
		case tEnd:
			w.end(t.name)
		}
	}
	if w.table != nil {
		w.flushTable()
	}
	return finish(w.sb.String())
}

var multiNL = regexp.MustCompile(`\n{3,}`)

func finish(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(l, "\u200b", "")
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	s = multiNL.ReplaceAllString(s, "\n\n")
	return strings.Trim(s, "\n ")
}

func (w *mdWriter) start(t token) {
	name := t.name
	if name == "body" || name == "html" {
		w.drop = 0
		return
	}
	if dropElems[name] {
		if !voidElems[name] && !t.self {
			w.drop++
		}
		return
	}
	if w.drop > 0 {
		return
	}
	if w.table != nil {
		w.tableStart(t)
		return
	}
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.ensure(2)
		lvl, _ := strconv.Atoi(name[1:])
		w.content(strings.Repeat("#", lvl))
		w.space = true
	case "p", "div", "section", "article", "header", "footer", "main", "address", "center", "dl", "figure":
		if name == "p" {
			w.ensure(2)
		} else {
			w.ensure(1)
		}
	case "dt", "dd":
		w.ensure(1)
	case "br":
		if w.sb.Len() > 0 {
			w.ensure(1)
			w.nl = max(w.nl, 1)
		}
	case "hr":
		w.ensure(2)
		w.content("---")
		w.ensure(2)
	case "blockquote":
		w.ensure(2)
		w.quote++
	case "ul", "ol":
		if len(w.lists) == 0 {
			w.ensure(2)
		} else {
			w.ensure(1)
		}
		w.lists = append(w.lists, listCtx{ordered: name == "ol"})
	case "li":
		w.ensure(1)
		if len(w.lists) == 0 {
			w.lists = append(w.lists, listCtx{})
		}
		l := &w.lists[len(w.lists)-1]
		l.n++
		if l.ordered {
			w.marker = strconv.Itoa(l.n) + ". "
		} else {
			w.marker = "- "
		}
		w.inLI = true
	case "b", "strong":
		w.content("**")
		w.space = false
	case "i", "em":
		w.content("_")
		w.space = false
	case "code", "kbd", "samp", "tt":
		if w.pre == 0 {
			w.content("`")
			w.space = false
		}
	case "pre":
		w.ensure(2)
		w.content("```")
		w.ensure(1)
		w.nl = 1
		w.pre++
	case "a":
		href := safeHref(t.attrs["href"])
		if href == "" {
			w.linkStack = append(w.linkStack, linkState{})
			return
		}
		w.content("[")
		w.space = false
		w.linkStack = append(w.linkStack, linkState{href: href, start: w.sb.Len()})
	case "img":
		if alt := strings.TrimSpace(t.attrs["alt"]); alt != "" {
			w.text(alt)
		}
	case "tr", "td", "th", "caption":
		w.ensure(1)
	}
}

func (w *mdWriter) end(name string) {
	if name == "head" || dropElems[name] {
		if w.drop > 0 {
			w.drop--
		}
		return
	}
	if w.drop > 0 {
		return
	}
	if w.table != nil {
		w.tableEnd(name)
		return
	}
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6", "p":
		w.ensure(2)
	case "div", "section", "article", "header", "footer", "main", "address", "center", "dl", "figure", "dt", "dd", "tr", "td", "th", "caption":
		w.ensure(1)
	case "table":
		w.ensure(2)
	case "blockquote":
		if w.quote > 0 {
			w.quote--
		}
		w.ensure(2)
	case "ul", "ol":
		if len(w.lists) > 0 {
			w.lists = w.lists[:len(w.lists)-1]
		}
		w.inLI = len(w.lists) > 0
		if len(w.lists) == 0 {
			w.ensure(2)
		} else {
			w.ensure(1)
		}
	case "li":
		w.ensure(1)
		w.marker = ""
	case "b", "strong":
		w.sb.WriteString("**")
	case "i", "em":
		w.sb.WriteString("_")
	case "code", "kbd", "samp", "tt":
		if w.pre == 0 {
			w.sb.WriteString("`")
		}
	case "pre":
		if w.pre > 0 {
			w.pre--
			w.ensure(1)
			w.nl = 1
			w.content("```")
			w.ensure(2)
		}
	case "a":
		if len(w.linkStack) == 0 {
			return
		}
		l := w.linkStack[len(w.linkStack)-1]
		w.linkStack = w.linkStack[:len(w.linkStack)-1]
		if l.href == "" {
			return
		}
		if w.sb.Len() == l.start {
			w.sb.WriteString(l.href)
		}
		w.sb.WriteString("](" + l.href + ")")
	}
}

func (w *mdWriter) tableStart(t token) {
	ts := w.table
	switch t.name {
	case "table":
		w.tableDepth++
	case "tr":
		if w.tableDepth == 1 {
			w.closeRow()
			ts.row = []string{}
		}
	case "td", "th":
		if w.tableDepth == 1 {
			w.closeCell()
			if ts.row == nil {
				ts.row = []string{}
			}
			ts.cell = &strings.Builder{}
		} else if ts.cell != nil {
			ts.cell.WriteByte(' ')
		}
	case "br", "p", "div":
		if ts.cell != nil {
			ts.cell.WriteByte(' ')
		}
	case "img":
		if alt := strings.TrimSpace(t.attrs["alt"]); alt != "" && ts.cell != nil {
			ts.cell.WriteString(alt)
		}
	}
}

func (w *mdWriter) tableEnd(name string) {
	switch name {
	case "table":
		w.tableDepth--
		if w.tableDepth <= 0 {
			w.flushTable()
		}
	case "td", "th":
		if w.tableDepth == 1 {
			w.closeCell()
		}
	case "tr":
		if w.tableDepth == 1 {
			w.closeRow()
		}
	}
}

func (w *mdWriter) closeCell() {
	ts := w.table
	if ts.cell == nil {
		return
	}
	c := strings.Join(strings.Fields(ts.cell.String()), " ")
	c = strings.ReplaceAll(c, "<", "&lt;")
	c = strings.ReplaceAll(c, "|", `\|`)
	ts.row = append(ts.row, c)
	ts.cell = nil
}

func (w *mdWriter) closeRow() {
	ts := w.table
	w.closeCell()
	if len(ts.row) > 0 {
		ts.rows = append(ts.rows, ts.row)
	}
	ts.row = nil
}

func (w *mdWriter) flushTable() {
	ts := w.table
	w.closeRow()
	w.table = nil
	w.tableDepth = 0
	cols := 0
	for _, r := range ts.rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return
	}
	// A one-column layout table is just text.
	if cols == 1 {
		for _, r := range ts.rows {
			if r[0] != "" {
				w.ensure(2)
				w.content(r[0])
			}
		}
		w.ensure(2)
		return
	}
	row := func(r []string) string {
		cells := make([]string, cols)
		copy(cells, r)
		return "| " + strings.Join(cells, " | ") + " |"
	}
	w.ensure(2)
	w.content(row(ts.rows[0]))
	sep := make([]string, cols)
	for i := range sep {
		sep[i] = "---"
	}
	w.ensure(1)
	w.content("| " + strings.Join(sep, " | ") + " |")
	for _, r := range ts.rows[1:] {
		w.ensure(1)
		w.content(row(r))
	}
	w.ensure(2)
}

// tableBlocks are elements that make a table a layout table.
var tableBlocks = map[string]bool{"table": true, "p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "hr": true, "section": true}

// simpleTable reports whether the table starting at toks[i] is a small data
// table (no nested tables or block elements, at least two columns) that can
// become a Markdown pipe table. Layout tables are rendered as blocks.
func simpleTable(toks []token, i int) bool {
	cols, maxCols := 0, 0
	for _, t := range toks[i+1:] {
		switch t.kind {
		case tStart:
			if tableBlocks[t.name] {
				return false
			}
			switch t.name {
			case "tr":
				cols = 0
			case "td", "th":
				cols++
				maxCols = max(maxCols, cols)
			}
		case tEnd:
			if t.name == "table" {
				return maxCols >= 2
			}
		}
	}
	return maxCols >= 2
}
