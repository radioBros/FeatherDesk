// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 TOTP is defined over HMAC-SHA1; authenticator apps expect it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTP parameters (D-19 §B): RFC 6238, HMAC-SHA1, 30 s steps, 6 digits,
// ±1 step accepted. Authenticator apps assume exactly these defaults.
const (
	totpStep    = 30
	totpDigits  = 6
	totpWindow  = 1
	totpKeySize = 20 // 160-bit secret (RFC 4226 §4 recommends ≥ 128)
)

// totpB32 is the unpadded RFC 4648 alphabet authenticator apps read.
var totpB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a random secret in base32 (32 characters).
func newTOTPSecret() string {
	k := make([]byte, totpKeySize)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return totpB32.EncodeToString(k)
}

// decodeTOTPSecret accepts the base32 form with or without spaces/padding.
func decodeTOTPSecret(s string) ([]byte, bool) {
	s = strings.ToUpper(strings.TrimRight(strings.ReplaceAll(s, " ", ""), "="))
	k, err := totpB32.DecodeString(s)
	return k, err == nil && len(k) > 0
}

// hotp is RFC 4226 §5.3 (dynamic truncation) for a key and counter.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	code := strconv.FormatUint(uint64(bin%mod), 10)
	return strings.Repeat("0", digits-len(code)) + code
}

// TOTPCode returns the 6-digit code of a base32 secret at t, as an
// authenticator app would show it. `seed --demo` uses it to enrol a demo
// user through the real /me/mfa API.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, ok := decodeTOTPSecret(secret)
	if !ok {
		return "", errors.New("authn: invalid TOTP secret")
	}
	return hotp(key, totpCounter(t), totpDigits), nil
}

// totpCounter is the RFC 6238 time step T = floor(unix / 30).
func totpCounter(t time.Time) uint64 { return uint64(t.Unix()) / totpStep }

// totpMatch returns the time step whose code equals code within ±window of
// now, or false. Every candidate is compared in constant time.
func totpMatch(key []byte, code string, now time.Time) (uint64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	c := totpCounter(now)
	var (
		found uint64
		ok    bool
	)
	for d := -totpWindow; d <= totpWindow; d++ {
		ctr := uint64(int64(c) + int64(d))
		if subtle.ConstantTimeCompare([]byte(hotp(key, ctr, totpDigits)), []byte(code)) == 1 && !ok {
			found, ok = ctr, true
		}
	}
	return found, ok
}

// otpauthURI is the Key URI Format understood by authenticator apps:
// otpauth://totp/<issuer>:<account>?secret=…&issuer=…&algorithm=SHA1&digits=6&period=30
func otpauthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{
		"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"},
		"digits": {strconv.Itoa(totpDigits)}, "period": {strconv.Itoa(totpStep)},
	}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// normCode strips spaces and dashes that users type into codes.
func normCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}

// isDigits reports whether s is n ASCII digits.
func isDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
