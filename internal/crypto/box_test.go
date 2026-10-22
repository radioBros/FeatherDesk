// SPDX-License-Identifier: AGPL-3.0-only

package crypto

import (
	"bytes"
	"strings"
	"testing"
)

var root = bytes.Repeat([]byte("k"), 32)

func TestSealOpenRoundTrip(t *testing.T) {
	b, _ := NewBox(root)
	s, err := b.SealString("smtp-password", "hunter2")
	if err != nil || !IsSealed(s) || strings.Contains(s, "hunter2") {
		t.Fatalf("seal: %q %v", s, err)
	}
	got, err := b.OpenString("smtp-password", s)
	if err != nil || got != "hunter2" {
		t.Fatalf("open: %q %v", got, err)
	}
}

func TestPurposeBound(t *testing.T) {
	b, _ := NewBox(root)
	s, _ := b.SealString("smtp-password", "x")
	if _, err := b.OpenString("webhook-secret", s); err == nil {
		t.Fatal("a value sealed for one purpose must not open for another")
	}
}

func TestNonceUnique(t *testing.T) {
	b, _ := NewBox(root)
	a, _ := b.SealString("p", "same")
	c, _ := b.SealString("p", "same")
	if a == c {
		t.Fatal("ciphertexts must differ (random nonce)")
	}
}

func TestRotation(t *testing.T) {
	old, _ := NewBox(root)
	s, _ := old.SealString("p", "v")
	newRoot := bytes.Repeat([]byte("n"), 32)
	rotated, _ := NewBox(newRoot, root)
	if got, err := rotated.OpenString("p", s); err != nil || got != "v" {
		t.Fatalf("previous key must still decrypt: %v", err)
	}
	fresh, _ := NewBox(newRoot)
	if _, err := fresh.OpenString("p", s); err == nil {
		t.Fatal("without the previous key decryption must fail")
	}
}

func TestTamperDetected(t *testing.T) {
	b, _ := NewBox(root)
	s, _ := b.SealString("p", "value")
	mut := []byte(s)
	mut[len(mut)-2] ^= 1
	if _, err := b.OpenString("p", string(mut)); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}

func TestMAC(t *testing.T) {
	b, _ := NewBox(root)
	m := b.MAC("csrf", []byte("sess"))
	if !b.VerifyMAC("csrf", []byte("sess"), m) || b.VerifyMAC("csrf", []byte("other"), m) || b.VerifyMAC("ws", []byte("sess"), m) {
		t.Fatal("mac verify")
	}
}
