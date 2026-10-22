// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

const mta = "mx.help.test"

func senderFixture(t *testing.T, db *store.DB, kind domain.UserKind, email string) string {
	t.Helper()
	var id string
	if kind == domain.KindStaff {
		id = storetest.CreateStaff(t, db, domain.RoleAgent, 1)
	} else {
		id = storetest.CreateCustomer(t, db, "")
	}
	err := db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE users SET email = ? WHERE id = ?`, email, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func resolve(t *testing.T, db *store.DB, from, ar string) (string, domain.UserKind, string) {
	t.Helper()
	id, k, v, err := authn.ResolveEmailSender(context.Background(), db, from, ar, mta)
	if err != nil {
		t.Fatal(err)
	}
	return id, k, v
}

func TestEmailSender_StaffDKIMPassAccepted(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		id := senderFixture(t, db, domain.KindStaff, "agent@corp.test")
		ar := "Authentication-Results: mx.help.test; spf=fail smtp.mailfrom=corp.test; dkim=pass (2048-bit key) header.d=corp.test header.s=sel; dmarc=fail"
		if got, k, v := resolve(t, db, `"Agent" <Agent@Corp.test>`, ar); got != id || k != domain.KindStaff || v != authn.SenderOK {
			t.Fatalf("%s %s %s", got, k, v)
		}
		// Subdomain signer aligns (relaxed).
		if _, _, v := resolve(t, db, "agent@corp.test", "mx.help.test 1; dkim=pass header.d=mail.corp.test"); v != authn.SenderOK {
			t.Fatalf("relaxed alignment: %s", v)
		}
	})
}

func TestEmailSender_StaffNoAuthResultsQuarantined(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		senderFixture(t, db, domain.KindStaff, "agent@corp.test")
		for _, ar := range []string{"", "mx.help.test; dkim=fail header.d=corp.test", "mx.help.test; dkim=pass header.d=evil.test", "mx.help.test; none"} {
			if _, _, v := resolve(t, db, "agent@corp.test", ar); v != authn.SenderSpoof {
				t.Fatalf("%q → %s", ar, v)
			}
		}
		if _, _, v, _ := authn.ResolveEmailSender(context.Background(), db, "agent@corp.test", "mx.help.test; dkim=pass header.d=corp.test", ""); v != authn.SenderSpoof {
			t.Fatal("no trusted authserv-id configured must never trust staff mail")
		}
	})
}

func TestEmailSender_ForeignAuthservIdIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		senderFixture(t, db, domain.KindStaff, "agent@corp.test")
		ar := "attacker.example; dkim=pass header.d=corp.test; dmarc=pass header.from=corp.test\nmx.help.test; dkim=fail header.d=corp.test"
		if _, _, v := resolve(t, db, "agent@corp.test", ar); v != authn.SenderSpoof {
			t.Fatalf("foreign authserv-id trusted: %s", v)
		}
	})
}

func TestEmailSender_SPFPassWithoutDMARCAlignmentQuarantined(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		senderFixture(t, db, domain.KindStaff, "agent@corp.test")
		if _, _, v := resolve(t, db, "agent@corp.test", "mx.help.test; spf=pass smtp.mailfrom=bounce.evil.test"); v != authn.SenderSpoof {
			t.Fatalf("spf alone: %s", v)
		}
		if _, _, v := resolve(t, db, "agent@corp.test", "mx.help.test; spf=pass smtp.mailfrom=corp.test; dmarc=pass header.from=evil.test"); v != authn.SenderSpoof {
			t.Fatalf("unaligned dmarc: %s", v)
		}
		if _, _, v := resolve(t, db, "agent@corp.test", "mx.help.test; spf=pass smtp.mailfrom=corp.test; dmarc=pass header.from=corp.test"); v != authn.SenderOK {
			t.Fatalf("spf+aligned dmarc: %s", v)
		}
	})
}

func TestEmailSender_DisabledStaffQuarantined(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		id := senderFixture(t, db, domain.KindStaff, "agent@corp.test")
		_ = db.Tx(context.Background(), func(tx *store.Tx) error {
			return authn.DisableUser(context.Background(), tx, id, "", "test", "")
		})
		if _, _, v := resolve(t, db, "agent@corp.test", "mx.help.test; dkim=pass header.d=corp.test"); v != authn.SenderDisabled {
			t.Fatalf("disabled staff: %s", v)
		}
	})
}

func TestEmailSender_CustomerResolvedNoTrustNeeded(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		id := senderFixture(t, db, domain.KindCustomer, "client@cust.test")
		if got, k, v := resolve(t, db, "Client <client@cust.test>", ""); got != id || k != domain.KindCustomer || v != authn.SenderOK {
			t.Fatalf("%s %s %s", got, k, v)
		}
		if got, _, v := resolve(t, db, "stranger@cust.test", ""); got != "" || v != authn.SenderUnknown {
			t.Fatalf("unknown: %s %s", got, v)
		}
		if _, _, v := resolve(t, db, "not an address", ""); v != authn.SenderUnknown {
			t.Fatalf("garbage: %s", v)
		}
	})
}
