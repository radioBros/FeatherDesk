// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bufio"
	"bytes"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/radioBros/FeatherDesk/api"
)

// GET /api/docs (getApiDocs, CON-011): a static API reference rendered once
// from the embedded spec. It has no script and no external asset, so it works
// offline and under a strict CSP; the full machine-readable document stays at
// /api/v1/openapi.yaml. yaml.v3 is test-only (D-01), so the spec is read with
// the same indentation scanner style as authn's x-scopes index.

type docOp struct {
	Method, Path, Summary, ID, Phase string
	Scopes                           []string
}

type docTag struct {
	Name string
	Ops  []docOp
}

var (
	apiDocsOnce sync.Once
	apiDocsHTML []byte
)

var docMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true}

// parseDocOps lists the operations under `paths:` with their first tag.
func parseDocOps(spec []byte) (title, version string, tags []docTag) {
	sc := bufio.NewScanner(bytes.NewReader(spec))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	byTag := map[string][]docOp{}
	section, path, tag, list := "", "", "", ""
	var cur *docOp
	flush := func() {
		if cur != nil {
			t := tag
			if t == "" {
				t = "Other"
			}
			byTag[t] = append(byTag[t], *cur)
			cur, tag = nil, ""
		}
	}
	unq := func(s string) string { return strings.Trim(strings.TrimSpace(s), `"'`) }
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := strings.TrimSpace(line)
		if indent == 0 {
			flush()
			section = strings.TrimSuffix(text, ":")
			continue
		}
		if section == "info" && indent == 2 {
			if k, v, ok := strings.Cut(text, ":"); ok {
				switch k {
				case "title":
					title = unq(v)
				case "version":
					version = unq(v)
				}
			}
			continue
		}
		if section != "paths" {
			continue
		}
		switch {
		case indent == 2 && strings.HasSuffix(text, ":"):
			flush()
			path = unq(strings.TrimSuffix(text, ":"))
		case indent == 4 && strings.HasSuffix(text, ":"):
			flush()
			if m := strings.TrimSuffix(text, ":"); docMethods[m] {
				cur = &docOp{Method: strings.ToUpper(m), Path: path}
			}
		case cur != nil && indent == 6:
			list = ""
			k, v, _ := strings.Cut(text, ":")
			switch k {
			case "summary":
				cur.Summary = unq(v)
			case "operationId":
				cur.ID = unq(v)
			case "x-phase":
				cur.Phase = unq(v)
			case "tags", "x-scopes":
				list = k
			}
		case cur != nil && indent == 8 && strings.HasPrefix(text, "- ") && list != "":
			v := unq(text[2:])
			if list == "tags" && tag == "" {
				tag = v
			} else if list == "x-scopes" {
				cur.Scopes = append(cur.Scopes, v)
			}
		}
	}
	flush()
	names := make([]string, 0, len(byTag))
	for n := range byTag {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		tags = append(tags, docTag{Name: n, Ops: byTag[n]})
	}
	return title, version, tags
}

var apiDocsTpl = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} API reference</title>
<style>
:root{color-scheme:light dark;--fg:#1d1d1f;--muted:#5f6368;--bg:#fff;--line:#e3e3e3;--code:#f5f5f7}
@media (prefers-color-scheme:dark){:root{--fg:#e8e8e8;--muted:#a0a0a0;--bg:#16171a;--line:#2c2d31;--code:#212226}}
body{margin:0 auto;max-width:960px;padding:24px 16px;font:15px/1.5 system-ui,sans-serif;color:var(--fg);background:var(--bg)}
h1{font-size:1.6em;margin:0 0 4px}h2{margin-top:2em;border-bottom:1px solid var(--line);padding-bottom:4px}
p.lead{color:var(--muted)}nav a{margin-right:12px;white-space:nowrap}
table{width:100%;border-collapse:collapse}td{padding:6px 8px;border-bottom:1px solid var(--line);vertical-align:top}
code{background:var(--code);padding:1px 4px;border-radius:4px;font-size:.92em;word-break:break-all}
.m{font-weight:600;font-family:ui-monospace,monospace;width:4.5em}.s{color:var(--muted);font-size:.9em}
</style></head><body>
<h1>{{.Title}} API</h1>
<p class="lead">Version {{.Version}}. Paths are relative to <code>/api/v1</code> unless they start with a root route. Download the full OpenAPI 3.1 document: <a href="/api/v1/openapi.yaml">openapi.yaml</a>.</p>
<nav>{{range .Tags}}<a href="#{{.Name}}">{{.Name}}</a> {{end}}</nav>
{{range .Tags}}<h2 id="{{.Name}}">{{.Name}}</h2><table>
{{range .Ops}}<tr><td class="m">{{.Method}}</td><td><code>{{.Path}}</code><br>{{.Summary}} <span class="s">{{.ID}}{{if .Phase}} · {{.Phase}}{{end}}{{if .Scopes}} · scopes: {{range $i, $s := .Scopes}}{{if $i}}, {{end}}{{$s}}{{end}}{{end}}</span></td></tr>
{{end}}</table>{{end}}
</body></html>
`))

func renderAPIDocs() []byte {
	title, version, tags := parseDocOps(api.Spec)
	if title == "" {
		title = "FeatherDesk"
	}
	var b bytes.Buffer
	_ = apiDocsTpl.Execute(&b, map[string]any{"Title": title, "Version": version, "Tags": tags})
	return b.Bytes()
}

func serveAPIDocs(w http.ResponseWriter, _ *http.Request) {
	apiDocsOnce.Do(func() { apiDocsHTML = renderAPIDocs() })
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	_, _ = w.Write(apiDocsHTML)
}
