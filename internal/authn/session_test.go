// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestSession_StoredHashedOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		if n := e.Int(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, c.Cookie); n != 0 {
			t.Fatal("raw session token stored in the DB")
		}
		if n := e.Int(`SELECT COUNT(*) FROM sessions WHERE id_hash = ? AND user_id = ?`, sha(c.Cookie), u.ID); n != 1 {
			t.Fatal("sha256(token) not stored")
		}
		rows, err := e.DB.Read().QueryContext(context.Background(), `SELECT id_hash, csrf_token, ip, user_agent FROM sessions`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, ip, ua string
			_ = rows.Scan(&a, &b, &ip, &ua)
			for _, v := range []string{a, b, ip, ua} {
				if strings.Contains(v, c.Cookie) {
					t.Fatal("raw token found in sessions row")
				}
			}
		}
	})
}

func TestSession_CookieFlags(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_PUBLIC_URL": "https://help.example.test"}})
		u := e.Customer("", domain.RoleRequester)
		c := e.Client()
		resp, body := login(c, domain.AudienceCustomer, u.Email, authntest.TestPassword)
		if resp.StatusCode != 200 {
			t.Fatalf("login: %d %s", resp.StatusCode, body)
		}
		var ck *http.Cookie
		for _, x := range resp.Cookies() {
			if x.Name == "fd_session" {
				ck = x
			}
		}
		if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
			t.Fatalf("cookie flags: %+v", ck)
		}
		if ck.MaxAge != int((90*24*time.Hour)/time.Second) {
			t.Fatalf("customer absolute lifetime: %d", ck.MaxAge)
		}
	})
}

func TestSession_InsecureCookiesRefusedForHTTPS(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	defer func() {
		if r := recover(); r != nil {
			t.Fatal(r)
		}
	}()
	tb := &fatalCatcher{TB: t}
	func() {
		defer func() { _ = recover() }()
		authntest.New(tb, db, authntest.Options{Env: map[string]string{"FD_PUBLIC_URL": "https://help.example.test", "FD_INSECURE_COOKIES": "true"}})
	}()
	if !strings.Contains(tb.msg, "FD_INSECURE_COOKIES") {
		t.Fatalf("expected boot error, got %q", tb.msg)
	}
}

// fatalCatcher turns Fatalf into a panic carrying the message.
type fatalCatcher struct {
	testing.TB
	msg string
}

func (f *fatalCatcher) Fatalf(format string, args ...any) {
	f.msg = strings.TrimSpace(strings.ReplaceAll(sprintf(format, args...), "\n", " "))
	panic("fatal")
}

func (f *fatalCatcher) Fatal(args ...any) { f.msg = sprint(args...); panic("fatal") }

func TestSession_RotatedOnLogin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		old := c.Cookie
		// Logging in again while presenting the old cookie: new token, old one revoked.
		if r, _ := login(c, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatal("relogin failed")
		}
		if c.Cookie == old || c.Cookie == "" {
			t.Fatal("session token not rotated")
		}
		if n := e.Int(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, sha(old)); n != 0 {
			t.Fatal("old session not revoked (fixation)")
		}
		stale := &authntest.Client{E: e, Cookie: old, IP: "192.0.2.5"}
		if r, _ := stale.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("old cookie still valid: %d", r.StatusCode)
		}
		// A planted (attacker-chosen) cookie value is never adopted.
		planted := &authntest.Client{E: e, Cookie: "attacker-chosen-session-value", IP: "192.0.2.6"}
		if r, _ := login(planted, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 200 {
			t.Fatal("login with planted cookie failed")
		}
		if planted.Cookie == "attacker-chosen-session-value" {
			t.Fatal("planted session id adopted")
		}
	})
}

func TestSession_IdleExpiry(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		u := e.StaffUser(domain.RoleAgent, 1, false)
		c := e.Login(u)
		e.Clock.Advance(11 * time.Hour)
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("expired before the idle limit")
		}
		e.Clock.Advance(11 * time.Hour) // 11 h since the last request: still inside 12 h idle
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
			t.Fatal("activity did not extend the idle window")
		}
		e.Clock.Advance(12*time.Hour + time.Second)
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatalf("idle session still valid: %d", r.StatusCode)
		}
	})
}

func TestSession_AbsoluteExpiry(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"FD_AUTH_STAFF_LOCAL": "true"}})
		u := e.StaffUser(domain.RoleAgent, 1, false)
		c := e.Login(u)
		elapsed := time.Duration(0)
		for i := 0; i < 20; i++ { // active every 11 h, so idle never triggers
			e.Clock.Advance(11 * time.Hour)
			elapsed += 11 * time.Hour
			r, _ := c.Do("GET", "/api/v1/me", nil)
			if r.StatusCode == 401 {
				if elapsed < 7*24*time.Hour {
					t.Fatalf("expired too early after %v", elapsed)
				}
				return
			}
			if elapsed >= 7*24*time.Hour {
				t.Fatalf("still valid after %v (absolute 7 d)", elapsed)
			}
		}
		t.Fatal("session never expired")
	})
}

func TestSession_LastSeenNoPerRequestWrite(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		before := e.Str(`SELECT CAST(last_seen_at AS TEXT) FROM sessions WHERE id_hash = ?`, sha(c.Cookie))
		flushes := e.Auth.SessionTouchFlushes()
		e.Clock.Advance(time.Minute)
		for i := 0; i < 100; i++ {
			if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
				t.Fatal("request failed")
			}
		}
		if after := e.Str(`SELECT CAST(last_seen_at AS TEXT) FROM sessions WHERE id_hash = ?`, sha(c.Cookie)); after != before {
			t.Fatal("last_seen_at written per request")
		}
		if e.Auth.SessionTouchFlushes() != flushes {
			t.Fatal("flush happened before the interval")
		}
		if err := e.Auth.FlushSessionTouches(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.Auth.SessionTouchFlushes() != flushes+1 {
			t.Fatal("expected exactly one flush transaction")
		}
		if after := e.Str(`SELECT CAST(last_seen_at AS TEXT) FROM sessions WHERE id_hash = ?`, sha(c.Cookie)); after == before {
			t.Fatal("flush did not persist last_seen_at")
		}
	})
}

func TestCSRF_CookieMutationsNeedToken(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Login(u)
		tok := c.CSRF
		c.CSRF = ""
		if r, b := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "No Token"}); r.StatusCode != 403 || !strings.Contains(string(b), "/csrf") {
			t.Fatalf("missing token: %d %s", r.StatusCode, b)
		}
		c.CSRF = "forged"
		if r, _ := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "Forged"}); r.StatusCode != 403 {
			t.Fatalf("forged token: %d", r.StatusCode)
		}
		c.CSRF, c.Origin = tok, "https://evil.example"
		if r, _ := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "Cross Site"}); r.StatusCode != 403 {
			t.Fatalf("foreign origin: %d", r.StatusCode)
		}
		c.Origin = ""
		if r, b := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "Same Site"}); r.StatusCode != 200 {
			t.Fatalf("valid token: %d %s", r.StatusCode, b)
		}
		// Another user's CSRF token does not work for this session.
		other := e.Login(e.Customer("", domain.RoleRequester))
		c.CSRF = other.CSRF
		if r, _ := c.Do("PATCH", "/api/v1/me", map[string]any{"name": "Other"}); r.StatusCode != 403 {
			t.Fatalf("token of another session accepted: %d", r.StatusCode)
		}
	})
}

func TestSecurity_AnonymousCrossSitePostRefused(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		c := e.Client()
		c.Origin = "https://evil.example"
		if r, _ := login(c, domain.AudienceCustomer, u.Email, authntest.TestPassword); r.StatusCode != 403 {
			t.Fatalf("cross-site login post: %d", r.StatusCode)
		}
	})
}
