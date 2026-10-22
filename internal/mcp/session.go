// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// session is a legacy (2025-03-26 … 2025-11-25) Streamable HTTP session,
// created by `initialize`. It is bound to the bearer token that created it,
// so a leaked session id is useless with another token. The 2026-07-28
// revision has no sessions.
type session struct {
	id          string
	version     string
	tokenKey    string // sha256 of the bearer token
	client      string // clientInfo name/version, for logs
	initialized bool
	lastSeen    time.Time
}

// sessions is the bounded in-memory session table (single node, like the
// rest of the platform's in-process state). Idle sessions expire; when full,
// the least recently used one is dropped.
type sessions struct {
	mu  sync.Mutex
	m   map[string]*session
	max int
	ttl time.Duration
	now func() time.Time
}

func newSessions(limit int, ttl time.Duration, now func() time.Time) *sessions {
	return &sessions{m: map[string]*session{}, max: limit, ttl: ttl, now: now}
}

func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *sessions) create(version, tokenKey, client string) *session {
	now := s.now()
	ss := &session{id: newSessionID(), version: version, tokenKey: tokenKey, client: client, lastSeen: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= s.max {
		var oldest *session
		for id, x := range s.m {
			if now.Sub(x.lastSeen) >= s.ttl {
				delete(s.m, id)
				continue
			}
			if oldest == nil || x.lastSeen.Before(oldest.lastSeen) {
				oldest = x
			}
		}
		if len(s.m) >= s.max && oldest != nil {
			delete(s.m, oldest.id)
		}
	}
	s.m[ss.id] = ss
	return ss
}

// get returns a live session created with the same token and refreshes it.
// It returns a copy, so callers never race on the table's entry.
func (s *sessions) get(id, tokenKey string) (session, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.m[id]
	if !ok || ss.tokenKey != tokenKey {
		return session{}, false
	}
	if now.Sub(ss.lastSeen) >= s.ttl {
		delete(s.m, id)
		return session{}, false
	}
	ss.lastSeen = now
	return *ss, true
}

func (s *sessions) markInitialized(id string) {
	s.mu.Lock()
	if ss, ok := s.m[id]; ok {
		ss.initialized = true
	}
	s.mu.Unlock()
}

// delete ends a session (HTTP DELETE); only its own token may end it.
func (s *sessions) delete(id, tokenKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.m[id]
	if !ok || ss.tokenKey != tokenKey {
		return false
	}
	delete(s.m, id)
	return true
}

func (s *sessions) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
