// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Customer directory (TKT-015) and organizations (TKT-016).

// UserDTO is the User schema (customers).
type UserDTO struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	Tier           *int       `json:"tier"`
	OrganizationID *string    `json:"organization_id"`
	Status         string     `json:"status"`
	Locale         string     `json:"locale"`
	IDPManaged     bool       `json:"idp_managed"`
	BreakGlass     bool       `json:"break_glass"`
	HasPassword    bool       `json:"has_password"`
	TeamIDs        []string   `json:"team_ids"`
	LastLoginAt    *time.Time `json:"last_login_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const customerCols = `id, kind, email, name, role, organization_id, status, locale, idp_managed, break_glass, password_hash IS NOT NULL, last_login_at, created_at, updated_at`

func scanCustomer(r interface{ Scan(...any) error }) (UserDTO, error) {
	var u UserDTO
	var org sql.NullString
	var last sql.NullTime
	err := r.Scan(&u.ID, &u.Kind, &u.Email, &u.Name, &u.Role, &org, &u.Status, &u.Locale, &u.IDPManaged, &u.BreakGlass, &u.HasPassword, &last, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return u, err
	}
	u.OrganizationID = dirStrPtr(org)
	if last.Valid {
		t := last.Time.UTC()
		u.LastLoginAt = &t
	}
	u.CreatedAt, u.UpdatedAt, u.TeamIDs = u.CreatedAt.UTC(), u.UpdatedAt.UTC(), []string{}
	return u, nil
}

func (d *Directory) loadCustomerDTO(ctx context.Context, q store.Querier, id string) (UserDTO, error) {
	if !dirValidID(id) {
		return UserDTO{}, problems.NotFound()
	}
	u, err := scanCustomer(q.QueryRowContext(ctx, `SELECT `+customerCols+` FROM users WHERE id = ? AND kind = 'customer'`, id))
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return u, problems.NotFound()
		}
		return u, err
	}
	return u, nil
}

func (d *Directory) listCustomers(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomers, authz.OpRead); err != nil {
		return err
	}
	limit, err := dirLimit(r)
	if err != nil {
		return err
	}
	order, err := dirSort(r, "id", map[string]string{"name": "name", "email": "email"})
	if err != nil {
		return err
	}
	where, args, err := order.after(r, `kind = 'customer'`, nil)
	if err != nil {
		return err
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like := prefixLike(q)
		where += ` AND (lower(name) LIKE ? ESCAPE '\' OR email LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	if org := r.URL.Query().Get("organization_id"); org != "" {
		where += ` AND organization_id = ?`
		args = append(args, org)
	}
	rows, err := d.db.Read().QueryContext(r.Context(), `SELECT `+customerCols+` FROM users WHERE `+where+` ORDER BY `+order.orderBy()+` LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	page := dirPage[UserDTO]{Items: []UserDTO{}}
	for rows.Next() {
		u, err := scanCustomer(rows)
		if err != nil {
			return err
		}
		page.Items = append(page.Items, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = order.next(last.ID, map[string]string{"name": last.Name, "email": last.Email})
	}
	dirJSON(w, http.StatusOK, page)
	return nil
}

func (d *Directory) getCustomer(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomers, authz.OpRead); err != nil {
		return err
	}
	u, err := d.loadCustomerDTO(r.Context(), d.db.Read(), urlID(r))
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, u)
	return nil
}

type customerIn struct {
	Email          string  `json:"email"`
	Name           string  `json:"name"`
	OrganizationID *string `json:"organization_id"`
	Role           string  `json:"role"`
	Locale         string  `json:"locale"`
	SendInvite     bool    `json:"send_invite"`
}

// createCustomer creates an active customer without a password. `kind` is
// never an input; an address already used by anyone (staff included) is 409.
func (d *Directory) createCustomer(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomerInvite, authz.OpWrite); err != nil {
		return err
	}
	var in customerIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	var errs []problems.FieldError
	email, ok := ParseCustomerEmail(in.Email)
	if !ok {
		errs = append(errs, problems.Field("email", "email", "a valid e-mail address"))
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		errs = append(errs, problems.Field("name", "length", "1..120 characters"))
	}
	if in.Role == "" {
		in.Role = string(domain.RoleRequester)
	}
	if !domain.Role(in.Role).ValidFor(domain.KindCustomer) {
		errs = append(errs, problems.Field("role", "enum", "requester or org_manager"))
	}
	if in.Locale == "" {
		in.Locale = d.Support(r.Context()).DefaultLocale
	}
	if !domain.ValidLocale(in.Locale) {
		errs = append(errs, problems.Field("locale", "enum", "unknown locale"))
	}
	ctx := r.Context()
	org := ""
	if in.OrganizationID != nil {
		org = *in.OrganizationID
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	id, now := store.NewID(), store.Now()
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		if org == "" && in.OrganizationID == nil {
			var err error
			if org, err = OrgForDomain(ctx, tx, EmailDomainOf(email)); err != nil {
				return err
			}
		} else if org != "" {
			if err := orgActive(ctx, tx, org); err != nil {
				return err
			}
		}
		if in.Role == string(domain.RoleOrgManager) && org == "" {
			return problems.Validation(problems.Field("role", "org_required", "an org_manager needs an organization"))
		}
		var kind string
		err := tx.QueryRowContext(ctx, `SELECT kind FROM users WHERE email = ?`, email).Scan(&kind)
		if err == nil {
			if kind == string(domain.KindStaff) {
				return problems.Conflict("the e-mail address belongs to a staff account")
			}
			return problems.Conflict("a customer with this e-mail address exists")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, organization_id, status, locale, created_at, updated_at)
			VALUES (?, 'customer', ?, ?, ?, ?, 'active', ?, ?, ?)`, id, email, in.Name, in.Role, store.Nullable(org), in.Locale, now, now)
		if store.IsUniqueViolation(err) {
			return problems.Conflict("the e-mail address is already registered")
		}
		return err
	})
	if err != nil {
		return err
	}
	u, err := d.loadCustomerDTO(ctx, d.db.Read(), id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, u)
	return nil
}

func orgActive(ctx context.Context, q store.Querier, id string) error {
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM organizations WHERE id = ?`, id).Scan(&status)
	if err != nil || status != "active" {
		return problems.Validation(problems.Field("organization_id", "not_found", "unknown or archived organization"))
	}
	return nil
}

type customerPatch struct {
	Name           *string `json:"name"`
	OrganizationID optID   `json:"organization_id"`
	Role           *string `json:"role"`
	Locale         *string `json:"locale"`
	Status         *string `json:"status"`
}

// optID distinguishes an absent field from null.
type optID struct {
	Set bool
	Val *string
}

func (o *optID) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Val = &s
	return nil
}

// patchCustomer updates a customer; status=disabled revokes every session and
// token through authn.DisableUser (I-5, AUTH-017).
func (d *Directory) patchCustomer(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomers, authz.OpWrite); err != nil {
		return err
	}
	var in customerPatch
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	p := dirPrincipal(r)
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		cur, err := d.loadCustomerDTO(ctx, tx, id)
		if err != nil {
			return err
		}
		var errs []problems.FieldError
		name, role, locale, org := cur.Name, cur.Role, cur.Locale, ptrStr(cur.OrganizationID)
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
			if name == "" || utf8.RuneCountInString(name) > 120 {
				errs = append(errs, problems.Field("name", "length", "1..120 characters"))
			}
		}
		if in.Role != nil {
			role = *in.Role
			if !domain.Role(role).ValidFor(domain.KindCustomer) {
				errs = append(errs, problems.Field("role", "enum", "requester or org_manager"))
			}
		}
		if in.Locale != nil {
			locale = *in.Locale
			if !domain.ValidLocale(locale) {
				errs = append(errs, problems.Field("locale", "enum", "unknown locale"))
			}
		}
		if in.OrganizationID.Set {
			org = ptrStr(in.OrganizationID.Val)
			if org != "" {
				if err := orgActive(ctx, tx, org); err != nil {
					return err
				}
			}
		}
		if role == string(domain.RoleOrgManager) && org == "" {
			errs = append(errs, problems.Field("role", "org_required", "an org_manager needs an organization"))
		}
		if in.Status != nil && !slices.Contains([]string{"active", "disabled"}, *in.Status) {
			errs = append(errs, problems.Field("status", "enum", "active or disabled"))
		}
		if len(errs) > 0 {
			return problems.Validation(errs...)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, role = ?, locale = ?, organization_id = ?, updated_at = ? WHERE id = ?`,
			name, role, locale, store.Nullable(org), store.Now(), id); err != nil {
			return err
		}
		if in.Status != nil && *in.Status != cur.Status {
			if *in.Status == "disabled" {
				return d.disable(ctx, tx, id, p.UserID, "disabled from the customer directory", server.ClientIP(r))
			}
			return authn.EnableUser(ctx, tx, id, p.UserID, server.ClientIP(r))
		}
		return nil
	})
	if err != nil {
		return err
	}
	u, err := d.loadCustomerDTO(ctx, d.db.Read(), id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, u)
	return nil
}

func (d *Directory) disable(ctx context.Context, tx *store.Tx, userID, actorID, reason, ip string) error {
	d.hookMu.RLock()
	fn := d.disabler
	d.hookMu.RUnlock()
	if fn == nil {
		fn = authn.DisableUser
	}
	return fn(ctx, tx, userID, actorID, reason, ip)
}

// ---- organizations ----

// OrganizationDTO is the Organization schema.
type OrganizationDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	EmailDomains []string `json:"email_domains"`
	Status       string   `json:"status"`
	Notes        string   `json:"notes"`
	MemberCount  int      `json:"member_count"`
	// ProjectIDs limits the projects the organization may open tickets in
	// (D-20); empty = every portal-visible project.
	ProjectIDs []string  `json:"project_ids"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// fillOrgProjects loads organization_projects for the organizations.
func fillOrgProjects(ctx context.Context, q store.Querier, orgs []OrganizationDTO) error {
	if len(orgs) == 0 {
		return nil
	}
	idx := map[string]int{}
	args := make([]any, len(orgs))
	for i := range orgs {
		orgs[i].ProjectIDs = []string{}
		idx[orgs[i].ID] = i
		args[i] = orgs[i].ID
	}
	rows, err := q.QueryContext(ctx, `SELECT organization_id, project_id FROM organization_projects WHERE organization_id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) ORDER BY project_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var o, p string
		if err := rows.Scan(&o, &p); err != nil {
			return err
		}
		orgs[idx[o]].ProjectIDs = append(orgs[idx[o]].ProjectIDs, p)
	}
	return rows.Err()
}

const orgCols = `o.id, o.name, o.email_domains, o.status, o.notes, (SELECT COUNT(*) FROM users u WHERE u.organization_id = o.id), o.created_at, o.updated_at`

func scanOrg(r interface{ Scan(...any) error }) (OrganizationDTO, error) {
	var o OrganizationDTO
	var doms store.JSON[[]string]
	err := r.Scan(&o.ID, &o.Name, &doms, &o.Status, &o.Notes, &o.MemberCount, &o.CreatedAt, &o.UpdatedAt)
	o.EmailDomains = doms.V
	if o.EmailDomains == nil {
		o.EmailDomains = []string{}
	}
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return o, err
}

func (d *Directory) loadOrg(ctx context.Context, q store.Querier, id string) (OrganizationDTO, error) {
	if !dirValidID(id) {
		return OrganizationDTO{}, problems.NotFound()
	}
	o, err := scanOrg(q.QueryRowContext(ctx, `SELECT `+orgCols+` FROM organizations o WHERE o.id = ?`, id))
	if err != nil && errors.Is(store.NotFound(err), store.ErrNotFound) {
		return o, problems.NotFound()
	}
	if err != nil {
		return o, err
	}
	one := []OrganizationDTO{o}
	err = fillOrgProjects(ctx, q, one)
	return one[0], err
}

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// normalizeDomains lower-cases, validates and de-duplicates e-mail domains;
// public webmail domains are refused for auto-join (TKT-016).
func normalizeDomains(in []string) ([]string, []problems.FieldError) {
	out := []string{}
	var errs []problems.FieldError
	for i, d := range in {
		d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@")))
		switch {
		case !domainRe.MatchString(d):
			errs = append(errs, problems.Field("email_domains["+itoa(i)+"]", "domain", "not a domain name"))
		case IsWebmailDomain(d):
			errs = append(errs, problems.Field("email_domains["+itoa(i)+"]", "webmail", "public webmail domains cannot be used for auto-join"))
		case !slices.Contains(out, d):
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out, errs
}

// domainsTaken reports a domain already claimed by another active organization.
func domainsTaken(ctx context.Context, q store.Querier, self string, doms []string) (string, error) {
	if len(doms) == 0 {
		return "", nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id, email_domains FROM organizations WHERE status = 'active' AND id <> ?`, self)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ds store.JSON[[]string]
		if err := rows.Scan(&id, &ds); err != nil {
			return "", err
		}
		for _, x := range ds.V {
			if slices.Contains(doms, strings.ToLower(x)) {
				return x, nil
			}
		}
	}
	return "", rows.Err()
}

func (d *Directory) listOrgs(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResOrganizations, authz.OpRead); err != nil {
		return err
	}
	limit, err := dirLimit(r)
	if err != nil {
		return err
	}
	order, err := dirSort(r, "o.id", map[string]string{"name": "o.name"})
	if err != nil {
		return err
	}
	where, args, err := order.after(r, "1=1", nil)
	if err != nil {
		return err
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where += ` AND lower(o.name) LIKE ? ESCAPE '\'`
		args = append(args, prefixLike(q))
	}
	rows, err := d.db.Read().QueryContext(r.Context(), `SELECT `+orgCols+` FROM organizations o WHERE `+where+` ORDER BY `+order.orderBy()+` LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	page := dirPage[OrganizationDTO]{Items: []OrganizationDTO{}}
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return err
		}
		page.Items = append(page.Items, o)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = order.next(last.ID, map[string]string{"name": last.Name})
	}
	if err := fillOrgProjects(r.Context(), d.db.Read(), page.Items); err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, page)
	return nil
}

func (d *Directory) getOrg(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResOrganizations, authz.OpRead); err != nil {
		return err
	}
	o, err := d.loadOrg(r.Context(), d.db.Read(), urlID(r))
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, o)
	return nil
}

type orgIn struct {
	Name         *string   `json:"name"`
	EmailDomains *[]string `json:"email_domains"`
	Notes        *string   `json:"notes"`
	Status       *string   `json:"status"`
	ProjectIDs   *[]string `json:"project_ids"` // D-20: ids or keys; [] = every portal-visible project
}

func (d *Directory) createOrg(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResOrganizations, authz.OpWrite); err != nil {
		return err
	}
	var in orgIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	id := store.NewID()
	if err := d.writeOrg(r, id, in, true); err != nil {
		return err
	}
	o, err := d.loadOrg(r.Context(), d.db.Read(), id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, o)
	return nil
}

func (d *Directory) patchOrg(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResOrganizations, authz.OpWrite); err != nil {
		return err
	}
	var in orgIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	id := urlID(r)
	if err := d.writeOrg(r, id, in, false); err != nil {
		return err
	}
	o, err := d.loadOrg(r.Context(), d.db.Read(), id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, o)
	return nil
}

func (d *Directory) writeOrg(r *http.Request, id string, in orgIn, create bool) error {
	ctx := r.Context()
	return d.db.Tx(ctx, func(tx *store.Tx) error {
		cur := OrganizationDTO{ID: id, Status: "active", EmailDomains: []string{}}
		if !create {
			var err error
			if cur, err = d.loadOrg(ctx, tx, id); err != nil {
				return err
			}
		}
		var errs []problems.FieldError
		if in.Name != nil {
			cur.Name = strings.TrimSpace(*in.Name)
		}
		if cur.Name == "" || utf8.RuneCountInString(cur.Name) > 200 {
			errs = append(errs, problems.Field("name", "length", "1..200 characters"))
		}
		if in.EmailDomains != nil {
			var derrs []problems.FieldError
			cur.EmailDomains, derrs = normalizeDomains(*in.EmailDomains)
			errs = append(errs, derrs...)
		}
		if in.Notes != nil {
			cur.Notes = *in.Notes
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "archived" {
				errs = append(errs, problems.Field("status", "enum", "active or archived"))
			}
			cur.Status = *in.Status
		}
		var projects []string
		if in.ProjectIDs != nil {
			projects = []string{}
			for i, ref := range *in.ProjectIDs {
				pr, err := d.ResolveProject(ctx, ref)
				if err != nil {
					errs = append(errs, problems.Field("project_ids["+itoa(i)+"]", "not_found", "unknown project"))
					continue
				}
				if !slices.Contains(projects, pr.ID) {
					projects = append(projects, pr.ID)
				}
			}
		}
		if len(errs) > 0 {
			return problems.Validation(errs...)
		}
		if cur.Status == "active" {
			taken, err := domainsTaken(ctx, tx, id, cur.EmailDomains)
			if err != nil {
				return err
			}
			if taken != "" {
				if !create { // updateOrganization documents no 409
					return problems.Validation(problems.Field("email_domains", "unique", "the domain "+taken+" belongs to another organization"))
				}
				return problems.Conflict("the domain " + taken + " belongs to another organization")
			}
		}
		now := store.Now()
		var err error
		if create {
			_, err = tx.ExecContext(ctx, `INSERT INTO organizations (id, name, email_domains, status, notes, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id, cur.Name, store.MustJSON(cur.EmailDomains), cur.Status, cur.Notes, now, now)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE organizations SET name = ?, email_domains = ?, status = ?, notes = ?, updated_at = ? WHERE id = ?`,
				cur.Name, store.MustJSON(cur.EmailDomains), cur.Status, cur.Notes, now, id)
		}
		if store.IsUniqueViolation(err) {
			if create {
				return problems.Conflict("an organization with this name exists")
			}
			return problems.Validation(problems.Field("name", "unique", "an organization with this name exists"))
		}
		if err != nil || projects == nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM organization_projects WHERE organization_id = ?`, id); err != nil {
			return err
		}
		for _, p := range projects {
			if _, err := tx.ExecContext(ctx, `INSERT INTO organization_projects (organization_id, project_id, created_at) VALUES (?, ?, ?)`, id, p, now); err != nil {
				return err
			}
		}
		return nil
	})
}
