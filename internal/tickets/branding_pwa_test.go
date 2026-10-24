// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func testPNG(t *testing.T, size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pwaMultipart(t *testing.T, fields map[string][]byte, source string) ([]byte, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, data := range fields {
		p, err := w.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Write(data)
	}
	if source != "" {
		_ = w.WriteField("source", source)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func TestBrandingPwaIcons_UploadAndReset(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		body := map[string][]byte{
			"icon_192": testPNG(t, 192), "icon_512": testPNG(t, 512),
			"maskable_512": testPNG(t, 512), "apple_180": testPNG(t, 180),
		}
		raw, ct := pwaMultipart(t, body, "default")
		r := e.do(adm, "POST", "/api/v1/admin/branding/pwa-icons", raw, "Content-Type", ct).expect(t, 200).m(t)
		if r["icon_192"] == nil || r["source"] != "default" {
			t.Fatalf("response %v", r)
		}
		if e.count(`SELECT COUNT(*) FROM branding_assets WHERE variant LIKE 'pwa-%'`) != 4 {
			t.Fatal("variants missing")
		}
		if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'branding.pwa_icons_updated'`) != 1 {
			t.Fatal("no audit")
		}
		g := e.do(adm, "GET", "/api/v1/admin/branding", nil).expect(t, 200).m(t)
		pwa := g["pwa_icons"].(map[string]any)
		if pwa["icon_192"] == nil {
			t.Fatalf("settings %v", g)
		}
		url := "/branding/" + pwa["icon_192"].(string)
		if e.do(adm, "GET", url, nil).StatusCode != 200 {
			t.Fatal("serve custom icon")
		}
		// Uploading a new icon slot must not remove PWA variants.
		e.uploadAsset(adm, "icon", "a.png", testPNG(t, 64)).expect(t, 201)
		if e.count(`SELECT COUNT(*) FROM branding_assets WHERE variant LIKE 'pwa-%'`) != 4 {
			t.Fatal("pwa wiped by icon upload")
		}
		e.do(adm, "DELETE", "/api/v1/admin/branding/pwa-icons", nil).expect(t, 204)
		if e.count(`SELECT COUNT(*) FROM branding_assets WHERE variant LIKE 'pwa-%'`) != 0 {
			t.Fatal("not deleted")
		}
		b, _ := e.set.Branding(context.Background())
		if b.PwaIcons.Icon192 != "" {
			t.Fatal("settings not cleared")
		}
	})
}

func TestBrandingPwaIcons_InvalidSize422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		body := map[string][]byte{
			"icon_192": testPNG(t, 191), "icon_512": testPNG(t, 512),
			"maskable_512": testPNG(t, 512), "apple_180": testPNG(t, 180),
		}
		raw, ct := pwaMultipart(t, body, "")
		e.do(adm, "POST", "/api/v1/admin/branding/pwa-icons", raw, "Content-Type", ct).expect(t, 422)
	})
}

func TestBrandingPwaIcons_ResetIconSlotKeepsPwa(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		body := map[string][]byte{
			"icon_192": testPNG(t, 192), "icon_512": testPNG(t, 512),
			"maskable_512": testPNG(t, 512), "apple_180": testPNG(t, 180),
		}
		raw, ct := pwaMultipart(t, body, "default")
		e.do(adm, "POST", "/api/v1/admin/branding/pwa-icons", raw, "Content-Type", ct).expect(t, 200)
		e.uploadAsset(adm, "icon", "i.png", testPNG(t, 64)).expect(t, 201)
		e.do(adm, "DELETE", "/api/v1/admin/branding/assets/icon", nil).expect(t, 204)
		if e.count(`SELECT COUNT(*) FROM branding_assets WHERE variant LIKE 'pwa-%'`) != 4 {
			t.Fatal("reset icon slot removed pwa icons")
		}
	})
}
