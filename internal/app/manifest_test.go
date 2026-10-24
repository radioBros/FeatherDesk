// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestWebAppManifest_PortalAndAgent(t *testing.T) {
	ctx := context.Background()
	set := settings.New(storetest.Open(t, store.SQLite), nil, func(string) string { return "" })
	b, _ := set.Branding(ctx)
	b.ProductName = "Globex Help Desk"
	b.Login.Subtitle = "We help"
	b.Colors.Primary = "#112233"
	b.PwaIcons = settings.BrandingPwaIcons{Icon192: "aa11bb22cc33dd44.png", Icon512: "bb22.png", Maskable512: "cc33.png"}
	if err := set.Put(ctx, settings.KeyBranding, b, ""); err != nil {
		t.Fatal(err)
	}
	portal, err := webAppManifest(ctx, set, "portal")
	if err != nil {
		t.Fatal(err)
	}
	if portal["start_url"] != "/" || portal["scope"] != "/" || portal["id"] != "/" {
		t.Fatalf("portal paths %v", portal)
	}
	if portal["short_name"] != "Globex Help " {
		t.Fatalf("short_name %q", portal["short_name"])
	}
	icons := portal["icons"].([]map[string]any)
	if icons[0]["src"] != "/branding/aa11bb22cc33dd44.png?v=aa11bb22" {
		t.Fatalf("icon cache bust %v", icons[0]["src"])
	}
	agent, err := webAppManifest(ctx, set, "agent")
	if err != nil || agent["start_url"] != "/agent/" || agent["scope"] != "/agent/" {
		t.Fatalf("agent %+v", agent)
	}
}

func TestServeManifest(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t, store.SQLite)
	set := settings.New(db, nil, func(string) string { return "" })
	m := &coreModule{d: &Deps{Settings: set}}
	req := httptest.NewRequest("GET", "/manifest.webmanifest?area=agent", nil)
	rec := httptest.NewRecorder()
	m.serveManifest(rec, req.WithContext(ctx))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("%d %s", rec.Code, rec.Header())
	}
	var doc map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc["start_url"] != "/agent/" {
		t.Fatalf("%v", doc)
	}
}
