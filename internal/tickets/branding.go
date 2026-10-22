// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Branding settings API (TKT-036, branding_decision). Settings live under
// settings.KeyBranding (default locale and locales under settings.KeySupport);
// a write fires settings.OnChange, which the platform turns into a
// settings.branding_changed frame on every socket's `me` topic. Assets are
// sanitised (SVG) or re-encoded (PNG/JPEG, metadata stripped), stored in the
// blob store under branding/<sha256>.<ext> and served by the platform at
// /branding/<sha256>.<ext>.

// MaxBrandingAsset is the upload cap per asset.
const MaxBrandingAsset = 1 << 20

var brandingSlots = []string{"logo_light", "logo_dark", "icon", "favicon", "email_logo", "login_background"}

// slotMax is the largest box each slot is scaled into (never upscaled).
var slotMax = map[string][2]int{
	"logo_light": {512, 128}, "logo_dark": {512, 128}, "icon": {256, 256}, "favicon": {64, 64},
	"email_logo": {600, 200}, "login_background": {1920, 1080},
}

func (m *Module) mountBranding(g server.Groups) {
	g.Admin.Method(http.MethodGet, "/branding", handler(m.getBranding))
	g.Admin.Method(http.MethodPut, "/branding", handler(m.putBranding))
	up := g.Admin.With(server.MaxBody(MaxBrandingAsset + 64<<10))
	up.Method(http.MethodPost, "/branding/assets/{slot}", handler(m.uploadBrandingAsset))
	g.Admin.Method(http.MethodDelete, "/branding/assets/{slot}", handler(m.resetBrandingAsset))
	// D-20: the portal logo of a project (/p/{key}), project admins too.
	g.ProjectAdmin.With(server.MaxBody(MaxBrandingAsset+64<<10)).Method(http.MethodPost, "/projects/{id}/logo", handler(m.uploadProjectLogo))
	g.ProjectAdmin.Method(http.MethodDelete, "/projects/{id}/logo", handler(m.deleteProjectLogo))
}

type colorSet struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
	Accent    string `json:"accent,omitempty"`
}

type brandingAssetDTO struct {
	Slot        string `json:"slot"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Width       *int   `json:"width"`
	Height      *int   `json:"height"`
	SHA256      string `json:"sha256"`
}

type brandingDTO struct {
	ProductName  string    `json:"product_name"`
	CompanyName  string    `json:"company_name"`
	SupportURL   *string   `json:"support_url"`
	Colors       colorSet  `json:"colors"`
	PortalColors *colorSet `json:"portal_colors"`
	AgentColors  *colorSet `json:"agent_colors"`
	DefaultTheme string    `json:"default_theme"`
	Radius       string    `json:"radius"`
	Font         string    `json:"font"`
	Login        struct {
		Title           string  `json:"title"`
		Subtitle        string  `json:"subtitle"`
		BackgroundColor *string `json:"background_color"`
	} `json:"login"`
	Portal struct {
		WelcomeMD        string `json:"welcome_md"`
		NewTicketIntroMD string `json:"new_ticket_intro_md"`
	} `json:"portal"`
	Email struct {
		HeaderColor string `json:"header_color"`
		FooterMD    string `json:"footer_md"`
	} `json:"email"`
	Legal struct {
		PrivacyURL *string `json:"privacy_url"`
		TermsURL   *string `json:"terms_url"`
		ImprintURL *string `json:"imprint_url"`
	} `json:"legal"`
	DefaultLocale    string                       `json:"default_locale"`
	AvailableLocales []string                     `json:"available_locales"`
	PoweredBy        bool                         `json:"powered_by"`
	CustomCSS        string                       `json:"custom_css"`
	Assets           map[string]*brandingAssetDTO `json:"assets,omitempty"`
}

func optURL(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func colorsOut(c settings.Colors) colorSet {
	return colorSet{Primary: c.Primary, Secondary: c.Secondary, Accent: c.Accent}
}

func (s *Service) brandingDTO(ctx context.Context) (*brandingDTO, error) {
	b, err := s.set.Branding(ctx)
	if err != nil {
		return nil, err
	}
	sup := s.dir.Support(ctx)
	d := &brandingDTO{ProductName: b.ProductName, CompanyName: b.CompanyName, SupportURL: optURL(b.SupportURL), Colors: colorsOut(b.Colors),
		DefaultTheme: b.DefaultTheme, Radius: b.Radius, Font: b.Font, PoweredBy: b.PoweredBy, CustomCSS: b.CustomCSS,
		DefaultLocale: sup.DefaultLocale, AvailableLocales: sup.Locales}
	if d.AvailableLocales == nil {
		d.AvailableLocales = slices.Clone(domain.Locales)
	}
	if c, ok := b.AreaColors["portal"]; ok && c.Primary != "" {
		cs := colorsOut(c)
		d.PortalColors = &cs
	}
	if c, ok := b.AreaColors["agent"]; ok && c.Primary != "" {
		cs := colorsOut(c)
		d.AgentColors = &cs
	}
	d.Login.Title, d.Login.Subtitle, d.Login.BackgroundColor = b.Login.Title, b.Login.Subtitle, optURL(b.Login.BackgroundColor)
	d.Portal.WelcomeMD, d.Portal.NewTicketIntroMD = b.Portal.WelcomeMD, b.Portal.NewTicketIntroMD
	d.Email.HeaderColor, d.Email.FooterMD = b.Email.HeaderColor, b.Email.FooterMD
	if d.Email.HeaderColor == "" {
		d.Email.HeaderColor = b.Colors.Primary
	}
	d.Legal.PrivacyURL, d.Legal.TermsURL, d.Legal.ImprintURL = optURL(b.Legal.PrivacyURL), optURL(b.Legal.TermsURL), optURL(b.Legal.ImprintURL)
	d.Assets = map[string]*brandingAssetDTO{}
	for _, slot := range brandingSlots {
		d.Assets[slot] = nil
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT slot, content_type, width, height, sha256, blob_key FROM branding_assets WHERE variant = ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a brandingAssetDTO
		var w, h int
		var key string
		if err := rows.Scan(&a.Slot, &a.ContentType, &w, &h, &a.SHA256, &key); err != nil {
			return nil, err
		}
		if w > 0 {
			a.Width, a.Height = &w, &h
		}
		a.URL = "/branding/" + strings.TrimPrefix(key, "branding/")
		d.Assets[a.Slot] = &a
	}
	return d, rows.Err()
}

func (m *Module) getBranding(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpRead) != nil {
		return problems.Forbidden("")
	}
	d, err := m.svc.brandingDTO(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validateBranding returns field errors and contrast warnings.
func validateBranding(in *brandingDTO) (errs, warnings []problems.FieldError) {
	length := func(f, v string, lo, hi int) {
		if n := utf8.RuneCountInString(v); n < lo || n > hi {
			errs = append(errs, problems.Field(f, "length", fmt.Sprintf("%d..%d characters", lo, hi)))
		}
	}
	colour := func(f, v string, required bool) {
		if v == "" && !required {
			return
		}
		if !hexColor.MatchString(v) {
			errs = append(errs, problems.Field(f, "color", "#RRGGBB"))
		}
	}
	link := func(f string, v *string) {
		if v != nil && *v != "" && !validURL(*v) {
			errs = append(errs, problems.Field(f, "url", "an http(s) URL"))
		}
	}
	in.ProductName = strings.TrimSpace(in.ProductName)
	length("product_name", in.ProductName, 1, 60)
	length("company_name", in.CompanyName, 0, 100)
	link("support_url", in.SupportURL)
	link("legal.privacy_url", in.Legal.PrivacyURL)
	link("legal.terms_url", in.Legal.TermsURL)
	link("legal.imprint_url", in.Legal.ImprintURL)
	sets := map[string]*colorSet{"colors": &in.Colors, "portal_colors": in.PortalColors, "agent_colors": in.AgentColors}
	for name, cs := range sets {
		if cs == nil {
			continue
		}
		colour(name+".primary", cs.Primary, true)
		colour(name+".secondary", cs.Secondary, false)
		colour(name+".accent", cs.Accent, false)
	}
	if in.Login.BackgroundColor != nil {
		colour("login.background_color", *in.Login.BackgroundColor, false)
	}
	colour("email.header_color", in.Email.HeaderColor, false)
	if !slices.Contains([]string{"light", "dark", "system"}, in.DefaultTheme) {
		errs = append(errs, problems.Field("default_theme", "enum", "light, dark or system"))
	}
	if in.Radius == "" {
		in.Radius = "default"
	}
	if !slices.Contains([]string{"sharp", "default", "rounded"}, in.Radius) {
		errs = append(errs, problems.Field("radius", "enum", "sharp, default or rounded"))
	}
	if in.Font == "" {
		in.Font = "system"
	}
	if !slices.Contains([]string{"system", "inter", "roboto", "source-sans", "noto-sans"}, in.Font) {
		errs = append(errs, problems.Field("font", "enum", "unknown font"))
	}
	length("login.title", in.Login.Title, 0, 200)
	length("login.subtitle", in.Login.Subtitle, 0, 300)
	length("portal.welcome_md", in.Portal.WelcomeMD, 0, 10000)
	length("portal.new_ticket_intro_md", in.Portal.NewTicketIntroMD, 0, 10000)
	length("email.footer_md", in.Email.FooterMD, 0, 5000)
	if len(in.CustomCSS) > 20480 {
		errs = append(errs, problems.Field("custom_css", "max_length", "at most 20 KB"))
	}
	if !domain.ValidLocale(in.DefaultLocale) {
		errs = append(errs, problems.Field("default_locale", "enum", "unknown locale"))
	}
	for i, l := range in.AvailableLocales {
		if !domain.ValidLocale(l) {
			errs = append(errs, problems.Field(fmt.Sprintf("available_locales[%d]", i), "enum", "unknown locale"))
		}
	}
	if len(in.AvailableLocales) > 0 && !slices.Contains(in.AvailableLocales, in.DefaultLocale) {
		errs = append(errs, problems.Field("available_locales", "default_missing", "must include default_locale"))
	}
	if len(errs) > 0 {
		return errs, nil
	}
	// WCAG 2.x: white text on the brand colours (buttons, headers) needs 4.5:1.
	check := func(f, bg string) {
		if bg == "" {
			return
		}
		if c := contrastRatio("#FFFFFF", bg); c < 4.5 {
			warnings = append(warnings, problems.Field(f, "contrast", fmt.Sprintf("white text on %s has a contrast of %.2f:1 (WCAG AA needs 4.5:1)", bg, c)))
		}
	}
	check("colors.primary", in.Colors.Primary)
	check("colors.secondary", in.Colors.Secondary)
	check("email.header_color", in.Email.HeaderColor)
	return nil, warnings
}

// relativeLuminance per WCAG 2.x.
func relativeLuminance(hex string) float64 {
	var r, g, b int
	_, _ = fmt.Sscanf(strings.TrimPrefix(hex, "#"), "%02x%02x%02x", &r, &g, &b)
	lin := func(c int) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio is the WCAG contrast of two #RRGGBB colours.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func (m *Module) putBranding(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	var in brandingDTO
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	errs, warnings := validateBranding(&in)
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	ctx := r.Context()
	s := m.svc
	cur, err := s.set.Branding(ctx)
	if err != nil {
		return err
	}
	b := cur
	b.ProductName, b.CompanyName, b.SupportURL = in.ProductName, in.CompanyName, deref(in.SupportURL)
	b.Colors = settings.Colors{Primary: in.Colors.Primary, Secondary: in.Colors.Secondary, Accent: in.Colors.Accent}
	b.AreaColors = map[string]settings.Colors{}
	if in.PortalColors != nil {
		b.AreaColors["portal"] = settings.Colors{Primary: in.PortalColors.Primary, Secondary: in.PortalColors.Secondary, Accent: in.PortalColors.Accent}
	}
	if in.AgentColors != nil {
		b.AreaColors["agent"] = settings.Colors{Primary: in.AgentColors.Primary, Secondary: in.AgentColors.Secondary, Accent: in.AgentColors.Accent}
	}
	b.DefaultTheme, b.Radius, b.Font = in.DefaultTheme, in.Radius, in.Font
	b.Login.Title, b.Login.Subtitle, b.Login.BackgroundColor = in.Login.Title, in.Login.Subtitle, deref(in.Login.BackgroundColor)
	b.Portal.WelcomeMD, b.Portal.NewTicketIntroMD = in.Portal.WelcomeMD, in.Portal.NewTicketIntroMD
	b.Email.HeaderColor, b.Email.FooterMD = in.Email.HeaderColor, in.Email.FooterMD
	if b.Email.HeaderColor == "" {
		b.Email.HeaderColor = b.Colors.Primary // e-mail header follows the primary colour by default
	}
	b.Legal.PrivacyURL, b.Legal.TermsURL, b.Legal.ImprintURL = deref(in.Legal.PrivacyURL), deref(in.Legal.TermsURL), deref(in.Legal.ImprintURL)
	b.PoweredBy, b.CustomCSS = in.PoweredBy, in.CustomCSS
	p := principal(r)
	sup := s.dir.Support(ctx)
	if sup.DefaultLocale != in.DefaultLocale || (len(in.AvailableLocales) > 0 && !slices.Equal(sup.Locales, in.AvailableLocales)) {
		sup.DefaultLocale = in.DefaultLocale
		if len(in.AvailableLocales) > 0 {
			sup.Locales = in.AvailableLocales
		}
		if err := s.set.Put(ctx, settings.KeySupport, sup, p.UserID); err != nil {
			return err
		}
	}
	if err := s.set.Put(ctx, settings.KeyBranding, b, p.UserID); err != nil {
		return err
	}
	d, err := s.brandingDTO(ctx)
	if err != nil {
		return err
	}
	if warnings == nil {
		warnings = []problems.FieldError{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": d, "warnings": warnings})
	return nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- assets ----

var errNotImage = errors.New("unsupported image")

func (m *Module) uploadBrandingAsset(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	slot := chi.URLParam(r, "slot")
	if !slices.Contains(brandingSlots, slot) {
		return problems.Validation(problems.Field("slot", "enum", "unknown slot"))
	}
	data, err := readUpload(r)
	if err != nil {
		return err
	}
	a, err := m.svc.storeBrandingAsset(r.Context(), slot, data, principal(r).UserID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, a)
	return nil
}

// readUpload reads the `file` part of a multipart upload (≤ MaxBrandingAsset).
func readUpload(r *http.Request) ([]byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, problems.Validation(problems.Field("file", "required", "multipart/form-data with a file part"))
	}
	var data []byte
	for {
		part, err := mr.NextPart()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, problems.PayloadTooLarge()
			}
			return nil, problems.Validation(problems.Field("file", "required", "a file part is required"))
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		// ≤ 1 MiB is held in memory for decoding (bounded by the cap).
		data, err = io.ReadAll(io.LimitReader(part, MaxBrandingAsset+1))
		_ = part.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return nil, problems.PayloadTooLarge()
			}
			return nil, err
		}
		break
	}
	if len(data) > MaxBrandingAsset {
		return nil, problems.PayloadTooLarge()
	}
	return data, nil
}

// adminProjectOf loads {id} for a caller who may change its portal settings
// (D-20): unknown projects and projects the caller does not administer are
// 404, a PAT without the admin scope 403.
func (m *Module) adminProjectOf(r *http.Request) (string, error) {
	p := principal(r)
	pr, err := m.svc.dir.Project(r.Context(), chi.URLParam(r, "id"))
	if err != nil || !p.AdminsProject(pr.ID) {
		return "", problems.NotFound()
	}
	if authz.CanProjectConfig(p, authz.PCPortal, pr.ID) != nil {
		return "", problems.Forbidden("token lacks admin")
	}
	return pr.ID, nil
}

// uploadProjectLogo stores the project's portal logo like the logo_light
// brand asset, as branding_assets variant "project:<id>".
func (m *Module) uploadProjectLogo(w http.ResponseWriter, r *http.Request) error {
	id, err := m.adminProjectOf(r)
	if err != nil {
		return err
	}
	data, err := readUpload(r)
	if err != nil {
		return err
	}
	assets, err := prepareAsset("logo_light", data)
	if err != nil {
		return err
	}
	a := assets[0]
	ctx := r.Context()
	s := m.svc
	sum := sha256.Sum256(a.data)
	sha := hex.EncodeToString(sum[:])
	key := "branding/" + sha + "." + a.ext
	if _, err := s.blob.Put(ctx, key, bytes.NewReader(a.data), int64(len(a.data)), a.ctype); err != nil {
		return err
	}
	variant := "project:" + id
	var old []string
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		var prev string
		if err := tx.QueryRowContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = 'logo_light' AND variant = ?`, variant).Scan(&prev); err == nil {
			old = append(old, prev)
		}
		var assetID string
		if err := tx.QueryRowContext(ctx, `INSERT INTO branding_assets (id, slot, variant, blob_key, content_type, width, height, size_bytes, sha256, created_at)
			VALUES (?, 'logo_light', ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (slot, variant) DO UPDATE SET blob_key = excluded.blob_key, content_type = excluded.content_type, width = excluded.width,
				height = excluded.height, size_bytes = excluded.size_bytes, sha256 = excluded.sha256, created_at = excluded.created_at
			RETURNING id`, store.NewID(), variant, key, a.ctype, a.w, a.h, len(a.data), sha, store.Now()).Scan(&assetID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE projects SET logo_asset_id = ?, updated_at = ? WHERE id = ?`, assetID, store.Now(), id)
		return err
	})
	if err != nil {
		return err
	}
	s.dir.Invalidate()
	s.deleteUnreferencedBlobs(ctx, old)
	out, err := s.dir.ProjectAdminJSON(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// deleteProjectLogo removes the project's logo: the portal falls back to the
// global one.
func (m *Module) deleteProjectLogo(w http.ResponseWriter, r *http.Request) error {
	id, err := m.adminProjectOf(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	s := m.svc
	var keys []string
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		var k string
		if err := tx.QueryRowContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = 'logo_light' AND variant = ?`, "project:"+id).Scan(&k); err == nil {
			keys = append(keys, k)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET logo_asset_id = NULL, updated_at = ? WHERE id = ?`, store.Now(), id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE slot = 'logo_light' AND variant = ?`, "project:"+id)
		return err
	})
	if err != nil {
		return err
	}
	s.dir.Invalidate()
	s.deleteUnreferencedBlobs(ctx, keys)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type encodedAsset struct {
	variant string
	data    []byte
	ctype   string
	ext     string
	w, h    int
}

// prepareAsset sniffs, sanitises or re-encodes an upload for slot.
func prepareAsset(slot string, data []byte) ([]encodedAsset, error) {
	ct := http.DetectContentType(data)
	isSVG := strings.Contains(ct, "svg") || (strings.HasPrefix(ct, "text/xml") || strings.HasPrefix(ct, "text/plain")) && bytes.Contains(data[:min(len(data), 1024)], []byte("<svg"))
	switch {
	case isSVG:
		if slot == "email_logo" {
			// Mail clients do not render SVG safely or at all.
			return nil, problems.Validation(problems.Field("file", "svg_not_allowed", "the e-mail logo must be PNG or JPEG"))
		}
		clean, err := sanitizeSVG(data)
		if err != nil {
			return nil, problems.Validation(problems.Field("file", "invalid_svg", "the SVG could not be parsed"))
		}
		return []encodedAsset{{data: clean, ctype: "image/svg+xml", ext: "svg"}}, nil
	case ct == "image/webp":
		// No stdlib WebP decoder (no new dependency, D-01): refuse.
		return nil, problems.Validation(problems.Field("file", "unsupported_type", "WebP cannot be processed; upload PNG, JPEG or SVG"))
	case ct == "image/png" || ct == "image/jpeg":
	default:
		return nil, problems.Validation(problems.Field("file", "unsupported_type", "upload PNG, JPEG or SVG (got "+ct+")"))
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, problems.Validation(problems.Field("file", "invalid_image", "the image could not be decoded"))
	}
	box := slotMax[slot]
	out := []encodedAsset{}
	encode := func(variant string, bw, bh int) error {
		scaled := scaleToFit(img, bw, bh)
		var buf bytes.Buffer
		ea := encodedAsset{variant: variant, w: scaled.Bounds().Dx(), h: scaled.Bounds().Dy()}
		if ct == "image/jpeg" {
			ea.ctype, ea.ext = "image/jpeg", "jpg"
			if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 90}); err != nil {
				return err
			}
		} else {
			ea.ctype, ea.ext = "image/png", "png"
			if err := png.Encode(&buf, scaled); err != nil {
				return err
			}
		}
		ea.data = buf.Bytes()
		out = append(out, ea)
		return nil
	}
	if err := encode("", box[0], box[1]); err != nil {
		return nil, err
	}
	if slot == "favicon" {
		if err := encode("apple", 180, 180); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scaleToFit downscales img into bw×bh with an area-average filter (never
// upscales); the result is a fresh RGBA image, so no metadata survives.
func scaleToFit(img image.Image, bw, bh int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := math.Min(1, math.Min(float64(bw)/float64(w), float64(bh)/float64(h)))
	nw, nh := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	fx, fy := float64(w)/float64(nw), float64(h)/float64(nh)
	for y := 0; y < nh; y++ {
		y0, y1 := int(float64(y)*fy), max(int(float64(y)*fy)+1, int(float64(y+1)*fy))
		for x := 0; x < nw; x++ {
			x0, x1 := int(float64(x)*fx), max(int(float64(x)*fx)+1, int(float64(x+1)*fx))
			var r, g, bb, a, n uint64
			for sy := y0; sy < y1 && sy < h; sy++ {
				for sx := x0; sx < x1 && sx < w; sx++ {
					cr, cg, cb, ca := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bb, a, n = r+uint64(cr), g+uint64(cg), bb+uint64(cb), a+uint64(ca), n+1
				}
			}
			if n == 0 {
				n = 1
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bb / n), uint16(a / n)})
		}
	}
	return dst
}

func (s *Service) storeBrandingAsset(ctx context.Context, slot string, data []byte, actor string) (*brandingAssetDTO, error) {
	assets, err := prepareAsset(slot, data)
	if err != nil {
		return nil, err
	}
	var main *brandingAssetDTO
	var oldKeys []string
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = ? AND variant NOT LIKE 'project:%'`, slot)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			oldKeys = append(oldKeys, k)
		}
		rows.Close()
		for _, a := range assets {
			sum := sha256.Sum256(a.data)
			sha := hex.EncodeToString(sum[:])
			key := "branding/" + sha + "." + a.ext
			if _, err := s.blob.Put(ctx, key, bytes.NewReader(a.data), int64(len(a.data)), a.ctype); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO branding_assets (id, slot, variant, blob_key, content_type, width, height, size_bytes, sha256, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (slot, variant) DO UPDATE SET blob_key = excluded.blob_key, content_type = excluded.content_type, width = excluded.width,
					height = excluded.height, size_bytes = excluded.size_bytes, sha256 = excluded.sha256, created_at = excluded.created_at`,
				store.NewID(), slot, a.variant, key, a.ctype, a.w, a.h, len(a.data), sha, store.Now()); err != nil {
				return err
			}
			if a.variant == "" {
				main = &brandingAssetDTO{Slot: slot, URL: "/branding/" + sha + "." + a.ext, ContentType: a.ctype, SHA256: sha}
				if a.w > 0 {
					w, h := a.w, a.h
					main.Width, main.Height = &w, &h
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	b, err := s.set.Branding(ctx)
	if err != nil {
		return nil, err
	}
	if b.Assets == nil {
		b.Assets = map[string]string{}
	}
	b.Assets[slot] = strings.TrimPrefix(main.URL, "/branding/")
	if err := s.set.Put(ctx, settings.KeyBranding, b, actor); err != nil {
		return nil, err
	}
	s.deleteUnreferencedBlobs(ctx, oldKeys)
	return main, nil
}

// deleteUnreferencedBlobs removes branding blobs no row points at any more.
func (s *Service) deleteUnreferencedBlobs(ctx context.Context, keys []string) {
	for _, k := range keys {
		var n int
		if err := s.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM branding_assets WHERE blob_key = ?`, k).Scan(&n); err == nil && n == 0 {
			if err := s.blob.Delete(context.WithoutCancel(ctx), k); err != nil && !errors.Is(err, blob.ErrNotFound) {
				s.log.Warn("branding: blob delete", "key", k, "err", err)
			}
		}
	}
}

func (m *Module) resetBrandingAsset(w http.ResponseWriter, r *http.Request) error {
	if authz.CanAdmin(principal(r), authz.ResBranding, authz.OpWrite) != nil {
		return problems.Forbidden("")
	}
	slot := chi.URLParam(r, "slot")
	if !slices.Contains(brandingSlots, slot) {
		return problems.Validation(problems.Field("slot", "enum", "unknown slot"))
	}
	ctx := r.Context()
	s := m.svc
	var keys []string
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM branding_assets WHERE slot = ? AND variant NOT LIKE 'project:%'`, slot)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			keys = append(keys, k)
		}
		rows.Close()
		_, err = tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE slot = ? AND variant NOT LIKE 'project:%'`, slot)
		return err
	})
	if err != nil {
		return err
	}
	b, err := s.set.Branding(ctx)
	if err != nil {
		return err
	}
	delete(b.Assets, slot)
	if err := s.set.Put(ctx, settings.KeyBranding, b, principal(r).UserID); err != nil {
		return err
	}
	s.deleteUnreferencedBlobs(ctx, keys)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- SVG sanitiser ----

var svgElements = map[string]bool{
	"svg": true, "g": true, "path": true, "rect": true, "circle": true, "ellipse": true, "line": true, "polyline": true,
	"polygon": true, "text": true, "tspan": true, "defs": true, "lineargradient": true, "radialgradient": true, "stop": true,
	"title": true, "desc": true, "clippath": true, "mask": true, "use": true, "symbol": true, "pattern": true,
}

var svgAttrs = map[string]bool{
	"id": true, "class": true, "d": true, "x": true, "y": true, "x1": true, "x2": true, "y1": true, "y2": true, "cx": true, "cy": true,
	"r": true, "rx": true, "ry": true, "width": true, "height": true, "viewbox": true, "points": true, "fill": true, "fill-opacity": true,
	"fill-rule": true, "clip-rule": true, "stroke": true, "stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true,
	"stroke-opacity": true, "stroke-dasharray": true, "stroke-miterlimit": true, "opacity": true, "transform": true, "offset": true,
	"stop-color": true, "stop-opacity": true, "gradientunits": true, "gradienttransform": true, "preserveaspectratio": true,
	"font-family": true, "font-size": true, "font-weight": true, "text-anchor": true, "dominant-baseline": true, "clip-path": true,
	"mask": true, "version": true, "xmlns": true, "patternunits": true, "fx": true, "fy": true, "role": true, "aria-label": true,
	"href": true, "style": false,
}

var badValue = regexp.MustCompile(`(?i)(javascript:|data:|url\(\s*['"]?\s*[^#'"\s)])`)

// sanitizeSVG re-serialises an SVG keeping only allowlisted elements and
// attributes: no script, no event handlers, no foreignObject, no external
// references (href only to #fragments, url() only to #ids).
func sanitizeSVG(data []byte) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.Entity = map[string]string{}
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	skip := 0
	sawSVG := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if skip > 0 || !svgElements[name] {
				skip++
				continue
			}
			if name == "svg" {
				sawSVG = true
			}
			var attrs []xml.Attr
			for _, a := range t.Attr {
				an := strings.ToLower(a.Name.Local)
				if a.Name.Space == "xmlns" || (a.Name.Space == "" && an == "xmlns") {
					continue // the encoder re-emits namespaces
				}
				if strings.HasPrefix(an, "on") || !svgAttrs[an] || badValue.MatchString(a.Value) {
					continue
				}
				if an == "href" && !strings.HasPrefix(strings.TrimSpace(a.Value), "#") {
					continue
				}
				attrs = append(attrs, xml.Attr{Name: xml.Name{Local: a.Name.Local}, Value: a.Value})
			}
			el := xml.StartElement{Name: xml.Name{Local: t.Name.Local}, Attr: attrs}
			if name == "svg" {
				el.Attr = append(el.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			if err := enc.EncodeToken(el); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: t.Name.Local}}); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skip == 0 {
				if err := enc.EncodeToken(t); err != nil {
					return nil, err
				}
			}
		}
		// Comments, processing instructions and directives (DOCTYPE, entities) are dropped.
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	if !sawSVG {
		return nil, errNotImage
	}
	return out.Bytes(), nil
}
