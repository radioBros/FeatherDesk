// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/radioBros/SimpleTickets/internal/settings"
)

var update = flag.Bool("update", false, "rewrite golden files")

const testPublic = "https://help.example.test"

func defaultBrand() Brand { return BrandFrom(settings.DefaultBranding(), testPublic) }

func customBrand() Brand {
	b := settings.DefaultBranding()
	b.ProductName = "Acme Desk"
	b.Colors.Primary = "#AA0000"
	b.Email.HeaderColor = "#112233"
	b.Email.FooterMD = "Acme Inc. · 1 Main St · **Springfield**"
	b.Legal.PrivacyURL = "https://acme.test/privacy"
	b.Legal.TermsURL = "https://acme.test/terms"
	b.Legal.ImprintURL = "https://acme.test/imprint"
	b.PoweredBy = false
	b.Assets = map[string]string{"email_logo": "0f0e0d.png"}
	return BrandFrom(b, testPublic)
}

func mustTemplates(t *testing.T) *Templates {
	t.Helper()
	tp, err := LoadTemplates(nil)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

// TestAllTemplatesParse renders every key × locale with fixture data.
func TestAllTemplatesParse(t *testing.T) {
	tp := mustTemplates(t)
	if got := tp.Locales(); !slices.Equal(got, []string{"de", "en", "es", "fr", "it"}) {
		t.Fatalf("locales = %v", got)
	}
	for _, loc := range tp.Locales() {
		for _, key := range TemplateKeys {
			r, err := tp.Render(key, loc, defaultBrand(), FixtureData(key, testPublic, testPublic+"/agent"))
			if err != nil {
				t.Errorf("%s/%s: %v", loc, key, err)
				continue
			}
			for _, part := range []string{r.Subject, r.Text, r.HTML} {
				if strings.Contains(part, "<no value>") || strings.TrimSpace(part) == "" {
					t.Errorf("%s/%s: empty or <no value> output", loc, key)
				}
			}
			if strings.HasPrefix(key, "customer.") || (strings.HasPrefix(key, "staff.") && key != TplStaffWebhookDisabled) {
				if !strings.HasPrefix(r.Subject, "[HD-1042] ") {
					t.Errorf("%s/%s: subject %q lacks [HD-1042]", loc, key, r.Subject)
				}
				if !strings.Contains(r.Text, ReplyMarkerFor(loc)) {
					t.Errorf("%s/%s: no reply marker", loc, key)
				}
			}
		}
	}
}

var fieldRe = regexp.MustCompile(`\.[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)
var actionRe = regexp.MustCompile(`\{\{.*?\}\}`)

func placeholders(src string) []string {
	set := map[string]bool{}
	for _, a := range actionRe.FindAllString(src, -1) {
		if strings.HasPrefix(a, "{{- /*") || strings.HasPrefix(a, "{{/*") {
			continue
		}
		for _, f := range fieldRe.FindAllString(a, -1) {
			set[f] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestTemplateCompleteness: every key × locale exists, uses the same
// placeholders as en, and the key list equals events_spec §2 + extras.
func TestTemplateCompleteness(t *testing.T) {
	sub, _ := fs.Sub(templateFS, "templates")
	tp := mustTemplates(t)
	for _, key := range append(slices.Clone(TemplateKeys), layoutKey) {
		en, err := fs.ReadFile(sub, "en/"+key+".tmpl")
		if err != nil {
			t.Fatal(err)
		}
		want := placeholders(string(en))
		for _, loc := range tp.Locales() {
			b, err := fs.ReadFile(sub, loc+"/"+key+".tmpl")
			if err != nil {
				t.Errorf("missing %s/%s", loc, key)
				continue
			}
			if got := placeholders(string(b)); !slices.Equal(got, want) {
				t.Errorf("%s/%s placeholders %v, en has %v", loc, key, got, want)
			}
			if loc == "de" || loc == "fr" || loc == "es" {
				if !strings.Contains(string(b), "needs_review") {
					t.Errorf("%s/%s lacks needs_review flag", loc, key)
				}
			}
		}
	}
	// Key list == events_spec §2 matrix (+ auth.* expansion and staff.webhook_disabled).
	spec, err := os.ReadFile(filepath.Join("..", "..", "development", "contracts", "events_spec.md"))
	if err != nil {
		t.Skip("events_spec.md not found")
	}
	sec := string(spec)
	if i := strings.Index(sec, "## 2."); i >= 0 {
		sec = sec[i:]
	}
	if i := strings.Index(sec, "## 3."); i >= 0 {
		sec = sec[:i]
	}
	matrix := map[string]bool{}
	for _, m := range regexp.MustCompile("`((?:customer|staff)\\.[a-z_]+)`").FindAllStringSubmatch(sec, -1) {
		matrix[m[1]] = true
	}
	for k := range matrix {
		if !slices.Contains(TemplateKeys, k) {
			t.Errorf("matrix key %s has no template", k)
		}
	}
	for _, k := range TemplateKeys {
		if !matrix[k] && !Transactional(k) && k != TplStaffWebhookDisabled { // auth.*, privacy.* (D-19 §C)
			t.Errorf("template %s is not in the events_spec matrix", k)
		}
	}
}

// TestMissingTemplateFailsBoot: deleting one fr file fails the load and names it.
func TestMissingTemplateFailsBoot(t *testing.T) {
	sub, _ := fs.Sub(templateFS, "templates")
	mfs := fstest.MapFS{}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "fr/customer.reply.tmpl" {
			return err
		}
		b, _ := fs.ReadFile(sub, p)
		mfs[p] = &fstest.MapFile{Data: b}
		return nil
	})
	_, err := LoadTemplates(mfs)
	if err == nil || !strings.Contains(err.Error(), "fr/customer.reply.tmpl") {
		t.Fatalf("err = %v", err)
	}
	if _, err := New(Deps{}); err != nil {
		t.Fatalf("embedded templates must load: %v", err)
	}
}

func TestSanitizedBody(t *testing.T) {
	tp := mustTemplates(t)
	d := FixtureData(TplCustomerReply, testPublic, testPublic).(*CustomerData)
	d.Message.Body = "Hi <script>alert(1)</script> [x](javascript:alert(2)) <img src=x onerror=alert(3)>"
	r, err := tp.Render(TplCustomerReply, "en", defaultBrand(), d)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "javascript:", "onerror"} {
		if strings.Contains(strings.ToLower(r.HTML), bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}
}

func TestSignatureSanitized(t *testing.T) {
	tp := mustTemplates(t)
	d := FixtureData(TplCustomerReply, testPublic, testPublic).(*CustomerData)
	d.Signature = "Grace <script>steal()</script> **Hopper**"
	r, err := tp.Render(TplCustomerReply, "en", defaultBrand(), d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.HTML, "<script") || !strings.Contains(r.HTML, "<strong>Hopper</strong>") {
		t.Errorf("signature not sanitised/rendered:\n%s", r.HTML)
	}
	// Signature sits below the message and above the footer.
	bi, si, fi := strings.Index(r.Text, "print server"), strings.Index(r.Text, "Hopper**"), strings.Index(r.Text, "\n-- \n")
	if bi >= si || si >= fi {
		t.Errorf("signature position wrong: body %d sig %d footer %d", bi, si, fi)
	}
}

func TestNoSVGLogo(t *testing.T) {
	b := settings.DefaultBranding()
	b.Assets = map[string]string{"email_logo": "abc.svg", "logo_light": "def.png"}
	br := BrandFrom(b, testPublic)
	if br.LogoURL != "" {
		t.Fatalf("SVG logo referenced: %s", br.LogoURL)
	}
	tp := mustTemplates(t)
	for _, key := range TemplateKeys {
		r, _ := tp.Render(key, "en", br, FixtureData(key, testPublic, testPublic))
		if strings.Contains(r.HTML, ".svg") {
			t.Errorf("%s references an SVG", key)
		}
	}
	if got := customBrand().LogoURL; got != testPublic+"/branding/0f0e0d.png" {
		t.Errorf("png logo url = %q", got)
	}
}

// TestCustomerViewAllowList: customer template data can't carry internal
// content (I-7). Adding a field needs a reviewed change here.
func TestCustomerViewAllowList(t *testing.T) {
	allowed := map[reflect.Type][]string{
		reflect.TypeOf(CustomerData{}):        {"Brand", "RecipientName", "Ticket", "Message", "Signature", "CSATURL"},
		reflect.TypeOf(CustomerTicketView{}):  {"Number", "Ref", "Subject", "URL"},
		reflect.TypeOf(CustomerMessageView{}): {"AuthorName", "Body"},
	}
	for typ, want := range allowed {
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			got = append(got, typ.Field(i).Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s fields %v, allow-list %v", typ.Name(), got, want)
		}
	}
}

func goldenCompare(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/notify/ -run TestGolden -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden output", path)
	}
}

// TestGolden: every key × locale (default brand) plus en with a custom brand.
func TestGolden(t *testing.T) {
	tp := mustTemplates(t)
	for _, loc := range tp.Locales() {
		for _, key := range TemplateKeys {
			r, err := tp.Render(key, loc, defaultBrand(), FixtureData(key, testPublic, testPublic+"/agent"))
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("testdata", "golden", key+"."+loc)
			goldenCompare(t, base+".txt", "Subject: "+r.Subject+"\n\n"+r.Text)
			goldenCompare(t, base+".html", r.HTML)
		}
	}
	for _, key := range TemplateKeys {
		r, err := tp.Render(key, "en", customBrand(), FixtureData(key, testPublic, testPublic+"/agent"))
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Join("testdata", "golden", "custom", key+".en")
		goldenCompare(t, base+".txt", "Subject: "+r.Subject+"\n\n"+r.Text)
		goldenCompare(t, base+".html", r.HTML)
	}
}

// TestBrandFixturesDifferOnlyInBrand: lines that differ between the default
// and the custom brand all carry a brand value.
func TestBrandFixturesDifferOnlyInBrand(t *testing.T) {
	tp := mustTemplates(t)
	cb := customBrand()
	brandBits := []string{cb.ProductName, cb.PrimaryColor, cb.HeaderColor, cb.LogoURL, "Springfield", "acme.test/", "Acme Inc", "<p style=\"margin:8px 0;\">", "</p>", "SimpleTickets", "#3F51B5"}
	for _, key := range TemplateKeys {
		a, _ := tp.Render(key, "en", defaultBrand(), FixtureData(key, testPublic, testPublic))
		b, _ := tp.Render(key, "en", cb, FixtureData(key, testPublic, testPublic))
		al, bl := strings.Split(a.HTML+"\n"+a.Text, "\n"), strings.Split(b.HTML+"\n"+b.Text, "\n")
		inA := map[string]bool{}
		for _, l := range al {
			inA[l] = true
		}
		for _, l := range bl {
			if inA[l] || strings.TrimSpace(l) == "" {
				continue
			}
			ok := false
			for _, bit := range brandBits {
				if strings.Contains(l, bit) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s: non-brand difference: %q", key, l)
			}
		}
		if strings.Contains(b.Subject+b.Text, "SimpleTickets") && strings.Contains(key, "auth.") {
			t.Errorf("%s: default product name leaked into custom brand", key)
		}
	}
}

// TestResetPassword_SSOVariant: authn sends auth.reset_password with
// vars.sso="true" to SSO users (AUTH-015). Every locale then renders the
// "sign in with SSO" text with no link and no expiry; without sso the link
// is there. Goldens live under testdata/golden/sso.
func TestResetPassword_SSOVariant(t *testing.T) {
	tp := mustTemplates(t)
	url := testPublic + "/login"
	for _, loc := range tp.Locales() {
		sso := &GenericData{RecipientName: "Ada Lovelace", Vars: map[string]string{"sso": "true", "url": url}}
		r, err := tp.Render(TplAuthResetPassword, loc, defaultBrand(), sso)
		if err != nil {
			t.Fatal(err)
		}
		body := r.Text + r.HTML
		if !strings.Contains(body, "SSO") {
			t.Errorf("%s: no SSO text:\n%s", loc, r.Text)
		}
		if strings.Contains(body, url) || strings.Contains(r.Text, "](") || strings.Contains(body, "/auth/token") {
			t.Errorf("%s: SSO reset mail carries a link:\n%s", loc, r.Text)
		}
		base := filepath.Join("testdata", "golden", "sso", TplAuthResetPassword+"."+loc)
		goldenCompare(t, base+".txt", "Subject: "+r.Subject+"\n\n"+r.Text)
		goldenCompare(t, base+".html", r.HTML)

		local := &GenericData{RecipientName: "Ada Lovelace", Vars: map[string]string{"url": testPublic + "/auth/token/abc", "expires_minutes": "60"}}
		r, err = tp.Render(TplAuthResetPassword, loc, defaultBrand(), local)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.Text, testPublic+"/auth/token/abc") || strings.Contains(r.Text, "SSO") {
			t.Errorf("%s: local reset mail lost its link:\n%s", loc, r.Text)
		}
	}
}
