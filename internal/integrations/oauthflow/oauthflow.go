// SPDX-License-Identifier: AGPL-3.0-only

// Package oauthflow is the provider-independent half of the OAuth popup
// install (D-21 §2), shared by every integration that connects through one:
//
//  1. The admin page opens a blank popup, then calls the provider's start
//     endpoint, which creates a State here and returns the consent URL.
//  2. The provider redirects the popup to the integration's callback, which
//     Takes the state (single use; it authenticates the callback, never the
//     session cookie), exchanges the code, and calls Finish.
//  3. Finish 302s the popup to the SPA page /agent/oauth/complete, which
//     reports to the admin page over BroadcastChannel and closes itself.
//
// A new provider writes only its consent URL, code exchange and token
// storage; the SPA side is web/src/areas/agent/integrations/oauth.
package oauthflow

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// TTL bounds how long a consent screen may stay open.
const TTL = 10 * time.Minute

// maxPending caps pending flows per store (expired ones are dropped first).
const maxPending = 64

type pending struct {
	userID  string
	expires time.Time
}

// States holds pending OAuth states in memory (one app process, D-11). A
// restart drops them: the admin clicks Connect again.
type States struct {
	Now func() time.Time // injectable for tests

	mu sync.Mutex
	m  map[string]pending
}

// NewStates returns an empty store.
func NewStates() *States { return &States{Now: time.Now, m: map[string]pending{}} }

// Put creates a random single-use state bound to userID.
func (s *States) Put(userID string) (state string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	if len(s.m) >= maxPending {
		var oldest string
		for k, v := range s.m {
			if oldest == "" || v.expires.Before(s.m[oldest].expires) {
				oldest = k
			}
		}
		delete(s.m, oldest)
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	state = base64.RawURLEncoding.EncodeToString(b)
	expires = now.Add(TTL)
	s.m[state] = pending{userID: userID, expires: expires}
	return state, expires
}

// Take consumes state and returns its user; ok=false when unknown, already
// used or expired.
func (s *States) Take(state string) (userID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, found := s.m[state]
	delete(s.m, state)
	if !found || s.Now().After(v.expires) {
		return "", false
	}
	return v.userID, true
}

// reasonPattern keeps the reason forwarded to the SPA a plain code.
var reasonPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// Code returns s when it is a plain reason code, else "error".
func Code(s string) string {
	if reasonPattern.MatchString(s) {
		return s
	}
	return "error"
}

// CompletePath is the SPA page the popup ends on, under FD_AGENT_URL.
const CompletePath = "/oauth/complete"

// Finish redirects the popup to <agentURL>/oauth/complete with the outcome:
// reason "" = success, else a failure code (sanitised).
func Finish(w http.ResponseWriter, r *http.Request, agentURL, provider, state, reason string) {
	q := url.Values{"provider": {provider}, "result": {"ok"}}
	if len(state) <= 64 {
		q.Set("state", state)
	}
	if reason != "" {
		q.Set("result", "error")
		q.Set("reason", Code(reason))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer") // the callback URL carried the code
	http.Redirect(w, r, agentURL+CompletePath+"?"+q.Encode(), http.StatusFound)
}
