// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func brandingBody(primary string) map[string]any {
	return map[string]any{"product_name": "Acme Help", "colors": map[string]any{"primary": primary}, "default_theme": "system", "default_locale": "en"}
}

func (e *env) uploadAsset(p *domain.Principal, slot, name string, content []byte) resp {
	body, ct := multipartBody(name, content)
	return e.do(p, "POST", "/api/v1/admin/branding/assets/"+slot, body, "Content-Type", ct)
}

func testJPEG(t *testing.T, w, h int, exif bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if exif {
		app1 := append([]byte{0xFF, 0xE1, 0x00, 0x16}, []byte("Exif\x00\x00GPS-SECRET-DATA!")...)
		b = append(append(append([]byte{}, b[:2]...), app1...), b[2:]...)
	}
	return b
}

func TestBranding_AdminOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.do(e.staff(domain.RoleSupervisor, 1), "PUT", "/api/v1/admin/branding", brandingBody("#3F51B5")).expect(t, 403)
		e.do(e.customer("", domain.RoleRequester), "GET", "/api/v1/admin/branding", nil).expect(t, 404)
		adm := e.staff(domain.RoleAdmin, 1)
		r := e.do(adm, "PUT", "/api/v1/admin/branding", brandingBody("#3F51B5")).expect(t, 200).m(t)
		if r["settings"].(map[string]any)["product_name"] != "Acme Help" {
			t.Fatalf("saved %v", r)
		}
		e.do(adm, "GET", "/api/v1/admin/branding", nil).expect(t, 200)
	})
}

func TestBranding_InvalidColour422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		e.do(adm, "PUT", "/api/v1/admin/branding", brandingBody("blue")).expect(t, 422)
		b := brandingBody("#3F51B5")
		b["support_url"] = "javascript:alert(1)"
		e.do(adm, "PUT", "/api/v1/admin/branding", b).expect(t, 422)
	})
}

func TestBranding_ContrastWarning(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		r := e.do(adm, "PUT", "/api/v1/admin/branding", brandingBody("#ffff00")).expect(t, 200).m(t)
		ws := r["warnings"].([]any)
		if len(ws) == 0 || ws[0].(map[string]any)["code"] != "contrast" {
			t.Fatalf("warnings %v", ws)
		}
		g := e.do(adm, "GET", "/api/v1/admin/branding", nil).expect(t, 200).m(t)
		if g["colors"].(map[string]any)["primary"] != "#ffff00" {
			t.Fatal("not saved")
		}
		if c := contrastRatio("#FFFFFF", "#000000"); c < 20.9 || c > 21.1 {
			t.Fatalf("contrast formula %f", c)
		}
	})
}

func TestBrandingAsset_Over1MB413(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, MaxBrandingAsset+10)...)
		e.uploadAsset(adm, "logo_light", "big.png", big).expect(t, 413)
	})
}

func TestBrandingAsset_SVGScriptStripped(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		svg := `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" onload="alert(1)" viewBox="0 0 10 10">` +
			`<script>alert(2)</script><foreignObject><div>x</div></foreignObject>` +
			`<a href="javascript:alert(3)"><rect width="10" height="10" fill="url(https://evil.test/x)" onclick="alert(4)"/></a>` +
			`<use xlink:href="https://evil.test/s.svg#x"/><circle cx="5" cy="5" r="4" fill="#ff0000"/></svg>`
		r := e.uploadAsset(adm, "logo_light", "logo.svg", []byte(svg)).expect(t, 201).m(t)
		got := e.do(adm, "GET", r["url"].(string), nil)
		if got.StatusCode != 200 {
			t.Fatalf("serve %d", got.StatusCode)
		}
		body := string(got.body)
		for _, bad := range []string{"script", "alert", "onload", "onclick", "foreignObject", "evil.test", "javascript"} {
			if strings.Contains(body, bad) {
				t.Fatalf("%q survived: %s", bad, body)
			}
		}
		if !strings.Contains(body, "<circle") {
			t.Fatalf("safe content dropped: %s", body)
		}
	})
}

func TestBrandingAsset_SVGEmailLogoRefused422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		e.uploadAsset(adm, "email_logo", "l.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)).expect(t, 422)
		e.uploadAsset(adm, "email_logo", "l.jpg", testJPEG(t, 20, 10, false)).expect(t, 201)
	})
}

func TestBrandingAsset_MetadataStripped(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		src := testJPEG(t, 1200, 600, true)
		if !bytes.Contains(src, []byte("GPS-SECRET-DATA")) {
			t.Fatal("fixture lacks EXIF")
		}
		r := e.uploadAsset(adm, "logo_light", "l.jpg", src).expect(t, 201).m(t)
		if r["width"] != float64(256) || r["height"] != float64(128) {
			t.Fatalf("not scaled into 512×128: %v×%v", r["width"], r["height"])
		}
		got := e.do(adm, "GET", r["url"].(string), nil)
		if bytes.Contains(got.body, []byte("Exif")) || bytes.Contains(got.body, []byte("GPS-SECRET")) {
			t.Fatal("metadata survived")
		}
	})
}

func TestBrandingAsset_URLChangesWithContent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		a := e.uploadAsset(adm, "icon", "a.jpg", testJPEG(t, 32, 32, false)).expect(t, 201).m(t)
		b := e.uploadAsset(adm, "icon", "b.jpg", testJPEG(t, 40, 32, false)).expect(t, 201).m(t)
		if a["url"] == b["url"] {
			t.Fatal("URL did not change with content")
		}
		e.do(adm, "GET", a["url"].(string), nil).expect(t, 404) // old blob removed
		g := e.do(adm, "GET", "/api/v1/admin/branding", nil).m(t)
		if g["assets"].(map[string]any)["icon"].(map[string]any)["url"] != b["url"] {
			t.Fatal("settings not pointing at the new asset")
		}
		f := e.uploadAsset(adm, "favicon", "f.jpg", testJPEG(t, 300, 300, false)).expect(t, 201).m(t)
		if f["width"] != float64(64) || e.count(`SELECT COUNT(*) FROM branding_assets WHERE slot = 'favicon'`) != 2 {
			t.Fatal("favicon variants missing")
		}
	})
}

func TestBrandingAsset_ResetRestoresDefault(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		adm := e.staff(domain.RoleAdmin, 1)
		a := e.uploadAsset(adm, "logo_dark", "a.jpg", testJPEG(t, 32, 32, false)).expect(t, 201).m(t)
		e.do(adm, "DELETE", "/api/v1/admin/branding/assets/logo_dark", nil).expect(t, 204)
		g := e.do(adm, "GET", "/api/v1/admin/branding", nil).expect(t, 200).m(t)
		if g["assets"].(map[string]any)["logo_dark"] != nil {
			t.Fatal("asset still set")
		}
		e.do(adm, "GET", a["url"].(string), nil).expect(t, 404)
		b, _ := e.set.Branding(context.Background())
		if _, ok := b.Assets["logo_dark"]; ok {
			t.Fatal("settings still reference the asset")
		}
	})
}

func TestBranding_ChangePushedOnMeTopic(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		// The same wiring app.Build installs.
		e.set.OnChange(func(key string) {
			if key == settings.KeyBranding {
				e.hub.Broadcast(realtime.Frame{Type: "event", Event: "settings.branding_changed"})
			}
		})
		adm := e.staff(domain.RoleAdmin, 1)
		cust := e.customer("", domain.RoleRequester)
		c := e.wsDial(cust)
		c.subscribe("s", "me")
		e.do(adm, "PUT", "/api/v1/admin/branding", brandingBody("#3F51B5")).expect(t, 200)
		if c.waitFor(3*time.Second, func(f map[string]any) bool { return f["event"] == "settings.branding_changed" && f["topic"] == "me" }) == nil {
			t.Fatal("no settings.branding_changed on me")
		}
	})
}
