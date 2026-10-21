// SPDX-License-Identifier: AGPL-3.0-only

// Package directory holds the user directory. This file is the admin staff
// API (AUTH-018): GET/POST /admin/staff and GET/PATCH /admin/staff/{id}.
// Customers, organizations and teams are served by other files of the
// package (tickets module).
package directory

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// StaffAdminDeps is what the staff admin module needs from app.Deps.
type StaffAdminDeps struct {
	Cfg      *config.Config
	DB       *store.DB
	Log      *slog.Logger
	Queue    *queue.Queue
	Settings *settings.Service
}

// StaffAdmin is the app module serving /admin/staff.
type StaffAdmin struct {
	d StaffAdminDeps
	// mail is swapped by tests; default enqueues "mail.auth".
	mail func(ctx context.Context, p authn.MailPayload)
}

// NewStaffAdmin builds the module.
func NewStaffAdmin(d StaffAdminDeps) (*StaffAdmin, error) {
	if d.DB == nil || d.Cfg == nil {
		return nil, errors.New("directory: DB and Cfg are required")
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &StaffAdmin{d: d}
	s.mail = func(ctx context.Context, p authn.MailPayload) { authn.EnqueueAuthMail(ctx, d.Queue, d.Log, p) }
	return s, nil
}

func (s *StaffAdmin) Name() string { return "directory.staff" }

func (s *StaffAdmin) Mount(g server.Groups) {
	r := g.Admin.With(authn.RequireScope(domain.ScopeAdmin))
	r.Get("/staff", s.list)
	r.Post("/staff", s.create)
	r.Get("/staff/{id}", s.get)
	r.Patch("/staff/{id}", s.patch)
}

func staffWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = staffJSONEncode(w, v)
}

func staffDenied(r *http.Request, res authz.AdminResource, op authz.AdminOp) error {
	if authz.CanAdmin(server.PrincipalFrom(r.Context()), res, op) != nil {
		return problems.Forbidden("")
	}
	return nil
}

// ---- list ----

func (s *StaffAdmin) list(w http.ResponseWriter, r *http.Request) {
	if err := staffDenied(r, authz.ResStaffUsers, authz.OpRead); err != nil {
		problems.Write(w, r, err)
		return
	}
	q := r.URL.Query()
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = min(v, 200)
	}
	where, args := `kind = 'staff'`, []any{}
	if role := q.Get("role"); role != "" {
		if !domain.Role(role).ValidFor(domain.KindStaff) {
			problems.Write(w, r, problems.Validation(problems.Field("role", "invalid", "")))
			return
		}
		where += ` AND role = ?`
		args = append(args, role)
	}
	if t := q.Get("team_id"); t != "" {
		where += ` AND id IN (SELECT user_id FROM team_members WHERE team_id = ?)`
		args = append(args, t)
	}
	if text := strings.ToLower(strings.TrimSpace(q.Get("q"))); text != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
		where += ` AND (lower(name) LIKE ? ESCAPE '\' OR lower(email) LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	if c := q.Get("cursor"); c != "" {
		b, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problems.Write(w, r, problems.Validation(problems.Field("cursor", "invalid", "")))
			return
		}
		where += ` AND id > ?`
		args = append(args, string(b))
	}
	rows, err := s.d.DB.Read().QueryContext(r.Context(), `SELECT id FROM users WHERE `+where+` ORDER BY id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			problems.Write(w, r, err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	var next any
	if len(ids) > limit {
		ids = ids[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(ids[limit-1]))
	}
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		u, err := authn.UserJSON(r.Context(), s.d.DB.Read(), id)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		items = append(items, u)
	}
	staffWriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ---- get ----

func (s *StaffAdmin) get(w http.ResponseWriter, r *http.Request) {
	if err := staffDenied(r, authz.ResStaffUsers, authz.OpRead); err != nil {
		problems.Write(w, r, err)
		return
	}
	u, err := s.loadStaff(r.Context(), s.d.DB.Read(), chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	staffWriteJSON(w, http.StatusOK, u)
}

type staffRow struct {
	ID, Email, Name, Locale string
	Role                    domain.Role
	Tier                    int
	Status                  domain.UserStatus
	IDPManaged, BreakGlass  bool
}

func (s *StaffAdmin) staffRow(ctx context.Context, q store.Querier, id string) (staffRow, error) {
	var u staffRow
	err := q.QueryRowContext(ctx, `SELECT id, email, name, locale, role, tier, status, idp_managed, break_glass FROM users WHERE id = ? AND kind = 'staff'`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Locale, &u.Role, &u.Tier, &u.Status, &u.IDPManaged, &u.BreakGlass)
	if err != nil {
		return staffRow{}, store.NotFound(err)
	}
	return u, nil
}

func (s *StaffAdmin) loadStaff(ctx context.Context, q store.Querier, id string) (map[string]any, error) {
	if _, err := s.staffRow(ctx, q, id); err != nil {
		return nil, err
	}
	return authn.UserJSON(ctx, q, id)
}

// ---- create ----

type staffCreate struct {
	Email      string      `json:"email"`
	Name       string      `json:"name"`
	Role       domain.Role `json:"role"`
	Tier       *int        `json:"tier"`
	TeamIDs    []string    `json:"team_ids"`
	Password   *string     `json:"password"`
	BreakGlass bool        `json:"break_glass"`
	SendInvite bool        `json:"send_invite"`
}

func (s *StaffAdmin) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := staffDenied(r, authz.ResStaffUsers, authz.OpWrite); err != nil {
		problems.Write(w, r, err)
		return
	}
	var in staffCreate
	if err := staffDecode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Email, in.Name = strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.Name)
	var f []problems.FieldError
	if !staffValidEmail(in.Email) {
		f = append(f, problems.Field("email", "email", "invalid email"))
	}
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 120 {
		f = append(f, problems.Field("name", "length", "1 to 120 characters"))
	}
	if !in.Role.ValidFor(domain.KindStaff) {
		f = append(f, problems.Field("role", "invalid", "admin, supervisor or agent"))
	}
	tier := domain.MinTier
	if in.Role == domain.RoleAdmin || in.Role == domain.RoleSupervisor {
		tier = domain.MaxTier
	}
	if in.Tier != nil {
		tier = *in.Tier
	}
	if tier < domain.MinTier || tier > domain.MaxTier {
		f = append(f, problems.Field("tier", "range", "1 to 3"))
	}
	if in.BreakGlass && in.Role != domain.RoleAdmin {
		f = append(f, problems.Field("break_glass", "admin_only", "only admins can be break-glass"))
	}
	if in.Password != nil {
		if c := authn.PasswordProblem(*in.Password); c != "" {
			f = append(f, problems.Field("password", c, ""))
		}
	}
	if len(f) > 0 {
		problems.Write(w, r, problems.Validation(f...))
		return
	}
	if in.SendInvite && in.Password == nil {
		if ok, err := s.emailEnabled(ctx); err != nil || !ok {
			problems.Write(w, r, problems.New(problems.SlugEmailDisabled, "email is not configured on this instance"))
			return
		}
	}
	var hash any
	if in.Password != nil {
		h, err := authn.HashPassword(ctx, *in.Password)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		hash = h
	}
	p := server.PrincipalFrom(ctx)
	id := store.NewID()
	status := domain.UserActive
	invite := in.SendInvite && in.Password == nil
	if invite {
		status = domain.UserPendingVerification
	}
	var tok string
	err := s.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := store.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, tier, status, password_hash, break_glass, created_at, updated_at)
			VALUES (?, 'staff', ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, in.Email, in.Name, string(in.Role), tier, string(status), hash, in.BreakGlass, now, now); err != nil {
			if store.IsUniqueViolation(err) {
				return problems.Conflict("a user with this email already exists")
			}
			return err
		}
		if _, err := staffSetLocalTeams(ctx, tx, id, in.TeamIDs); err != nil {
			return err
		}
		if invite {
			var err error
			if tok, err = authn.IssueAuthToken(ctx, tx, id, authn.PurposeInvite, map[string]any{"invited_by": p.UserID}); err != nil {
				return err
			}
		}
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: p.UserID, Action: "auth.staff_changed", TargetType: "user", TargetID: id, IP: server.ClientIP(r),
			Data: map[string]any{"op": "create", "role": in.Role, "tier": tier, "invite": invite}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if invite {
		s.mail(ctx, authn.MailPayload{Template: authn.TplInvite, UserID: id, To: in.Email, Locale: p.Locale,
			Vars: map[string]string{"url": authn.LinkURL(s.d.Cfg, domain.KindStaff, "/invite", tok), "name": in.Name}})
	}
	out, err := s.loadStaff(ctx, s.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	staffWriteJSON(w, http.StatusCreated, out)
}

func (s *StaffAdmin) emailEnabled(ctx context.Context) (bool, error) {
	return notify.EnabledWith(ctx, s.d.Settings), nil
}

// setLocalTeams makes the local memberships of userID equal teamIDs (nil =
// unchanged); IdP-sourced memberships are left alone. It returns the teams
// the user left, for the M-4 re-validation (TKT-017).
func staffSetLocalTeams(ctx context.Context, tx *store.Tx, userID string, teamIDs []string) (removed []string, err error) {
	if teamIDs == nil {
		return nil, nil
	}
	want := map[string]bool{}
	for _, t := range teamIDs {
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM teams WHERE id = ?`, t).Scan(&id); err != nil {
			if errors.Is(store.NotFound(err), store.ErrNotFound) {
				return nil, problems.Validation(problems.Field("team_ids", "unknown_team", t))
			}
			return nil, err
		}
		want[id] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT team_id, source FROM team_members WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	for rows.Next() {
		var t, src string
		if err := rows.Scan(&t, &src); err != nil {
			rows.Close()
			return nil, err
		}
		have[t] = src
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for t := range want {
		if _, ok := have[t]; !ok {
			if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, user_id, source, created_at) VALUES (?, ?, 'local', ?)`, t, userID, store.Now()); err != nil {
				return nil, err
			}
		}
	}
	for t, src := range have {
		if src == "local" && !want[t] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = ? AND user_id = ? AND source = 'local'`, t, userID); err != nil {
				return nil, err
			}
			removed = append(removed, t)
		}
	}
	tx.AfterCommit(func() { authn.InvalidateTeams(userID) })
	sort.Strings(removed)
	return removed, nil
}

// ---- patch ----

type staffPatch struct {
	Name       *string            `json:"name"`
	Role       *domain.Role       `json:"role"`
	Tier       *int               `json:"tier"`
	Status     *domain.UserStatus `json:"status"`
	TeamIDs    *[]string          `json:"team_ids"`
	BreakGlass *bool              `json:"break_glass"`
}

func (s *StaffAdmin) patch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := staffDenied(r, authz.ResStaffUsers, authz.OpWrite); err != nil {
		problems.Write(w, r, err)
		return
	}
	var in staffPatch
	if err := staffDecode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	var f []problems.FieldError
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if l := utf8.RuneCountInString(n); l < 1 || l > 120 {
			f = append(f, problems.Field("name", "length", "1 to 120 characters"))
		}
	}
	if in.Role != nil && !in.Role.ValidFor(domain.KindStaff) {
		f = append(f, problems.Field("role", "invalid", "admin, supervisor or agent"))
	}
	if in.Tier != nil && (*in.Tier < domain.MinTier || *in.Tier > domain.MaxTier) {
		f = append(f, problems.Field("tier", "range", "1 to 3"))
	}
	if in.Status != nil && *in.Status != domain.UserActive && *in.Status != domain.UserDisabled {
		f = append(f, problems.Field("status", "invalid", "active or disabled"))
	}
	if len(f) > 0 {
		problems.Write(w, r, problems.Validation(f...))
		return
	}
	p := server.PrincipalFrom(ctx)
	id := chi.URLParam(r, "id")
	ip := server.ClientIP(r)
	err := s.d.DB.Tx(ctx, func(tx *store.Tx) error {
		if err := staffLockAdmins(ctx, tx); err != nil {
			return err
		}
		u, err := s.staffRow(ctx, tx, id)
		if err != nil {
			return err
		}
		roleChange := in.Role != nil && *in.Role != u.Role
		tierChange := in.Tier != nil && *in.Tier != u.Tier
		if (roleChange || tierChange) && authz.CanEditStaffRoleTier(p, u.IDPManaged) != nil {
			return problems.Forbidden("role and tier are managed by the identity provider")
		}
		newRole := u.Role
		if in.Role != nil {
			newRole = *in.Role
		}
		disabling := in.Status != nil && *in.Status == domain.UserDisabled && u.Status != domain.UserDisabled
		losesAdmin := u.Role == domain.RoleAdmin && u.Status == domain.UserActive && (newRole != domain.RoleAdmin || disabling)
		if losesAdmin {
			var others int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kind = 'staff' AND role = 'admin' AND status = 'active' AND id <> ?`, id).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return problems.Conflict("last-admin: the last active admin cannot be demoted or disabled")
			}
		}
		bg := u.BreakGlass
		if in.BreakGlass != nil {
			bg = *in.BreakGlass
		}
		if newRole != domain.RoleAdmin {
			if in.BreakGlass != nil && *in.BreakGlass {
				return problems.Validation(problems.Field("break_glass", "admin_only", "only admins can be break-glass"))
			}
			bg = false
		}
		changes := map[string]any{}
		now := store.Now()
		if in.Name != nil && *in.Name != u.Name {
			changes["name"] = true
			if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, updated_at = ? WHERE id = ?`, *in.Name, now, id); err != nil {
				return err
			}
		}
		if roleChange || tierChange || bg != u.BreakGlass {
			tier := u.Tier
			if in.Tier != nil {
				tier = *in.Tier
			}
			changes["role"] = map[string]any{"from": u.Role, "to": newRole}
			changes["tier"] = map[string]any{"from": u.Tier, "to": tier}
			changes["break_glass"] = bg
			if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, tier = ?, break_glass = ?, updated_at = ? WHERE id = ?`, string(newRole), tier, bg, now, id); err != nil {
				return err
			}
		}
		var leftTeams []string
		if in.TeamIDs != nil {
			changes["team_ids"] = *in.TeamIDs
			if leftTeams, err = staffSetLocalTeams(ctx, tx, id, *in.TeamIDs); err != nil {
				return err
			}
		}
		if in.Status != nil && *in.Status != u.Status {
			changes["status"] = *in.Status
			if disabling {
				if err := authn.DisableUser(ctx, tx, id, p.UserID, "disabled by admin", ip); err != nil {
					return err
				}
			} else if u.Status == domain.UserDisabled {
				if err := authn.EnableUser(ctx, tx, id, p.UserID, ip); err != nil {
					return err
				}
			}
		}
		// M-4 re-validation (TKT-017): tickets assigned to this user that no
		// longer satisfy team + tier + active are unassigned with an event.
		// A role, tier or status change affects every team (""); leaving a
		// team affects that team only.
		if roleChange || tierChange || disabling {
			if err := MembershipChanged(ctx, tx, p.UserID, "", []string{id}); err != nil {
				return err
			}
		} else {
			for _, t := range leftTeams {
				if err := MembershipChanged(ctx, tx, p.UserID, t, []string{id}); err != nil {
					return err
				}
			}
		}
		if len(changes) == 0 {
			return nil
		}
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: p.UserID, Action: "auth.staff_changed", TargetType: "user", TargetID: id, IP: ip,
			Data: map[string]any{"op": "update", "changes": changes}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	authn.InvalidateTeams(id)
	out, err := s.loadStaff(ctx, s.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	staffWriteJSON(w, http.StatusOK, out)
}

// SetMailer replaces the invite mail sender (tests).
func (s *StaffAdmin) SetMailer(fn func(ctx context.Context, p authn.MailPayload)) { s.mail = fn }
