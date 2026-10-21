// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// Notification centre API (NOT-038): GET /me/notifications[?unread=&type=&cursor=&limit=],
// GET /me/notifications/unread-count, POST /me/notifications/{id}/read,
// POST /me/notifications/read-all, DELETE /me/notifications/{id}. Every
// route acts on the caller's own rows only: another user's id is 404.

func (m *Module) mountInApp(g server.Groups) {
	g.Authed.Get("/me/notifications", m.listMine)
	g.Authed.Get("/me/notifications/unread-count", m.unreadMine)
	g.Authed.Post("/me/notifications/read-all", m.readAllMine)
	g.Authed.Post("/me/notifications/{id}/read", m.readMine)
	g.Authed.Delete("/me/notifications/{id}", m.deleteMine)
}

// cursorRe: the cursor is the last notification id of the previous page.
var cursorRe = regexp.MustCompile(`^[0-9a-fA-F-]{1,64}$`)

func (m *Module) listMine(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	v := r.URL.Query()
	lq := listQuery{userID: p.UserID, limit: 50}
	var fe []problems.FieldError
	switch v.Get("unread") {
	case "", "false":
	case "true":
		lq.unread = true
	default:
		fe = append(fe, problems.Field("unread", "boolean", "true or false"))
	}
	if t := v.Get("type"); t != "" {
		if !slices.Contains(InAppTypes, t) {
			fe = append(fe, problems.Field("type", "enum", "unknown notification type"))
		}
		lq.typ = t
	}
	if c := v.Get("cursor"); c != "" {
		if !cursorRe.MatchString(c) {
			fe = append(fe, problems.Field("cursor", "invalid", "malformed cursor"))
		}
		lq.cursor = c
	}
	if l := v.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			fe = append(fe, problems.Field("limit", "range", "1..200"))
		}
		lq.limit = n
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	items, next, err := m.listNotifications(r.Context(), lq)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	unread, err := m.unreadCount(r.Context(), m.db.Read(), p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var nc any
	if next != "" {
		nc = next
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nc, "unread_count": unread})
}

func (m *Module) unreadMine(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	n, err := m.unreadCount(r.Context(), m.db.Read(), p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": n})
}

func (m *Module) readMine(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	id := chi.URLParam(r, "id")
	changed, unread, err := m.markRead(r.Context(), p.UserID, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if changed > 0 {
		m.sendRead(p.UserID, []string{id}, false, unread)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": unread})
}

func (m *Module) readAllMine(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	changed, unread, err := m.markRead(r.Context(), p.UserID, "")
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if changed > 0 {
		m.sendRead(p.UserID, nil, true, unread)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unread_count": unread})
}

func (m *Module) deleteMine(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	id := chi.URLParam(r, "id")
	unread, err := m.deleteOne(r.Context(), p.UserID, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.sendRead(p.UserID, []string{id}, false, unread)
	w.WriteHeader(http.StatusNoContent)
}
