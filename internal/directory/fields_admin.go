// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Custom field definitions admin (TKT-039), admin only.

var fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type fieldIn struct {
	Key              string            `json:"key"`
	Label            map[string]string `json:"label"`
	Type             domain.FieldType  `json:"type"`
	Options          *[]FieldOption    `json:"options"`
	Required         *bool             `json:"required"`
	CustomerVisible  *bool             `json:"customer_visible"`
	CustomerEditable *bool             `json:"customer_editable"`
	CategoryIDs      optIDs            `json:"category_ids"`
	ProjectIDs       optIDs            `json:"project_ids"` // D-20: null = all projects
	Sort             *int              `json:"sort"`
	Active           *bool             `json:"active"`
}

// optIDs distinguishes an absent list from null (null = all categories).
type optIDs struct {
	Set bool
	Val []string
}

func (o *optIDs) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Val = nil
		return nil
	}
	var v []string
	if err := jsonUnmarshal(b, &v); err != nil {
		return err
	}
	if v == nil {
		v = []string{}
	}
	o.Val = v
	return nil
}

func isSelect(t domain.FieldType) bool {
	return t == domain.FieldSelect || t == domain.FieldMultiselect
}

// validateField checks a complete definition; label needs at least one locale.
func (d *Directory) validateField(ctx context.Context, f *FieldDef) []problems.FieldError {
	var errs []problems.FieldError
	if !fieldKeyRe.MatchString(f.Key) {
		errs = append(errs, problems.Field("key", "pattern", "lower-case slug: a-z, 0-9, _"))
	}
	if len(f.Label) == 0 {
		errs = append(errs, problems.Field("label", "required", "at least one locale"))
	}
	for l, v := range f.Label {
		if !domain.ValidLocale(l) || strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > 120 {
			errs = append(errs, problems.Field("label."+l, "invalid", "known locale, 1..120 characters"))
		}
	}
	if !slices.Contains(domain.FieldTypes, f.Type) {
		errs = append(errs, problems.Field("type", "enum", "unknown field type"))
	}
	if isSelect(f.Type) && len(f.Options) == 0 {
		errs = append(errs, problems.Field("options", "required", "select fields need options"))
	}
	if !isSelect(f.Type) && len(f.Options) > 0 {
		errs = append(errs, problems.Field("options", "not_allowed", "only select fields have options"))
	}
	seen := map[string]bool{}
	for i, o := range f.Options {
		if strings.TrimSpace(o.Value) == "" || len(o.Value) > 100 || seen[o.Value] {
			errs = append(errs, problems.Field("options["+itoa(i)+"].value", "invalid", "unique, 1..100 characters"))
		}
		seen[o.Value] = true
	}
	if f.CustomerEditable && !f.CustomerVisible {
		errs = append(errs, problems.Field("customer_editable", "requires_visible", "a customer-editable field must be customer-visible"))
	}
	for i, c := range f.CategoryIDs {
		if _, err := d.Category(ctx, c); err != nil {
			errs = append(errs, problems.Field("category_ids["+itoa(i)+"]", "not_found", "unknown category"))
		}
	}
	for i, p := range f.ProjectIDs {
		if pr, err := d.ResolveProject(ctx, p); err != nil {
			errs = append(errs, problems.Field("project_ids["+itoa(i)+"]", "not_found", "unknown project"))
		} else {
			f.ProjectIDs[i] = pr.ID
		}
	}
	return errs
}

func projectIDsJSON(ids []string) any {
	if ids == nil {
		return nil
	}
	return store.MustJSON(ids)
}

func (d *Directory) listFieldDefs(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomFields, authz.OpRead); err != nil {
		return err
	}
	defs, err := d.FieldDefs(r.Context())
	if err != nil {
		return err
	}
	if defs == nil {
		defs = []FieldDef{}
	}
	dirJSON(w, http.StatusOK, defs)
	return nil
}

func (d *Directory) getFieldDef(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomFields, authz.OpRead); err != nil {
		return err
	}
	f, ok := d.FieldByID(r.Context(), urlID(r))
	if !ok {
		return problems.NotFound()
	}
	dirJSON(w, http.StatusOK, f)
	return nil
}

func (d *Directory) createFieldDef(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomFields, authz.OpWrite); err != nil {
		return err
	}
	var in fieldIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	f := FieldDef{ID: store.NewID(), Key: in.Key, Label: in.Label, Type: in.Type, Options: []FieldOption{}, Active: true}
	if in.Options != nil {
		f.Options = *in.Options
	}
	f.Required, f.CustomerVisible, f.CustomerEditable = deref(in.Required), deref(in.CustomerVisible), deref(in.CustomerEditable)
	if in.CategoryIDs.Set {
		f.CategoryIDs = in.CategoryIDs.Val
	}
	if in.ProjectIDs.Set {
		f.ProjectIDs = in.ProjectIDs.Val
	}
	if in.Sort != nil {
		f.Sort = *in.Sort
	}
	if errs := d.validateField(ctx, &f); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		now := store.Now()
		var cats any
		if f.CategoryIDs != nil {
			cats = store.MustJSON(f.CategoryIDs)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO custom_field_defs (id, key, label, type, options, required, customer_visible, customer_editable, category_ids, project_ids, sort, active, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE, ?, ?)`, f.ID, f.Key, store.MustJSON(f.Label), string(f.Type), store.MustJSON(f.Options),
			f.Required, f.CustomerVisible, f.CustomerEditable, cats, projectIDsJSON(f.ProjectIDs), f.Sort, now, now)
		if store.IsUniqueViolation(err) {
			return problems.Conflict("a field with this key exists")
		}
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	out, _ := d.FieldByID(ctx, f.ID)
	dirJSON(w, http.StatusCreated, out)
	return nil
}

func deref(b *bool) bool { return b != nil && *b }

// patchFieldDef updates a definition. The key and type are immutable (they
// are not part of CustomFieldPatch; a `type` change is refused with 409 once
// values exist); removing an option still in use is 409 — archive the field
// with active=false instead.
func (d *Directory) patchFieldDef(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCustomFields, authz.OpWrite); err != nil {
		return err
	}
	var in fieldIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	id := urlID(r)
	cur, ok := d.FieldByID(ctx, id)
	if !ok {
		return problems.NotFound()
	}
	if in.Key != "" && in.Key != cur.Key {
		return problems.Validation(problems.Field("key", "immutable", "the key cannot change"))
	}
	f := cur
	if in.Label != nil {
		f.Label = in.Label
	}
	if in.Options != nil {
		f.Options = *in.Options
	}
	if in.Required != nil {
		f.Required = *in.Required
	}
	if in.CustomerVisible != nil {
		f.CustomerVisible = *in.CustomerVisible
	}
	if in.CustomerEditable != nil {
		f.CustomerEditable = *in.CustomerEditable
	}
	if in.CategoryIDs.Set {
		f.CategoryIDs = in.CategoryIDs.Val
	}
	if in.ProjectIDs.Set {
		f.ProjectIDs = in.ProjectIDs.Val
	}
	if in.Sort != nil {
		f.Sort = *in.Sort
	}
	if in.Active != nil {
		f.Active = *in.Active
	}
	typeChange := in.Type != "" && in.Type != cur.Type
	if typeChange {
		f.Type = in.Type
	}
	if errs := d.validateField(ctx, &f); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var used int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_field_values WHERE field_id = ?`, id).Scan(&used); err != nil {
			return err
		}
		if typeChange && used > 0 {
			return problems.Conflict("the type cannot change once values exist")
		}
		for _, o := range cur.Options {
			if slices.ContainsFunc(f.Options, func(x FieldOption) bool { return x.Value == o.Value }) {
				continue
			}
			n, err := optionUses(ctx, tx, cur, o.Value)
			if err != nil {
				return err
			}
			if n > 0 {
				return problems.Conflict("option " + o.Value + " is in use; archive the field instead")
			}
		}
		var cats any
		if f.CategoryIDs != nil {
			cats = store.MustJSON(f.CategoryIDs)
		}
		_, err := tx.ExecContext(ctx, `UPDATE custom_field_defs SET label = ?, type = ?, options = ?, required = ?, customer_visible = ?, customer_editable = ?,
			category_ids = ?, project_ids = ?, sort = ?, active = ?, updated_at = ? WHERE id = ?`, store.MustJSON(f.Label), string(f.Type), store.MustJSON(f.Options),
			f.Required, f.CustomerVisible, f.CustomerEditable, cats, projectIDsJSON(f.ProjectIDs), f.Sort, f.Active, store.Now(), id)
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	out, _ := d.FieldByID(ctx, id)
	dirJSON(w, http.StatusOK, out)
	return nil
}

// optionUses counts tickets using an option value.
func optionUses(ctx context.Context, tx *store.Tx, f FieldDef, value string) (int, error) {
	var n int
	var err error
	if f.Type == domain.FieldSelect {
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_field_values WHERE field_id = ? AND value_text = ?`, f.ID, value).Scan(&n)
		return n, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT value_json FROM ticket_field_values WHERE field_id = ?`, f.ID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var v store.JSON[[]string]
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
		if slices.Contains(v.V, value) {
			n++
		}
	}
	return n, rows.Err()
}
