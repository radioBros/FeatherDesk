// SPDX-License-Identifier: AGPL-3.0-only

package apispec

import (
	"html/template"
	"strings"
)

// docsTpl is the API reference page: one self-contained HTML document, no
// script and no external asset (served under default-src 'none').
var docsTpl = template.Must(template.New("docs").Funcs(template.FuncMap{
	"lower": strings.ToLower,
	"codeClass": func(code string) string {
		switch {
		case strings.HasPrefix(code, "2"):
			return "ok"
		case strings.HasPrefix(code, "3"):
			return "redir"
		case strings.HasPrefix(code, "4"):
			return "client"
		}
		return "server"
	},
	"pathHTML": func(p string) template.HTML {
		var b strings.Builder
		for {
			i := strings.Index(p, "{")
			j := strings.Index(p, "}")
			if i < 0 || j < i {
				b.WriteString(template.HTMLEscapeString(p))
				break
			}
			b.WriteString(template.HTMLEscapeString(p[:i]))
			b.WriteString(`<span class="pp">` + template.HTMLEscapeString(p[i:j+1]) + `</span>`)
			p = p[j+1:]
		}
		return template.HTML(b.String()) //nolint:gosec // escaped above
	},
}).Parse(docsHTML))

const docsHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} API reference</title>
<meta name="description" content="REST API reference of {{.Title}} {{.Version}}: every operation with parameters, request and response examples.">
<style>
:root{color-scheme:light dark;--fg:#1b1d21;--muted:#5d6470;--bg:#fff;--bg2:#f6f7f9;--line:#e3e6ea;--accent:#3b5bdb;--code:#eef0f3;
--get:#1971c2;--post:#2f9e44;--put:#e8590c;--patch:#e8590c;--delete:#c92a2a;--panel:#0f1218;--panel2:#171b23;--panelfg:#e6e9ef;--panelmuted:#9aa3b2}
@media (prefers-color-scheme:dark){:root{--fg:#e6e8ec;--muted:#9aa1ad;--bg:#111317;--bg2:#181b20;--line:#2a2e35;--accent:#7c9cff;--code:#20242b}}
*{box-sizing:border-box}html{scroll-padding-top:72px}
body{margin:0;font:15px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif;color:var(--fg);background:var(--bg)}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.88em}
code{background:var(--code);padding:1px 5px;border-radius:4px;overflow-wrap:anywhere}
header.top{position:sticky;top:0;z-index:5;display:flex;align-items:center;gap:12px;padding:10px 20px;background:var(--bg);border-bottom:1px solid var(--line)}
header.top .brand{font-weight:700;font-size:1.05em}header.top .ver{color:var(--muted);font-size:.85em}header.top .sp{flex:1}
.btn{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;border-radius:8px;border:1px solid var(--line);font-weight:600;font-size:.9em;color:var(--fg);background:var(--bg2)}
.btn.primary{background:var(--accent);border-color:var(--accent);color:#fff}.btn:hover{text-decoration:none;filter:brightness(1.05)}
.layout{display:grid;grid-template-columns:280px minmax(0,1fr)}
nav.side{position:sticky;top:53px;height:calc(100vh - 53px);overflow-y:auto;padding:16px 12px 40px;border-right:1px solid var(--line);background:var(--bg2);font-size:.88em}
nav.side h4{margin:16px 8px 4px;font-size:.75em;text-transform:uppercase;letter-spacing:.06em;color:var(--muted)}
nav.side a{display:flex;gap:8px;align-items:baseline;padding:3px 8px;border-radius:6px;color:var(--fg)}nav.side a:hover{background:var(--line);text-decoration:none}
nav.side a.tag{font-weight:600;margin-top:6px}nav.side .op{padding-left:16px;color:var(--muted)}nav.side .op span.s{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.mb{display:inline-block;min-width:3.6em;font:700 .72em/1.6 ui-monospace,monospace;text-align:center;border-radius:4px;color:#fff;padding:0 4px;flex:none}
.mb.GET{background:var(--get)}.mb.POST{background:var(--post)}.mb.PUT,.mb.PATCH{background:var(--patch)}.mb.DELETE{background:var(--delete)}
main{padding:24px 32px 120px;min-width:0}
main>section{max-width:1280px}
h1{font-size:2em;margin:.2em 0}h2{font-size:1.5em;margin:2.4em 0 .4em;padding-top:.4em;border-top:1px solid var(--line)}
h3{font-size:1.15em;margin:.2em 0 .4em}.lead{color:var(--muted);font-size:1.05em}
.callout{border:1px solid var(--line);background:var(--bg2);border-radius:10px;padding:14px 18px;margin:16px 0}
article.op{border:1px solid var(--line);border-radius:12px;margin:22px 0;overflow:hidden;background:var(--bg)}
article.op:target{box-shadow:0 0 0 2px var(--accent)}
.oph{display:flex;flex-wrap:wrap;align-items:center;gap:10px;padding:12px 16px;border-bottom:1px solid var(--line);background:var(--bg2)}
.oph code.path{background:none;padding:0;font-size:.95em;font-weight:600}.pp{color:var(--accent)}
.oph a.hash{margin-left:auto;color:var(--muted);font-size:.85em}
.opb{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,.95fr)}
.opl{padding:14px 18px;min-width:0}.opr{background:var(--panel);color:var(--panelfg);padding:14px 16px;min-width:0}
.meta{display:flex;flex-wrap:wrap;gap:6px;margin:4px 0 10px}
.chip{display:inline-block;font-size:.78em;padding:1px 8px;border-radius:999px;border:1px solid var(--line);color:var(--muted);background:var(--bg2)}
.chip.pub{border-color:#2f9e44;color:#2f9e44}.chip.scope{font-family:ui-monospace,monospace}
.lbl{font-size:.75em;text-transform:uppercase;letter-spacing:.06em;color:var(--muted);margin:16px 0 6px;font-weight:600}
table{width:100%;border-collapse:collapse;font-size:.9em}th{text-align:left;font-weight:600;color:var(--muted);font-size:.85em}
td,th{padding:6px 8px;border-bottom:1px solid var(--line);vertical-align:top}td p{margin:0}td p+p{margin-top:4px}
td.n{white-space:nowrap}td.n code{font-weight:600}.req{color:#c92a2a;font-size:.75em;font-weight:600;margin-left:4px}
.t{font-family:ui-monospace,monospace;font-size:.82em;color:var(--muted);overflow-wrap:break-word;min-width:6.5em}
.tw{overflow-x:auto}.c{color:var(--muted);font-size:.82em}
.d1 code{margin-left:12px}.d2 code{margin-left:24px}
.resp{display:flex;gap:10px;align-items:baseline;padding:6px 0;border-bottom:1px solid var(--line);font-size:.9em}
.resp p{margin:0}.code{font:700 .8em ui-monospace,monospace;padding:1px 6px;border-radius:4px;flex:none}
.code.ok{background:#d3f9d8;color:#1b5e20}.code.redir{background:#e7f5ff;color:#1864ab}.code.client{background:#fff3bf;color:#7a4b00}.code.server{background:#ffe3e3;color:#a61e1e}
@media (prefers-color-scheme:dark){.code.ok{background:#1b3b24;color:#8ce99a}.code.redir{background:#11304a;color:#74c0fc}.code.client{background:#3b2f0b;color:#ffd43b}.code.server{background:#3b1414;color:#ffa8a8}}
.opr .lbl{color:var(--panelmuted)}.opr pre{margin:0 0 12px;background:var(--panel2);border:1px solid #232835;border-radius:8px;padding:12px;overflow-x:auto;color:var(--panelfg);line-height:1.5;max-height:520px}
pre.light{background:var(--bg2);color:var(--fg);border:1px solid var(--line);border-radius:8px;padding:12px;overflow-x:auto}
.desc p:first-child{margin-top:0}.desc ul,.desc ol{padding-left:22px}
.codes{columns:3 180px;font-size:.88em}.codes code{display:inline-block;margin:2px 0}
.mobile-toc{display:none}
@media (max-width:1100px){.opb{grid-template-columns:1fr}}
@media (max-width:600px){.tw table{min-width:540px}}
@media (max-width:900px){.layout{grid-template-columns:1fr}nav.side{display:none}.mobile-toc{display:block}main{padding:16px 16px 80px}header.top{padding:10px 16px}header.top .ver,header.top .manual{display:none}}
</style></head><body>
<header class="top"><span class="brand">{{.Title}} API</span><span class="ver">v{{.Version}} · OpenAPI 3.1</span><span class="sp"></span>
<a class="btn manual" href="/agent">Open the app</a>
<a class="btn primary" href="/api/v1/openapi.yaml" download="featherdesk-openapi.yaml">⬇ Download openapi.yaml</a></header>
<div class="layout">
<nav class="side" aria-label="API sections">
<h4>Guide</h4>
<a href="#overview">Overview</a><a href="#authentication">Authentication</a><a href="#requests">Requests and pagination</a><a href="#errors">Errors</a><a href="#webhooks">Webhooks</a>
<h4>Reference ({{.OpCount}} operations)</h4>
{{range .Tags}}<a class="tag" href="#{{.Anchor}}">{{.Name}}</a>{{range .Ops}}<a class="op" href="#{{.Anchor}}"><span class="mb {{.Method}}">{{.Method}}</span><span class="s">{{if .SummaryText}}{{.Summary}}{{else}}{{.Path}}{{end}}</span></a>{{end}}{{end}}
</nav>
<main>
<section id="overview">
<h1>{{.Title}} API reference</h1>
<p class="lead">{{.Summary}}. Version {{.Version}}. Every operation below with its parameters, request body, responses and copy-ready examples.</p>
<div class="callout"><strong>Machine-readable spec.</strong> The full OpenAPI 3.1 document is at <a href="/api/v1/openapi.yaml" download="featherdesk-openapi.yaml"><code>/api/v1/openapi.yaml</code></a> — import it into Postman, Insomnia or Bruno, or generate a client with openapi-generator / oapi-codegen / openapi-typescript.</div>
<div class="desc">{{.Intro}}</div>
<div class="mobile-toc callout"><strong>Sections</strong>: <a href="#authentication">Authentication</a> · <a href="#errors">Errors</a> · <a href="#webhooks">Webhooks</a>{{range .Tags}} · <a href="#{{.Anchor}}">{{.Name}}</a>{{end}}</div>
</section>

<section id="authentication">
<h2>Authentication</h2>
<div class="tw"><table><thead><tr><th>Method</th><th>How</th><th>Use it for</th></tr></thead><tbody>
<tr><td class="n"><strong>Personal access token</strong></td><td><code>Authorization: Bearer fd_pat_…</code>. Create one under <em>Profile → API tokens</em> and choose its scopes. The token can do what its owner can, limited to the scopes listed on each operation.</td><td>Scripts, integrations, CI, AI assistants</td></tr>
<tr><td class="n"><strong>Session cookie</strong></td><td>The <code>fd_session</code> cookie set at sign-in. Every <code>POST</code>, <code>PUT</code>, <code>PATCH</code> and <code>DELETE</code> must also send <code>X-CSRF-Token</code> with the <code>csrf_token</code> value from <code>GET /api/v1/me</code>.</td><td>The web app, browser-based tools</td></tr>
</tbody></table></div>
<p class="lbl">Quick start</p>
<pre class="light">export FD=https://help.example.com
export TOKEN=fd_pat_…   # Profile → API tokens

curl "$FD/api/v1/me" -H "Authorization: Bearer $TOKEN"

curl -X POST "$FD/api/v1/agent/tickets" -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: 6f1c2b0e' \
  -d '{"subject":"VPN drops every 10 minutes","body_md":"Since this morning…","requester_email":"ada@example.com"}'</pre>
<p>Operations marked <span class="chip pub">public</span> need no authentication. Paths under <code>/api/v1/agent</code> are for staff, <code>/api/v1/portal</code> for customers and <code>/api/v1/admin</code> for administrators; other users get <code>404</code>.</p>
</section>

<section id="requests">
<h2>Requests and pagination</h2>
<ul>
<li><strong>Base URL</strong> <code>{{.Host}}{{.Base}}</code>. Requests and responses are JSON (<code>application/json</code>, UTF-8) unless an operation says otherwise; timestamps are RFC 3339 in UTC; IDs are UUIDs.</li>
<li><strong>Pagination</strong>: list operations take <code>?limit=</code> (1–200, default 50) and return <code>{"items": […], "next_cursor": "…"}</code>; pass <code>next_cursor</code> back as <code>?cursor=</code> until it is <code>null</code>.</li>
<li><strong>Idempotency</strong>: create operations accept <code>Idempotency-Key</code>. Retrying with the same key within 24 h returns the first response instead of creating a duplicate; the same key with a different body is a <code>422</code>.</li>
<li><strong>Concurrency</strong>: ticket responses carry an <code>ETag</code>; send it as <code>If-Match</code> on <code>PATCH</code> to get <code>412</code> instead of overwriting someone else's change.</li>
<li><strong>Rate limits</strong>: over the limit you get <code>429</code> with <code>Retry-After</code> (seconds).</li>
<li><strong>Realtime</strong>: <code>GET /api/v1/ws</code> upgrades to a WebSocket (subprotocol <code>featherdesk.v1</code>) that pushes ticket changes; writes always go through REST.</li>
</ul>
</section>

<section id="errors">
<h2>Errors</h2>
<p>Errors are <a href="https://www.rfc-editor.org/rfc/rfc9457">RFC 9457</a> problem documents (<code>application/problem+json</code>). Branch on <code>status</code> and the <code>type</code> slug; <code>title</code> and <code>detail</code> are for humans. Objects you cannot see answer <code>404</code>; actions you may not perform on objects you can see answer <code>403</code>.</p>
{{range .Problems}}<div id="{{.Anchor}}" class="resp" style="display:block"><p><span class="code {{if ge .Status 500}}server{{else}}client{{end}}">{{.Status}}</span> <strong>{{.Name}}</strong></p><div class="desc c">{{.Desc}}</div>
<pre class="light">HTTP/1.1 {{.Status}}
Content-Type: application/problem+json{{if .Header}}
{{.Header}}{{end}}

{{.Example}}</pre></div>
{{end}}
<p class="lbl">Field error codes (<code>errors[].code</code> on 422)</p>
<div class="codes">{{range .FieldCodes}}<code>{{.}}</code><br>{{end}}</div>
<p class="lbl">Problem types (<code>type</code> = <code>https://featherdesk.dev/problems/&lt;slug&gt;</code>)</p>
<div class="codes">{{range .ProblemSlugs}}<code>{{.}}</code><br>{{end}}</div>
</section>

{{range .Tags}}<section id="{{.Anchor}}">
<h2>{{.Name}}</h2>{{if .Desc}}<div class="desc lead">{{.Desc}}</div>{{end}}
{{range .Ops}}<article class="op" id="{{.Anchor}}">
<div class="oph"><span class="mb {{.Method}}">{{.Method}}</span><code class="path">{{pathHTML .Path}}</code><a class="hash" href="#{{.Anchor}}" aria-label="Link to {{.SummaryText}}">#</a></div>
<div class="opb"><div class="opl">
<h3>{{.Summary}}</h3>
<div class="meta">{{if .Public}}<span class="chip pub">public</span>{{else}}<span class="chip">auth: token or session</span>{{range .Scopes}}<span class="chip scope">{{.}}</span>{{end}}{{end}}{{if .Audience}}<span class="chip">{{.Audience}}</span>{{end}}{{if .WebSocket}}<span class="chip">WebSocket</span>{{end}}</div>
{{if .Desc}}<div class="desc">{{.Desc}}</div>{{end}}
{{if .Params}}<p class="lbl">Parameters</p><div class="tw"><table><thead><tr><th>Name</th><th>In</th><th>Type</th><th>Description</th></tr></thead><tbody>
{{range .Params}}<tr><td class="n"><code>{{.Name}}</code>{{if .Required}}<span class="req">required</span>{{end}}</td><td class="c">{{.In}}</td><td class="t">{{.Type}}</td><td>{{.Desc}}{{if .Constraints}}<div class="c">{{.Constraints}}</div>{{end}}</td></tr>
{{end}}</tbody></table></div>{{end}}
{{with .Body}}<p class="lbl">Request body · <code>{{.ContentType}}</code>{{if .Required}} · required{{end}}</p>{{if .Desc}}<div class="desc c">{{.Desc}}</div>{{end}}
{{if .Fields}}<div class="tw"><table><thead><tr><th>Field</th><th>Type</th><th>Description</th></tr></thead><tbody>
{{range .Fields}}<tr class="d{{.Depth}}"><td class="n"><code>{{.Name}}</code>{{if .Required}}<span class="req">required</span>{{end}}</td><td class="t">{{.Type}}</td><td>{{.Desc}}{{if .Constraints}}<div class="c">{{.Constraints}}</div>{{end}}</td></tr>
{{end}}</tbody></table></div>{{end}}{{end}}
<p class="lbl">Responses</p>
{{range .Responses}}<div class="resp"><span class="code {{codeClass .Code}}">{{.Code}}</span><div>{{.Desc}}{{if .Schema}}<div class="c">{{.ContentType}} · <span class="t">{{.Schema}}</span></div>{{end}}{{if .Headers}}<div class="c">Headers: {{range $i, $h := .Headers}}{{if $i}}, {{end}}<code>{{$h}}</code>{{end}}</div>{{end}}{{if .ErrorAnchor}}<div class="c"><a href="#{{.ErrorAnchor}}">Error format and example</a></div>{{end}}</div></div>
{{end}}
</div><div class="opr">
<p class="lbl">Request</p><pre>{{.Curl}}</pre>
{{with .Body}}{{if .Example}}<p class="lbl">Request body</p><pre>{{.Example}}</pre>{{end}}{{end}}
{{if .OKStatus}}<p class="lbl">Response · {{.OKStatus}}{{if .OKType}} · {{.OKType}}{{end}}</p>{{if .OKExample}}<pre>{{.OKExample}}</pre>{{else}}<pre>{{if eq .OKStatus "204"}}(no body){{else}}({{.OKType}}){{end}}</pre>{{end}}{{end}}
</div></div></article>
{{end}}</section>
{{end}}

<section id="webhooks">
<h2>Webhooks</h2>
<p>Admins register webhook URLs under <em>Administration → Webhooks</em> and pick events. FeatherDesk sends a <code>POST</code> with a JSON envelope; delivery is at least once (deduplicate on the envelope <code>id</code>), failed deliveries are retried with back-off, and a webhook is disabled after 20 consecutive failures or a <code>410 Gone</code>.</p>
<div class="tw"><table><thead><tr><th>Header</th><th>Value</th></tr></thead><tbody>
<tr><td class="n"><code>X-FeatherDesk-Event</code></td><td>The event type, e.g. <code>ticket.created</code></td></tr>
<tr><td class="n"><code>X-FeatherDesk-Delivery</code></td><td>Unique delivery id (UUID)</td></tr>
<tr><td class="n"><code>X-FeatherDesk-Signature</code></td><td><code>t=&lt;unix seconds&gt;,v1=&lt;hex HMAC-SHA256(secret, t + "." + body)&gt;</code>. Recompute it with the webhook's secret, compare in constant time and reject timestamps older than 5 minutes.</td></tr>
</tbody></table></div>
{{range .Webhooks}}<article class="op" id="{{.Anchor}}"><div class="oph"><span class="mb POST">EVENT</span><code class="path">{{.Name}}</code><a class="hash" href="#{{.Anchor}}">#</a></div>
<div class="opb"><div class="opl"><h3>{{.Summary}}</h3>{{if .When}}<p>{{.When}}</p>{{end}}{{if .Desc}}<div class="desc">{{.Desc}}</div>{{end}}
<p class="c">Headers <code>X-FeatherDesk-Event: {{.Name}}</code>, <code>X-FeatherDesk-Delivery</code>, <code>X-FeatherDesk-Signature</code>. <code>data.ticket</code> is the ticket after the change; <code>data.actor</code> is who made it (<code>null</code> for automatic changes).</p></div>
<div class="opr"><p class="lbl">Payload</p><pre>{{.Example}}</pre></div></div></article>
{{end}}
</section>
</main></div>
</body></html>
`
