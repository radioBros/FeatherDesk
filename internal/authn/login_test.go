// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestRoutes_DocumentedInSpec(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		apispec.AssertRoutes(t, e.App.Server.Router())
	})
}

func login(c *authntest.Client, aud domain.Audience, email, pw string) (*http.Response, []byte) {
	return c.Do("POST", "/api/v1/auth/login", map[string]any{"audience": string(aud), "email": email, "password": pw})
}

func TestLogin_SuccessReturnsMeAndCookie(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Client()
		resp, body := login(c, domain.AudienceCustomer, strings.ToUpper(u.Email), authntest.TestPassword)
		if resp.StatusCode != 200 {
			t.Fatalf("login: %d %s", resp.StatusCode, body)
		}
		me := authntest.JSON(t, body)
		if me["id"] != u.ID || me["csrf_token"] == "" || me["audience"] != "customer" {
			t.Fatalf("me: %v", me)
		}
		if c.Cookie == "" {
			t.Fatal("no session cookie")
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control = %q", got)
		}
		resp, body = c.Do("GET", "/api/v1/me", nil)
		if resp.StatusCode != 200 || authntest.JSON(t, body)["email"] != u.Email {
			t.Fatalf("/me: %d %s", resp.StatusCode, body)
		}
	})
}

func TestLogin_AudienceMismatchGenericError(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		cust := e.Customer("", domain.RoleRequester)
		staff := e.StaffUser(domain.RoleAgent, 1, false)
		r1, b1 := login(e.Client(), domain.AudienceStaff, cust.Email, authntest.TestPassword)
		r2, b2 := login(e.Client(), domain.AudienceCustomer, staff.Email, authntest.TestPassword)
		r3, b3 := login(e.Client(), domain.AudienceCustomer, cust.Email, "wrong-password-123")
		r4, b4 := login(e.Client(), domain.AudienceCustomer, "nobody@example.test", "wrong-password-123")
		for i, r := range []*http.Response{r1, r2, r3, r4} {
			if r.StatusCode != 401 {
				t.Fatalf("case %d: status %d", i, r.StatusCode)
			}
		}
		// Identical problem bodies for every failure cause (no enumeration).
		norm := func(b []byte) string { return string(bytes.TrimSpace(b)) }
		if norm(b1) != norm(b2) || norm(b2) != norm(b3) || norm(b3) != norm(b4) {
			t.Fatalf("bodies differ:\n%s\n%s\n%s\n%s", b1, b2, b3, b4)
		}
	})
}

func TestLogin_StaffLocalOffExceptBreakGlass(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{}) // FD_AUTH_STAFF_LOCAL unset = false
		agent := e.StaffUser(domain.RoleAgent, 1, false)
		admin := e.StaffUser(domain.RoleAdmin, 3, true)
		if r, _ := login(e.Client(), domain.AudienceStaff, agent.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatalf("agent local login with staff local off: %d", r.StatusCode)
		}
		if r, b := login(e.Client(), domain.AudienceStaff, admin.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatalf("break-glass login: %d %s", r.StatusCode, b)
		}
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.break_glass_login' AND actor_id = ?`, admin.ID); n != 1 {
			t.Fatalf("break-glass audit rows = %d", n)
		}
		if !strings.Contains(e.Logs.String(), "break-glass admin login") {
			t.Fatal("no WARN log for break-glass login")
		}
	})
}

func TestLogin_DisabledAndPendingRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		a := e.Customer("", domain.RoleRequester)
		b := e.Customer("", domain.RoleRequester)
		e.Exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, a.ID)
		e.Exec(`UPDATE users SET status = 'pending_verification' WHERE id = ?`, b.ID)
		for _, u := range []authntest.User{a, b} {
			if r, _ := login(e.Client(), domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 401 {
				t.Fatalf("%s: %d", u.Email, r.StatusCode)
			}
		}
	})
}

func TestLogin_RehashOnLogin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		before := e.Str(`SELECT password_hash FROM users WHERE id = ?`, u.ID)
		restore := authn.SetArgonParams(authn.ArgonParams{MemoryKiB: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
		defer restore()
		if r, b := login(e.Client(), domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatalf("login: %d %s", r.StatusCode, b)
		}
		after := e.Str(`SELECT password_hash FROM users WHERE id = ?`, u.ID)
		if after == before || !strings.Contains(after, "m=1024,") {
			t.Fatalf("not rehashed: %s → %s", before, after)
		}
	})
}

// loginAttempt is one failing login used by the timing comparisons.
type loginAttempt struct {
	aud             domain.Audience
	email, password string
}

// loginMedians interleaves n attempts of a and b (fresh client each, every
// one must answer 401) so load drift hits both equally, and returns their
// median durations. Both are warmed once first (the dummy hash).
func loginMedians(t *testing.T, e *authntest.Env, n int, a, b loginAttempt) (time.Duration, time.Duration) {
	t.Helper()
	sample := func(x loginAttempt) time.Duration {
		c := e.Client()
		c.NoCheck = true
		start := time.Now()
		r, _ := login(c, x.aud, x.email, x.password)
		d := time.Since(start)
		if r.StatusCode != 401 {
			t.Fatalf("%s: status %d", x.email, r.StatusCode)
		}
		return d
	}
	median := func(ds []time.Duration) time.Duration {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[len(ds)/2]
	}
	sample(a)
	sample(b)
	var as, bs []time.Duration
	for i := 0; i < n; i++ {
		as = append(as, sample(a))
		bs = append(bs, sample(b))
	}
	return median(as), median(bs)
}

// timingSamples is the per-side sample count; under -race the ratio is not
// asserted, so only the 401 path is exercised (cheaply).
func timingSamples() int {
	if raceEnabled {
		return 5
	}
	return 41
}

// assertTimingSimilar fails when b/a is outside ±20 % (skipped under -race,
// which distorts wall-clock ratios).
func assertTimingSimilar(t *testing.T, a, b time.Duration) {
	t.Helper()
	ratio := float64(b) / float64(a)
	t.Logf("median a=%v b=%v ratio=%.2f", a, b, ratio)
	if raceEnabled {
		t.Skip("timing ratio not asserted under -race")
	}
	if ratio < 0.8 || ratio > 1.2 {
		t.Fatalf("timing ratio %.2f outside ±20%%", ratio)
	}
}

// TestLogin_UnknownUserTimingSimilar: the median time of a login for an
// unknown address is within ±20 % of a known address with a wrong password
// (the dummy hash is verified). Runs on SQLite with production argon params.
func TestLogin_UnknownUserTimingSimilar(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	db := storetest.Open(t, store.SQLite)
	e := authntest.New(t, db, authntest.Options{})
	restore := authn.SetArgonParams(authn.DefaultArgon)
	defer restore()
	u := e.Customer("", domain.RoleRequester)
	e.SetPassword(u.ID) // production-cost hash
	known, unknown := loginMedians(t, e, timingSamples(),
		loginAttempt{domain.AudienceCustomer, u.Email, "definitely-wrong-pw"},
		loginAttempt{domain.AudienceCustomer, "nobody-here@example.test", "definitely-wrong-pw"})
	assertTimingSimilar(t, known, unknown)
}

func TestThrottle_IPBucket429(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Throttle: true})
		var last *http.Response
		for i := 0; i < 11; i++ {
			last, _ = login(e.Client(), domain.AudienceCustomer, "x@example.test", "wrong-password-123")
		}
		if last.StatusCode != 429 || last.Header.Get("Retry-After") == "" {
			t.Fatalf("11th attempt: %d retry-after=%q", last.StatusCode, last.Header.Get("Retry-After"))
		}
		other := e.Client()
		other.IP = "192.0.2.99"
		if r, _ := login(other, domain.AudienceCustomer, "x@example.test", "wrong-password-123"); r.StatusCode != 401 {
			t.Fatalf("other IP: %d", r.StatusCode)
		}
	})
}

func TestThrottle_AccountSoftLock(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Throttle: true})
		u := e.Customer("", domain.RoleRequester)
		home := e.Client()
		home.IP = "198.51.100.7"
		if r, _ := login(home, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatal("first login failed")
		}
		for i := 0; i < 10; i++ { // ten failures from ten IPs (not IP-throttled)
			c := e.Client()
			c.IP = "203.0.113." + string(rune('1'+i))
			login(c, domain.AudienceCustomer, u.Email, "wrong-password-123")
		}
		attacker := e.Client()
		attacker.IP = "203.0.113.200"
		if r, _ := login(attacker, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 401 {
			t.Fatalf("locked account from new IP: %d", r.StatusCode)
		}
		home2 := e.Client()
		home2.IP = "198.51.100.7"
		if r, _ := login(home2, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatalf("known IP during lock: %d", r.StatusCode)
		}
	})
}
