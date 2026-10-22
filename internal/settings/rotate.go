// SPDX-License-Identifier: AGPL-3.0-only

package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// RotateReport counts the values re-encrypted by RotateSecrets, per location.
type RotateReport struct {
	Settings  int `json:"settings"`          // settings.smtp password_enc
	Webhooks  int `json:"webhooks"`          // webhooks.secret_enc
	Mailboxes int `json:"inbound_mailboxes"` // hmac_secret_enc + imap password / refresh token
	MFA       int `json:"mfa"`               // user_mfa TOTP secrets (confirmed and pending)
	Total     int `json:"total"`
}

// sealedJSONFields maps a JSON field holding ciphertext to its Box purpose.
var (
	smtpFields = map[string]string{"password_enc": PurposeSMTPPassword}
	imapFields = map[string]string{"password_enc": PurposeIMAPPassword, "oauth_refresh_token_enc": PurposeIMAPRefresh}
)

// RotateSecrets re-encrypts every stored secret with the current FD_SECRET_KEY
// (PLT-019, `featherdesk rotate-secrets`). The box must also hold the old key
// as previous (FD_SECRET_KEY_PREVIOUS) so existing ciphertext opens. Everything
// happens in one write transaction: a value that opens with neither key aborts
// the whole rotation, so nothing is left half-rotated. Afterwards
// FD_SECRET_KEY_PREVIOUS can be removed.
func RotateSecrets(ctx context.Context, db *store.DB, box *crypto.Box) (RotateReport, error) {
	var rep RotateReport
	reseal := func(purpose, sealed, where string) (string, error) {
		pt, err := box.Open(purpose, sealed)
		if err != nil {
			return "", fmt.Errorf("rotate-secrets: %s does not open with FD_SECRET_KEY or FD_SECRET_KEY_PREVIOUS: %w", where, err)
		}
		return box.Seal(purpose, pt)
	}
	resealJSON := func(raw []byte, fields map[string]string, where string) ([]byte, int, error) {
		var doc map[string]any
		if len(raw) == 0 {
			return raw, 0, nil
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, 0, fmt.Errorf("rotate-secrets: %s: %w", where, err)
		}
		n := 0
		for f, purpose := range fields {
			s, _ := doc[f].(string)
			if s == "" {
				continue
			}
			out, err := reseal(purpose, s, where+"."+f)
			if err != nil {
				return nil, 0, err
			}
			doc[f] = out
			n++
		}
		if n == 0 {
			return raw, 0, nil
		}
		b, err := json.Marshal(doc)
		return b, n, err
	}
	err := db.Tx(ctx, func(tx *store.Tx) error {
		rep = RotateReport{}
		// settings.smtp
		var raw []byte
		switch err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, KeySMTP).Scan(&raw); {
		case err == nil:
			out, n, err := resealJSON(raw, smtpFields, "settings.smtp")
			if err != nil {
				return err
			}
			if n > 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`, string(out), KeySMTP); err != nil {
					return err
				}
				rep.Settings += n
			}
		case store.NotFound(err) != store.ErrNotFound:
			return err
		}

		// webhooks.secret_enc
		type row struct{ id, a, b string }
		var hooks []row
		rows, err := tx.QueryContext(ctx, `SELECT id, secret_enc FROM webhooks WHERE secret_enc <> ''`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.a); err != nil {
				rows.Close()
				return err
			}
			hooks = append(hooks, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range hooks {
			out, err := reseal(PurposeWebhook, h.a, "webhooks "+h.id)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET secret_enc = ? WHERE id = ?`, out, h.id); err != nil {
				return err
			}
			rep.Webhooks++
		}

		// inbound_mailboxes: hmac_secret_enc and the imap document.
		var boxes []row
		rows, err = tx.QueryContext(ctx, `SELECT id, hmac_secret_enc, imap FROM inbound_mailboxes`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r row
			var imap []byte
			if err := rows.Scan(&r.id, &r.a, &imap); err != nil {
				rows.Close()
				return err
			}
			r.b = string(imap)
			boxes = append(boxes, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, mb := range boxes {
			n := 0
			hmac := mb.a
			if hmac != "" {
				if hmac, err = reseal(PurposeWebhook, hmac, "inbound_mailboxes "+mb.id+" hmac_secret_enc"); err != nil {
					return err
				}
				n++
			}
			imap, m, err := resealJSON([]byte(mb.b), imapFields, "inbound_mailboxes "+mb.id+" imap")
			if err != nil {
				return err
			}
			n += m
			if n == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE inbound_mailboxes SET hmac_secret_enc = ?, imap = ? WHERE id = ?`, hmac, string(imap), mb.id); err != nil {
				return err
			}
			rep.Mailboxes += n
		}
		// user_mfa: confirmed and pending TOTP secrets (D-19 §B).
		var mfa []row
		rows, err = tx.QueryContext(ctx, `SELECT user_id, totp_secret_enc, totp_pending_enc FROM user_mfa WHERE totp_secret_enc <> '' OR totp_pending_enc <> ''`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.a, &r.b); err != nil {
				rows.Close()
				return err
			}
			mfa = append(mfa, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, u := range mfa {
			sec, pend := u.a, u.b
			for _, p := range []*string{&sec, &pend} {
				if *p == "" {
					continue
				}
				if *p, err = reseal(PurposeMFA, *p, "user_mfa "+u.id); err != nil {
					return err
				}
				rep.MFA++
			}
			if _, err := tx.ExecContext(ctx, `UPDATE user_mfa SET totp_secret_enc = ?, totp_pending_enc = ? WHERE user_id = ?`, sec, pend, u.id); err != nil {
				return err
			}
		}
		rep.Total = rep.Settings + rep.Webhooks + rep.Mailboxes + rep.MFA
		return nil
	})
	return rep, err
}
