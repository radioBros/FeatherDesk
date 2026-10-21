// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// IMAPConfig is the stored `inbound_mailboxes.imap` document. Fields with an
// `env` tag are overridden (and locked) by ST_IMAP_* for the env mailbox.
type IMAPConfig struct {
	Host             string `json:"host" env:"HOST"`
	Port             int    `json:"port" env:"PORT"`
	Security         string `json:"security" env:"SECURITY"` // none | starttls | tls
	Username         string `json:"username" env:"USERNAME"`
	PasswordEnc      string `json:"password_enc,omitempty"`
	Folder           string `json:"folder" env:"FOLDER"`
	ProcessedAction  string `json:"processed_action"` // move | flag
	ProcessedFolder  string `json:"processed_folder"`
	Auth             string `json:"auth"` // password | xoauth2
	AllowInsecureTLS bool   `json:"allow_insecure_tls,omitempty"`

	// XOAUTH2 (Gmail / M365): refresh token sealed with PurposeIMAPRefresh;
	// the access token lives in memory only.
	OAuthTokenURL     string `json:"oauth_token_url,omitempty"`
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthClientSecret string `json:"oauth_client_secret_enc,omitempty"`
	OAuthRefreshEnc   string `json:"oauth_refresh_token_enc,omitempty"`
	TrustedAuthservID string `json:"trusted_authserv_id,omitempty"`
	AllowListMail     bool   `json:"allow_list_mail,omitempty"`
	EnvManaged        bool   `json:"env_managed,omitempty"`
}

func (c *IMAPConfig) defaults() {
	if c.Port == 0 {
		c.Port = 993
	}
	if c.Security == "" {
		c.Security = "tls"
	}
	if c.Folder == "" {
		c.Folder = "INBOX"
	}
	if c.ProcessedAction == "" {
		c.ProcessedAction = "move"
	}
	if c.ProcessedFolder == "" {
		c.ProcessedFolder = "SimpleTickets/Processed"
	}
	if c.Auth == "" {
		c.Auth = "password"
	}
}

// mailbox is one inbound_mailboxes row with the effective IMAP config.
type mailbox struct {
	ID            string
	Name          string
	Kind          string // imap | webhook
	TeamID        string
	Address       string
	IMAP          IMAPConfig // effective (env overlaid when EnvManaged)
	Stored        IMAPConfig // as stored
	TokenHash     string
	HMACSecretEnc string
	Active        bool
	LastOKAt      sql.NullTime
	LastError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LockedByEnv   []string
}

const mailboxCols = `id, name, kind, team_id, address, imap, webhook_token_hash, hmac_secret_enc, active, last_ok_at, last_error, created_at, updated_at`

func (m *Module) scanMailbox(row interface{ Scan(...any) error }) (*mailbox, error) {
	var mb mailbox
	var team, tok sql.NullString
	var imapRaw store.RawJSON
	if err := row.Scan(&mb.ID, &mb.Name, &mb.Kind, &team, &mb.Address, &imapRaw, &tok, &mb.HMACSecretEnc, &mb.Active, &mb.LastOKAt, &mb.LastError, &mb.CreatedAt, &mb.UpdatedAt); err != nil {
		return nil, err
	}
	mb.TeamID, mb.TokenHash = team.String, tok.String
	if len(imapRaw) > 0 {
		_ = json.Unmarshal(imapRaw, &mb.Stored)
	}
	mb.IMAP = mb.Stored
	if mb.Kind == "imap" {
		mb.IMAP.defaults()
	}
	if mb.Stored.EnvManaged {
		mb.LockedByEnv = m.overlayEnv(&mb)
	}
	if mb.LockedByEnv == nil {
		mb.LockedByEnv = []string{}
	}
	return &mb, nil
}

// overlayEnv applies ST_IMAP_* to the env mailbox and returns locked fields.
func (m *Module) overlayEnv(mb *mailbox) []string {
	locked := settings.Overlay(&mb.IMAP, "ST_IMAP_", m.d.Getenv)
	if m.d.Getenv("ST_IMAP_PASSWORD") != "" {
		locked = append(locked, "password")
	}
	if t := strings.TrimSpace(m.d.Getenv("ST_IMAP_TEAM")); t != "" {
		locked = append(locked, "team_id")
	}
	return locked
}

func (m *Module) getMailbox(ctx context.Context, id string) (*mailbox, error) {
	mb, err := m.scanMailbox(m.d.DB.Read().QueryRowContext(ctx, `SELECT `+mailboxCols+` FROM inbound_mailboxes WHERE id = ?`, id))
	if err != nil {
		return nil, store.NotFound(err)
	}
	return mb, nil
}

func (m *Module) listMailboxes(ctx context.Context, onlyActiveIMAP bool) ([]*mailbox, error) {
	q := `SELECT ` + mailboxCols + ` FROM inbound_mailboxes`
	if onlyActiveIMAP {
		q += ` WHERE active AND kind = 'imap'`
	}
	rows, err := m.d.DB.Read().QueryContext(ctx, q+` ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*mailbox
	for rows.Next() {
		mb, err := m.scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mb)
	}
	return out, rows.Err()
}

// imapPassword returns the effective plaintext password (env beats stored).
func (m *Module) imapPassword(mb *mailbox) (string, error) {
	if mb.Stored.EnvManaged {
		if p := m.d.Getenv("ST_IMAP_PASSWORD"); p != "" {
			return p, nil
		}
	}
	if mb.Stored.PasswordEnc == "" {
		return "", nil
	}
	return m.d.Secrets.OpenString(settings.PurposeIMAPPassword, mb.Stored.PasswordEnc)
}

// ensureEnvMailbox creates the env-defined IMAP mailbox once when
// ST_IMAP_HOST is set (its fields stay env-locked).
func (m *Module) ensureEnvMailbox(ctx context.Context) error {
	if strings.TrimSpace(m.d.Getenv("ST_IMAP_HOST")) == "" {
		return nil
	}
	all, err := m.listMailboxes(ctx, false)
	if err != nil {
		return err
	}
	for _, mb := range all {
		if mb.Stored.EnvManaged {
			return nil
		}
	}
	cfg := IMAPConfig{EnvManaged: true}
	settings.Overlay(&cfg, "ST_IMAP_", m.d.Getenv)
	cfg.defaults()
	addr := m.d.Getenv("ST_IMAP_ADDRESS")
	if addr == "" {
		addr = cfg.Username
	}
	if !strings.Contains(addr, "@") {
		addr = cfg.Username + "@" + cfg.Host
	}
	var team string
	team, err = m.resolveEnvTeam(ctx)
	if err != nil {
		return err
	}
	now := store.Now()
	return m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO inbound_mailboxes (id, name, kind, team_id, address, imap, active, created_at, updated_at) VALUES (?, ?, 'imap', ?, ?, ?, TRUE, ?, ?)`,
			store.NewID(), "Environment mailbox", store.Nullable(team), strings.ToLower(addr), store.MustJSON(cfg), now, now)
		return err
	})
}

// resolveEnvTeam maps ST_IMAP_TEAM (team id or name) to a team id.
func (m *Module) resolveEnvTeam(ctx context.Context) (string, error) {
	t := strings.TrimSpace(m.d.Getenv("ST_IMAP_TEAM"))
	if t == "" {
		return "", nil
	}
	var id string
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM teams WHERE id = ? OR lower(name) = lower(?)`, t, t).Scan(&id)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return "", nil
	}
	return id, err
}

// effectiveTeam returns the team for new tickets of mb (env lock applies).
func (m *Module) effectiveTeam(ctx context.Context, mb *mailbox) string {
	if mb.Stored.EnvManaged {
		if t, err := m.resolveEnvTeam(ctx); err == nil && t != "" {
			return t
		}
	}
	return mb.TeamID
}
