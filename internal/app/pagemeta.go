// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
)

// pageMeta brands index.html for shared links: the product name, login
// subtitle and logo, or on a project entry page (/p/{key}) the project's
// portal title, description and logo. Link previews don't render SVG, so
// SVG logos fall back to the FeatherDesk PNG.
func pageMeta(set *settings.Service, publicURL string, project func(ctx context.Context, key string) (*directory.PortalProject, error)) func(*http.Request) server.PageMeta {
	base := strings.TrimRight(publicURL, "/")
	abs := func(u string) string {
		if base == "" {
			return ""
		}
		return base + u
	}
	return func(r *http.Request) server.PageMeta {
		b, _ := set.Branding(r.Context()) // defaults on error
		if b.ProductName == "" {
			b.ProductName = "FeatherDesk"
		}
		m := server.PageMeta{Title: b.ProductName, SiteName: b.ProductName, Description: b.Login.Subtitle, ImageURL: abs("/featherdesk-logo.png")}
		for _, slot := range []string{"logo_light", "logo_dark", "icon"} {
			if a := brandAsset(b, slot); a != "" && !strings.HasSuffix(strings.ToLower(a), ".svg") {
				m.ImageURL = abs("/branding/" + a)
				break
			}
		}
		for _, slot := range []string{"favicon", "icon"} {
			if a := brandAsset(b, slot); a != "" {
				m.IconURL = "/branding/" + a
				break
			}
		}
		if key, ok := strings.CutPrefix(r.URL.Path, "/p/"); ok && project != nil && key != "" && !strings.Contains(key, "/") {
			if p, _ := project(r.Context(), key); p != nil {
				m.Title = p.Name
				if p.PortalTitle != nil && *p.PortalTitle != "" {
					m.Title = *p.PortalTitle
				}
				if p.Description != "" {
					m.Description = p.Description
				}
				if p.LogoURL != nil && !strings.HasSuffix(strings.ToLower(*p.LogoURL), ".svg") {
					m.ImageURL = abs(*p.LogoURL)
				}
			}
		}
		return m
	}
}

// brandAsset returns the "<sha>.<ext>" of a branding slot, "" when unset or malformed.
func brandAsset(b settings.Branding, slot string) string {
	a := b.Assets[slot]
	if a == "" || strings.ContainsAny(a, "/\\?#") {
		return ""
	}
	return a
}
