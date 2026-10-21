// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// ---- KB-006: rendering ----

func TestKBRender_ScriptStripped(t *testing.T) {
	r := newRenderer(1 << 20)
	for _, md := range []string{
		"<script>alert(1)</script>\n\nhello",
		"<img src=x onerror=alert(1)>",
		"[click](javascript:alert(1))",
		"<iframe src=https://evil.example></iframe>",
		"<a href=\"#\" onclick=\"steal()\">x</a>",
	} {
		html := r.RenderString(md)
		low := strings.ToLower(html)
		for _, bad := range []string{"<script", "onerror", "javascript:", "<iframe", "onclick"} {
			if strings.Contains(low, bad) {
				t.Errorf("%q rendered %q (contains %s)", md, html, bad)
			}
		}
	}
}

func TestKBRender_ExternalImageStripped(t *testing.T) {
	r := newRenderer(1 << 20)
	asset := "/api/v1/kb/assets/0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f"
	html := r.RenderString("![tracker](https://evil.example/p.png) ![data](data:image/png;base64,AAAA) ![ok](" + asset + ")")
	if strings.Contains(html, "evil.example") || strings.Contains(html, "data:image") {
		t.Fatalf("external image kept: %s", html)
	}
	if !strings.Contains(html, `src="`+asset+`"`) {
		t.Fatalf("KB asset image dropped: %s", html)
	}
	if !strings.Contains(html, "tracker") {
		t.Fatalf("alt text of a stripped image should remain: %s", html)
	}
	// GFM tables, autolinks and heading anchors survive sanitising.
	html = r.RenderString("# Getting Started\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nsee https://example.com")
	for _, want := range []string{`<h1 id="getting-started">`, "<table>", `href="https://example.com"`, `rel="nofollow`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
}

// TestKBRender_CacheBoundedSweep: rendering 5k distinct articles keeps the
// render cache ≤ 4 MB (KB-006 memory bound).
func TestKBRender_CacheBoundedSweep(t *testing.T) {
	r := newRenderer(renderCacheBytes)
	body := strings.Repeat("Paragraph with **bold** text and a [link](https://example.com).\n\n", 30)
	now := time.Now()
	for i := 0; i < 5000; i++ {
		r.Render(fmt.Sprintf("article-%d", i), "en", now, body)
		if b := r.cache.Bytes(); b > renderCacheBytes {
			t.Fatalf("render cache %d bytes after %d articles", b, i)
		}
	}
	if r.cache.Len() == 0 || r.cache.Len() >= 5000 {
		t.Fatalf("cache should hold a bounded subset, holds %d", r.cache.Len())
	}
	// A changed updated_at is a new key: the edit is never served stale.
	a := r.Render("x", "en", now, "old")
	b := r.Render("x", "en", now.Add(time.Second), "new")
	if a == b {
		t.Fatal("updated_at must invalidate the cached render")
	}
}

func BenchmarkKBRender(b *testing.B) {
	r := newRenderer(renderCacheBytes)
	body := strings.Repeat("Some *markdown* with `code` and a table\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n", 20)
	now := time.Now()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Render(fmt.Sprint(i%5000), "en", now, body)
	}
	b.ReportMetric(float64(r.cache.Bytes()), "cache-bytes")
}

// ---- KB-006: assets ----

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{1}, 64)...)

func multipartBody(t *testing.T, filename string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func (e *env) upload(who, filename string, data []byte) resp {
	e.t.Helper()
	body, ct := multipartBody(e.t, filename, data)
	return e.do("POST", "/api/v1/agent/kb/assets", who, body, "Content-Type", ct)
}

func TestKBAsset_SVGRefused422(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
		e.want(e.upload("agent", "logo.svg", svg), 422)
		e.want(e.upload("agent", "logo.png", svg), 422)                                     // sniffed, not trusted by name
		e.want(e.upload("agent", "evil.svg", pngBytes), 422)                                // .svg name refused even with PNG bytes
		e.want(e.upload("agent", "doc.pdf", []byte("%PDF-1.4 hello")), 422)                 // not an image
		e.want(e.upload("agent", "big.png", append(pngBytes, make([]byte, 1<<20)...)), 413) // ST_MAX_UPLOAD_MB=1
		r := e.upload("agent", "shot.png", pngBytes)
		e.want(r, 201)
		var out assetCreated
		r.json(t, &out)
		if out.URL != AssetPathPrefix+out.ID {
			t.Fatalf("asset url %q", out.URL)
		}
		var key string
		if err := e.db.Read().QueryRowContext(context.Background(), `SELECT blob_key FROM kb_assets WHERE id = ?`, out.ID).Scan(&key); err != nil || !strings.HasPrefix(key, "kb/") {
			t.Fatalf("blob key %q, %v", key, err)
		}
		e.guard("POST", "/api/v1/agent/kb/assets", "customer", nil, 404)
	})
}

func TestKBAsset_InternalImageNeedsStaff(t *testing.T) {
	engines(t, func(t *testing.T, e *env) {
		var internal, public, orphan assetCreated
		for _, a := range []*assetCreated{&internal, &public, &orphan} {
			r := e.upload("agent", "img.png", pngBytes)
			e.want(r, 201)
			r.json(t, a)
		}
		inCat := e.category("staff-only", VisInternal, "")
		e.article(inCat, "internal-howto", VisInternal, "published", "Internal", "![diagram]("+internal.URL+")")
		pubCat := e.category("open-docs", VisPublic, "")
		e.article(pubCat, "public-howto", VisPublic, "published", "Public", "![shot]("+public.URL+")")
		// Internal image: anonymous and customers get 404, staff 200.
		e.want(e.do("GET", internal.URL, "", nil), 404)
		e.want(e.do("GET", internal.URL, "customer", nil), 404)
		r := e.do("GET", internal.URL, "agent", nil)
		e.want(r, 200)
		if r.Header().Get("Content-Type") != "image/png" || !bytes.Equal(r.Body.Bytes(), pngBytes) {
			t.Fatalf("asset body/type wrong: %s", r.Header().Get("Content-Type"))
		}
		// Public image: anonymous 200, immutable cache.
		r = e.do("GET", public.URL, "", nil)
		e.want(r, 200)
		if cc := r.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Fatalf("public asset Cache-Control %q", cc)
		}
		// Unreferenced (or draft-only) images are staff-only.
		e.want(e.do("GET", orphan.URL, "", nil), 404)
		e.want(e.do("GET", orphan.URL, "agent", nil), 200)
		// Archiving the public article hides its image again.
		var id string
		_ = e.db.Read().QueryRowContext(context.Background(), `SELECT id FROM kb_articles WHERE slug = 'public-howto'`).Scan(&id)
		e.want(e.do("PATCH", "/api/v1/agent/kb/articles/"+id, "supervisor", map[string]any{"status": "archived"}), 200)
		e.want(e.do("GET", public.URL, "", nil), 404)
		// The editor links the asset to the article that first used it.
		var linked store.NullString
		_ = e.db.Read().QueryRowContext(context.Background(), `SELECT article_id FROM kb_assets WHERE id = ?`, public.ID).Scan(&linked)
		if linked.String != id {
			t.Fatalf("asset not linked to its article: %q", linked.String)
		}
	})
}
