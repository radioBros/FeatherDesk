// SPDX-License-Identifier: AGPL-3.0-only

package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// TestRotationReencryptsAll (PLT-019): after ST_SECRET_KEY changes (old key
// kept as ST_SECRET_KEY_PREVIOUS), rotate-secrets re-encrypts every stored
// secret (SMTP password, webhook secret, mailbox HMAC secret, IMAP password
// and refresh token) so that the new key alone opens them and the old one no
// longer does. Plaintext never reaches the database.
func TestRotationReencryptsAll(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		oldKey, newKey := bytes.Repeat([]byte("o"), 32), bytes.Repeat([]byte("n"), 32)
		oldBox, _ := crypto.NewBox(oldKey)
		plain := map[string]string{
			"smtp": "smtp-pw-123", "hook": "whsec-456", "hmac": "hmac-789", "imap": "imap-pw-000", "refresh": "refresh-tok-111",
		}
		// Written by a server that ran with the old key.
		old := New(db, oldBox, func(string) string { return "" })
		cur, err := old.SMTP(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pw := plain["smtp"]
		if err := old.PutSMTP(ctx, cur.SMTP, &pw, ""); err != nil {
			t.Fatal(err)
		}
		seal := func(purpose, s string) string {
			v, err := oldBox.SealString(purpose, s)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		imapDoc, _ := json.Marshal(map[string]any{"host": "imap.example.test", "port": 993, "username": "support",
			"password_enc": seal(PurposeIMAPPassword, plain["imap"]), "oauth_refresh_token_enc": seal(PurposeIMAPRefresh, plain["refresh"])})
		err = db.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO webhooks (id, url, secret_enc) VALUES (?, ?, ?)`,
				store.NewID(), "https://hooks.example.test/x", seal(PurposeWebhook, plain["hook"])); err != nil {
				return err
			}
			// The second mailbox has no secrets at all: it must not trip the rotation.
			if _, err := tx.ExecContext(ctx, `INSERT INTO inbound_mailboxes (id, name, kind, imap, hmac_secret_enc) VALUES (?, ?, 'imap', ?, ?)`,
				store.NewID(), "support", string(imapDoc), seal(PurposeWebhook, plain["hmac"])); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO inbound_mailboxes (id, name, kind) VALUES (?, ?, 'webhook')`, store.NewID(), "empty")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}

		// New key alone cannot read the old ciphertext; rotation with the old
		// key as previous re-seals everything.
		newOnly, _ := crypto.NewBox(newKey)
		if _, err := New(db, newOnly, func(string) string { return "" }).SMTPPassword(ctx); err == nil {
			t.Fatal("new key opened old ciphertext before rotation")
		}
		if _, err := RotateSecrets(ctx, db, newOnly); err == nil {
			t.Fatal("rotation without the previous key must fail (and change nothing)")
		}
		rotating, _ := crypto.NewBox(newKey, oldKey)
		rep, err := RotateSecrets(ctx, db, rotating)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Settings != 1 || rep.Webhooks != 1 || rep.Mailboxes != 3 || rep.Total != 5 {
			t.Fatalf("report %+v, want settings 1, webhooks 1, mailboxes 3", rep)
		}

		// Read everything back with the new key only.
		got := map[string]string{}
		open := func(name, purpose, sealed string) {
			if !crypto.IsSealed(sealed) {
				t.Fatalf("%s stored without the enc:v1: prefix: %q", name, sealed)
			}
			if _, err := oldBox.OpenString(purpose, sealed); err == nil {
				t.Errorf("%s still opens with the old key", name)
			}
			v, err := newOnly.OpenString(purpose, sealed)
			if err != nil {
				t.Fatalf("%s does not open with the new key: %v", name, err)
			}
			got[name] = v
		}
		smtp, err := New(db, newOnly, func(string) string { return "" }).SMTPPassword(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got["smtp"] = smtp
		var raw []byte
		if err := db.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var smtpDoc map[string]any
		if err := json.Unmarshal(raw, &smtpDoc); err != nil || smtpDoc["host"] == nil {
			t.Fatalf("smtp document damaged by rotation: %s (%v)", raw, err)
		}
		open("smtp-at-rest", PurposeSMTPPassword, smtpDoc["password_enc"].(string))
		var hook string
		if err := db.Read().QueryRowContext(ctx, `SELECT secret_enc FROM webhooks`).Scan(&hook); err != nil {
			t.Fatal(err)
		}
		open("hook", PurposeWebhook, hook)
		var hmac string
		var imapRaw []byte
		if err := db.Read().QueryRowContext(ctx, `SELECT hmac_secret_enc, imap FROM inbound_mailboxes WHERE kind = 'imap'`).Scan(&hmac, &imapRaw); err != nil {
			t.Fatal(err)
		}
		open("hmac", PurposeWebhook, hmac)
		var imap map[string]any
		if err := json.Unmarshal(imapRaw, &imap); err != nil {
			t.Fatal(err)
		}
		if imap["host"] != "imap.example.test" || imap["port"] != float64(993) {
			t.Fatalf("imap document damaged by rotation: %s", imapRaw)
		}
		open("imap", PurposeIMAPPassword, imap["password_enc"].(string))
		open("refresh", PurposeIMAPRefresh, imap["oauth_refresh_token_enc"].(string))
		delete(got, "smtp-at-rest")
		for k, want := range plain {
			if got[k] != want {
				t.Errorf("%s = %q, want %q", k, got[k], want)
			}
		}
		// No plaintext anywhere in the rotated rows.
		dump := string(raw) + hook + hmac + string(imapRaw)
		for _, p := range plain {
			if strings.Contains(dump, p) {
				t.Fatalf("plaintext %q stored at rest", p)
			}
		}
	})
}
