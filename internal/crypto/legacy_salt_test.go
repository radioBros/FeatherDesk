// SPDX-License-Identifier: AGPL-3.0-only

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

// sealSimpleTickets seals the way SimpleTickets 1.0.0 did (salt "simpletickets").
func sealSimpleTickets(t *testing.T, root []byte, purpose, plaintext string) string {
	t.Helper()
	k, err := hkdf.Key(sha256.New, root, []byte("simpletickets"), "secretbox:"+purpose, 32)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	_, _ = rand.Read(nonce)
	return Prefix + base64.RawStdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(plaintext), []byte(purpose)))
}

func macSimpleTickets(root []byte, purpose string, msg []byte) []byte {
	k, _ := hkdf.Key(sha256.New, root, []byte("simpletickets"), "mac:"+purpose, 32)
	m := hmac.New(sha256.New, k)
	m.Write(msg)
	return m.Sum(nil)
}

func TestOpensSimpleTicketsSealedValue(t *testing.T) {
	b, _ := NewBox(root)
	got, err := b.OpenString("smtp-password", sealSimpleTickets(t, root, "smtp-password", "hunter2"))
	if err != nil || got != "hunter2" {
		t.Fatalf("open legacy: %q %v", got, err)
	}
	// Still bound to its purpose.
	if _, err := b.OpenString("imap-password", sealSimpleTickets(t, root, "smtp-password", "x")); err == nil {
		t.Fatal("legacy value opened under another purpose")
	}
}

func TestOpensSimpleTicketsValueUnderPreviousKey(t *testing.T) {
	old := make([]byte, 32)
	copy(old, "previous-root-secret-0123456789ab")
	b, _ := NewBox(root, old)
	if got, err := b.OpenString("webhook", sealSimpleTickets(t, old, "webhook", "s3cret")); err != nil || got != "s3cret" {
		t.Fatalf("open legacy under previous key: %q %v", got, err)
	}
}

func TestNewValuesUseFeatherDeskSalt(t *testing.T) {
	b, _ := NewBox(root)
	s, _ := b.SealString("smtp-password", "hunter2")
	k, _ := hkdf.Key(sha256.New, root, []byte("featherdesk"), "secretbox:smtp-password", 32)
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	raw, _ := base64.RawStdEncoding.DecodeString(s[len(Prefix):])
	if _, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("smtp-password")); err != nil {
		t.Fatal("new value not sealed with the featherdesk salt")
	}
	if want := macSimpleTickets(root, "p", []byte("m")); hmac.Equal(b.MAC("p", []byte("m")), want) {
		t.Fatal("MAC still uses the simpletickets salt")
	}
}

func TestVerifiesSimpleTicketsMAC(t *testing.T) {
	b, _ := NewBox(root)
	msg := []byte("ticket")
	if !b.VerifyMAC("plus-token", msg, macSimpleTickets(root, "plus-token", msg)) {
		t.Fatal("legacy MAC rejected")
	}
	if b.VerifyMAC("csat", msg, macSimpleTickets(root, "plus-token", msg)) {
		t.Fatal("legacy MAC accepted under another purpose")
	}
	if got := b.MACs("plus-token", msg); !hmac.Equal(got[0], b.MAC("plus-token", msg)) {
		t.Fatal("MACs must list the current MAC first")
	}
}
