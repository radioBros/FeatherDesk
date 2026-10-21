// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/radioBros/SimpleTickets/api"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func fast(t *testing.T) {
	t.Helper()
	restore := SetArgonParams(ArgonParams{MemoryKiB: 512, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	t.Cleanup(restore)
}

func TestPassword_RoundTrip(t *testing.T) {
	fast(t)
	ctx := context.Background()
	h, err := HashPassword(ctx, "s3cret-passphrase")
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=512,t=1,p=1$") {
		t.Fatalf("hash %q %v", h, err)
	}
	if ok, _ := VerifyPassword(ctx, "s3cret-passphrase", h); !ok {
		t.Fatal("correct password rejected")
	}
	if ok, _ := VerifyPassword(ctx, "s3cret-passphrasE", h); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword(ctx, "s3cret-passphrase")
	if h == h2 {
		t.Fatal("salt not random")
	}
	if _, err := VerifyPassword(ctx, "x", "$argon2id$garbage"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestPassword_RehashOnParamChange(t *testing.T) {
	fast(t)
	h, _ := HashPassword(context.Background(), "s3cret-passphrase")
	if NeedsRehash(h) {
		t.Fatal("fresh hash needs rehash")
	}
	restore := SetArgonParams(DefaultArgon)
	defer restore()
	if !NeedsRehash(h) {
		t.Fatal("param change not detected")
	}
	if ok, _ := VerifyPassword(context.Background(), "s3cret-passphrase", h); !ok {
		t.Fatal("old-parameter hash no longer verifies")
	}
	if !strings.Contains(DefaultArgon.String(), "m=19456") {
		t.Fatal("default params drifted from the OWASP minimum")
	}
}

func (p ArgonParams) String() string {
	return "m=" + itoa(int(p.MemoryKiB)) + ",t=" + itoa(int(p.Time)) + ",p=" + itoa(int(p.Threads))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestPassword_RejectsCommon(t *testing.T) {
	for pw, want := range map[string]string{
		"short":                  "password_too_short",
		strings.Repeat("a", 129): "password_too_long",
		"Password123":            "password_too_common",
		"qwertyuiop":             "password_too_common",
		"1234567890":             "password_too_common",
		"aaaaaaaaaaaa":           "password_too_common",
		"abcdefghijklm":          "password_too_common",
		"correct-horse-9":        "",
	} {
		if got := PasswordProblem(pw); got != want {
			t.Errorf("%q: %q want %q", pw, got, want)
		}
	}
}

// TestPassword_HashBudget: one production-parameter hash stays near the
// AUTH-003 budget (≤ 150 ms on CI; ×4 slack for a loaded dev machine) and the
// semaphore bounds concurrency to 2.
func TestPassword_HashBudget(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	restore := SetArgonParams(DefaultArgon)
	defer restore()
	start := time.Now()
	if _, err := HashPassword(context.Background(), "budget-check-pw"); err != nil {
		t.Fatal(err)
	}
	d := time.Since(start)
	t.Logf("one argon2id hash (m=19456,t=2,p=1): %v", d)
	// Wall-clock budgets are not meaningful under the race detector or on
	// oversubscribed CI; the semaphore bound below is checked everywhere.
	if d > 600*time.Millisecond && !raceBuild {
		t.Fatalf("hash took %v", d)
	}
	if cap(hashSem) != 2 {
		t.Fatalf("semaphore %d", cap(hashSem))
	}
}

func BenchmarkPassword_Hash(b *testing.B) {
	restore := SetArgonParams(DefaultArgon)
	defer restore()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = HashPassword(context.Background(), "bench-password-1")
	}
}

// TestScopeIndex_MatchesSpec compares the runtime scanner with a full YAML parse.
func TestScopeIndex_MatchesSpec(t *testing.T) {
	idx, err := loadScopeIndex()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(api.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for p, item := range spec.Paths {
		for m, n := range item {
			up, ok := httpMethods[m]
			if !ok {
				continue
			}
			var op struct {
				X []string `yaml:"x-scopes"`
			}
			_ = n.Decode(&op)
			want[up+" "+p] = strings.Join(op.X, ",")
		}
	}
	got := map[string]string{}
	for _, op := range idx.ops {
		got[op.method+" "+op.path] = strings.Join(op.scopes, ",")
	}
	if len(got) != len(want) {
		t.Fatalf("operations: scanner %d, yaml %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: scanner %q, yaml %q", k, got[k], v)
		}
	}
}

func TestScopeIndex_LiteralWins(t *testing.T) {
	idx, _ := loadScopeIndex()
	if op := idx.match("GET", "/agent/tickets/counts"); op == nil || op.path != "/agent/tickets/counts" {
		t.Fatalf("got %+v", op)
	}
	if op := idx.match("GET", "/agent/tickets/0192"); op == nil || op.path != "/agent/tickets/{id}" {
		t.Fatalf("got %+v", op)
	}
	if op := idx.match("GET", "/nope"); op != nil {
		t.Fatal("matched unknown path")
	}
}

func TestReturnTo_Table(t *testing.T) {
	hostile := []string{
		"//evil.com", "//evil.com/agent", `/\evil.com`, `/\/evil.com`, "https://evil.com", "https:evil", "http:/evil.com",
		"%2F%2Fevil.com", "/%2F/evil.com", "/%2fevil.com", "/%5Cevil.com", "javascript:alert(1)", "/javascript:alert(1)",
		"JaVaScRiPt:alert(1)", "data:text/html,x", "evil.com", "agent", " /agent", "/agent\r\nLocation: x", "/agent\x00",
		"\t/agent", "/api/v1/me", "/setup", "///evil.com", "/\\\\evil", "", "?x=1", "#frag", "/%0d%0aSet-Cookie:x",
	}
	for _, h := range hostile {
		if got := SafeReturnTo(h, domain.KindStaff); got != "/agent" {
			t.Errorf("staff %q → %q", h, got)
		}
		if got := SafeReturnTo(h, domain.KindCustomer); got != "/" {
			t.Errorf("customer %q → %q", h, got)
		}
	}
	if got := SafeReturnTo("/agent/tickets/42?tab=notes", domain.KindStaff); got != "/agent/tickets/42?tab=notes" {
		t.Fatalf("legit staff path: %q", got)
	}
	if got := SafeReturnTo("/agent/tickets", domain.KindCustomer); got != "/" {
		t.Fatalf("customer redirected into /agent: %q", got)
	}
	if got := SafeReturnTo("/tickets/42", domain.KindCustomer); got != "/tickets/42" {
		t.Fatalf("legit customer path: %q", got)
	}
	if got := SafeReturnTo("/tickets/42", domain.KindStaff); got != "/agent" {
		t.Fatalf("staff sent to portal path: %q", got)
	}
	if len(hostile) < 25 {
		t.Fatal("need 25+ hostile inputs")
	}
}

func TestAuthToken_SingleUse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		var tok string
		_ = db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = IssueAuthToken(ctx, tx, uid, PurposeResetPassword, map[string]any{"k": "v"})
			return err
		})
		consume := func(purpose string) error {
			return db.Tx(ctx, func(tx *store.Tx) error {
				got, data, err := ConsumeAuthToken(ctx, tx, tok, purpose)
				if err == nil && (got != uid || data["k"] != "v") {
					t.Fatalf("consume returned %s %v", got, data)
				}
				return err
			})
		}
		if err := consume(PurposeMagicLink); err != ErrTokenInvalid {
			t.Fatalf("wrong purpose (TestAuthToken_PurposeBound): %v", err)
		}
		if err := consume(PurposeResetPassword); err != nil {
			t.Fatal(err)
		}
		if err := consume(PurposeResetPassword); err != ErrTokenInvalid {
			t.Fatalf("second consume: %v", err)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM auth_tokens WHERE token_hash = ?`, tok); n != 0 {
			t.Fatal("raw token stored")
		}
	})
}

func countRows(t *testing.T, db *store.DB, q string, args ...any) int {
	var n int
	if err := db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuthToken_PurposeBound(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		var tok string
		_ = db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = IssueAuthToken(ctx, tx, uid, PurposeVerifyEmail, nil)
			return err
		})
		for _, p := range []string{PurposeResetPassword, PurposeMagicLink, PurposeInvite} {
			err := db.Tx(ctx, func(tx *store.Tx) error { _, _, err := ConsumeAuthToken(ctx, tx, tok, p); return err })
			if err != ErrTokenInvalid {
				t.Fatalf("%s: %v", p, err)
			}
		}
	})
}

func TestAuthToken_Expired(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		now := store.Now()
		var tok string
		_ = db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = issueAuthTokenAt(ctx, tx, uid, PurposeMagicLink, nil, now)
			return err
		})
		err := db.Tx(ctx, func(tx *store.Tx) error {
			_, _, err := consumeAuthTokenAt(ctx, tx, tok, PurposeMagicLink, now.Add(16*time.Minute))
			return err
		})
		if err != ErrTokenInvalid {
			t.Fatalf("15-minute magic link after 16 min: %v", err)
		}
	})
}

func TestAuthToken_ConcurrentOneWinner(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		var tok string
		_ = db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = IssueAuthToken(ctx, tx, uid, PurposeInvite, nil)
			return err
		})
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := db.Tx(ctx, func(tx *store.Tx) error { _, _, err := ConsumeAuthToken(ctx, tx, tok, PurposeInvite); return err })
				if err == nil {
					mu.Lock()
					wins++
					mu.Unlock()
				} else if err != ErrTokenInvalid {
					t.Errorf("consume: %v", err)
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("winners %d", wins)
		}
	})
}

func TestThrottle_AccountLockUnit(t *testing.T) {
	th := newThrottle()
	now := time.Now()
	th.success("a@x.test", "1.1.1.1")
	for i := 0; i < 9; i++ {
		th.fail("a@x.test", now)
	}
	if th.locked("a@x.test", "2.2.2.2", now) {
		t.Fatal("locked after 9 failures")
	}
	th.fail("a@x.test", now)
	if !th.locked("a@x.test", "2.2.2.2", now) || th.locked("a@x.test", "1.1.1.1", now) {
		t.Fatal("lock semantics")
	}
	if th.locked("a@x.test", "2.2.2.2", now.Add(16*time.Minute)) {
		t.Fatal("lock did not expire")
	}
}
