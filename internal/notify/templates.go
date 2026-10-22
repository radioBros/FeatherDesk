// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"bytes"
	"embed"
	"fmt"
	htmltpl "html/template"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	texttpl "text/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/settings"
)

//go:embed templates/layout.html.tmpl templates/*/*.tmpl
var templateFS embed.FS

// layoutKey holds per-locale labels used by the HTML layout and text footer.
const layoutKey = "_layout"

// Templates is the parsed, boot-validated template set (NOT-002).
type Templates struct {
	locales []string
	text    map[string]map[string]*texttpl.Template // locale → key → template
	layout  *htmltpl.Template
	md      goldmark.Markdown
	policy  *bluemonday.Policy
}

// LoadTemplates parses every key × locale from fsys (nil = embedded). A
// missing file fails with an error naming it, so boot fails fast.
func LoadTemplates(fsys fs.FS) (*Templates, error) {
	if fsys == nil {
		sub, err := fs.Sub(templateFS, "templates")
		if err != nil {
			return nil, err
		}
		fsys = sub
	}
	ents, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("notify templates: %w", err)
	}
	t := &Templates{text: map[string]map[string]*texttpl.Template{},
		md:     goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify)),
		policy: bluemonday.UGCPolicy()}
	for _, e := range ents {
		if e.IsDir() {
			t.locales = append(t.locales, e.Name())
		}
	}
	slices.Sort(t.locales)
	if !slices.Contains(t.locales, "en") {
		return nil, fmt.Errorf("notify templates: locale %q missing", "en")
	}
	for _, loc := range t.locales {
		t.text[loc] = map[string]*texttpl.Template{}
		for _, key := range append(slices.Clone(TemplateKeys), layoutKey) {
			name := path.Join(loc, key+".tmpl")
			b, err := fs.ReadFile(fsys, name)
			if err != nil {
				return nil, fmt.Errorf("notify templates: missing %s: %w", name, err)
			}
			tp, err := texttpl.New(name).Parse(string(b))
			if err != nil {
				return nil, fmt.Errorf("notify templates: %s: %w", name, err)
			}
			want := []string{"subject", "body"}
			if key == layoutKey {
				want = []string{"privacy", "terms", "imprint", "powered_by", "by_author", "automated"}
			}
			for _, d := range want {
				if tp.Lookup(d) == nil {
					return nil, fmt.Errorf("notify templates: %s: missing {{define %q}}", name, d)
				}
			}
			t.text[loc][key] = tp
		}
	}
	lb, err := fs.ReadFile(fsys, "layout.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("notify templates: missing layout.html.tmpl: %w", err)
	}
	if t.layout, err = htmltpl.New("layout").Parse(string(lb)); err != nil {
		return nil, fmt.Errorf("notify templates: layout.html.tmpl: %w", err)
	}
	return t, nil
}

// Locales lists the discovered locales.
func (t *Templates) Locales() []string { return slices.Clone(t.locales) }

// Has reports whether key is a known template key.
func (t *Templates) Has(key string) bool { return slices.Contains(TemplateKeys, key) }

// Locale picks the first available locale of the candidates, else "en".
func (t *Templates) Locale(cands ...string) string {
	for _, c := range cands {
		if _, ok := t.text[c]; ok {
			return c
		}
	}
	return "en"
}

// Brand is the email-relevant subset of settings.Branding (D-13).
type Brand struct {
	ProductName  string
	CompanyName  string
	HeaderColor  string
	PrimaryColor string
	LogoURL      string // absolute PNG URL; the FeatherDesk logo when no PNG email logo is set
	DefaultLogo  bool   // LogoURL is the FeatherDesk mark, so the product name goes beside it
	FooterMD     string
	PrivacyURL   string
	TermsURL     string
	ImprintURL   string
	PoweredBy    bool
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{3}([0-9A-Fa-f]{3})?$`)

// BrandFrom builds the email brand. Only a PNG email logo is ever referenced
// (many mail clients block SVG).
func BrandFrom(b settings.Branding, publicURL string) Brand {
	br := Brand{ProductName: b.ProductName, CompanyName: b.CompanyName, HeaderColor: b.Email.HeaderColor, PrimaryColor: b.Colors.Primary,
		FooterMD: b.Email.FooterMD, PrivacyURL: safeURL(b.Legal.PrivacyURL), TermsURL: safeURL(b.Legal.TermsURL), ImprintURL: safeURL(b.Legal.ImprintURL), PoweredBy: b.PoweredBy}
	if br.ProductName == "" {
		br.ProductName = "FeatherDesk"
	}
	if !hexColor.MatchString(br.HeaderColor) {
		br.HeaderColor = "#3F51B5"
	}
	if !hexColor.MatchString(br.PrimaryColor) {
		br.PrimaryColor = "#3F51B5"
	}
	if a := b.Assets["email_logo"]; strings.HasSuffix(strings.ToLower(a), ".png") && !strings.ContainsAny(a, "/\\?#") {
		br.LogoURL = strings.TrimRight(publicURL, "/") + "/branding/" + a
	} else if publicURL != "" {
		br.LogoURL, br.DefaultLogo = strings.TrimRight(publicURL, "/")+"/featherdesk-logo.png", true
	}
	return br
}

func safeURL(u string) string {
	l := strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:") {
		return strings.TrimSpace(u)
	}
	return ""
}

// Rendered is one rendered template.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// branded is implemented by the template data types so the renderer can
// inject the brand and read ticket context.
type branded interface {
	setBrand(Brand)
	ticketRef() string // "" = not a ticket mail (no [KEY-123] tag, no marker)
}

// Render executes key in locale with data (a *CustomerData, *StaffData or
// *GenericData). Markdown bodies become HTML via goldmark and are sanitised
// by bluemonday before they reach the layout (I-7 is enforced by the data
// types, XSS by the sanitiser).
func (t *Templates) Render(key, locale string, brand Brand, data branded) (Rendered, error) {
	locale = t.Locale(locale)
	tp := t.text[locale][key]
	if tp == nil {
		return Rendered{}, fmt.Errorf("notify: unknown template %q", key)
	}
	data.setBrand(brand)
	var sb, bb bytes.Buffer
	if err := tp.ExecuteTemplate(&sb, "subject", data); err != nil {
		return Rendered{}, fmt.Errorf("notify: %s/%s subject: %w", locale, key, err)
	}
	if err := tp.ExecuteTemplate(&bb, "body", data); err != nil {
		return Rendered{}, fmt.Errorf("notify: %s/%s body: %w", locale, key, err)
	}
	subject := strings.Join(strings.Fields(sb.String()), " ")
	marker := ""
	if ref := data.ticketRef(); ref != "" {
		subject = fmt.Sprintf("[%s] %s", ref, subject)
		marker = thread.ReplyMarker(locale)
	}
	body := strings.TrimSpace(bb.String())
	labels := map[string]string{}
	for _, l := range []string{"privacy", "terms", "imprint", "powered_by", "by_author", "automated"} {
		var b bytes.Buffer
		_ = t.text[locale][layoutKey].ExecuteTemplate(&b, l, nil)
		labels[l] = strings.TrimSpace(b.String())
	}

	// Text part.
	var tx strings.Builder
	if marker != "" {
		tx.WriteString(marker + "\n\n")
	}
	tx.WriteString(body)
	tx.WriteString("\n\n-- \n")
	if f := strings.TrimSpace(brand.FooterMD); f != "" {
		tx.WriteString(f + "\n")
	}
	tx.WriteString(labels["automated"] + " " + brand.ProductName + ".\n")
	for _, l := range [][2]string{{"privacy", brand.PrivacyURL}, {"terms", brand.TermsURL}, {"imprint", brand.ImprintURL}} {
		if l[1] != "" {
			tx.WriteString(labels[l[0]] + ": " + l[1] + "\n")
		}
	}

	// HTML part.
	bodyHTML, err := t.MarkdownHTML(body)
	if err != nil {
		return Rendered{}, err
	}
	footerHTML, err := t.MarkdownHTML(brand.FooterMD)
	if err != nil {
		return Rendered{}, err
	}
	var hb bytes.Buffer
	err = t.layout.Execute(&hb, map[string]any{
		"Lang": locale, "Subject": subject, "Marker": marker, "Brand": brand,
		"HeaderColor": htmltpl.CSS(brand.HeaderColor), "PrimaryColor": htmltpl.CSS(brand.PrimaryColor),
		"Body": bodyHTML, "Footer": footerHTML,
		"Privacy": labels["privacy"], "Terms": labels["terms"], "Imprint": labels["imprint"],
		"PoweredByLabel": labels["powered_by"], "ByAuthorLabel": labels["by_author"], "Automated": labels["automated"],
	})
	if err != nil {
		return Rendered{}, fmt.Errorf("notify: layout: %w", err)
	}
	return Rendered{Subject: subject, Text: tx.String(), HTML: hb.String()}, nil
}

// MarkdownHTML renders Markdown with goldmark (raw HTML disabled) and
// sanitises the result with bluemonday's UGC policy.
func (t *Templates) MarkdownHTML(md string) (htmltpl.HTML, error) {
	if strings.TrimSpace(md) == "" {
		return "", nil
	}
	var b bytes.Buffer
	if err := t.md.Convert([]byte(md), &b); err != nil {
		return "", err
	}
	return htmltpl.HTML(t.policy.SanitizeBytes(b.Bytes())), nil //nolint:gosec // sanitised by bluemonday
}

// ReplyMarkerFor is the localized "reply above this line" marker.
func ReplyMarkerFor(locale string) string { return thread.ReplyMarker(locale) }
