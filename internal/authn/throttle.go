// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/server"
)

// throttle implements AUTH-012 in memory (single node, TD-AUTH-01):
//   - a per-IP token bucket per action (10/min, burst 10);
//   - a per-account soft lock: 10 failures within 15 min lock the account for
//     15 min against IPs that never logged in to it successfully.
type throttle struct {
	limiter  *server.Limiter
	perMin   float64
	burst    int
	disabled bool

	mu       sync.Mutex
	accounts map[string]*accountState
}

type accountState struct {
	failures []time.Time
	locked   time.Time // lock expiry
	knownIPs map[string]struct{}
}

const (
	lockFailures  = 10
	lockWindow    = 15 * time.Minute
	lockDuration  = 15 * time.Minute
	maxAccounts   = 50_000
	maxKnownIPs   = 16
	defaultPerMin = 10
	defaultBurst  = 10
)

func newThrottle() *throttle {
	return &throttle{limiter: server.NewLimiter(50_000), perMin: defaultPerMin, burst: defaultBurst, accounts: map[string]*accountState{}}
}

// allowIP takes a token from the (action, ip) bucket.
func (t *throttle) allowIP(action, ip string) (bool, time.Duration) {
	if t.disabled {
		return true, 0
	}
	return t.limiter.Allow(action+"|"+ip, t.perMin/60, t.burst)
}

// locked reports whether email is soft-locked for this ip.
func (t *throttle) locked(email, ip string, now time.Time) bool {
	if t.disabled {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.accounts[email]
	if a == nil || !now.Before(a.locked) {
		return false
	}
	_, known := a.knownIPs[ip]
	return !known
}

func (t *throttle) state(email string) *accountState {
	a := t.accounts[email]
	if a == nil {
		if len(t.accounts) >= maxAccounts {
			t.accounts = map[string]*accountState{}
		}
		a = &accountState{knownIPs: map[string]struct{}{}}
		t.accounts[email] = a
	}
	return a
}

// fail records a failed login for email.
func (t *throttle) fail(email string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.state(email)
	cut := now.Add(-lockWindow)
	kept := a.failures[:0]
	for _, f := range a.failures {
		if f.After(cut) {
			kept = append(kept, f)
		}
	}
	a.failures = append(kept, now)
	if len(a.failures) >= lockFailures {
		a.locked = now.Add(lockDuration)
		a.failures = a.failures[:0]
	}
}

// success clears failures and remembers ip as known for email.
func (t *throttle) success(email, ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.state(email)
	a.failures = a.failures[:0]
	if len(a.knownIPs) >= maxKnownIPs {
		a.knownIPs = map[string]struct{}{}
	}
	a.knownIPs[ip] = struct{}{}
}
