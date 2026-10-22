// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
)

// writeJSON sends v with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON object body into dst (422 on malformed input).
func decodeJSON(r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return problems.UnsupportedMedia(ct)
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return problems.PayloadTooLarge()
		}
		if errors.Is(err, io.EOF) {
			return problems.Validation(problems.Field("body", "required", "a JSON body is required"))
		}
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			return problems.Validation(problems.Field(ute.Field, "type", "wrong type"))
		}
		return problems.BadRequest("malformed JSON")
	}
	return nil
}

// principal returns the caller (the route groups guarantee one).
func principal(r *http.Request) *domain.Principal { return server.PrincipalFrom(r.Context()) }

// handler adapts an error-returning handler.
type handler func(w http.ResponseWriter, r *http.Request) error

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		problems.Write(w, r, err)
	}
}

// requireScope rejects PATs lacking scope (I-6: effective = role ∩ scopes).
func requireScope(r *http.Request, scope string) error {
	p := principal(r)
	if p == nil {
		return problems.Unauthenticated()
	}
	if !p.HasScope(scope) {
		return problems.Forbidden("token lacks " + scope)
	}
	return nil
}

func readScope(r *http.Request) error {
	p := principal(r)
	if p == nil {
		return problems.Unauthenticated()
	}
	if !p.HasScope(domain.ScopeTicketsRead) && !p.HasScope(domain.ScopeTicketsWrite) {
		return problems.Forbidden("token lacks tickets:read")
	}
	return nil
}

func urlParam(r *http.Request, k string) string { return chi.URLParam(r, k) }
