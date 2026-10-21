// SPDX-License-Identifier: AGPL-3.0-only

package settings

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// PLT-027 named tests.

func TestOverlayEnvWins(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	s := newSvc(t, db, map[string]string{"ST_SMTP_HOST": "env.host"})
	_ = s.PutSMTP(context.Background(), SMTP{Host: "ui.host", Port: 25}, nil, "")
	v, _ := s.SMTP(context.Background())
	if v.Host != "env.host" {
		t.Fatal(v.Host)
	}
}

func TestOverlayReportsLockedFields(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	s := newSvc(t, db, map[string]string{"ST_SMTP_PORT": "465", "ST_SMTP_PASSWORD": "x"})
	v, _ := s.SMTP(context.Background())
	if strings.Join(v.LockedByEnv, ",") != "port,password" {
		t.Fatal(v.LockedByEnv)
	}
}

// A write to a locked field never replaces the stored value (env keeps winning,
// and removing the env var later restores what the admin last really saved).
func TestOverlayRejectsWriteToLockedField(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	ctx := context.Background()
	_ = newSvc(t, db, nil).PutSMTP(ctx, SMTP{Host: "stored.host", Port: 25}, nil, "")
	locked := newSvc(t, db, map[string]string{"ST_SMTP_HOST": "env.host"})
	if err := locked.PutSMTP(ctx, SMTP{Host: "attempted.host", Port: 26}, nil, ""); err != nil {
		t.Fatal(err)
	}
	v, _ := newSvc(t, db, nil).SMTP(ctx)
	if v.Host != "stored.host" || v.Port != 26 {
		t.Fatalf("locked field overwritten: %+v", v.SMTP)
	}
}

func TestOverlayNeverReturnsSecret(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	ctx := context.Background()
	s := newSvc(t, db, map[string]string{"ST_SMTP_PASSWORD": "envpw"})
	pw := "storedpw"
	_ = s.PutSMTP(ctx, SMTP{Host: "h"}, &pw, "")
	v, _ := s.SMTP(ctx)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "pw") && (strings.Contains(string(b), "envpw") || strings.Contains(string(b), "storedpw") || strings.Contains(string(b), "enc:v1")) {
		t.Fatalf("secret in JSON: %s", b)
	}
}
