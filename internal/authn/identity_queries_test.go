// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// DB-010 identity queries (written by authn against store.Querier, D-18).

// TestAuthTokenConsumedOnce: eight concurrent consumers of one single-use token
// → exactly one wins (UPDATE … WHERE used_at IS NULL … RETURNING), the others
// get ErrTokenInvalid. Both engines.
func TestAuthTokenConsumedOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		uid := storetest.CreateCustomer(t, db, "")
		var tok string
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			var err error
			tok, err = authn.IssueAuthToken(ctx, tx, uid, authn.PurposeResetPassword, map[string]any{"k": "v"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		const n = 8
		var (
			wg              sync.WaitGroup
			start           = make(chan struct{})
			mu              sync.Mutex
			wins, losses    int
			winnerUser      string
			unexpectedError error
		)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				var gotUser string
				err := db.Tx(ctx, func(tx *store.Tx) error {
					u, _, err := authn.ConsumeAuthToken(ctx, tx, tok, authn.PurposeResetPassword)
					gotUser = u
					return err
				})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
					winnerUser = gotUser
				case errors.Is(err, authn.ErrTokenInvalid):
					losses++
				default:
					unexpectedError = err
				}
			}()
		}
		close(start)
		wg.Wait()
		if unexpectedError != nil {
			t.Fatal(unexpectedError)
		}
		if wins != 1 || losses != n-1 || winnerUser != uid {
			t.Fatalf("concurrent consume: wins=%d losses=%d user=%q, want exactly one winner", wins, losses, winnerUser)
		}
		var used int
		if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_tokens WHERE user_id = ? AND used_at IS NOT NULL`, uid).Scan(&used); err != nil || used != 1 {
			t.Fatalf("used rows: %d %v", used, err)
		}
	})
}

// TestEmailCaseInsensitiveUnique: users(lower(email)) is unique at the
// database level (migration 0002), and the authn write paths normalise, so
// Foo@Example.com and foo@example.com can never be two accounts. Both engines.
func TestEmailCaseInsensitiveUnique(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		insert := func(email string) error {
			return db.Tx(ctx, func(tx *store.Tx) error {
				now := store.Now()
				_, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, status, created_at, updated_at) VALUES (?, 'customer', ?, 'X', 'requester', 'active', ?, ?)`,
					store.NewID(), email, now, now)
				return err
			})
		}
		// DB-level guard: raw SQL that skips normalisation is still refused.
		if err := insert("Foo@Example.com"); err != nil {
			t.Fatal(err)
		}
		if err := insert("foo@example.com"); !store.IsUniqueViolation(err) {
			t.Fatalf("case variant accepted by the database: %v", err)
		}
		if err := insert("FOO@EXAMPLE.COM"); !store.IsUniqueViolation(err) {
			t.Fatalf("upper-case variant accepted by the database: %v", err)
		}
		// Application path: create-admin normalises and reports the conflict.
		authntest.FastArgon()
		if _, err := authn.CreateAdmin(ctx, db, "Admin@Example.org", "A", "", "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := authn.CreateAdmin(ctx, db, "  ADMIN@example.ORG ", "B", "", "test"); !errors.Is(err, authn.ErrUserExists) {
			t.Fatalf("case-variant create-admin: %v, want ErrUserExists", err)
		}
		var n int
		var stored string
		if err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*), MIN(email) FROM users WHERE lower(email) = ?`, "admin@example.org").Scan(&n, &stored); err != nil {
			t.Fatal(err)
		}
		if n != 1 || stored != "admin@example.org" {
			t.Fatalf("admin rows=%d stored=%q, want one lower-cased row", n, stored)
		}
	})
}

// TestBatchTouch: 500 sessions used once each → their last_seen_at is only
// buffered (no write on the request path), then one flush updates all 500 in a
// single writer transaction. Both engines.
func TestBatchTouch(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{})
		u := e.Customer("", domain.RoleRequester)
		const n = 500
		created := e.Clock.Now().Truncate(time.Microsecond)
		toks := make([]string, n)
		err := e.DB.Tx(context.Background(), func(tx *store.Tx) error {
			for i := range toks {
				toks[i] = fmt.Sprintf("batch-touch-session-token-%04d-%s", i, strings.Repeat("x", 20))
				if _, err := tx.ExecContext(context.Background(), `INSERT INTO sessions (id_hash, user_id, audience, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
					VALUES (?, ?, 'customer', ?, ?, ?, ?, '', '')`, sha(toks[i]), u.ID, fmt.Sprintf("csrf-%d", i), created, created, created.Add(24*time.Hour)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		e.Clock.Advance(time.Minute)
		before := e.DB.WriteStats() // the app's own handle on the same database
		for _, tok := range toks {
			c := e.Client()
			c.Cookie = tok
			c.NoCheck = true // 500 identical /me answers; the spec check is covered elsewhere
			if r, body := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 {
				t.Fatalf("GET /me: %d %s", r.StatusCode, body)
			}
		}
		if d := e.DB.WriteStats(); d != before {
			t.Fatalf("request path wrote: %+v → %+v", before, d)
		}
		if got := e.Int(`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND last_seen_at > ?`, u.ID, created); got != 0 {
			t.Fatalf("%d sessions written before the flush", got)
		}
		if err := e.Auth.FlushSessionTouches(context.Background()); err != nil {
			t.Fatal(err)
		}
		after := e.DB.WriteStats()
		if txs := after.Txs - before.Txs; txs != 1 {
			t.Fatalf("flush used %d transactions, want 1", txs)
		}
		if got := e.Int(`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND last_seen_at > ?`, u.ID, created); got != n {
			t.Fatalf("flush updated %d of %d sessions", got, n)
		}
		t.Logf("one flush: %d tx, %d statements for %d sessions", after.Txs-before.Txs, after.Statements-before.Statements, n)
	})
}

// TestRequestPathDoesNoWrites (PLT-023): 100 authenticated GETs, by session
// cookie and by PAT, run zero writer transactions and zero writer statements;
// last_seen_at / last_used_at go to the write-behind buffers instead. Both
// engines.
func TestRequestPathDoesNoWrites(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{"ST_AUTH_STAFF_LOCAL": "true"}})
		staff := e.Login(e.StaffUser(domain.RoleAgent, 1, false))
		cust := e.Login(e.Customer("", domain.RoleRequester))
		pat := e.Login(e.Customer("", domain.RoleRequester)).PAT("tickets:read")
		e.Clock.Advance(time.Minute)
		before := e.DB.WriteStats()
		paths := []string{"/api/v1/me", "/api/v1/me/sessions", "/api/v1/me/tokens"}
		for i := 0; i < 100; i++ {
			var c *authntest.Client
			path := paths[i%len(paths)]
			switch i % 3 {
			case 0:
				c = staff
			case 1:
				c = cust
			default:
				c, path = pat, "/api/v1/me"
			}
			if r, body := c.Do("GET", path, nil); r.StatusCode != 200 {
				t.Fatalf("GET %s: %d %s", path, r.StatusCode, body)
			}
		}
		if after := e.DB.WriteStats(); after != before {
			t.Fatalf("100 authenticated GETs wrote: txs %d, statements %d (want 0, 0)", after.Txs-before.Txs, after.Statements-before.Statements)
		}
	})
}

// TestLoginRateLimited (PLT-009): from one IP, attempts 1–10 within a minute
// are answered normally (401) and the 11th gets 429 with Retry-After.
func TestLoginRateLimited(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := authntest.New(t, db, authntest.Options{Throttle: true})
		c := e.Client()
		c.IP = "198.51.100.11"
		for i := 1; i <= 10; i++ {
			if r, _ := login(c, domain.AudienceStaff, "someone@example.test", "wrong-password-123"); r.StatusCode != 401 {
				t.Fatalf("attempt %d: %d, want 401", i, r.StatusCode)
			}
		}
		r, _ := login(c, domain.AudienceStaff, "someone@example.test", "wrong-password-123")
		if r.StatusCode != 429 {
			t.Fatalf("11th attempt: %d, want 429", r.StatusCode)
		}
		if ra := r.Header.Get("Retry-After"); ra == "" || ra == "0" {
			t.Fatalf("Retry-After = %q", ra)
		}
	})
}
