// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Single-use email token purposes (AUTH-013) and their lifetimes.
const (
	PurposeVerifyEmail   = "verify_email"
	PurposeResetPassword = "reset_password"
	PurposeMagicLink     = "magic_link"
	PurposeInvite        = "invite"
)

var authTokenTTL = map[string]time.Duration{
	PurposeVerifyEmail:   48 * time.Hour,
	PurposeResetPassword: time.Hour,
	PurposeMagicLink:     15 * time.Minute,
	PurposeInvite:        7 * 24 * time.Hour,
}

// ErrTokenInvalid: unknown, used, expired or wrong-purpose token.
var ErrTokenInvalid = errors.New("authn: token invalid or expired")

// IssueAuthToken creates a single-use token for userID (32 random bytes,
// only sha256 stored). Earlier unused tokens of the same purpose are retired.
func IssueAuthToken(ctx context.Context, tx *store.Tx, userID, purpose string, data map[string]any) (string, error) {
	return issueAuthTokenAt(ctx, tx, userID, purpose, data, store.Now())
}

func issueAuthTokenAt(ctx context.Context, tx *store.Tx, userID, purpose string, data map[string]any, now time.Time) (string, error) {
	ttl, ok := authTokenTTL[purpose]
	if !ok {
		return "", errors.New("authn: unknown token purpose " + purpose)
	}
	if data == nil {
		data = map[string]any{}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_tokens SET used_at = ? WHERE user_id = ? AND purpose = ? AND used_at IS NULL`, now, userID, purpose); err != nil {
		return "", err
	}
	tok := randomToken(32)
	_, err := tx.ExecContext(ctx, `INSERT INTO auth_tokens (id, user_id, purpose, token_hash, data, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), userID, purpose, hashToken(tok), store.MustJSON(data), now.Add(ttl), now)
	return tok, err
}

// ConsumeAuthToken atomically marks the token used and returns its user and
// data: one UPDATE … RETURNING, so concurrent consumers have one winner.
func ConsumeAuthToken(ctx context.Context, tx *store.Tx, tok, purpose string) (string, map[string]any, error) {
	return consumeAuthTokenAt(ctx, tx, tok, purpose, store.Now())
}

func consumeAuthTokenAt(ctx context.Context, tx *store.Tx, tok, purpose string, now time.Time) (string, map[string]any, error) {
	if tok == "" || len(tok) > 128 {
		return "", nil, ErrTokenInvalid
	}
	var (
		userID string
		data   store.JSON[map[string]any]
	)
	err := tx.QueryRowContext(ctx, `UPDATE auth_tokens SET used_at = ? WHERE token_hash = ? AND purpose = ? AND used_at IS NULL AND expires_at > ? RETURNING user_id, data`,
		now, hashToken(tok), purpose, now).Scan(&userID, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrTokenInvalid
	}
	if err != nil {
		return "", nil, err
	}
	if data.V == nil {
		data.V = map[string]any{}
	}
	return userID, data.V, nil
}

// MailPayload is the "mail.auth" job handled by the notifications module.
type MailPayload struct {
	Template string            `json:"template"` // auth.verify_email | auth.reset_password | auth.magic_link | auth.invite | auth.already_registered
	UserID   string            `json:"user_id"`
	To       string            `json:"to"`
	Locale   string            `json:"locale"`
	Vars     map[string]string `json:"vars"`
}

// Auth mail templates.
const (
	TplVerifyEmail       = "auth.verify_email"
	TplResetPassword     = "auth.reset_password"
	TplMagicLink         = "auth.magic_link"
	TplInvite            = "auth.invite"
	TplAlreadyRegistered = "auth.already_registered"
)

// EnqueueAuthMail submits p after commit; a failure is logged (the user can
// ask again), never surfaced to the caller (no enumeration).
func EnqueueAuthMail(ctx context.Context, q *queue.Queue, log *slog.Logger, p MailPayload) {
	if q == nil {
		return
	}
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	if !domain.ValidLocale(p.Locale) {
		p.Locale = domain.Locales[0]
	}
	if err := q.Enqueue(context.WithoutCancel(ctx), MailJobKind, p); err != nil && log != nil {
		log.Error("auth mail not queued", "template", p.Template, "user_id", p.UserID, "err", err)
	}
}

func (m *Module) enqueueMail(ctx context.Context, p MailPayload) error {
	EnqueueAuthMail(ctx, m.d.Queue, m.log, p)
	return nil
}

// LinkURL builds the SPA landing link for a token: customer links point at the
// portal, staff links at the agent area (e.g. /invite?token=…).
func LinkURL(cfg *config.Config, kind domain.UserKind, page, token string) string {
	base := cfg.PortalURL
	if kind == domain.KindStaff {
		base = cfg.AgentURL
	}
	return strings.TrimRight(base, "/") + page + "?" + url.Values{"token": {token}}.Encode()
}
