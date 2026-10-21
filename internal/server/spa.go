// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// spaHandler serves the embedded Vite build: hashed files under /assets/ are
// immutable; everything else (index.html, favicon) is revalidated. Unknown
// non-API GET paths fall back to index.html (history-mode routing). A
// pre-compressed .gz sibling is served when the client accepts gzip.
type spaHandler struct {
	fs fs.FS
}

func newSPA(fsys fs.FS) http.Handler {
	if fsys == nil {
		return nil
	}
	return &spaHandler{fs: fsys}
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
