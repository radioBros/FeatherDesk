// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestTOTP_RFC6238Vectors: Appendix B of RFC 6238 (SHA1 rows, 8 digits,
// ASCII key "12345678901234567890").
func TestTOTP_RFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		if got := hotp(key, totpCounter(time.Unix(v.unix, 0)), 8); got != v.want {
			t.Errorf("T=%d: got %s want %s", v.unix, got, v.want)
		}
	}
	// RFC 4226 Appendix D (HOTP, 6 digits) for the counter path.
	for i, want := range []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"} {
		if got := hotp(key, uint64(i), 6); got != want {
			t.Errorf("HOTP count %d: got %s want %s", i, got, want)
		}
	}
}

// TestTOTP_WindowPlusMinusOne: codes of the previous, current and next step
// match (returning their own step); two steps away does not.
func TestTOTP_WindowPlusMinusOne(t *testing.T) {
	key, ok := decodeTOTPSecret(newTOTPSecret())
	if !ok || len(key) != totpKeySize {
		t.Fatal("secret")
	}
	now := time.Unix(1_800_000_015, 0)
	c := totpCounter(now)
	for d := int64(-2); d <= 2; d++ {
		code := hotp(key, uint64(int64(c)+d), totpDigits)
		got, ok := totpMatch(key, code, now)
		want := d >= -1 && d <= 1
		if ok != want {
			t.Errorf("offset %d: match=%v want %v", d, ok, want)
		}
		if ok && got != uint64(int64(c)+d) {
			t.Errorf("offset %d: step %d", d, got)
		}
	}
	if _, ok := totpMatch(key, "12345", now); ok {
		t.Error("5 digits accepted")
	}
}

func TestTOTP_SecretAndURI(t *testing.T) {
	s := newTOTPSecret()
	if len(s) != 32 || strings.Contains(s, "=") {
		t.Fatalf("secret %q", s)
	}
	if _, ok := decodeTOTPSecret(strings.ToLower(s[:4]) + " " + s[4:]); !ok {
		t.Fatal("lower case / spaced secret not accepted")
	}
	u, err := url.Parse(otpauthURI("Acme Help", "ada@example.test", s))
	if err != nil || u.Scheme != "otpauth" || u.Host != "totp" {
		t.Fatalf("uri %v %v", u, err)
	}
	q := u.Query()
	if q.Get("secret") != s || q.Get("issuer") != "Acme Help" || q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Fatalf("query %v", q)
	}
	if u.Path != "/Acme Help:ada@example.test" {
		t.Fatalf("label %q", u.Path)
	}
}

func TestRecoveryCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c := newRecoveryCode()
		if len(c) != 14 || c[4] != '-' || c[9] != '-' || seen[c] {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
		if recoveryHash("u1", c) != recoveryHash("u1", strings.ToUpper(strings.ReplaceAll(c, "-", " "))) {
			t.Fatal("normalisation")
		}
		if recoveryHash("u1", c) == recoveryHash("u2", c) {
			t.Fatal("hash not bound to the user")
		}
	}
	for range 200 {
		if c := newEmailCode(); !isDigits(c, 6) {
			t.Fatalf("email code %q", c)
		}
	}
}

func TestMFAPolicyCoverage(t *testing.T) {
	staff := userRow{Kind: "staff"}
	cust := userRow{Kind: "customer"}
	sso := userRow{Kind: "staff", IDPManaged: true}
	for _, c := range []struct {
		policy string
		u      userRow
		want   bool
	}{
		{MFARequiredNone, staff, false}, {MFARequiredStaff, staff, true}, {MFARequiredStaff, cust, false},
		{MFARequiredAll, cust, true}, {MFARequiredAll, sso, false}, {MFARequiredStaff, sso, false},
	} {
		if got := mfaRequiredFor(c.policy, c.u); got != c.want {
			t.Errorf("%s/%s/sso=%v: %v", c.policy, c.u.Kind, c.u.IDPManaged, got)
		}
	}
}
