// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// CookieName is the session cookie (AUTH-001).
const CookieName = "fd_session"

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken is the only form in which session, PAT and email tokens are stored.
func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// csrfFor derives the per-session CSRF token: an HMAC of the session hash
// under FD_SECRET_KEY, so validation needs no DB read.
func (m *Module) csrfFor(idHash string) string {
	return base64.RawURLEncoding.EncodeToString(m.d.Secrets.MAC("csrf-token", []byte(idHash)))
}

// ValidCSRF implements server.CSRFValidator.
func (m *Module) ValidCSRF(p *domain.Principal, token string) bool {
	if p == nil || p.SessionID == "" || token == "" {
		return false
	}
	return hmac.Equal([]byte(m.csrfFor(p.SessionID)), []byte(token))
}

// publicSessionID is the id exposed by GET /me/sessions (a prefix of the
// stored hash, never the cookie value).
func publicSessionID(idHash string) string { return idHash[:32] }

func (m *Module) idle(a domain.Audience) time.Duration {
	if a == domain.AudienceStaff {
		return m.opt.IdleStaff
	}
	return m.opt.IdleCustomer
}

func (m *Module) absolute(a domain.Audience) time.Duration {
	if a == domain.AudienceStaff {
		return m.opt.AbsoluteStaff
	}
	return m.opt.AbsoluteCustomer
}

type newSession struct {
	Token  string
	IDHash string
	CSRF   string
}

type sessionExtra struct {
	OIDCIDToken string
	OIDCSID     string
	MFA         string // second factor used (audit only; D-19 §B)
}

// createSession inserts a session row inside tx and returns the raw token
// (only its hash is stored).
func (m *Module) createSession(ctx context.Context, tx *store.Tx, r *http.Request, userID string, aud domain.Audience, extra sessionExtra) (newSession, error) {
	tok := randomToken(32)
	s := newSession{Token: tok, IDHash: hashToken(tok)}
	s.CSRF = m.csrfFor(s.IDHash)
	now := m.nowDB()
	ip, ua := "", ""
	if r != nil {
		ip, ua = server.ClientIP(r), truncate(r.UserAgent(), 256)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, audience, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent, oidc_id_token, oidc_sid)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.IDHash, userID, string(aud), s.CSRF, now, now, now.Add(m.absolute(aud)), ip, ua, store.Nullable(extra.OIDCIDToken), store.Nullable(extra.OIDCSID))
	return s, err
}

func (m *Module) nowDB() time.Time { return m.now().UTC().Truncate(time.Microsecond) }

// revokePresentedSession deletes the session whose cookie came with r
// (session fixation: a login always starts from a fresh token).
func (m *Module) revokePresentedSession(ctx context.Context, tx *store.Tx, r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	h := hashToken(c.Value)
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h); err != nil {
		return err
	}
	tx.AfterCommit(func() { closeSessionSockets(h, "session replaced") })
	return nil
}

// RevokeUserSessions deletes every session of userID except keepIDHash and
// closes their sockets after commit (password change/reset, role loss).
func RevokeUserSessions(ctx context.Context, tx *store.Tx, userID, keepIDHash string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id_hash FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		if h != keepIDHash {
			ids = append(ids, h)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h); err != nil {
			return err
		}
	}
	tx.AfterCommit(func() {
		for _, h := range ids {
			closeSessionSockets(h, "session revoked")
		}
	})
	return nil
}

// cookie builds the fd_session cookie: HttpOnly, SameSite=Lax, Path=/,
// Secure whenever the instance or the request is HTTPS (unless the operator
// opted out with FD_INSECURE_COOKIES on an http FD_PUBLIC_URL).
func (m *Module) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: m.secureCookies(r), MaxAge: maxAge,
	}
}

func (m *Module) secureCookies(r *http.Request) bool {
	if strings.HasPrefix(m.d.Cfg.PublicURL, "https://") {
		return true
	}
	if m.opt.InsecureCookies {
		return false
	}
	return r != nil && server.IsHTTPS(r)
}

func (m *Module) setSessionCookie(w http.ResponseWriter, r *http.Request, s newSession, aud domain.Audience) {
	http.SetCookie(w, m.cookie(r, s.Token, int(m.absolute(aud)/time.Second)))
}

func (m *Module) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, m.cookie(r, "", -1))
}

// ---- coalesced last_seen_at (never a write per request, AUTH-001) ----

type sessionTouches struct {
	mu sync.Mutex
	m  map[string]time.Time
	// Flushes counts flush transactions (tests).
	flushes int
}

const maxTouches = 100_000

func newSessionTouches() *sessionTouches { return &sessionTouches{m: map[string]time.Time{}} }

func (s *sessionTouches) touch(idHash string, t time.Time) {
	s.mu.Lock()
	if old, ok := s.m[idHash]; !ok || t.After(old) {
		if ok || len(s.m) < maxTouches {
			s.m[idHash] = t
		}
	}
	s.mu.Unlock()
}

func (s *sessionTouches) get(idHash string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[idHash]
	return t, ok
}

// FlushSessionTouches writes buffered last_seen_at values in one transaction.
func (m *Module) FlushSessionTouches(ctx context.Context) error {
	s := m.touches
	s.mu.Lock()
	if len(s.m) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.m
	s.m = map[string]time.Time{}
	s.mu.Unlock()
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		for h, t := range batch {
			if _, err := tx.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ? AND last_seen_at < ?`, t, h, t); err != nil {
				return err
			}
		}
		return nil
	})
	s.mu.Lock()
	if err != nil {
		for h, t := range batch {
			if old, ok := s.m[h]; !ok || t.After(old) {
				s.m[h] = t
			}
		}
	} else {
		s.flushes++
	}
	s.mu.Unlock()
	return err
}

// ---- WebSocket closing across hubs (I-5, AUTH-032) ----

var hubs sync.Map // *realtime.Hub → struct{}

func registerHub(h *realtime.Hub) {
	if h != nil {
		hubs.Store(h, struct{}{})
	}
}

func closeUserSockets(userID, reason string) {
	hubs.Range(func(k, _ any) bool {
		k.(*realtime.Hub).CloseUser(userID, realtime.CloseUnauthenticated, reason)
		return true
	})
}

func closeSessionSockets(idHash, reason string) {
	hubs.Range(func(k, _ any) bool {
		k.(*realtime.Hub).CloseSession(idHash, realtime.CloseUnauthenticated, reason)
		return true
	})
}
