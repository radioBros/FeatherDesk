// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Two-factor authentication for local accounts (D-19 §B, AUTH-035…AUTH-043).
//
//   - A first factor (password, magic link, verification or invite link) that
//     succeeds for a user with MFA, or for a user the policy forces to enrol,
//     yields an mfa_challenge instead of a session. Only completing the
//     challenge (POST /auth/mfa/verify or /auth/mfa/enroll/confirm) creates
//     the session. OIDC logins and the setup wizard never pass through here.
//   - Every stored value is useless on its own: TOTP secrets are sealed with
//     crypto.Box (settings.PurposeMFA), challenge tokens and recovery codes
//     are sha256, e-mail codes are an HMAC under ST_SECRET_KEY.

// Methods.
const (
	MFAMethodTOTP     = "totp"
	MFAMethodEmail    = "email"
	MFAMethodRecovery = "recovery"
)

// Policy values of settings `security.mfa_required`.
const (
	MFARequiredNone  = "none"
	MFARequiredStaff = "staff"
	MFARequiredAll   = "all"
)

// KeySecurity is the settings document holding the MFA policy.
const KeySecurity = "security"

const (
	mfaChallengeTTL   = 5 * time.Minute
	mfaEmailCodeTTL   = 10 * time.Minute
	mfaMaxAttempts    = 5
	mfaEmailMaxSends  = 3
	mfaEmailResend    = 30 * time.Second
	mfaPendingTTL     = 15 * time.Minute // /me/mfa/totp/setup → confirm
	mfaReauthTTL      = 10 * time.Minute
	mfaRecoveryCount  = 10
	mfaReauthMax      = 10_000
	purposeEmailCode  = "mfa-email-code"
	recoveryAlphabet  = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	recoveryGroupSize = 4
	recoveryGroups    = 3 // xxxx-xxxx-xxxx ≈ 59 bits
)

// SecuritySettings is the `security` settings document.
type SecuritySettings struct {
	MFARequired string `json:"mfa_required"`
}

// ValidMFAPolicy reports whether p is a policy value.
func ValidMFAPolicy(p string) bool {
	return p == MFARequiredNone || p == MFARequiredStaff || p == MFARequiredAll
}

// mfaPolicy reads `security.mfa_required` (cached by the settings service).
func (m *Module) mfaPolicy(ctx context.Context) (string, error) {
	s := SecuritySettings{MFARequired: MFARequiredNone}
	if m.d.Settings == nil {
		return s.MFARequired, nil
	}
	if err := m.d.Settings.Get(ctx, KeySecurity, &s); err != nil {
		return "", err
	}
	if !ValidMFAPolicy(s.MFARequired) {
		s.MFARequired = MFARequiredNone
	}
	return s.MFARequired, nil
}

// mfaApplies: SSO-managed accounts get MFA from their IdP (D-19 §B scope).
// A break-glass admin keeps local login even when IdP-managed (AUTH-044), so
// every local sign-in it makes goes through the second step and the policy.
func mfaApplies(u userRow) bool { return !u.IDPManaged || breakGlassAdmin(u) }

// mfaRequiredFor reports whether policy forces u to have a second factor.
func mfaRequiredFor(policy string, u userRow) bool {
	if !mfaApplies(u) {
		return false
	}
	switch policy {
	case MFARequiredAll:
		return true
	case MFARequiredStaff:
		return u.Kind == domain.KindStaff
	}
	return false
}

// ---- user_mfa ----

type mfaState struct {
	TOTPSecretEnc string
	TOTPEnabledAt store.NullTime
	LastCounter   sql.NullInt64
	PendingEnc    string
	PendingAt     store.NullTime
	EmailEnabled  bool
}

func (s mfaState) totp() bool    { return s.TOTPSecretEnc != "" }
func (s mfaState) enabled() bool { return s.totp() || s.EmailEnabled }

// loadMFA returns the user's row (zero value when none).
func loadMFA(ctx context.Context, q store.Querier, userID string) (mfaState, error) {
	var s mfaState
	err := q.QueryRowContext(ctx, `SELECT totp_secret_enc, totp_enabled_at, totp_last_counter, totp_pending_enc, totp_pending_at, email_enabled
		FROM user_mfa WHERE user_id = ?`, userID).Scan(&s.TOTPSecretEnc, &s.TOTPEnabledAt, &s.LastCounter, &s.PendingEnc, &s.PendingAt, &s.EmailEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return mfaState{}, nil
	}
	return s, err
}

// ensureMFARow creates the user's user_mfa row when missing.
func ensureMFARow(ctx context.Context, tx *store.Tx, userID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO user_mfa (user_id, created_at, updated_at) VALUES (?, ?, ?) ON CONFLICT (user_id) DO NOTHING`, userID, now, now)
	return err
}

// recoveryRemaining counts unused recovery codes.
func recoveryRemaining(ctx context.Context, q store.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM mfa_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// useTOTPCounter records step ctr as used; false means it (or a later step)
// was already accepted: a replayed code. One guarded UPDATE, so two
// concurrent requests with the same code have one winner.
func useTOTPCounter(ctx context.Context, tx *store.Tx, userID string, ctr uint64, now time.Time) (bool, error) {
	res, err := tx.ExecContext(ctx, `UPDATE user_mfa SET totp_last_counter = ?, updated_at = ? WHERE user_id = ? AND (totp_last_counter IS NULL OR totp_last_counter < ?)`,
		int64(ctr), now, userID, int64(ctr))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ---- recovery codes ----

func newRecoveryCode() string {
	var b strings.Builder
	lim := big.NewInt(int64(len(recoveryAlphabet)))
	for g := range recoveryGroups {
		if g > 0 {
			b.WriteByte('-')
		}
		for range recoveryGroupSize {
			n, err := rand.Int(rand.Reader, lim)
			if err != nil {
				panic(err)
			}
			b.WriteByte(recoveryAlphabet[n.Int64()])
		}
	}
	return b.String()
}

// normRecovery lower-cases a typed code and drops separators.
func normRecovery(s string) string { return strings.ToLower(normCode(s)) }

func recoveryHash(userID, code string) string { return hashToken(userID + ":" + normRecovery(code)) }

// replaceRecoveryCodes deletes every code of userID and stores n fresh ones;
// the clear values are returned once and never stored.
func replaceRecoveryCodes(ctx context.Context, tx *store.Tx, userID string, now time.Time) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, mfaRecoveryCount)
	seen := map[string]bool{}
	for len(codes) < mfaRecoveryCount {
		c := newRecoveryCode()
		if seen[c] {
			continue
		}
		seen[c] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO mfa_recovery_codes (id, user_id, code_hash, created_at) VALUES (?, ?, ?, ?)`,
			store.NewID(), userID, recoveryHash(userID, c), now); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, nil
}

// useRecoveryCode consumes one code (single use: guarded UPDATE).
func useRecoveryCode(ctx context.Context, tx *store.Tx, userID, code string, now time.Time) (bool, error) {
	c := normRecovery(code)
	if len(c) != recoveryGroups*recoveryGroupSize {
		return false, nil
	}
	res, err := tx.ExecContext(ctx, `UPDATE mfa_recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
		now, userID, recoveryHash(userID, c))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ---- e-mail codes ----

func newEmailCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	s := n.String()
	return strings.Repeat("0", 6-len(s)) + s
}

// emailCodeMAC binds a code to its challenge under ST_SECRET_KEY: six digits
// under a plain hash would fall to an offline search in milliseconds.
func (m *Module) emailCodeMAC(challengeID, code string) string {
	return hex.EncodeToString(m.d.Secrets.MAC(purposeEmailCode, []byte(challengeID+":"+code)))
}

func (m *Module) emailCodeOK(challengeID, code, stored string) bool {
	if stored == "" || !isDigits(code, 6) {
		return false
	}
	return hmac.Equal([]byte(m.emailCodeMAC(challengeID, code)), []byte(stored))
}

// ---- TOTP secrets ----

func (m *Module) sealTOTP(secret string) (string, error) {
	return m.d.Secrets.SealString(settings.PurposeMFA, secret)
}

func (m *Module) openTOTP(sealed string) ([]byte, bool) {
	s, err := m.d.Secrets.OpenString(settings.PurposeMFA, sealed)
	if err != nil || s == "" {
		return nil, false
	}
	return decodeTOTPSecret(s)
}

// totpIssuer is the product name shown by authenticator apps.
func (m *Module) totpIssuer(ctx context.Context) string {
	if m.d.Settings != nil {
		if b, err := m.d.Settings.Branding(ctx); err == nil && strings.TrimSpace(b.ProductName) != "" {
			return strings.TrimSpace(b.ProductName)
		}
	}
	return "SimpleTickets"
}

// ---- re-authentication window (in memory, single node like ws-tickets) ----

type reauthSet struct {
	mu sync.Mutex
	m  map[string]time.Time // session id_hash → expiry
}

func newReauthSet() *reauthSet { return &reauthSet{m: map[string]time.Time{}} }

func (s *reauthSet) mark(idHash string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= mfaReauthMax {
		now := until.Add(-mfaReauthTTL)
		for k, v := range s.m {
			if !v.After(now) {
				delete(s.m, k)
			}
		}
		if len(s.m) >= mfaReauthMax {
			s.m = map[string]time.Time{}
		}
	}
	s.m[idHash] = until
}

func (s *reauthSet) until(idHash string, now time.Time) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[idHash]
	if ok && !now.Before(t) {
		delete(s.m, idHash)
		return time.Time{}, false
	}
	return t, ok
}

// ---- challenges ----

type mfaChallenge struct {
	ID             string
	UserID         string
	Purpose        string
	LoginMethod    string
	PendingEnc     string
	EmailCodeHash  string
	EmailExpiresAt store.NullTime
	EmailSentAt    store.NullTime
	EmailSends     int
	Attempts       int
	ExpiresAt      time.Time
}

var errChallengeInvalid = errors.New("authn: mfa challenge invalid or expired")

// issueChallenge stores a new challenge for u and returns its raw token.
// Earlier open challenges of the user are retired (one at a time).
func (m *Module) issueChallenge(ctx context.Context, tx *store.Tx, userID, purpose, method, ip string) (string, time.Time, error) {
	now := m.nowDB()
	if _, err := tx.ExecContext(ctx, `UPDATE mfa_challenges SET used_at = ? WHERE user_id = ? AND used_at IS NULL`, now, userID); err != nil {
		return "", time.Time{}, err
	}
	tok := randomToken(32)
	exp := now.Add(mfaChallengeTTL)
	_, err := tx.ExecContext(ctx, `INSERT INTO mfa_challenges (id, token_hash, user_id, purpose, login_method, ip, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), hashToken(tok), userID, purpose, method, truncate(ip, 64), exp, now)
	return tok, exp, err
}

// loadChallenge returns an open challenge (unused, unexpired, attempts left).
func (m *Module) loadChallenge(ctx context.Context, q store.Querier, tok string) (mfaChallenge, error) {
	var c mfaChallenge
	if tok == "" || len(tok) > 128 {
		return c, errChallengeInvalid
	}
	err := q.QueryRowContext(ctx, `SELECT id, user_id, purpose, login_method, totp_pending_enc, email_code_hash, email_expires_at, email_sent_at, email_sends, attempts, expires_at
		FROM mfa_challenges WHERE token_hash = ? AND used_at IS NULL AND expires_at > ? AND attempts < ?`, hashToken(tok), m.nowDB(), mfaMaxAttempts).
		Scan(&c.ID, &c.UserID, &c.Purpose, &c.LoginMethod, &c.PendingEnc, &c.EmailCodeHash, &c.EmailExpiresAt, &c.EmailSentAt, &c.EmailSends, &c.Attempts, &c.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, errChallengeInvalid
	}
	return c, err
}

// consumeChallenge marks c used; false when a concurrent request won, it
// expired meanwhile or its attempts ran out.
func (m *Module) consumeChallenge(ctx context.Context, tx *store.Tx, id string) (bool, error) {
	now := m.nowDB()
	res, err := tx.ExecContext(ctx, `UPDATE mfa_challenges SET used_at = ?, email_code_hash = '', totp_pending_enc = '' WHERE id = ? AND used_at IS NULL AND expires_at > ? AND attempts < ?`,
		now, id, now, mfaMaxAttempts)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// failChallenge counts one wrong code; it returns true when the challenge is
// now exhausted (the user has to sign in again).
func (m *Module) failChallenge(ctx context.Context, id string) (bool, error) {
	var attempts int
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE mfa_challenges SET attempts = attempts + 1 WHERE id = ? RETURNING attempts`, id).Scan(&attempts)
	})
	if err != nil {
		return false, store.NotFound(err)
	}
	return attempts >= mfaMaxAttempts, nil
}

// ---- pruning ----

// PruneMFAChallenges deletes challenges that expired or were used more than
// a day ago, and stale unconfirmed TOTP setups.
func PruneMFAChallenges(ctx context.Context, db *store.DB, now time.Time) error {
	now = now.UTC()
	return db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_challenges WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?)`, now.Add(-time.Hour), now.Add(-24*time.Hour)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE user_mfa SET totp_pending_enc = '', totp_pending_at = NULL WHERE totp_pending_enc <> '' AND totp_pending_at < ?`, now.Add(-mfaPendingTTL))
		return err
	})
}
