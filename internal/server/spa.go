// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// spaHandler serves the embedded Vite build: hashed files under /assets/ are
// immutable; everything else (index.html, favicon) is revalidated. Unknown
// non-API GET paths fall back to index.html (history-mode routing). A
// pre-compressed .gz sibling is served when the client accepts gzip.
// index.html gets the branded title and link-preview tags (PageMeta).
type spaHandler struct {
	fs   fs.FS
	meta func(r *http.Request) (PageMeta, bool)
}

func newSPA(fsys fs.FS, meta func(r *http.Request) (PageMeta, bool)) http.Handler {
	if fsys == nil {
		return nil
	}
	return &spaHandler{fs: fsys, meta: meta}
}

// PageMeta is what a shared link shows in Slack, Teams, WhatsApp and the
// like. Crawlers don't run the SPA, so it is written into index.html.
type PageMeta struct {
	Title          string
	SiteName       string
	Description    string
	ImageURL       string // absolute (og:image); "" leaves it out
	IconURL        string // favicon; "" keeps the built-in one
	ManifestArea   string // agent | portal; "" skips the manifest link
	ThemeColor     string
	AppleTouchIcon string
	ShortName      string // apple-mobile-web-app-title (≤12 runes)
}

var (
	titleTag    = regexp.MustCompile(`<title>[^<]*</title>`)
	faviconTag  = regexp.MustCompile(`<link[^>]*id="fd-favicon"[^>]*>`)
	manifestTag = regexp.MustCompile(`<link[^>]*id="fd-manifest"[^>]*>`)
)

// injectMeta replaces the static <title> with the branded one plus the
// description, Open Graph and Twitter tags, and swaps the favicon.
func injectMeta(page []byte, m PageMeta) []byte {
	var tags strings.Builder
	fmt.Fprintf(&tags, "<title>%s</title>", html.EscapeString(m.Title))
	for _, t := range [][3]string{
		{"name", "description", m.Description},
		{"property", "og:type", "website"},
		{"property", "og:site_name", m.SiteName},
		{"property", "og:title", m.Title},
		{"property", "og:description", m.Description},
		{"property", "og:image", m.ImageURL},
		{"name", "twitter:card", "summary"},
	} {
		if t[2] != "" {
			fmt.Fprintf(&tags, "\n    <meta %s=\"%s\" content=\"%s\" />", t[0], t[1], html.EscapeString(t[2]))
		}
	}
	shortTitle := m.ShortName
	if shortTitle == "" {
		shortTitle = m.SiteName
	}
	if shortTitle != "" {
		fmt.Fprintf(&tags, "\n    <meta name=\"apple-mobile-web-app-title\" content=\"%s\" />", html.EscapeString(shortTitle))
	}
	if m.ManifestArea != "" {
		fmt.Fprintf(&tags, "\n    <link rel=\"manifest\" id=\"fd-manifest\" href=\"/manifest.webmanifest?area=%s\" />", html.EscapeString(m.ManifestArea))
	}
	if m.ThemeColor != "" {
		fmt.Fprintf(&tags, "\n    <meta name=\"theme-color\" content=\"%s\" />", html.EscapeString(m.ThemeColor))
	}
	if m.AppleTouchIcon != "" {
		fmt.Fprintf(&tags, "\n    <link rel=\"apple-touch-icon\" href=\"%s\" />", html.EscapeString(m.AppleTouchIcon))
	}
	fmt.Fprintf(&tags, "\n    <meta name=\"apple-mobile-web-app-capable\" content=\"yes\" />")
	fmt.Fprintf(&tags, "\n    <meta name=\"mobile-web-app-capable\" content=\"yes\" />")
	if m.ManifestArea != "" {
		// The static link (index.html, for the Vite dev server) gives way to the per-area one written above.
		page = manifestTag.ReplaceAllLiteral(page, nil)
	}
	out := titleTag.ReplaceAllLiteral(page, []byte(tags.String()))
	if m.IconURL != "" {
		out = faviconTag.ReplaceAllLiteral(out, []byte(`<link rel="icon" id="fd-favicon" href="`+html.EscapeString(m.IconURL)+`" />`))
	}
	return out
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" || strings.HasSuffix(r.URL.Path, "/") {
		p = "index.html"
	}
	if !h.serveFile(w, r, p) {
		// History fallback; but a missing hashed asset is a real 404.
		if strings.HasPrefix(p, "assets/") || path.Ext(p) != "" && p != "index.html" {
			http.NotFound(w, r)
			return
		}
		if !h.serveFile(w, r, "index.html") {
			http.NotFound(w, r)
		}
	}
}

func (h *spaHandler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	st, err := fs.Stat(h.fs, name)
	if err != nil || st.IsDir() {
		return false
	}
	hdr := w.Header()
	if name == "index.html" && h.meta != nil {
		if m, ok := h.meta(r); ok {
			b, err := fs.ReadFile(h.fs, name)
			if err != nil {
				return false
			}
			hdr.Set("Cache-Control", "no-cache")
			hdr.Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(injectMeta(b, m)))
			return true
		}
	}
	if strings.HasPrefix(name, "assets/") {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hdr.Set("Cache-Control", "no-cache")
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	hdr.Set("Content-Type", ct)
	ae := r.Header.Get("Accept-Encoding")
	for _, enc := range []struct{ token, ext, name string }{{"br", ".br", "br"}, {"gzip", ".gz", "gzip"}} {
		if !strings.Contains(ae, enc.token) {
			continue
		}
		if b, err := fs.ReadFile(h.fs, name+enc.ext); err == nil {
			hdr.Set("Content-Encoding", enc.name)
			hdr.Add("Vary", "Accept-Encoding")
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
			return true
		}
	}
	f, err := h.fs.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // read-only file, content already served
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, time.Time{}, rs)
		return true
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	return true
}
