// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// setupState is the first-run wizard (AUTH-033). The setup token lives only
// in memory (its sha256) and in the log; it is discarded once an admin exists.
type setupState struct {
	mu   sync.Mutex
	hash []byte
}

func (s *setupState) pending() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hash != nil
}

func (s *setupState) check(tok string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hash == nil {
		return false
	}
	h := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(h[:], s.hash) == 1
}

func (s *setupState) disarm() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.hash = nil
	s.mu.Unlock()
}

// activeAdminExists reports whether any active staff admin exists.
func activeAdminExists(ctx context.Context, q store.Querier) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kind = 'staff' AND role = 'admin' AND status = 'active'`).Scan(&n)
	return n > 0, err
}

// armSetup prints a fresh setup token when the instance has no admin and no
// env bootstrap; otherwise the wizard stays closed (every /setup route 404).
func (m *Module) armSetup(ctx context.Context) error {
	has, err := activeAdminExists(ctx, m.d.DB.Read())
	if err != nil {
		return err
	}
	m.hasAdmin.Store(has)
	m.setup = &setupState{}
	if has || m.opt.BootstrapEmail != "" {
		return nil
	}
	tok := randomToken(16) // 128 bit
	h := sha256.Sum256([]byte(tok))
	m.setup.hash = h[:]
	m.log.Warn("setup token: " + tok + " — open " + strings.TrimRight(m.d.Cfg.PublicURL, "/") + "/setup to create the first admin")
	return nil
}

// bootstrapAdmin creates the break-glass admin from ST_BOOTSTRAP_ADMIN_* on
// an empty users table; a no-op otherwise (AUTH-009).
func (m *Module) bootstrapAdmin(ctx context.Context) error {
	if m.opt.BootstrapEmail == "" {
		return nil
	}
	var n int
	if err := m.d.DB.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := CreateAdmin(ctx, m.d.DB, m.opt.BootstrapEmail, "Administrator", m.opt.BootstrapPassword, "bootstrap"); err != nil {
		return fmt.Errorf("authn: bootstrap admin: %w", err)
	}
	m.log.Warn("break-glass admin created from ST_BOOTSTRAP_ADMIN_EMAIL; unset ST_BOOTSTRAP_ADMIN_PASSWORD now", "email", m.opt.BootstrapEmail)
	return nil
}

// ErrUserExists is returned by CreateAdmin for a taken email.
var ErrUserExists = errors.New("authn: a user with this email already exists")

// CreateAdmin inserts a local break-glass staff admin (CLI create-admin and
// env bootstrap). An empty password generates one, which is returned.
func CreateAdmin(ctx context.Context, db *store.DB, email, name, password, via string) (generated string, err error) {
	email = normEmail(email)
	if !validEmail(email) {
		return "", errors.New("authn: invalid email")
	}
	if strings.TrimSpace(name) == "" {
		name = "Administrator"
	}
	if password == "" {
		password = randomToken(15)
		generated = password
	} else if c := PasswordProblem(password); c != "" {
		return "", errors.New("authn: password rejected: " + c)
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	id := store.NewID()
	err = db.Tx(ctx, func(tx *store.Tx) error {
		if err := insertAdmin(ctx, tx, id, email, name, hash, "en"); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{Action: "auth.admin_created", TargetType: "user", TargetID: id, Data: map[string]any{"via": via}})
	})
	if store.IsUniqueViolation(err) {
		return "", ErrUserExists
	}
	return generated, err
}

func insertAdmin(ctx context.Context, tx *store.Tx, id, email, name, hash, locale string) error {
	now := store.Now()
	_, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, tier, status, password_hash, break_glass, locale, created_at, updated_at)
		VALUES (?, 'staff', ?, ?, 'admin', 3, 'active', ?, ?, ?, ?, ?)`, id, email, name, hash, true, locale, now, now)
	return err
}

// ResetPassword sets a new local password for email and revokes every
// session of the user (CLI reset-password). Empty password generates one.
func ResetPassword(ctx context.Context, db *store.DB, email, password string) (generated string, err error) {
	if password == "" {
		password = randomToken(15)
		generated = password
	} else if c := PasswordProblem(password); c != "" {
		return "", errors.New("authn: password rejected: " + c)
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	err = db.Tx(ctx, func(tx *store.Tx) error {
		u, err := loadUser(ctx, tx, "u.email = ?", normEmail(email))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, store.Now(), u.ID); err != nil {
			return err
		}
		if err := RevokeUserSessions(ctx, tx, u.ID, ""); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{Action: "auth.password_reset", TargetType: "user", TargetID: u.ID, Data: map[string]any{"via": "cli"}})
	})
	return generated, err
}

// ---- HTTP ----

func (m *Module) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if !m.setup.pending() {
		problems.Write(w, r, problems.NotFound())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"setup_pending": true, "has_admin": false, "env_bootstrap": false})
}

type setupRequest struct {
	SetupToken string `json:"setup_token"`
	Admin      struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	} `json:"admin"`
	Branding *struct {
		ProductName  *string `json:"product_name"`
		CompanyName  *string `json:"company_name"`
		PrimaryColor *string `json:"primary_color"`
	} `json:"branding"`
	SMTP *struct {
		Host             string  `json:"host"`
		Port             int     `json:"port"`
		Security         string  `json:"security"`
		Username         string  `json:"username"`
		Password         *string `json:"password"`
		Auth             string  `json:"auth"`
		HELO             string  `json:"helo"`
		TimeoutSeconds   int     `json:"timeout_seconds"`
		AllowInsecureTLS bool    `json:"allow_insecure_tls"`
	} `json:"smtp"`
	Senders *struct {
		Customer *senderIdentity `json:"customer"`
		Staff    *senderIdentity `json:"staff"`
	} `json:"senders"`
	Locale string `json:"locale"`
}

type senderIdentity struct {
	FromName    string  `json:"from_name"`
	FromAddress string  `json:"from_address"`
	ReplyTo     *string `json:"reply_to"`
}

func (in *setupRequest) validate() []problems.FieldError {
	var f []problems.FieldError
	in.Admin.Email, in.Admin.Name = normEmail(in.Admin.Email), strings.TrimSpace(in.Admin.Name)
	if !validEmail(in.Admin.Email) {
		f = append(f, problems.Field("admin.email", "email", "invalid email"))
	}
	if n := utf8.RuneCountInString(in.Admin.Name); n < 1 || n > 120 {
		f = append(f, problems.Field("admin.name", "length", "1 to 120 characters"))
	}
	if c := PasswordProblem(in.Admin.Password); c != "" {
		f = append(f, problems.Field("admin.password", c, ""))
	}
	if b := in.Branding; b != nil {
		if b.ProductName != nil && (utf8.RuneCountInString(*b.ProductName) > 60 || strings.TrimSpace(*b.ProductName) == "") {
			f = append(f, problems.Field("branding.product_name", "length", "1 to 60 characters"))
		}
		if b.CompanyName != nil && utf8.RuneCountInString(*b.CompanyName) > 100 {
			f = append(f, problems.Field("branding.company_name", "length", "at most 100 characters"))
		}
		if b.PrimaryColor != nil && !hexColor.MatchString(*b.PrimaryColor) {
			f = append(f, problems.Field("branding.primary_color", "color", "#RRGGBB"))
		}
	}
	if s := in.SMTP; s != nil {
		if strings.TrimSpace(s.Host) == "" {
			f = append(f, problems.Field("smtp.host", "required", ""))
		}
		if s.Port < 1 || s.Port > 65535 {
			f = append(f, problems.Field("smtp.port", "range", "1 to 65535"))
		}
		if s.Security == "" {
			s.Security = "starttls"
		}
		if s.Security != "none" && s.Security != "starttls" && s.Security != "tls" {
			f = append(f, problems.Field("smtp.security", "invalid", ""))
		}
		if s.Auth == "" {
			s.Auth = "plain"
		}
		if s.Auth != "none" && s.Auth != "plain" && s.Auth != "login" && s.Auth != "cram-md5" {
			f = append(f, problems.Field("smtp.auth", "invalid", ""))
		}
		if s.TimeoutSeconds == 0 {
			s.TimeoutSeconds = 10
		}
		if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 120 {
			f = append(f, problems.Field("smtp.timeout_seconds", "range", "1 to 120"))
		}
	}
	if s := in.Senders; s != nil {
		for name, id := range map[string]*senderIdentity{"customer": s.Customer, "staff": s.Staff} {
			if id != nil && !validEmail(normEmail(id.FromAddress)) {
				f = append(f, problems.Field("senders."+name+".from_address", "email", "invalid email"))
			}
		}
	}
	if in.Locale != "" && !domain.ValidLocale(in.Locale) {
		f = append(f, problems.Field("locale", "invalid", ""))
	}
	return f
}

func (m *Module) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !m.setup.pending() {
		problems.Write(w, r, problems.NotFound())
		return
	}
	if m.throttled(w, r, "setup") {
		return
	}
	var in setupRequest
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if !m.setup.check(in.SetupToken) {
		_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return Audit(ctx, tx, AuditEntry{Action: "setup.token_rejected", TargetType: "setup", IP: server.ClientIP(r)})
		})
		problems.Write(w, r, problems.Forbidden("invalid setup token"))
		return
	}
	if f := in.validate(); len(f) > 0 {
		problems.Write(w, r, problems.Validation(f...))
		return
	}
	if in.Locale == "" {
		in.Locale = "en"
	}
	hash, err := HashPassword(ctx, in.Admin.Password)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	// Documents are read before the write transaction (the SQLite writer has
	// one connection; settings.Put would deadlock inside it).
	br, err := m.d.Settings.Branding(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	smtp := settings.DefaultSMTP()
	if err := m.d.Settings.Get(ctx, settings.KeySMTP, &smtp); err != nil {
		problems.Write(w, r, err)
		return
	}
	emailDoc := settings.Email{DisabledTemplates: []string{}, InboundBlocklist: []string{}}
	if err := m.d.Settings.Get(ctx, settings.KeyEmail, &emailDoc); err != nil {
		problems.Write(w, r, err)
		return
	}
	if b := in.Branding; b != nil {
		if b.ProductName != nil {
			br.ProductName = strings.TrimSpace(*b.ProductName)
		}
		if b.CompanyName != nil {
			br.CompanyName = strings.TrimSpace(*b.CompanyName)
		}
		if b.PrimaryColor != nil {
			br.Colors.Primary = *b.PrimaryColor
		}
	}
	if s := in.SMTP; s != nil {
		smtp.Host, smtp.Port, smtp.Security, smtp.Username = strings.TrimSpace(s.Host), s.Port, s.Security, s.Username
		smtp.Auth, smtp.HELO, smtp.TimeoutSeconds, smtp.AllowInsecureTLS = s.Auth, s.HELO, s.TimeoutSeconds, s.AllowInsecureTLS
		if s.Password != nil {
			enc := ""
			if *s.Password != "" {
				if enc, err = m.d.Secrets.SealString(settings.PurposeSMTPPassword, *s.Password); err != nil {
					problems.Write(w, r, err)
					return
				}
			}
			smtp.PasswordEnc = enc
		}
	}
	if s := in.Senders; s != nil {
		conv := func(id *senderIdentity, dst *settings.Identity) {
			if id == nil {
				return
			}
			dst.Name, dst.Address = id.FromName, normEmail(id.FromAddress)
			if id.ReplyTo != nil {
				dst.ReplyTo = *id.ReplyTo
			}
		}
		conv(s.Customer, &emailDoc.CustomerFrom)
		conv(s.Staff, &emailDoc.StaffFrom)
	}

	m.setupMu.Lock()
	defer m.setupMu.Unlock()
	if !m.setup.pending() {
		problems.Write(w, r, problems.NotFound())
		return
	}
	id := store.NewID()
	ip := server.ClientIP(r)
	var sess newSession
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		if err := lockSetup(ctx, tx); err != nil {
			return err
		}
		// Re-check inside the locked transaction: two installers racing
		// cannot both create an admin.
		has, err := activeAdminExists(ctx, tx)
		if err != nil {
			return err
		}
		if has {
			return problems.NotFound()
		}
		if err := insertAdmin(ctx, tx, id, in.Admin.Email, in.Admin.Name, hash, in.Locale); err != nil {
			if store.IsUniqueViolation(err) {
				return problems.Conflict("a user with this email already exists")
			}
			return err
		}
		now := m.nowDB()
		docs := map[string]any{
			settings.KeyBranding: br,
			settings.KeySetup:    settings.Setup{Completed: true, CompletedAt: now.Format("2006-01-02T15:04:05Z07:00")},
		}
		if in.SMTP != nil {
			docs[settings.KeySMTP] = smtp
		}
		if in.Senders != nil {
			docs[settings.KeyEmail] = emailDoc
		}
		for k, v := range docs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
				k, store.MustJSON(v), now, id); err != nil {
				return err
			}
		}
		if sess, err = m.createSession(ctx, tx, r, id, domain.AudienceStaff, sessionExtra{}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: id, Action: "setup.completed", TargetType: "user", TargetID: id, IP: ip,
			Data: map[string]any{"smtp": in.SMTP != nil}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.setup.disarm()
	m.hasAdmin.Store(true)
	m.d.Settings.Invalidate()
	m.invalidateAuthConfig()
	m.log.Info("first-run setup completed", "admin_id", id)
	u, err := loadUser(ctx, m.d.DB.Read(), "u.id = ?", id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	me, err := m.meJSON(ctx, m.d.DB.Read(), u, sess.IDHash, nil)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.setSessionCookie(w, r, sess, domain.AudienceStaff)
	writeJSON(w, http.StatusCreated, me)
}
