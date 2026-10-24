// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/settings"
)

func (m *coreModule) serveManifest(w http.ResponseWriter, r *http.Request) {
	area := manifestArea(r.URL.Query().Get("area"))
	doc, err := webAppManifest(r.Context(), m.d.Settings, area)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/manifest+json")
	h.Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(doc)
}

func manifestArea(area string) string {
	if area == "agent" {
		return "agent"
	}
	return "portal"
}

func webAppManifest(ctx context.Context, set *settings.Service, area string) (map[string]any, error) {
	b, err := set.Branding(ctx)
	if err != nil {
		return nil, err
	}
	sup, err := set.Support(ctx)
	if err != nil {
		return nil, err
	}
	name := b.ProductName
	if name == "" {
		name = "FeatherDesk"
	}
	start, scope, id := "/", "/", "/"
	if area == "agent" {
		// start_url must lie inside scope, and "/agent" is not inside "/agent/".
		start, scope, id = "/agent/", "/agent/", "/agent/"
	}
	primary := themeColor(b, area)
	m := map[string]any{
		"id": id, "start_url": start, "scope": scope,
		"name": name, "short_name": truncateRunes(name, 12),
		"display": "standalone", "theme_color": primary, "background_color": "#FFFFFF",
		"lang": sup.DefaultLocale,
		"icons": []map[string]any{
			{"src": pwaIconHref(b.PwaIcons.Icon192, "/pwa/icon-192.png"), "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": pwaIconHref(b.PwaIcons.Icon512, "/pwa/icon-512.png"), "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": pwaIconHref(b.PwaIcons.Maskable512, "/pwa/maskable-512.png"), "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	if sub := strings.TrimSpace(b.Login.Subtitle); sub != "" {
		m["description"] = sub
	}
	return m, nil
}

// themeColor is the area's primary colour (its override, else the base one) for the
// browser chrome of the installed app.
func themeColor(b settings.Branding, area string) string {
	if c := b.AreaColors[area].Primary; c != "" {
		return c
	}
	if b.Colors.Primary != "" {
		return b.Colors.Primary
	}
	return "#3F51B5"
}

func pwaIconHref(filename, fallback string) string {
	if filename == "" {
		return fallback
	}
	sha := filename
	if i := strings.Index(filename, "."); i > 0 {
		sha = filename[:i]
	}
	v := sha
	if len(v) > 8 {
		v = v[:8]
	}
	return "/branding/" + filename + "?v=" + v
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n >= max {
			return s[:i]
		}
		n++
	}
	return s
}
