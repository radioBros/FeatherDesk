// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"net/http"
	"strconv"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
)

// ---- agent ticket routes ----

func (m *Module) agentList(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	q, err := ParseAgentListQuery(r.URL.Query())
	if err != nil {
		return err
	}
	page, err := m.svc.ListStaff(r.Context(), principal(r), q)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

func (m *Module) agentCreate(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	// I-12: a token holding tickets:act_as_customer creates the ticket for
	// the requester, whose first message it posts on the customer's behalf.
	actAs := p.ActsAsCustomer()
	if !actAs {
		if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
			return err
		}
	}
	var in CreateInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	if actAs {
		if !p.HasScope(domain.ScopeTicketsWrite) && (in.TeamID != nil || in.AssigneeID != nil || len(in.Tags) > 0) {
			return problems.Forbidden("team_id, assignee_id and tags need tickets:write")
		}
		in.onBehalf = &onBehalf{OwnerID: p.UserID, TokenID: p.TokenID, IP: server.ClientIP(r)}
		id, err := m.svc.Create(r.Context(), p, in)
		if err != nil {
			return err
		}
		out, err := m.svc.onBehalfCreated(r.Context(), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, out)
		return nil
	}
	id, err := m.svc.Create(r.Context(), p, in)
	if err != nil {
		return err
	}
	d, err := m.svc.UpdateResult(r.Context(), p, id, nil)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, d.Ticket)
	return nil
}

func (m *Module) agentCounts(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	project := ""
	if v := r.URL.Query().Get("project"); v != "" {
		pid, err := m.svc.projectParam(r.Context(), v)
		if err != nil {
			return err
		}
		project = pid
	}
	c, err := m.svc.CountsFor(r.Context(), principal(r), project)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

func (m *Module) agentByNumber(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	n, err := strconv.ParseInt(urlParam(r, "n"), 10, 64)
	if err != nil || n < 1 {
		return problems.NotFound()
	}
	d, err := m.svc.StaffTicketByNumber(r.Context(), principal(r), n)
	if err != nil {
		return err
	}
	w.Header().Set("ETag", d.etag)
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) agentByRef(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	d, err := m.svc.StaffTicketByRef(r.Context(), principal(r), urlParam(r, "ref"))
	if err != nil {
		return err
	}
	w.Header().Set("ETag", d.etag)
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) agentGet(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	d, err := m.svc.StaffTicket(r.Context(), principal(r), urlParam(r, "id"))
	if err != nil {
		return err
	}
	w.Header().Set("ETag", d.etag)
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) agentPatch(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in PatchInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	out, err := m.svc.Patch(r.Context(), p, urlParam(r, "id"), in, r.Header.Get("If-Match"))
	if err != nil {
		return err
	}
	return m.writeUpdate(w, r, out)
}

func (m *Module) writeUpdate(w http.ResponseWriter, r *http.Request, out Outcome) error {
	res, err := m.svc.UpdateResult(r.Context(), principal(r), out.TicketID, out.Warnings)
	if err != nil {
		return err
	}
	if res.Visible {
		w.Header().Set("ETag", res.Ticket.etag)
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (m *Module) agentMessage(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in MessageInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	res, err := m.svc.AddStaffMessage(r.Context(), p, urlParam(r, "id"), in)
	if err != nil {
		return err
	}
	msg, err := m.svc.StaffMessage(r.Context(), p, res.TicketID, res.MessageID)
	if err != nil {
		// The reply may have moved the ticket out of the caller's sight
		// (set_status); the message itself was created.
		if problems.From(err).Slug == problems.SlugNotFound {
			writeJSON(w, http.StatusCreated, map[string]any{"id": res.MessageID, "ticket_id": res.TicketID, "kind": in.Kind,
				"body_md": in.BodyMD, "attachments": []any{}, "created_at": m.svc.clock()})
			return nil
		}
		return err
	}
	writeJSON(w, http.StatusCreated, msg)
	return nil
}

func (m *Module) agentEditMessage(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in struct {
		BodyMD string `json:"body_md"`
	}
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	id, mid := urlParam(r, "id"), urlParam(r, "mid")
	if err := m.svc.EditNote(r.Context(), p, id, mid, in.BodyMD); err != nil {
		return err
	}
	msg, err := m.svc.StaffMessage(r.Context(), p, id, mid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, msg)
	return nil
}

func (m *Module) agentEscalate(dir int) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
			return err
		}
		var in EscalateInput
		if err := decodeJSON(r, &in); err != nil {
			return err
		}
		out, err := m.svc.Escalate(r.Context(), principal(r), urlParam(r, "id"), dir, in)
		if err != nil {
			return err
		}
		return m.writeUpdate(w, r, out)
	}
}

func (m *Module) agentEvents(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	page, err := m.svc.Timeline(r.Context(), principal(r), urlParam(r, "id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

func (m *Module) agentListLinks(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	p := principal(r)
	id := urlParam(r, "id")
	if !validID(id) {
		return problems.NotFound()
	}
	q := m.svc.db.Read()
	if _, err := m.svc.loadVisible(r.Context(), q, p, "t.id = ?", id); err != nil {
		return err
	}
	links, err := m.svc.listLinks(r.Context(), q, p, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, links)
	return nil
}

func (m *Module) agentCreateLink(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in LinkInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	p := principal(r)
	id := urlParam(r, "id")
	linkID, err := m.svc.CreateLink(r.Context(), p, id, in)
	if err != nil {
		return err
	}
	links, err := m.svc.listLinks(r.Context(), m.svc.db.Read(), p, id)
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.ID == linkID {
			writeJSON(w, http.StatusCreated, l)
			return nil
		}
	}
	return problems.NotFound()
}

func (m *Module) agentDeleteLink(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	if err := m.svc.DeleteLink(r.Context(), principal(r), urlParam(r, "id"), urlParam(r, "link_id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- portal ticket routes ----

func (m *Module) portalList(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	q, err := ParsePortalListQuery(r.URL.Query())
	if err != nil {
		return err
	}
	page, err := m.svc.ListCustomer(r.Context(), principal(r), q)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, page)
	return nil
}

func (m *Module) portalCreate(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in CreateInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	// Staff-only inputs are not part of PortalTicketCreate.
	in.TeamID, in.AssigneeID, in.RequesterEmail, in.RequesterName, in.Tags = nil, nil, "", "", nil
	p := principal(r)
	id, err := m.svc.Create(r.Context(), p, in)
	if err != nil {
		return err
	}
	d, err := m.svc.CustomerTicket(r.Context(), p, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, d)
	return nil
}

func (m *Module) portalGet(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	d, err := m.svc.CustomerTicket(r.Context(), principal(r), urlParam(r, "id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) portalByNumber(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	n, err := strconv.ParseInt(urlParam(r, "n"), 10, 64)
	if err != nil || n < 1 {
		return problems.NotFound()
	}
	d, err := m.svc.CustomerTicketByNumber(r.Context(), principal(r), n)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) portalByRef(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	d, err := m.svc.CustomerTicketByRef(r.Context(), principal(r), urlParam(r, "ref"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (m *Module) portalReply(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	var in MessageInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	in.Kind, in.SetStatus = "", ""
	p := principal(r)
	res, err := m.svc.AddCustomerReply(r.Context(), p, urlParam(r, "id"), in)
	if err != nil {
		return err
	}
	if res.FollowupID != "" {
		// Lifecycle §2: the closed ticket stays untouched; the reply opened a
		// follow-up whose id is returned in `detail` (OpenAPI createPortalReply).
		return &problems.Error{Slug: problems.SlugConflict, Detail: res.FollowupID,
			Extra: map[string]any{"followup_ticket_id": res.FollowupID}}
	}
	msg, err := m.svc.CustomerMessage(r.Context(), p, res.MessageID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, msg)
	return nil
}

func (m *Module) portalStatus(fn func(s *Service, r *http.Request) error) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
			return err
		}
		if err := fn(m.svc, r); err != nil {
			return err
		}
		d, err := m.svc.CustomerTicket(r.Context(), principal(r), urlParam(r, "id"))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, d)
		return nil
	}
}
