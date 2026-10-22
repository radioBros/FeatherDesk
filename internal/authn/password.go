// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// ArgonParams are the argon2id cost parameters (AUTH-003). The defaults are
// the OWASP minimum (m=19 MiB, t=2, p=1); with the 2-slot semaphore the peak
// is 38 MiB (CONVENTIONS §10). Changing them is forward-compatible: old hashes
// verify and are rehashed on the next successful login.
type ArgonParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	KeyLen    uint32
	SaltLen   int
}

// DefaultArgon is the production parameter set.
var DefaultArgon = ArgonParams{MemoryKiB: 19456, Time: 2, Threads: 1, KeyLen: 32, SaltLen: 16}

// argon is the active parameter set (tests may lower it through export_test).
var argon = DefaultArgon

// hashSem bounds concurrent hashes so a login burst stays inside the RSS budget.
var hashSem = make(chan struct{}, 2)

// Password length rules (AUTH-003).
const (
	PasswordMin = 10
	PasswordMax = 128
)

var (
	errBadHash = errors.New("authn: malformed password hash")
)

// HashPassword returns an argon2id PHC string.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argon.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquire(ctx); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argon.Time, argon.MemoryKiB, argon.Threads, argon.KeyLen)
	release()
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argon.MemoryKiB, argon.Time, argon.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func acquire(ctx context.Context) error {
	select {
	case hashSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release() { <-hashSem }

type phc struct {
	m, t uint32
	p    uint8
	salt []byte
	key  []byte
}

func parsePHC(s string) (phc, error) {
	parts := strings.Split(s, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return phc{}, errBadHash
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return phc{}, errBadHash
	}
	var h phc
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.m, &h.t, &h.p); err != nil {
		return phc{}, errBadHash
	}
	var err error
	if h.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return phc{}, errBadHash
	}
	if h.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(h.key) == 0 {
		return phc{}, errBadHash
	}
	if h.m == 0 || h.t == 0 || h.p == 0 || h.m > 1<<20 || h.t > 10 {
		return phc{}, errBadHash
	}
	return h, nil
}

// VerifyPassword compares password with a PHC hash in constant time.
func VerifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	h, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	if err := acquire(ctx); err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), h.salt, h.t, h.m, h.p, uint32(len(h.key)))
	release()
	return subtle.ConstantTimeCompare(key, h.key) == 1, nil
}

// NeedsRehash reports whether encoded was made with other parameters.
func NeedsRehash(encoded string) bool {
	h, err := parsePHC(encoded)
	if err != nil {
		return true
	}
	return h.m != argon.MemoryKiB || h.t != argon.Time || h.p != argon.Threads || uint32(len(h.key)) != argon.KeyLen
}

var (
	dummyMu   sync.Mutex
	dummyHash string
)

// dummy is a valid hash of a random password made with the current
// parameters: unknown users are verified against it so the response time
// does not reveal whether the account exists.
func dummy() string {
	dummyMu.Lock()
	defer dummyMu.Unlock()
	if dummyHash == "" || NeedsRehash(dummyHash) {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		dummyHash, _ = HashPassword(context.Background(), base64.RawStdEncoding.EncodeToString(b))
	}
	return dummyHash
}

// PasswordProblem returns a validation code ("" when acceptable): length
// 10..128 characters and not in the embedded common-password list.
func PasswordProblem(pw string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < PasswordMin:
		return "password_too_short"
	case n > PasswordMax:
		return "password_too_long"
	case isCommonPassword(pw):
		return "password_too_common"
	}
	return ""
}
