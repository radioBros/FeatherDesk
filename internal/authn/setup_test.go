// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

var tokenRe = regexp.MustCompile(`setup token: ([A-Za-z0-9_-]+)`)

func setupToken(t *testing.T, e *authntest.Env) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(e.Logs.String())
	if m == nil {
		t.Fatalf("no setup token in log:\n%s", e.Logs.String())
	}
	return m[1]
}

func setupBody(tok string) map[string]any {
	return map[string]any{
		"setup_token": tok,
		"admin":       map[string]any{"email": "owner@example.test", "name": "Owner", "password": "owner-password-77"},
		"branding":    map[string]any{"product_name": "Acme Help", "company_name": "Acme", "primary_color": "#112233"},
		"smtp":        map[string]any{"host": "smtp.acme.test", "port": 587, "security": "starttls", "username": "u", "password": "smtp-pw-secret"},
	}
}

func TestSetup_StatusLeaksNothing(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		r, b := e.Client().Do("GET", "/api/v1/setup/status", nil)
		m := authntest.JSON(t, b)
		if r.StatusCode != 200 || m["setup_pending"] != true || len(m) != 3 || m["has_admin"] != false {
			t.Fatalf("status: %d %s", r.StatusCode, b)
		}
		if strings.Contains(string(b), setupToken(t, e)) {
			t.Fatal("token in status")
		}
	})
}

func TestSetup_WrongToken403AndThrottled(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Throttle: true})
		c := e.Client()
		codes := map[int]int{}
		for i := 0; i < 12; i++ {
			r, _ := c.Do("POST", "/api/v1/setup/complete", setupBody("wrong-token"))
			codes[r.StatusCode]++
		}
		if codes[403] != 10 || codes[429] != 2 {
			t.Fatalf("codes: %v", codes)
		}
		if n := e.Int(`SELECT COUNT(*) FROM users`); n != 0 {
			t.Fatal("user created")
		}
	})
}

func TestSetup_CompleteCreatesAdminAndSignsIn(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Client()
		r, b := c.Do("POST", "/api/v1/setup/complete", setupBody(setupToken(t, e)))
		if r.StatusCode != 201 {
			t.Fatalf("complete: %d %s", r.StatusCode, b)
		}
		me := authntest.JSON(t, b)
		if me["role"] != "admin" || me["break_glass"] != true || c.Cookie == "" {
			t.Fatalf("me: %v", me)
		}
		br, _ := e.App.Deps.Settings.Branding(context.Background())
		if br.ProductName != "Acme Help" || br.Colors.Primary != "#112233" {
			t.Fatalf("branding: %+v", br)
		}
		smtp, _ := e.App.Deps.Settings.SMTP(context.Background())
		pw, _ := e.App.Deps.Settings.SMTPPassword(context.Background())
		if smtp.Host != "smtp.acme.test" || pw != "smtp-pw-secret" {
			t.Fatalf("smtp: %v pw=%q", smtp, pw)
		}
		if strings.Contains(e.Str(`SELECT CAST(value AS TEXT) FROM settings WHERE key = 'smtp'`), "smtp-pw-secret") {
			t.Fatal("SMTP password stored in clear")
		}
		_, b = e.Client().Do("GET", "/api/v1/auth/config", nil)
		if authntest.JSON(t, b)["setup_pending"] != false {
			t.Fatal("auth config still reports setup pending")
		}
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'setup.completed'`); n != 1 {
			t.Fatal("not audited")
		}
	})
}

func TestSetup_404OnceAdminExists(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		tok := setupToken(t, e)
		e.Client().Do("POST", "/api/v1/setup/complete", setupBody(tok))
		for _, p := range []string{"/api/v1/setup/status"} {
			if r, _ := e.Client().Do("GET", p, nil); r.StatusCode != 404 {
				t.Fatalf("GET %s: %d", p, r.StatusCode)
			}
		}
		if r, _ := e.Client().Do("POST", "/api/v1/setup/complete", setupBody(tok)); r.StatusCode != 404 {
			t.Fatalf("second complete: %d", r.StatusCode)
		}
		// A fresh boot against a DB that has an admin keeps the wizard closed.
		e2 := authntest.New(t, db, authntest.Options{})
		if tokenRe.MatchString(e2.Logs.String()) {
			t.Fatal("setup token printed although an admin exists")
		}
		if r, _ := e2.Client().Do("GET", "/api/v1/setup/status", nil); r.StatusCode != 404 {
			t.Fatal("wizard open after restart")
		}
	})
}

func TestSetup_ConcurrentCompleteOneWinner(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		tok := setupToken(t, e)
		var wg sync.WaitGroup
		codes := make([]int, 10)
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := e.Client()
				body := setupBody(tok)
				body["admin"].(map[string]any)["email"] = "owner" + string(rune('a'+i)) + "@example.test"
				r, _ := c.Do("POST", "/api/v1/setup/complete", body)
				codes[i] = r.StatusCode
			}(i)
		}
		wg.Wait()
		wins := 0
		for _, c := range codes {
			if c == 201 {
				wins++
			} else if c != 404 {
				t.Fatalf("unexpected status %d (%v)", c, codes)
			}
		}
		if wins != 1 || e.Int(`SELECT COUNT(*) FROM users WHERE role = 'admin'`) != 1 {
			t.Fatalf("winners %d codes %v", wins, codes)
		}
	})
}

func TestSetup_DisabledByEnvBootstrap(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{
			"ST_BOOTSTRAP_ADMIN_EMAIL": "Boot@Example.test", "ST_BOOTSTRAP_ADMIN_PASSWORD": "bootstrap-pass-99"}})
		if tokenRe.MatchString(e.Logs.String()) {
			t.Fatal("setup token printed with env bootstrap")
		}
		if r, _ := e.Client().Do("GET", "/api/v1/setup/status", nil); r.StatusCode != 404 {
			t.Fatal("wizard open")
		}
	})
}

// TestSetup_DriveByTakeover: a fresh instance probed without the token —
// every /setup and /api/v1 path tried — never gains an admin, and no default
// credentials exist.
func TestSetup_DriveByTakeover(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		c := e.Client()
		c.NoCheck = true
		ops, _ := apispec.Operations()
		for op := range ops {
			method, path, _ := strings.Cut(op, " ")
			path = strings.ReplaceAll(path, "{}", "0192f3c4-5b6a-7c8d-9e0f-1a2b3c4d5e6f")
			body := setupBody("")
			c.Do(method, path, body)
		}
		for _, cred := range [][2]string{{"admin@example.com", "admin"}, {"admin", "admin"}, {"admin@localhost", "password"}} {
			login(c, domain.AudienceStaff, cred[0], cred[1])
		}
		if n := e.Int(`SELECT COUNT(*) FROM users`); n != 0 {
			t.Fatalf("%d users exist after the drive-by", n)
		}
	})
}

func TestSetup_TokenNeverInHTTPResponse(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		tok := setupToken(t, e)
		c := e.Client()
		check := func(b []byte, h map[string][]string) {
			if strings.Contains(string(b), tok) {
				t.Fatal("setup token in a response body")
			}
			for k, vs := range h {
				for _, v := range vs {
					if strings.Contains(v, tok) {
						t.Fatalf("setup token in header %s", k)
					}
				}
			}
		}
		for _, p := range []string{"/api/v1/setup/status", "/api/v1/auth/config"} {
			r, b := c.Do("GET", p, nil)
			check(b, r.Header)
		}
		r, b := c.Do("POST", "/api/v1/setup/complete", setupBody(tok))
		check(b, r.Header)
		r, b = c.Do("GET", "/api/v1/me", nil)
		check(b, r.Header)
	})
}

func TestBootstrap_CreatesOnlyOnEmptyDB(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		env := map[string]string{"ST_BOOTSTRAP_ADMIN_EMAIL": "boot@example.test", "ST_BOOTSTRAP_ADMIN_PASSWORD": "bootstrap-pass-99"}
		e := authntest.New(t, db, authntest.Options{Env: env})
		if n := e.Int(`SELECT COUNT(*) FROM users WHERE email = 'boot@example.test' AND role = 'admin' AND break_glass = ?`, true); n != 1 {
			t.Fatal("bootstrap admin not created")
		}
		if r, _ := login(e.Client(), domain.AudienceStaff, "boot@example.test", "bootstrap-pass-99"); r.StatusCode != 200 {
			t.Fatal("bootstrap admin cannot log in (staff local off)")
		}
		// Non-empty DB: a different bootstrap email does nothing.
		db2 := storetest.Open(t, db.Dialect)
		storetest.CreateCustomer(t, db2, "")
		e2 := authntest.New(t, db2, authntest.Options{Env: env})
		if n := e2.Int(`SELECT COUNT(*) FROM users WHERE role = 'admin'`); n != 0 {
			t.Fatal("bootstrap ran on a non-empty DB")
		}
	})
}

func TestBootstrap_NoopWhenAdminExists(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_BOOTSTRAP_ADMIN_EMAIL": "boot@example.test", "ST_BOOTSTRAP_ADMIN_PASSWORD": "bootstrap-pass-99"}})
		e2 := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_BOOTSTRAP_ADMIN_EMAIL": "other@example.test", "ST_BOOTSTRAP_ADMIN_PASSWORD": "bootstrap-pass-99"}})
		_ = e
		if n := e2.Int(`SELECT COUNT(*) FROM users`); n != 1 {
			t.Fatalf("users = %d", n)
		}
	})
}

func TestResetPasswordRevokesSessions(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.StaffUser(domain.RoleAdmin, 3, true)
		c := e.Login(u)
		gen, err := authn.ResetPassword(context.Background(), e.DB, u.Email, "")
		if err != nil || len(gen) < 16 {
			t.Fatalf("reset: %v %q", err, gen)
		}
		if r, _ := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatal("session survived reset-password")
		}
		if r, _ := login(e.Client(), domain.AudienceStaff, u.Email, gen); r.StatusCode != 200 {
			t.Fatal("generated password does not work")
		}
	})
}

func TestCLI_CreateAdminThenLocalLogin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{}) // ST_AUTH_STAFF_LOCAL=false
		var out strings.Builder
		restore := authn.SetCLIIO(strings.NewReader("cli-admin-pass-123\n"), &out, func(k string) string { return e.Vars[k] })
		defer restore()
		if err := authn.RunCLI(context.Background(), "create-admin", []string{"--email", "CLI@example.test", "--name", "Ops", "--password-stdin"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "cli@example.test") {
			t.Fatalf("output: %s", out.String())
		}
		if r, b := login(e.Client(), domain.AudienceStaff, "cli@example.test", "cli-admin-pass-123"); r.StatusCode != 200 {
			t.Fatalf("login: %d %s", r.StatusCode, b)
		}
		if err := authn.RunCLI(context.Background(), "create-admin", []string{"--email", "cli@example.test"}); err == nil {
			t.Fatal("duplicate admin accepted")
		}
		out.Reset()
		if err := authn.RunCLI(context.Background(), "reset-password", []string{"--email", "cli@example.test"}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "generated password:") {
			t.Fatalf("output: %s", out.String())
		}
	})
}
