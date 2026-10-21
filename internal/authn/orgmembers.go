// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// orgManager returns the caller's organization when they are an org_manager.
func orgManager(r *http.Request) (string, error) {
	p := principal(r)
	if !p.IsCustomer() || p.Role != domain.RoleOrgManager || p.OrganizationID == "" {
		return "", problems.Forbidden("organization managers only")
	}
	return p.OrganizationID, nil
}

// GET /portal/organization/members: UserRef list of the caller's org.
func (m *Module) handleListOrgMembers(w http.ResponseWriter, r *http.Request) {
	org, err := orgManager(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	rows, err := m.d.DB.Read().QueryContext(r.Context(), `SELECT id, name FROM users WHERE kind = 'customer' AND organization_id = ? AND status <> 'disabled' ORDER BY name, id`, org)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			problems.Write(w, r, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "kind": "customer"})
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type orgInvite struct {
	Email string      `json:"email"`
	Name  string      `json:"name"`
	Role  domain.Role `json:"role"`
}

// POST /portal/organization/members: invite a colleague into the caller's
// organization (always kind=customer; AUTH-027).
func (m *Module) handleInviteOrgMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org, err := orgManager(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var in orgInvite
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Email, in.Name = normEmail(in.Email), strings.TrimSpace(in.Name)
	if in.Role == "" {
		in.Role = domain.RoleRequester
	}
	var fields []problems.FieldError
	if !validEmail(in.Email) {
		fields = append(fields, problems.Field("email", "email", "invalid email"))
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 120 {
		fields = append(fields, problems.Field("name", "length", "1 to 120 characters"))
	}
	if !validCustomerRole(in.Role) {
		fields = append(fields, problems.Field("role", "invalid", ""))
	}
	if len(fields) > 0 {
		problems.Write(w, r, problems.Validation(fields...))
		return
	}
	if !m.requireEmail(w, r) {
		return
	}
	p := principal(r)
	id := store.NewID()
	var tok string
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := m.nowDB()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, organization_id, status, locale, created_at, updated_at)
			VALUES (?, 'customer', ?, ?, ?, ?, 'pending_verification', ?, ?, ?)`, id, in.Email, in.Name, string(in.Role), org, p.Locale, now, now); err != nil {
			if store.IsUniqueViolation(err) {
				return problems.Conflict("a user with this email already exists")
			}
			return err
		}
		var err error
		if tok, err = IssueAuthToken(ctx, tx, id, PurposeInvite, map[string]any{"invited_by": p.UserID}); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.invite_sent", TargetType: "user", TargetID: id, IP: server.ClientIP(r),
			Data: map[string]any{"organization_id": org, "role": in.Role}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.sendMail(ctx, MailPayload{Template: TplInvite, UserID: id, To: in.Email, Locale: p.Locale,
		Vars: map[string]string{"url": LinkURL(m.d.Cfg, domain.KindCustomer, "/invite", tok), "name": in.Name}})
	out, err := UserJSON(ctx, m.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type orgMemberPatch struct {
	Role   *domain.Role `json:"role"`
	Status *string      `json:"status"`
}

// PATCH /portal/organization/members/{id}: role or status of a colleague in
// the same organization (other orgs → 404).
func (m *Module) handlePatchOrgMember(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org, err := orgManager(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var in orgMemberPatch
	if err := decodeJSON(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if in.Role == nil && in.Status == nil {
		problems.Write(w, r, problems.Validation(problems.Field("body", "required", "role or status")))
		return
	}
	if in.Role != nil && !validCustomerRole(*in.Role) {
		problems.Write(w, r, problems.Validation(problems.Field("role", "invalid", "")))
		return
	}
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		problems.Write(w, r, problems.Validation(problems.Field("status", "invalid", "")))
		return
	}
	p := principal(r)
	id := chi.URLParam(r, "id")
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		u, err := loadUser(ctx, tx, "u.id = ?", id)
		if err != nil {
			return err
		}
		if u.Kind != domain.KindCustomer || u.OrgID.String != org {
			return problems.NotFound()
		}
		if u.ID == p.UserID {
			return problems.Forbidden("you cannot change your own membership")
		}
		ip := server.ClientIP(r)
		if in.Role != nil && *in.Role != u.Role {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, string(*in.Role), m.nowDB(), id); err != nil {
				return err
			}
			if err := Audit(ctx, tx, AuditEntry{ActorID: p.UserID, Action: "auth.customer_role_changed", TargetType: "user", TargetID: id, IP: ip,
				Data: map[string]any{"from": u.Role, "to": *in.Role}}); err != nil {
				return err
			}
		}
		if in.Status != nil {
			switch {
			case *in.Status == "disabled" && u.Status != domain.UserDisabled:
				return DisableUser(ctx, tx, id, p.UserID, "disabled by organization manager", ip)
			case *in.Status == "active" && u.Status == domain.UserDisabled:
				return EnableUser(ctx, tx, id, p.UserID, ip)
			}
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		problems.Write(w, r, problems.NotFound())
		return
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := UserJSON(ctx, m.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
