// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
)

// WSTicketTTL is the lifetime of a one-time WebSocket ticket (AUTH-032).
const WSTicketTTL = 30 * time.Second

// wsTickets keeps one-time tickets in memory: sha256 → origin credential.
// Bounded; expired entries are dropped lazily on insert (no timer).
type wsTickets struct {
	mu  sync.Mutex
	max int
	m   map[string]wsTicket
}

type wsTicket struct {
	userID, sessionID, tokenID string
	exp                        time.Time
}

func newWSTickets(max int) *wsTickets { return &wsTickets{max: max, m: map[string]wsTicket{}} }

func (w *wsTickets) put(hash string, t wsTicket, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.m) >= w.max {
		for k, v := range w.m {
			if !now.Before(v.exp) {
				delete(w.m, k)
			}
		}
		if len(w.m) >= w.max {
			return false
		}
	}
	w.m[hash] = t
	return true
}

// take removes and returns the ticket (single use).
func (w *wsTickets) take(hash string) (wsTicket, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.m[hash]
	if ok {
		delete(w.m, hash)
	}
	return t, ok
}

func (w *wsTickets) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.m)
}

// handleWSTicket: POST /ws/ticket (authenticated, CSRF-checked for cookies).
func (m *Module) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	tok := randomToken(32)
	now := m.now()
	if !m.wst.put(hashToken(tok), wsTicket{userID: p.UserID, sessionID: p.SessionID, tokenID: p.TokenID, exp: now.Add(WSTicketTTL)}, now) {
		problems.Write(w, r, problems.RateLimited(5*time.Second))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": tok, "expires_in": int(WSTicketTTL / time.Second)})
}

// consumeWSTicket resolves a ticket to a fresh principal from its origin
// credential (so a revoked session or token cannot open a socket).
func (m *Module) consumeWSTicket(ctx context.Context, tok string) (*domain.Principal, error) {
	if len(tok) > 128 {
		return nil, errInvalidCred
	}
	t, ok := m.wst.take(hashToken(tok))
	if !ok || !m.now().Before(t.exp) {
		return nil, errInvalidCred
	}
	var p *domain.Principal
	var err error
	switch {
	case t.tokenID != "":
		p, err = m.patPrincipal(ctx, "t.id = ?", t.tokenID, false)
	case t.sessionID != "":
		p, err = m.sessionPrincipal(ctx, t.sessionID, false)
	default:
		return nil, errInvalidCred
	}
	if err != nil {
		return nil, err
	}
	if p.UserID != t.userID {
		return nil, errInvalidCred
	}
	return p, nil
}

// expiringSet is a bounded set with per-entry expiry (back-channel logout
// `jti` replay guard: 10 k entries, 10 min).
type expiringSet struct {
	mu  sync.Mutex
	max int
	m   map[string]time.Time
}

func newExpiringSet(max int) *expiringSet { return &expiringSet{max: max, m: map[string]time.Time{}} }

// add returns false when key is already present and unexpired.
func (s *expiringSet) add(key string, ttl time.Duration, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exp, ok := s.m[key]; ok && now.Before(exp) {
		return false
	}
	if len(s.m) >= s.max {
		var oldestK string
		var oldest time.Time
		for k, v := range s.m {
			if !now.Before(v) {
				delete(s.m, k)
				continue
			}
			if oldestK == "" || v.Before(oldest) {
				oldestK, oldest = k, v
			}
		}
		if len(s.m) >= s.max && oldestK != "" {
			delete(s.m, oldestK)
		}
	}
	s.m[key] = now.Add(ttl)
	return true
}
