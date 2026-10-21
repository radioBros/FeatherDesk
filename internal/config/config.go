// SPDX-License-Identifier: AGPL-3.0-only

// Package config parses the core ST_* environment (PLT-002). Modules read
// their own variables through Env, so this struct only holds what the core needs.
//
// Load collects every problem before failing, so an operator fixes the whole
// environment in one pass instead of one variable per restart.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Config holds the core settings. Tags drive parsing and docs/configuration.md:
// env = variable name, default = default value, desc = one-line description.
type Config struct {
	PublicURL        string        `env:"ST_PUBLIC_URL" default:"http://localhost:8080" desc:"Absolute base URL used in links, OIDC redirect URIs and the WebSocket Origin check"`
	PortalURL        string        `env:"ST_PORTAL_URL" desc:"Customer portal base URL (defaults to ST_PUBLIC_URL)"`
	AgentURL         string        `env:"ST_AGENT_URL" desc:"Staff panel base URL (defaults to ST_PUBLIC_URL/agent)"`
	ListenAddr       string        `env:"ST_LISTEN_ADDR" default:":8080" desc:"HTTP listen address"`
	DataDir          string        `env:"ST_DATA_DIR" default:"/data" desc:"Directory for the SQLite DB, attachments, backups and the instance lock"`
	DatabaseURL      string        `env:"ST_DATABASE_URL" desc:"Empty or sqlite:///path selects SQLite; postgres://… selects Postgres"`
	DBReaders        int           `env:"ST_DB_READERS" default:"2" desc:"SQLite reader pool size"`
	DBPool           int           `env:"ST_DB_POOL" default:"5" desc:"Postgres connection pool size"`
	BlobURL          string        `env:"ST_BLOB_URL" desc:"Empty = local files under ST_DATA_DIR/attachments; s3://bucket/prefix?endpoint=…&region=… for S3"`
	SecretKeyRaw     string        `env:"ST_SECRET_KEY" secret:"true" desc:"Root secret (base64 or raw, ≥ 32 bytes). Required; in ST_DEV it is generated into ST_DATA_DIR/secret.key"`
	SecretKeyPrevRaw string        `env:"ST_SECRET_KEY_PREVIOUS" secret:"true" desc:"Previous root secret, accepted for decryption during rotation"`
	TrustedProxies   []string      `env:"ST_TRUSTED_PROXIES" desc:"Comma-separated CIDRs whose X-Forwarded-For is trusted"`
	AllowedOrigins   []string      `env:"ST_ALLOWED_ORIGINS" desc:"Extra comma-separated origins (scheme://host[:port]) accepted besides ST_PUBLIC_URL for browser logins and WebSockets, e.g. http://localhost:3200"`
	LogLevel         string        `env:"ST_LOG_LEVEL" default:"info" desc:"debug, info, warn or error"`
	LogFormat        string        `env:"ST_LOG_FORMAT" default:"json" desc:"json or text"`
	MaxBodyKB        int           `env:"ST_MAX_BODY_KB" default:"1024" desc:"Maximum JSON request body in KiB"`
	MaxUploadMB      int           `env:"ST_MAX_UPLOAD_MB" default:"25" desc:"Maximum upload size in MiB (attachments, branding, KB images)"`
	QueueCapacity    int           `env:"ST_QUEUE_CAPACITY" default:"1024" desc:"Bound of each in-process queue channel"`
	WSMaxConns       int           `env:"ST_WS_MAX_CONNS" default:"1000" desc:"Global WebSocket connection cap"`
	WSMaxConnsUser   int           `env:"ST_WS_MAX_CONNS_PER_USER" default:"5" desc:"WebSocket connections per user"`
	ShutdownTimeout  time.Duration `env:"ST_SHUTDOWN_TIMEOUT" default:"20s" desc:"Graceful shutdown budget"`
	MigrateOnBoot    bool          `env:"ST_MIGRATE_ON_BOOT" default:"true" desc:"Apply pending migrations when serve starts"`
	MetricsToken     string        `env:"ST_METRICS_TOKEN" secret:"true" desc:"Bearer token that enables /metrics (disabled when empty)"`
	AuditRetention   int           `env:"ST_AUDIT_RETENTION_DAYS" default:"365" desc:"Days to keep audit_log rows"`
	Dev              bool          `env:"ST_DEV" default:"false" desc:"Development mode (generated secret, text logs allowed, SPA from the Vite proxy)"`

	// Derived values (not read from env).
	getenv        func(string) string
	SecretKey     []byte         `env:"-"`
	SecretKeyPrev []byte         `env:"-"`
	TrustedNets   []netip.Prefix `env:"-"`
}

// Load parses the environment through getenv (os.Getenv in production).
func Load(getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	c := &Config{getenv: getenv}
	var errs []error
	bad := map[string]bool{} // unparsable: skip further checks to report each once
	v := reflect.ValueOf(c).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		if name == "" || name == "-" {
			continue
		}
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			raw = f.Tag.Get("default")
		}
		if raw == "" {
			continue
		}
		if err := setField(v.Field(i), raw); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			bad[name] = true
		}
	}
	errs = append(errs, c.finish(bad)...)
	if len(errs) > 0 {
		return nil, &LoadError{Errs: errs}
	}
	return c, nil
}

// LoadError lists every configuration problem at once.
type LoadError struct{ Errs []error }

func (e *LoadError) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, err := range e.Errs {
		b.WriteString("\n  - ")
		b.WriteString(err.Error())
	}
	return b.String()
}

func setField(f reflect.Value, raw string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("want an integer, got %q", raw)
		}
		f.SetInt(int64(n))
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("want true/false, got %q", raw)
		}
		f.SetBool(b)
	case reflect.Int64: // time.Duration
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("want a duration like 20s, got %q", raw)
		}
		f.SetInt(int64(d))
	case reflect.Slice:
		var parts []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		f.Set(reflect.ValueOf(parts))
	default:
		return fmt.Errorf("unsupported field kind %s", f.Kind())
	}
	return nil
}

func (c *Config) finish(bad map[string]bool) []error {
	var errs []error
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("ST_PUBLIC_URL: want an absolute http(s) URL, got %q", c.PublicURL))
	} else {
		c.PublicURL = strings.TrimRight(c.PublicURL, "/")
		if c.Dev && u.Scheme == "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
			errs = append(errs, errors.New("ST_DEV: refused because ST_PUBLIC_URL is a public https URL"))
		}
	}
	if c.PortalURL == "" {
		c.PortalURL = c.PublicURL
	}
	if c.AgentURL == "" {
		c.AgentURL = c.PublicURL + "/agent"
	}
	if c.DatabaseURL != "" && !strings.HasPrefix(c.DatabaseURL, "sqlite://") && !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		errs = append(errs, fmt.Errorf("ST_DATABASE_URL: want sqlite:///path or postgres://…"))
	}
	if c.BlobURL != "" && !strings.HasPrefix(c.BlobURL, "s3://") && !strings.HasPrefix(c.BlobURL, "file://") {
		errs = append(errs, fmt.Errorf("ST_BLOB_URL: want s3://bucket/prefix?… or file:///path"))
	}
	if strings.HasPrefix(c.BlobURL, "s3://") && (c.getenv("ST_S3_ACCESS_KEY") == "" || c.getenv("ST_S3_SECRET_KEY") == "") {
		errs = append(errs, errors.New("ST_S3_ACCESS_KEY / ST_S3_SECRET_KEY: required when ST_BLOB_URL is s3://"))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("ST_LOG_LEVEL: want debug|info|warn|error, got %q", c.LogLevel))
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("ST_LOG_FORMAT: want json|text, got %q", c.LogFormat))
	}
	for name, n := range map[string]int{"ST_DB_READERS": c.DBReaders, "ST_DB_POOL": c.DBPool, "ST_QUEUE_CAPACITY": c.QueueCapacity, "ST_WS_MAX_CONNS": c.WSMaxConns, "ST_WS_MAX_CONNS_PER_USER": c.WSMaxConnsUser, "ST_MAX_BODY_KB": c.MaxBodyKB, "ST_MAX_UPLOAD_MB": c.MaxUploadMB} {
		if n <= 0 && !bad[name] {
			errs = append(errs, fmt.Errorf("%s: must be > 0", name))
		}
	}
	for _, o := range c.AllowedOrigins {
		if u, err := url.Parse(strings.TrimSpace(o)); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			errs = append(errs, fmt.Errorf("ST_ALLOWED_ORIGINS: %q is not an origin like https://host[:port]", o))
		}
	}
	for _, cidr := range c.TrustedProxies {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			if a, err2 := netip.ParseAddr(cidr); err2 == nil {
				p = netip.PrefixFrom(a, a.BitLen())
			} else {
				errs = append(errs, fmt.Errorf("ST_TRUSTED_PROXIES: %q is not a CIDR", cidr))
				continue
			}
		}
		c.TrustedNets = append(c.TrustedNets, p)
	}
	key, err := c.resolveSecret()
	if err != nil {
		errs = append(errs, err)
	}
	c.SecretKey = key
	if c.SecretKeyPrevRaw != "" {
		if c.SecretKeyPrev, err = decodeSecret(c.SecretKeyPrevRaw); err != nil {
			errs = append(errs, fmt.Errorf("ST_SECRET_KEY_PREVIOUS: %w", err))
		}
	}
	return errs
}

func (c *Config) resolveSecret() ([]byte, error) {
	if c.SecretKeyRaw != "" {
		k, err := decodeSecret(c.SecretKeyRaw)
		if err != nil {
			return nil, fmt.Errorf("ST_SECRET_KEY: %w", err)
		}
		return k, nil
	}
	if !c.Dev {
		return nil, errors.New("ST_SECRET_KEY: required (32+ random bytes, e.g. `openssl rand -base64 32`)")
	}
	path := filepath.Join(c.DataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		return decodeSecret(strings.TrimSpace(string(b)))
	}
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	if err := os.MkdirAll(c.DataDir, 0o700); err == nil {
		_ = os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)), 0o600)
	}
	return k, nil
}

func decodeSecret(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) >= 32 {
			return b, nil
		}
	}
	if len(s) >= 32 {
		return []byte(s), nil
	}
	return nil, errors.New("must be at least 32 bytes (base64 or raw)")
}

// SQLitePath is where the default SQLite database lives.
func (c *Config) SQLitePath() string { return filepath.Join(c.DataDir, "simpletickets.db") }

// EffectiveDatabaseURL resolves the empty default to the SQLite file in DataDir.
func (c *Config) EffectiveDatabaseURL() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return "sqlite://" + c.SQLitePath()
}

// PublicOrigin is scheme://host[:port] of ST_PUBLIC_URL (for Origin checks).
func (c *Config) PublicOrigin() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Origins lists every browser origin accepted by the CSRF and WebSocket
// Origin checks: ST_PUBLIC_URL first, then ST_ALLOWED_ORIGINS.
func (c *Config) Origins() []string {
	out := []string{}
	if o := c.PublicOrigin(); o != "" {
		out = append(out, o)
	}
	for _, o := range c.AllowedOrigins {
		if u, err := url.Parse(strings.TrimSpace(o)); err == nil && u.Host != "" {
			out = append(out, u.Scheme+"://"+u.Host)
		}
	}
	return out
}

// Redacted returns the config as key → value with secrets masked, for logs.
func (c *Config) Redacted() map[string]string {
	out := map[string]string{}
	v := reflect.ValueOf(c).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		if name == "" || name == "-" {
			continue
		}
		val := fmt.Sprint(v.Field(i).Interface())
		if f.Tag.Get("secret") == "true" && val != "" {
			val = "***"
		}
		if name == "ST_DATABASE_URL" {
			val = RedactURL(val)
		}
		out[name] = val
	}
	return out
}

// RedactURL hides the password of a URL with userinfo.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxx")
	}
	return u.String()
}

// Markdown renders the core variables as a table for docs/configuration.md.
func Markdown() string {
	var rows []string
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("env")
		if name == "" || name == "-" {
			continue
		}
		def := f.Tag.Get("default")
		if def == "" {
			def = "—"
		} else {
			def = "`" + def + "`"
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s | %s |", name, def, f.Tag.Get("desc")))
	}
	sort.Strings(rows)
	return "| Variable | Default | Description |\n|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
}
