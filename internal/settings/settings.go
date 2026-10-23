// SPDX-License-Identifier: AGPL-3.0-only

// Package settings is the typed, cached view of the `settings` table
// (support, branding, smtp, email, setup) with env-override-and-lock (PLT-027).
//
// Env wins: when FD_SMTP_HOST is set, the stored host is ignored and "host"
// appears in LockedByEnv so the admin UI renders it read-only. Secrets are
// sealed with crypto.Box before they reach the database and never leave
// through a getter that serialises to JSON.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/store"
)

const (
	KeySupport  = "support"
	KeyBranding = "branding"
	KeySMTP     = "smtp"
	KeyEmail    = "email"
	KeySetup    = "setup"
	KeySlack    = "slack" // Slack app credentials + workspace connection (D-21)
)

// Secret purposes for crypto.Box.
const (
	PurposeSMTPPassword = "smtp-password"
	PurposeIMAPPassword = "imap-password"
	PurposeIMAPRefresh  = "imap-oauth-refresh"
	PurposeWebhook      = "webhook-secret"
	PurposeMFA          = "mfa-totp" // user_mfa TOTP secrets (D-19 §B)
	PurposeSlackSecret  = "slack-client-secret"
	PurposeSlackToken   = "slack-bot-token"
)

// Service caches documents in memory; writes invalidate and fire OnChange hooks.
type Service struct {
	db     *store.DB
	box    *crypto.Box
	getenv func(string) string

	mu    sync.RWMutex
	cache map[string]json.RawMessage
	hooks []func(key string)
}

func New(db *store.DB, box *crypto.Box, getenv func(string) string) *Service {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Service{db: db, box: box, getenv: getenv, cache: map[string]json.RawMessage{}}
}

// Box exposes the secret box (modules sealing their own secrets).
func (s *Service) Box() *crypto.Box { return s.box }

// OnChange registers fn, called after a successful Put (e.g. realtime pushes
// settings.branding_changed, notify drops its SMTP client).
func (s *Service) OnChange(fn func(key string)) {
	s.mu.Lock()
	s.hooks = append(s.hooks, fn)
	s.mu.Unlock()
}

// GetRaw returns the stored JSON document (cached). Missing keys yield "{}".
func (s *Service) GetRaw(ctx context.Context, key string) (json.RawMessage, error) {
	s.mu.RLock()
	if v, ok := s.cache[key]; ok {
		s.mu.RUnlock()
		return v, nil
	}
	s.mu.RUnlock()
	var raw string
	err := s.db.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			raw = "{}"
		} else {
			return nil, err
		}
	}
	v := json.RawMessage(raw)
	s.mu.Lock()
	s.cache[key] = v
	s.mu.Unlock()
	return v, nil
}

// Get decodes the stored document over dst; fields absent in the stored JSON
// keep whatever defaults dst already holds.
func (s *Service) Get(ctx context.Context, key string, dst any) error {
	raw, err := s.GetRaw(ctx, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// Put stores v as the whole document for key.
func (s *Service) Put(ctx context.Context, key string, v any, actorID string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var actor any
	if actorID != "" {
		actor = actorID
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
			key, string(b), store.Now(), actor)
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[key] = json.RawMessage(b)
	hooks := slices.Clone(s.hooks)
	s.mu.Unlock()
	for _, h := range hooks {
		h(key)
	}
	return nil
}

// Invalidate drops the cache (tests, or after a restore).
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cache = map[string]json.RawMessage{}
	s.mu.Unlock()
}

// ---- typed documents ----

// Support holds support-workflow settings (TKT-019).
type Support struct {
	TierLabels               map[string]string `json:"tier_labels"`
	SelfRegistration         bool              `json:"self_registration"`
	AutoCloseDays            int               `json:"auto_close_days"`
	ReopenWindowDays         int               `json:"reopen_window_days"`
	CustomerUrgentAllowed    bool              `json:"customer_urgent_allowed"`
	UrgentRequiresSupervisor bool              `json:"urgent_requires_supervisor"`
	TriageVisibleToAgents    bool              `json:"triage_visible_to_agents"`
	PortalEnabled            bool              `json:"portal_enabled"`
	KBEnabled                bool              `json:"kb_enabled"`
	KBSitemapEnabled         bool              `json:"kb_sitemap_enabled"`
	DefaultLocale            string            `json:"default_locale"`
	Locales                  []string          `json:"locales"`
	AllowedEmailDomains      []string          `json:"allowed_email_domains,omitempty"`
}

func DefaultSupport() Support {
	return Support{
		TierLabels: map[string]string{"1": "L1", "2": "L2", "3": "L3"}, SelfRegistration: true, AutoCloseDays: 7,
		ReopenWindowDays: 7, TriageVisibleToAgents: true, PortalEnabled: true, KBEnabled: true, DefaultLocale: "en",
		Locales: []string{"en", "it", "de", "fr", "es"},
	}
}

func (s *Service) Support(ctx context.Context) (Support, error) {
	v := DefaultSupport()
	err := s.Get(ctx, KeySupport, &v)
	return v, err
}

// Branding mirrors D-13 §1. Asset values are sha256 keys served at /branding/<sha>.<ext>.
type Branding struct {
	ProductName  string            `json:"product_name"`
	CompanyName  string            `json:"company_name"`
	SupportURL   string            `json:"support_url"`
	Colors       Colors            `json:"colors"`
	AreaColors   map[string]Colors `json:"area_colors"`
	DefaultTheme string            `json:"default_theme"`
	Radius       string            `json:"radius"`
	Font         string            `json:"font"`
	Login        struct {
		Title           string `json:"title"`
		Subtitle        string `json:"subtitle"`
		BackgroundColor string `json:"background_color"`
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
		PrivacyURL string `json:"privacy_url"`
		TermsURL   string `json:"terms_url"`
		ImprintURL string `json:"imprint_url"`
	} `json:"legal"`
	PoweredBy bool              `json:"powered_by"`
	CustomCSS string            `json:"custom_css"`
	Assets    map[string]string `json:"assets"` // slot → "<sha256>.<ext>"
}

type Colors struct {
	Primary   string `json:"primary,omitempty"`
	Secondary string `json:"secondary,omitempty"`
	Accent    string `json:"accent,omitempty"`
}

func DefaultBranding() Branding {
	b := Branding{ProductName: "FeatherDesk", CompanyName: "RadioBros", DefaultTheme: "system", Radius: "default", Font: "system", PoweredBy: true,
		Colors: Colors{Primary: "#3F51B5", Secondary: "#607D8B", Accent: "#00BFA5"}, AreaColors: map[string]Colors{}, Assets: map[string]string{}}
	b.Email.HeaderColor = "#3F51B5"
	return b
}

func (s *Service) Branding(ctx context.Context) (Branding, error) {
	v := DefaultBranding()
	err := s.Get(ctx, KeyBranding, &v)
	return v, err
}

// SMTP is the stored SMTP document (D-12). PasswordEnc is ciphertext; use
// SMTPPassword to obtain the effective plaintext. Env tags drive the overlay.
type SMTP struct {
	Host             string `json:"host" env:"HOST"`
	Port             int    `json:"port" env:"PORT"`
	Security         string `json:"security" env:"SECURITY"` // none | starttls | tls
	Username         string `json:"username" env:"USERNAME"`
	PasswordEnc      string `json:"password_enc"`
	Auth             string `json:"auth" env:"AUTH"` // plain | login | cram-md5 | none
	HELO             string `json:"helo" env:"HELO"`
	TimeoutSeconds   int    `json:"timeout_seconds" env:"TIMEOUT_SECONDS"`
	AllowInsecureTLS bool   `json:"allow_insecure_tls" env:"ALLOW_INSECURE_TLS"`
}

// SMTPView is what the API returns: no secrets, plus which fields env locks.
type SMTPView struct {
	SMTP
	PasswordSet bool     `json:"password_set"`
	LockedByEnv []string `json:"locked_by_env"`
}

// MarshalJSON hides PasswordEnc.
func (v SMTPView) MarshalJSON() ([]byte, error) {
	type plain SMTP
	m := map[string]any{}
	b, _ := json.Marshal(plain(v.SMTP))
	_ = json.Unmarshal(b, &m)
	delete(m, "password_enc")
	m["password_set"] = v.PasswordSet
	m["locked_by_env"] = v.LockedByEnv
	return json.Marshal(m)
}

func DefaultSMTP() SMTP {
	return SMTP{Port: 587, Security: "starttls", Auth: "plain", TimeoutSeconds: 10}
}

// SMTP returns the effective SMTP config (stored, overlaid by FD_SMTP_*).
func (s *Service) SMTP(ctx context.Context) (SMTPView, error) {
	v := DefaultSMTP()
	if err := s.Get(ctx, KeySMTP, &v); err != nil {
		return SMTPView{}, err
	}
	locked := Overlay(&v, "FD_SMTP_", s.getenv)
	pw := v.PasswordEnc != ""
	if s.getenv("FD_SMTP_PASSWORD") != "" {
		locked = append(locked, "password")
		pw = true
	}
	if locked == nil {
		locked = []string{}
	}
	return SMTPView{SMTP: v, PasswordSet: pw, LockedByEnv: locked}, nil
}

// SMTPPassword returns the effective plaintext password (env beats stored).
func (s *Service) SMTPPassword(ctx context.Context) (string, error) {
	if p := s.getenv("FD_SMTP_PASSWORD"); p != "" {
		return p, nil
	}
	v := DefaultSMTP()
	if err := s.Get(ctx, KeySMTP, &v); err != nil {
		return "", err
	}
	return s.box.OpenString(PurposeSMTPPassword, v.PasswordEnc)
}

// PutSMTP stores in. Env-locked fields keep their stored value (env still
// wins on read). newPassword: nil = keep, "" = clear, else seal and store.
func (s *Service) PutSMTP(ctx context.Context, in SMTP, newPassword *string, actorID string) error {
	cur := DefaultSMTP()
	if err := s.Get(ctx, KeySMTP, &cur); err != nil {
		return err
	}
	locked := Overlay(&SMTP{}, "FD_SMTP_", s.getenv)
	keepLocked(&in, &cur, locked)
	in.PasswordEnc = cur.PasswordEnc
	if newPassword != nil {
		enc, err := s.box.SealString(PurposeSMTPPassword, *newPassword)
		if err != nil {
			return err
		}
		in.PasswordEnc = enc
	}
	return s.Put(ctx, KeySMTP, in, actorID)
}

// Identity is a sender (From/Reply-To) for one audience.
type Identity struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	ReplyTo string `json:"reply_to"`
}

// Email holds sender identities and template toggles (D-12).
type Email struct {
	CustomerFrom      Identity `json:"customer_from"`
	StaffFrom         Identity `json:"staff_from"`
	DisabledTemplates []string `json:"disabled_templates"`
	TriageMailbox     string   `json:"triage_mailbox"`
	PlusAddressing    bool     `json:"plus_addressing"`
	InboundBlocklist  []string `json:"inbound_blocklist"`
}

type EmailView struct {
	Email
	LockedByEnv []string `json:"locked_by_env"`
}

func (s *Service) Email(ctx context.Context) (EmailView, error) {
	v := Email{DisabledTemplates: []string{}, InboundBlocklist: []string{}}
	if err := s.Get(ctx, KeyEmail, &v); err != nil {
		return EmailView{}, err
	}
	locked := []string{}
	for _, o := range []struct {
		env   string
		field string
		dst   *Identity
	}{{"FD_SMTP_FROM_CUSTOMER", "customer_from", &v.CustomerFrom}, {"FD_SMTP_FROM_STAFF", "staff_from", &v.StaffFrom}} {
		if raw := s.getenv(o.env); raw != "" {
			if a, err := mail.ParseAddress(raw); err == nil {
				o.dst.Name, o.dst.Address = a.Name, a.Address
				locked = append(locked, o.field)
			}
		}
	}
	return EmailView{Email: v, LockedByEnv: locked}, nil
}

func (s *Service) PutEmail(ctx context.Context, in Email, actorID string) error {
	return s.Put(ctx, KeyEmail, in, actorID)
}

// Setup tracks the first-run wizard (D-13 §3).
type Setup struct {
	Completed   bool   `json:"completed"`
	CompletedAt string `json:"completed_at,omitempty"`
}

func (s *Service) Setup(ctx context.Context) (Setup, error) {
	var v Setup
	err := s.Get(ctx, KeySetup, &v)
	return v, err
}

// ---- overlay (PLT-027) ----

// Overlay copies env values into the exported fields of dst that carry an
// `env:"NAME"` tag (looked up as prefix+NAME) and returns the json names of the
// fields it overrode. Used by SMTP (FD_SMTP_*) and inbound IMAP (FD_IMAP_*).
func Overlay(dst any, prefix string, getenv func(string) string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}
	v := reflect.ValueOf(dst).Elem()
	t := v.Type()
	var locked []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("env")
		if tag == "" {
			continue
		}
		raw := strings.TrimSpace(getenv(prefix + tag))
		if raw == "" {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.String:
			fv.SetString(raw)
		case reflect.Int:
			n, err := strconv.Atoi(raw)
			if err != nil {
				continue
			}
			fv.SetInt(int64(n))
		case reflect.Bool:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				continue
			}
			fv.SetBool(b)
		default:
			continue
		}
		locked = append(locked, jsonName(f))
	}
	return locked
}

func keepLocked(in, cur any, locked []string) {
	iv, cv := reflect.ValueOf(in).Elem(), reflect.ValueOf(cur).Elem()
	t := iv.Type()
	for i := 0; i < t.NumField(); i++ {
		if slices.Contains(locked, jsonName(t.Field(i))) {
			iv.Field(i).Set(cv.Field(i))
		}
	}
}

func jsonName(f reflect.StructField) string {
	n, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if n == "" {
		return f.Name
	}
	return n
}

// String helps logging without leaking secrets.
func (v SMTPView) String() string {
	return fmt.Sprintf("smtp{%s:%d %s user=%q pw=%t locked=%v}", v.Host, v.Port, v.Security, v.Username, v.PasswordSet, v.LockedByEnv)
}
