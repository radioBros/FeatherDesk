// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"net/url"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/domain"
)

// AreaHome is where a principal lands after login.
func AreaHome(kind domain.UserKind) string {
	if kind == domain.KindStaff {
		return "/agent"
	}
	return "/"
}

// SafeReturnTo accepts only a same-origin relative path inside the caller's
// area (AUTH-019): it must start with a single "/", carry no scheme, host,
// backslash or control character (also after one round of percent-decoding),
// and a customer is never sent into /agent. Anything else → the area home.
func SafeReturnTo(raw string, kind domain.UserKind) string {
	home := AreaHome(kind)
	if raw == "" || len(raw) > 2048 || !safePath(raw) {
		return home
	}
	dec, err := url.PathUnescape(raw)
	if err != nil || !safePath(dec) {
		return home
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return home
	}
	p := strings.ToLower(u.Path)
	if strings.HasPrefix(p, "/api/") || p == "/api" || strings.HasPrefix(p, "/setup") {
		return home
	}
	isAgent := p == "/agent" || strings.HasPrefix(p, "/agent/")
	if kind == domain.KindStaff && !isAgent {
		return home
	}
	if kind != domain.KindStaff && isAgent {
		return home
	}
	return raw
}

func safePath(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.ContainsRune(s, '\\') {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// "/javascript:…" is a path, but refuse any scheme-looking first segment.
	first, _, _ := strings.Cut(s[1:], "/")
	return !strings.Contains(first, ":")
}
