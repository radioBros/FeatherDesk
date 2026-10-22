// SPDX-License-Identifier: AGPL-3.0-only

// Package crypto seals secrets at rest (SMTP/IMAP passwords, webhook secrets,
// OAuth refresh tokens) with AES-256-GCM (PLT-019). Each purpose gets its own
// key derived from FD_SECRET_KEY via HKDF-SHA256, so one leaked derived key
// never decrypts another purpose.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
)

// Prefix marks a sealed value so callers can tell ciphertext from plaintext.
const Prefix = "enc:v1:"

// salts are the HKDF salts, current first. SimpleTickets 1.0.0 derived every
// key with "simpletickets": values it sealed (SMTP/IMAP passwords, webhook
// secrets, TOTP secrets) and MACs it issued (reply plus-tokens, CSAT links)
// still open and verify after the upgrade. New values always use salts[0].
var salts = [][]byte{[]byte("featherdesk"), []byte("simpletickets")}

var ErrDecrypt = errors.New("crypto: cannot decrypt (wrong FD_SECRET_KEY or tampered value)")

// Box seals and opens values. It is safe for concurrent use.
type Box struct {
	keys [][]byte // current first, then previous (rotation)
	mu   sync.Mutex
	aead map[string][]cipher.AEAD
}

// NewBox takes the root secret and optional previous secrets (FD_SECRET_KEY_PREVIOUS).
func NewBox(root []byte, previous ...[]byte) (*Box, error) {
	if len(root) < 32 {
		return nil, errors.New("crypto: root secret must be ≥ 32 bytes")
	}
	b := &Box{keys: [][]byte{root}, aead: map[string][]cipher.AEAD{}}
	for _, p := range previous {
		if len(p) >= 32 {
			b.keys = append(b.keys, p)
		}
	}
	return b, nil
}

func (b *Box) ciphers(purpose string) ([]cipher.AEAD, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if a, ok := b.aead[purpose]; ok {
		return a, nil
	}
	var out []cipher.AEAD
	for _, salt := range salts {
		for _, root := range b.keys {
			k, err := hkdf.Key(sha256.New, root, salt, "secretbox:"+purpose, 32)
			if err != nil {
				return nil, err
			}
			blk, err := aes.NewCipher(k)
			if err != nil {
				return nil, err
			}
			g, err := cipher.NewGCM(blk)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
	}
	b.aead[purpose] = out
	return out, nil
}

// Seal encrypts plaintext for purpose and returns "enc:v1:<base64(nonce|ct)>".
// The purpose is also authenticated, so a value can't be moved to another field.
func (b *Box) Seal(purpose string, plaintext []byte) (string, error) {
	a, err := b.ciphers(purpose)
	if err != nil {
		return "", err
	}
	g := a[0]
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := g.Seal(nonce, nonce, plaintext, []byte(purpose))
	return Prefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// SealString is Seal for strings; the empty string stays empty (unset secret).
func (b *Box) SealString(purpose, s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return b.Seal(purpose, []byte(s))
}

// Open decrypts a value produced by Seal, trying the previous keys after a
// rotation and the SimpleTickets salt after an upgrade.
func (b *Box) Open(purpose, sealed string) ([]byte, error) {
	if !strings.HasPrefix(sealed, Prefix) {
		return nil, ErrDecrypt
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, Prefix))
	if err != nil {
		return nil, ErrDecrypt
	}
	a, err := b.ciphers(purpose)
	if err != nil {
		return nil, err
	}
	for _, g := range a {
		if len(raw) < g.NonceSize() {
			return nil, ErrDecrypt
		}
		if pt, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(purpose)); err == nil {
			return pt, nil
		}
	}
	return nil, ErrDecrypt
}

// OpenString is Open for strings; the empty string opens to "".
func (b *Box) OpenString(purpose, sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	pt, err := b.Open(purpose, sealed)
	return string(pt), err
}

// IsSealed reports whether s carries the sealed-value prefix.
func IsSealed(s string) bool { return strings.HasPrefix(s, Prefix) }

// MAC returns a hex-free raw HMAC-SHA256 of msg under a purpose key (CSRF tokens,
// plus-address reply tokens, ws-tickets). Verify with hmac.Equal.
func (b *Box) MAC(purpose string, msg []byte) []byte {
	return macWith(b.keys[0], salts[0], purpose, msg)
}

// MACs returns every MAC msg may legitimately carry: the current one first, then
// those under previous keys and the SimpleTickets salt. For callers that compare
// a truncated MAC themselves; others use VerifyMAC.
func (b *Box) MACs(purpose string, msg []byte) [][]byte {
	out := make([][]byte, 0, len(salts)*len(b.keys))
	for _, salt := range salts {
		for _, root := range b.keys {
			out = append(out, macWith(root, salt, purpose, msg))
		}
	}
	return out
}

// VerifyMAC checks mac against msg using the current and previous keys.
func (b *Box) VerifyMAC(purpose string, msg, mac []byte) bool {
	for _, want := range b.MACs(purpose, msg) {
		if hmac.Equal(want, mac) {
			return true
		}
	}
	return false
}

func macWith(root, salt []byte, purpose string, msg []byte) []byte {
	k, _ := hkdf.Key(sha256.New, root, salt, "mac:"+purpose, 32)
	m := hmac.New(sha256.New, k)
	m.Write(msg)
	return m.Sum(nil)
}
