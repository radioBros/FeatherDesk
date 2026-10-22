// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestPageMeta(t *testing.T) {
	ctx := context.Background()
	set := settings.New(storetest.Open(t, store.SQLite), nil, func(string) string { return "" })
	title, logo := "Acme Desk", "/branding/ffee.png"
	project := func(_ context.Context, key string) (*directory.PortalProject, error) {
		if key != "ACME" {
			return nil, nil
		}
		return &directory.PortalProject{Key: "ACME", Name: "Acme", Description: "Acme support", PortalTitle: &title, LogoURL: &logo}, nil
	}
	meta := pageMeta(set, "https://help.example.com/", project)

	// Unbranded: the FeatherDesk name and logo.
	m := meta(httptest.NewRequest("GET", "/", nil))
	if m.Title != "FeatherDesk" || m.SiteName != "FeatherDesk" || m.ImageURL != "https://help.example.com/featherdesk-logo.png" || m.IconURL != "" {
		t.Fatalf("default meta %+v", m)
	}

	b, _ := set.Branding(ctx)
	b.ProductName = "Globex Help"
	b.Login.Subtitle = "We are here to help"
	b.Assets = map[string]string{"logo_light": "aa11.svg", "logo_dark": "bb22.png", "favicon": "cc33.ico"}
	if err := set.Put(ctx, settings.KeyBranding, b, ""); err != nil {
		t.Fatal(err)
	}
	m = meta(httptest.NewRequest("GET", "/agent/tickets", nil))
	if m.Title != "Globex Help" || m.SiteName != "Globex Help" || m.Description != "We are here to help" {
		t.Fatalf("branded meta %+v", m)
	}
	if m.ImageURL != "https://help.example.com/branding/bb22.png" || m.IconURL != "/branding/cc33.ico" {
		t.Fatalf("SVG logo must be skipped for the preview image: %+v", m)
	}

	// A project entry page shows the project; the site name stays the brand.
	m = meta(httptest.NewRequest("GET", "/p/ACME", nil))
	if m.Title != "Acme Desk" || m.SiteName != "Globex Help" || m.Description != "Acme support" || m.ImageURL != "https://help.example.com/branding/ffee.png" {
		t.Fatalf("project meta %+v", m)
	}
	if m = meta(httptest.NewRequest("GET", "/p/NOPE", nil)); m.Title != "Globex Help" {
		t.Fatalf("unknown project must fall back to the brand: %+v", m)
	}
}
