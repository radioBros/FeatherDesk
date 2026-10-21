// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// dirHandler adapts an error-returning handler.
type dirHandler func(w http.ResponseWriter, r *http.Request) error

func (h dirHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		problems.Write(w, r, err)
	}
}

func dirJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func dirDecode(r *http.Request, dst any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		switch {
		case errors.As(err, &mbe):
			return problems.PayloadTooLarge()
		case errors.Is(err, io.EOF):
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

func dirPrincipal(r *http.Request) *domain.Principal { return server.PrincipalFrom(r.Context()) }

// dirCan maps the administration matrix (authz.CanAdmin) to 403.
func dirCan(r *http.Request, res authz.AdminResource, op authz.AdminOp) error {
	if authz.CanAdmin(dirPrincipal(r), res, op) != nil {
		return problems.Forbidden("")
	}
	return nil
}

func dirLimit(r *http.Request) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 200 {
		return 0, problems.Validation(problems.Field("limit", "range", "1..200"))
	}
	return n, nil
}

func dirCursor(r *http.Request) (string, error) {
	c := r.URL.Query().Get("cursor")
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", problems.Validation(problems.Field("cursor", "invalid", "invalid cursor"))
	}
	return string(b), nil
}

func dirNextCursor(lastID string) *string {
	c := base64.RawURLEncoding.EncodeToString([]byte(lastID))
	return &c
}

type dirPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// prefixLike builds a LIKE pattern for a prefix search (escaped).
func prefixLike(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.ToLower(strings.TrimSpace(q))) + "%"
}

func dirValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// Mount registers the directory routes (customers, organizations, teams,
// categories, calendars, custom-field definitions, support settings and the
// picker lookups). Staff administration (/admin/staff) is mounted by the
// auth module; GET /portal/organization/members belongs to authn as well.
func (d *Directory) Mount(g server.Groups) {
	g.Agent.Route("/customers", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listCustomers))
		r.Method(http.MethodPost, "/", dirHandler(d.createCustomer))
		r.Method(http.MethodGet, "/{id}", dirHandler(d.getCustomer))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchCustomer))
	})
	g.Agent.Route("/organizations", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listOrgs))
		r.Method(http.MethodPost, "/", dirHandler(d.createOrg))
		r.Method(http.MethodGet, "/{id}", dirHandler(d.getOrg))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchOrg))
	})
	g.Agent.Method(http.MethodGet, "/teams", dirHandler(d.lookupTeams))
	g.Agent.Method(http.MethodGet, "/categories", dirHandler(d.lookupCategories))
	g.Agent.Method(http.MethodGet, "/staff", dirHandler(d.lookupStaff))
	g.Agent.Method(http.MethodGet, "/custom-fields", dirHandler(d.lookupFieldsAgent))
	g.Portal.Method(http.MethodGet, "/categories", dirHandler(d.portalCategories))
	g.Portal.Method(http.MethodGet, "/custom-fields", dirHandler(d.lookupFieldsPortal))

	g.Support.Route("/teams", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listTeams))
		r.Method(http.MethodPost, "/", dirHandler(d.createTeam))
		r.Method(http.MethodGet, "/{id}", dirHandler(d.getTeam))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchTeam))
		r.Method(http.MethodDelete, "/{id}", dirHandler(d.deleteTeam))
		r.Method(http.MethodGet, "/{id}/members", dirHandler(d.listMembers))
		r.Method(http.MethodPost, "/{id}/members", dirHandler(d.addMember))
		r.Method(http.MethodDelete, "/{id}/members/{user_id}", dirHandler(d.removeMember))
	})
	g.Support.Route("/categories", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listCategories))
		r.Method(http.MethodPost, "/", dirHandler(d.createCategory))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchCategory))
	})
	g.Support.Route("/calendars", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listCalendars))
		r.Method(http.MethodPost, "/", dirHandler(d.createCalendar))
		r.Method(http.MethodGet, "/{id}", dirHandler(d.getCalendar))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchCalendar))
		r.Method(http.MethodDelete, "/{id}", dirHandler(d.deleteCalendar))
	})
	g.Admin.Route("/custom-fields", func(r chi.Router) {
		r.Method(http.MethodGet, "/", dirHandler(d.listFieldDefs))
		r.Method(http.MethodPost, "/", dirHandler(d.createFieldDef))
		r.Method(http.MethodGet, "/{id}", dirHandler(d.getFieldDef))
		r.Method(http.MethodPatch, "/{id}", dirHandler(d.patchFieldDef))
	})
	g.Admin.Method(http.MethodGet, "/settings", dirHandler(d.getSettings))
	g.Admin.Method(http.MethodPatch, "/settings", dirHandler(d.patchSettings))
}

func urlID(r *http.Request) string { return chi.URLParam(r, "id") }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
