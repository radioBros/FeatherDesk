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

func newSvc(t *testing.T, db *store.DB, env map[string]string) *Service {
	box, _ := crypto.NewBox(bytes.Repeat([]byte("s"), 32))
	return New(db, box, func(k string) string { return env[k] })
}

func TestDefaultsFromMigration(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		s := newSvc(t, db, nil)
		sup, err := s.Support(context.Background())
		if err != nil || sup.AutoCloseDays != 7 || sup.TierLabels["2"] != "L2" || len(sup.Locales) != 5 || !sup.PortalEnabled {
			t.Fatalf("support %+v %v", sup, err)
		}
		b, err := s.Branding(context.Background())
		if err != nil || b.ProductName != "SimpleTickets" || !b.PoweredBy {
			t.Fatalf("branding %+v %v", b, err)
		}
	})
}

func TestPutGetCacheAndHook(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := newSvc(t, db, nil)
		var fired []string
		s.OnChange(func(k string) { fired = append(fired, k) })
		b, _ := s.Branding(ctx)
		b.ProductName = "Acme Help"
		if err := s.Put(ctx, KeyBranding, b, ""); err != nil {
			t.Fatal(err)
		}
		// A second service on the same DB sees the change (not just the cache).
		got, _ := newSvc(t, db, nil).Branding(ctx)
		if got.ProductName != "Acme Help" || len(fired) != 1 || fired[0] != KeyBranding {
			t.Fatalf("got %q fired %v", got.ProductName, fired)
		}
	})
}

// TestSMTPEnvLockAndSecret: env overrides and locks fields; the stored
// password is sealed and never serialised.
func TestSMTPEnvLockAndSecret(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := newSvc(t, db, map[string]string{"ST_SMTP_HOST": "mail.env.example", "ST_SMTP_PORT": "2525"})
		pw := "s3cret!"
		if err := s.PutSMTP(ctx, SMTP{Host: "ui.example", Port: 587, Security: "tls", Username: "u"}, &pw, "admin"); err != nil {
			t.Fatal(err)
		}
		v, err := s.SMTP(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if v.Host != "mail.env.example" || v.Port != 2525 || v.Security != "tls" || !v.PasswordSet {
			t.Fatalf("view %+v", v)
		}
		if strings.Join(v.LockedByEnv, ",") != "host,port" {
			t.Fatalf("locked %v", v.LockedByEnv)
		}
		js, _ := json.Marshal(v)
		if bytes.Contains(js, []byte("password_enc")) || bytes.Contains(js, []byte(pw)) {
			t.Fatalf("secret leaked: %s", js)
		}
		var raw string
		_ = db.Read().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'smtp'`).Scan(&raw)
		if strings.Contains(raw, pw) || !strings.Contains(raw, crypto.Prefix) {
			t.Fatalf("password not sealed at rest: %s", raw)
		}
		got, err := s.SMTPPassword(ctx)
		if err != nil || got != pw {
			t.Fatalf("password %q %v", got, err)
		}
		// nil keeps the password; env password beats stored.
		_ = s.PutSMTP(ctx, SMTP{Host: "x", Port: 1}, nil, "")
		if got, _ := s.SMTPPassword(ctx); got != pw {
			t.Fatal("nil password must keep the stored one")
		}
		s2 := newSvc(t, db, map[string]string{"ST_SMTP_PASSWORD": "fromenv"})
		if got, _ := s2.SMTPPassword(ctx); got != "fromenv" {
			t.Fatal("env password must win")
		}
	})
}

func TestEmailIdentityEnv(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	s := newSvc(t, db, map[string]string{"ST_SMTP_FROM_CUSTOMER": `"Acme Support" <support@acme.test>`})
	v, err := s.Email(context.Background())
	if err != nil || v.CustomerFrom.Address != "support@acme.test" || v.CustomerFrom.Name != "Acme Support" || v.LockedByEnv[0] != "customer_from" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestOverlay(t *testing.T) {
	var in struct {
		Host string `json:"host" env:"HOST"`
		Port int    `json:"port" env:"PORT"`
		TLS  bool   `json:"tls" env:"TLS"`
		Name string `json:"name"`
	}
	locked := Overlay(&in, "ST_IMAP_", func(k string) string {
		return map[string]string{"ST_IMAP_HOST": "imap.x", "ST_IMAP_TLS": "true", "ST_IMAP_PORT": "bad"}[k]
	})
	if in.Host != "imap.x" || !in.TLS || in.Port != 0 || strings.Join(locked, ",") != "host,tls" {
		t.Fatalf("%+v %v", in, locked)
	}
}
