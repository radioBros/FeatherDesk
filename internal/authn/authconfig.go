// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/notify"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/settings"
)

// authConfigCache is the serialised GET /auth/config body. It is rebuilt
// after a settings write or the end of first-run setup, so a cached request
// costs zero DB reads (AUTH-016).
type authConfigCache struct{ body []byte }

func (m *Module) invalidateAuthConfig() { m.cfgCache.Store(nil) }

func (m *Module) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	c := m.cfgCache.Load()
	if c == nil {
		v, err := m.buildAuthConfig(r.Context())
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		b, err := json.Marshal(v)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		c = &authConfigCache{body: b}
		m.cfgCache.Store(c)
	}
	body := c.body
	if key := r.URL.Query().Get("project"); key != "" && len(body) > 1 && body[len(body)-1] == '}' {
		// D-20: the /p/{key} portal entry also gets the project's branding
		// overrides (null when unknown, inactive or not portal-visible).
		var proj any
		if fn := publicProject.Load(); fn != nil && len(key) <= 36 {
			v, err := (*fn)(r.Context(), key)
			if err != nil {
				problems.Write(w, r, err)
				return
			}
			proj = v
		}
		pb, err := json.Marshal(proj)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		body = append(append(append(append([]byte{}, body[:len(body)-1]...), `,"project":`...), pb...), '}')
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// publicProject resolves the portal view of a project by key for
// GET /auth/config?project= (D-20). authn cannot import the directory:
// app/modules.go plugs in (*directory.Directory).PublicProject.
var publicProject atomic.Pointer[func(ctx context.Context, key string) (any, error)]

// SetPublicProject installs the project lookup of GET /auth/config?project=.
func SetPublicProject(fn func(ctx context.Context, key string) (any, error)) {
	publicProject.Store(&fn)
}

// emailEnabled reports notify.EnabledWith (NOT-022): the effective SMTP host
// (stored settings or FD_SMTP_HOST) is set. The notifications module owns the
// rule and delivery health; a broken server is reported on its admin pages.
func (m *Module) emailEnabled(ctx context.Context) (bool, error) {
	return notify.EnabledWith(ctx, m.d.Settings), nil
}

func (m *Module) selfRegistration(sup settings.Support) bool {
	if m.opt.SelfRegistration != nil {
		return *m.opt.SelfRegistration
	}
	return sup.SelfRegistration
}

func (m *Module) buildAuthConfig(ctx context.Context) (map[string]any, error) {
	sup, br := settings.DefaultSupport(), settings.DefaultBranding()
	if m.d.Settings != nil {
		var err error
		if sup, err = m.d.Settings.Support(ctx); err != nil {
			return nil, err
		}
		if br, err = m.d.Settings.Branding(ctx); err != nil {
			return nil, err
		}
	}
	email, err := m.emailEnabled(ctx)
	if err != nil {
		return nil, err
	}
	staff := map[string]any{"oidc": m.opt.Staff != nil, "local": m.opt.StaffLocal}
	if m.opt.Staff != nil {
		staff["oidc_label"] = m.opt.Staff.label
	}
	cust := map[string]any{
		"local": true, "oidc": m.opt.Customer != nil, "magic_link": email,
		"self_registration": email && m.selfRegistration(sup), "password_reset": email,
	}
	if m.opt.Customer != nil {
		cust["oidc_label"] = m.opt.Customer.label
	}
	if !sup.PortalEnabled {
		cust = map[string]any{"local": false, "oidc": false, "magic_link": false, "self_registration": false, "password_reset": false}
	}
	tiers := map[string]string{}
	for _, k := range []string{"1", "2", "3"} {
		if v, ok := sup.TierLabels[k]; ok {
			tiers[k] = v
		} else {
			tiers[k] = "L" + k
		}
	}
	maxBytes := 25 << 20
	if m.d.Cfg.MaxUploadMB > 0 {
		maxBytes = m.d.Cfg.MaxUploadMB << 20
	}
	locales, def := publicLocales(sup)
	return map[string]any{
		"staff":                   staff,
		"customer":                cust,
		"branding":                PublicBranding(br, sup),
		"tier_labels":             tiers,
		"upload":                  map[string]any{"max_bytes": maxBytes},
		"message_max_length":      65536,
		"customer_urgent_allowed": sup.CustomerUrgentAllowed,
		"email_enabled":           email,
		"setup_pending":           m.setup.pending(),
		"portal_enabled":          sup.PortalEnabled,
		"kb_enabled":              sup.KBEnabled,
		"locales":                 locales,
		"default_locale":          def,
		"version":                 m.d.Version,
	}, nil
}

func publicLocales(sup settings.Support) ([]string, string) {
	locales := []string{}
	for _, l := range sup.Locales {
		if domain.ValidLocale(l) && !slices.Contains(locales, l) {
			locales = append(locales, l)
		}
	}
	if len(locales) == 0 {
		locales = slices.Clone(domain.Locales)
	}
	def := sup.DefaultLocale
	if !domain.ValidLocale(def) {
		def = domain.Locales[0]
	}
	return locales, def
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// PublicBranding maps the stored branding document onto the OpenAPI
// `BrandingSettings` schema (empty URLs and colours become null, asset keys
// become BrandingAsset objects). No secrets are involved.
func PublicBranding(b settings.Branding, sup settings.Support) map[string]any {
	str := func(s string, max int) string { return truncate(s, max) }
	nullable := func(s string) any {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return s
	}
	color := func(s string) any {
		if hexColor.MatchString(s) {
			return s
		}
		return nil
	}
	colorSet := func(c settings.Colors, fallback string) map[string]any {
		out := map[string]any{"primary": fallback}
		if hexColor.MatchString(c.Primary) {
			out["primary"] = c.Primary
		}
		if hexColor.MatchString(c.Secondary) {
			out["secondary"] = c.Secondary
		}
		if hexColor.MatchString(c.Accent) {
			out["accent"] = c.Accent
		}
		return out
	}
	enum := func(v string, allowed []string, def string) string {
		if slices.Contains(allowed, v) {
			return v
		}
		return def
	}
	primary := settings.DefaultBranding().Colors.Primary
	colors := colorSet(b.Colors, primary)
	area := func(name string) any {
		c, ok := b.AreaColors[name]
		if !ok || !hexColor.MatchString(c.Primary) {
			return nil
		}
		return colorSet(c, primary)
	}
	locales, def := publicLocales(sup)
	email := map[string]any{"footer_md": b.Email.FooterMD}
	if hexColor.MatchString(b.Email.HeaderColor) {
		email["header_color"] = b.Email.HeaderColor
	}
	assets := map[string]any{}
	for _, slot := range []string{"logo_light", "logo_dark", "icon", "favicon", "email_logo", "login_background"} {
		v, ok := b.Assets[slot]
		if !ok || v == "" {
			assets[slot] = nil
			continue
		}
		ext := path.Ext(v)
		assets[slot] = map[string]any{"slot": slot, "url": "/branding/" + v, "content_type": contentTypeFor(ext), "sha256": strings.TrimSuffix(v, ext)}
	}
	return map[string]any{
		"product_name":  str(b.ProductName, 60),
		"company_name":  str(b.CompanyName, 100),
		"support_url":   nullable(b.SupportURL),
		"colors":        colors,
		"portal_colors": area("portal"),
		"agent_colors":  area("agent"),
		"default_theme": enum(b.DefaultTheme, []string{"light", "dark", "system"}, "system"),
		"radius":        enum(b.Radius, []string{"sharp", "default", "rounded"}, "default"),
		"font":          enum(b.Font, []string{"system", "inter", "roboto", "source-sans", "noto-sans"}, "system"),
		"login": map[string]any{"title": b.Login.Title, "subtitle": b.Login.Subtitle,
			"background_color": color(b.Login.BackgroundColor)},
		"portal": map[string]any{"welcome_md": b.Portal.WelcomeMD, "new_ticket_intro_md": b.Portal.NewTicketIntroMD},
		"email":  email,
		"legal": map[string]any{"privacy_url": nullable(b.Legal.PrivacyURL), "terms_url": nullable(b.Legal.TermsURL),
			"imprint_url": nullable(b.Legal.ImprintURL)},
		"default_locale":    def,
		"available_locales": locales,
		"powered_by":        b.PoweredBy,
		"custom_css":        str(b.CustomCSS, 20480),
		"assets":            assets,
	}
}

func contentTypeFor(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	}
	return "application/octet-stream"
}
