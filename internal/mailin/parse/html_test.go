// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHTMLToMarkdownGolden: HTML-only fixtures (marketing mail here; the
// Outlook fixture is covered by TestParseCorpus) (NOT-025).
func TestHTMLToMarkdownGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/html/*.html")
	if len(files) == 0 {
		t.Fatal("no html fixtures")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".html")
		golden(t, filepath.Join("testdata", "golden", "html_"+name+".md"), HTMLToMarkdown(string(b))+"\n")
	}
}

// TestHTMLToMarkdownStripsActive: <script>, <style>, on*= and javascript:
// links never survive (NOT-025).
func TestHTMLToMarkdownStripsActive(t *testing.T) {
	inputs := []string{
		`<script>alert(1)</script>hello`,
		`<SCRIPT type="x">alert(1)</SCRIPT >hello`,
		`<style>body{}</style><p onclick="alert(1)">hello</p>`,
		`<a href="javascript:alert(1)">hello</a>`,
		`<a href="java&#x09;script:alert(1)">hello</a>`,
		`<a href=" javascript:alert(1)">hello</a>`,
		`<a href="data:text/html,<script>alert(1)</script>">hello</a>`,
		`<img src=x onerror=alert(1)>hello`,
		`<p>&lt;script&gt;alert(1)&lt;/script&gt;hello</p>`,
		`<svg><script>alert(1)</script></svg>hello`,
		`<iframe src="https://evil"></iframe>hello`,
		`<a href="vbscript:x">hello</a>`,
		`<div style="background:url(javascript:alert(1))">hello</div>`,
		`<scr<script>ipt>alert(1)</script>hello`,
	}
	for _, in := range inputs {
		out := HTMLToMarkdown(in)
		low := strings.ToLower(out)
		for _, bad := range []string{"<script", "</script", "<style", "onclick", "onerror", "javascript:", "vbscript:", "data:text", "<iframe", "<svg", "alert(1)>"} {
			if strings.Contains(low, bad) {
				t.Errorf("%q → %q contains %q", in, out, bad)
			}
		}
		if !strings.Contains(out, "hello") {
			t.Errorf("%q → %q lost the text", in, out)
		}
	}
}

func TestHTMLToMarkdownElements(t *testing.T) {
	cases := map[string]string{
		`<h2>Title</h2><p>a <b>b</b> <i>c</i></p>`:                                  "## Title\n\na **b** _c_",
		`<ul><li>x</li><li>y</li></ul>`:                                             "- x\n- y",
		`<ol><li>x</li><li>y</li></ol>`:                                             "1. x\n2. y",
		`<p>a<br>b</p>`:                                                             "a\nb",
		`<a href="https://e.com/a b">e</a>`:                                         "[e](https://e.com/ab)",
		`<a href="https://e.com"></a>`:                                              "[https://e.com](https://e.com)",
		`<blockquote><p>q1</p><p>q2</p></blockquote>after`:                          "> q1\n>\n> q2\n\nafter",
		`<table><tr><td>a</td><td>b</td></tr><tr><td>1</td><td>2</td></tr></table>`: "| a | b |\n| --- | --- |\n| 1 | 2 |",
		`plain &amp; simple &euro;`:                                                 "plain & simple €",
		`<pre>x  y</pre>`:                                                           "```\nx  y\n```",
		``:                                                                          "",
	}
	for in, want := range cases {
		if got := HTMLToMarkdown(in); got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
}
