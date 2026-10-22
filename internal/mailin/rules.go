// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"net/mail"
	"strings"
	"sync"
	"time"
)

// ---- blocklist (settings.email.inbound_blocklist) ----

// blocked reports whether addr matches an entry: a full address, "@domain"
// or "domain" (subdomains of a domain entry match too).
func blocked(list []string, addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	_, domain, _ := strings.Cut(addr, "@")
	for _, e := range list {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if strings.Contains(e, "@") && !strings.HasPrefix(e, "@") {
			if e == addr {
				return true
			}
			continue
		}
		d := strings.TrimPrefix(e, "@")
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

// ---- per-sender rate limit (NOT-029) ----

// senderLimiter keeps a sliding one-hour window of arrival times per sender.
// Memory is bounded: at most maxKeys senders, oldest evicted.
type senderLimiter struct {
	mu      sync.Mutex
	maxKeys int
	m       map[string][]time.Time
}

func newSenderLimiter(maxKeys int) *senderLimiter {
	return &senderLimiter{maxKeys: maxKeys, m: map[string][]time.Time{}}
}

// Allow records an arrival for key and reports whether it is within limit
// per hour.
func (l *senderLimiter) Allow(key string, limit int, now time.Time) bool {
	if limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-time.Hour)
	ts := l.m[key]
	i := 0
	for i < len(ts) && !ts[i].After(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) >= limit {
		l.m[key] = ts
		return false
	}
	if _, ok := l.m[key]; !ok && len(l.m) >= l.maxKeys {
		l.evict(cut)
	}
	l.m[key] = append(ts, now)
	return true
}

func (l *senderLimiter) evict(cut time.Time) {
	for k, ts := range l.m {
		if len(ts) == 0 || !ts[len(ts)-1].After(cut) {
			delete(l.m, k)
		}
	}
	for k := range l.m { // still full: drop an arbitrary entry
		if len(l.m) < l.maxKeys {
			break
		}
		delete(l.m, k)
	}
}

// ---- Authentication-Results (RFC 8601), staff replies (NOT-028) ----

// authPass reports whether any Authentication-Results header added by a
// trusted authserv-id shows dkim=pass (header.d aligned with fromDomain) or
// spf=pass (smtp.mailfrom aligned with fromDomain). Alignment is relaxed:
// equal domains or one a subdomain of the other.
func authPass(h mail.Header, trusted []string, fromDomain string) bool {
	if len(trusted) == 0 || fromDomain == "" {
		return false
	}
	fromDomain = strings.ToLower(fromDomain)
	for _, v := range h["Authentication-Results"] {
		v = unfold(v)
		id, rest, ok := strings.Cut(v, ";")
		if !ok {
			continue
		}
		id = strings.ToLower(strings.TrimSpace(id))
		if f := strings.Fields(id); len(f) > 0 {
			id = f[0] // drop an optional version number
		}
		if !contains(trusted, id) {
			continue
		}
		for _, res := range strings.Split(rest, ";") {
			method, result, props := parseResinfo(res)
			if result != "pass" {
				continue
			}
			switch method {
			case "dkim":
				if aligned(props["header.d"], fromDomain) || aligned(domainOf(props["header.i"]), fromDomain) {
					return true
				}
			case "spf":
				if aligned(domainOf(props["smtp.mailfrom"]), fromDomain) {
					return true
				}
			}
		}
	}
	return false
}

func unfold(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// parseResinfo parses "dkim=pass (comment) header.d=example.com header.s=x".
func parseResinfo(s string) (method, result string, props map[string]string) {
	props = map[string]string{}
	s = stripComments(s)
	for i, f := range strings.Fields(s) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(k), strings.Trim(strings.ToLower(v), `"`)
		if i == 0 {
			method, result = k, v
			continue
		}
		props[k] = v
	}
	return
}

func stripComments(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func domainOf(v string) string {
	if _, d, ok := strings.Cut(v, "@"); ok {
		return d
	}
	return v
}

func aligned(d, from string) bool {
	d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
	if d == "" {
		return false
	}
	return d == from || strings.HasSuffix(from, "."+d) || strings.HasSuffix(d, "."+from)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
