// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"container/list"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/radioBros/FeatherDesk/internal/problems"
)

// Limiter is an in-memory token-bucket store with LRU eviction (bounded
// memory, PLT-009). Keys are "<name>|<principal or ip>".
type Limiter struct {
	mu  sync.Mutex
	max int
	ll  *list.List
	m   map[string]*list.Element
	now func() time.Time
}

type bucket struct {
	key    string
	tokens float64
	last   time.Time
}

func NewLimiter(maxKeys int) *Limiter {
	return &Limiter{max: maxKeys, ll: list.New(), m: map[string]*list.Element{}, now: time.Now}
}

// Allow takes one token from key's bucket; it returns the wait until the next
// token when empty.
func (l *Limiter) Allow(key string, perSecond float64, burst int) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var b *bucket
	if el, ok := l.m[key]; ok {
		l.ll.MoveToFront(el)
		b = el.Value.(*bucket)
		b.tokens = math.Min(float64(burst), b.tokens+now.Sub(b.last).Seconds()*perSecond)
		b.last = now
	} else {
		b = &bucket{key: key, tokens: float64(burst), last: now}
		l.m[key] = l.ll.PushFront(b)
		for l.ll.Len() > l.max {
			old := l.ll.Back()
			l.ll.Remove(old)
			delete(l.m, old.Value.(*bucket).key)
		}
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / perSecond * float64(time.Second))
	return false, wait
}

// Len is the number of tracked keys (tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ll.Len()
}

// Middleware limits by principal when authenticated, else by client IP.
func (l *Limiter) Middleware(name string, perSecond float64, burst int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := name + "|ip:" + ClientIP(r)
			if p := PrincipalFrom(r.Context()); p != nil {
				key = name + "|u:" + p.UserID
			}
			if ok, wait := l.Allow(key, perSecond, burst); !ok {
				problems.Write(w, r, problems.RateLimited(wait))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
