// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// writeJSON sends v; auth responses are never cached (AUTH-020).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func noContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

var accepted = map[string]bool{"ok": true}

// decodeJSON reads a JSON body; malformed input is a 422 (every auth
// operation documents 422, not every one documents 400).
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return problems.Validation(problems.Field("body", "required", "request body required"))
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return problems.PayloadTooLarge()
		}
		return problems.Validation(problems.Field("body", "invalid_json", "malformed JSON body"))
	}
	return nil
}

// validEmail accepts a bare address (no display name) up to 254 bytes.
func validEmail(s string) bool {
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " <>\r\n") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}

func principal(r *http.Request) *domain.Principal { return server.PrincipalFrom(r.Context()) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// RequireScope is the route-level I-6 guard for PAT callers: the token must
// carry one of scopes (session principals always pass). Other modules use it
// on their routes; it complements the spec-driven check in Authenticate.
func RequireScope(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := server.PrincipalFrom(r.Context())
			if p == nil {
				problems.Write(w, r, problems.Unauthenticated())
				return
			}
			if p.Scopes != nil {
				ok := false
				for _, s := range scopes {
					if p.HasScope(s) {
						ok = true
						break
					}
				}
				if !ok {
					problems.Write(w, r, problems.Forbidden("token scope does not allow this operation"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireSession refuses PAT callers (account management needs a browser session).
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := server.PrincipalFrom(r.Context())
		if p != nil && p.TokenID != "" {
			problems.Write(w, r, problems.Forbidden("not available with an API token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
