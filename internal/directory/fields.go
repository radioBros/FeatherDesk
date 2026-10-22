// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// FieldOption is one choice of a select/multiselect field.
type FieldOption struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label"`
}

// FieldDef is a custom field definition (TKT-039).
type FieldDef struct {
	ID               string            `json:"id"`
	Key              string            `json:"key"`
	Label            map[string]string `json:"label"`
	Type             domain.FieldType  `json:"type"`
	Options          []FieldOption     `json:"options"`
	Required         bool              `json:"required"`
	CustomerVisible  bool              `json:"customer_visible"`
	CustomerEditable bool              `json:"customer_editable"`
	CategoryIDs      []string          `json:"category_ids"` // nil = all categories
	ProjectIDs       []string          `json:"project_ids"`  // D-20: nil = all projects
	Sort             int               `json:"sort"`
	Active           bool              `json:"active"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// Access returns the I-9 visibility of the field.
func (f FieldDef) Access() authz.FieldAccess {
	return authz.FieldAccess{CustomerVisible: f.CustomerVisible, CustomerEditable: f.CustomerEditable}
}

// AppliesTo reports whether the field applies to a ticket of categoryID in
// projectID ("" = any project, for lookups without a project). An empty scope
// list means every category (project), like null: that is how the admin UI
// shows it.
func (f FieldDef) AppliesTo(categoryID, projectID string) bool {
	if len(f.ProjectIDs) > 0 && projectID != "" && !slices.Contains(f.ProjectIDs, projectID) {
		return false
	}
	if len(f.CategoryIDs) == 0 {
		return true
	}
	return categoryID != "" && slices.Contains(f.CategoryIDs, categoryID)
}

func loadFieldDefs(ctx context.Context, q store.Querier) ([]FieldDef, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, key, label, type, options, required, customer_visible, customer_editable, category_ids, sort, active, created_at, updated_at, project_ids
		FROM custom_field_defs ORDER BY sort, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FieldDef
	for rows.Next() {
		var f FieldDef
		var label store.JSON[map[string]string]
		var opts store.JSON[[]FieldOption]
		var cats, projects store.JSON[[]string]
		if err := rows.Scan(&f.ID, &f.Key, &label, &f.Type, &opts, &f.Required, &f.CustomerVisible, &f.CustomerEditable, &cats, &f.Sort, &f.Active, &f.CreatedAt, &f.UpdatedAt, &projects); err != nil {
			return nil, err
		}
		if projects.Valid && projects.V != nil {
			f.ProjectIDs = projects.V
		}
		f.Label, f.Options = label.V, opts.V
		if f.Label == nil {
			f.Label = map[string]string{}
		}
		if f.Options == nil {
			f.Options = []FieldOption{}
		}
		if cats.Valid && cats.V != nil {
			f.CategoryIDs = cats.V
		}
		f.CreatedAt, f.UpdatedAt = f.CreatedAt.UTC(), f.UpdatedAt.UTC()
		out = append(out, f)
	}
	return out, rows.Err()
}

// FieldDefs returns all definitions (active and archived) from the snapshot.
func (d *Directory) FieldDefs(ctx context.Context) ([]FieldDef, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.fields, nil
}

// FieldByKey returns a definition by key.
func (d *Directory) FieldByKey(ctx context.Context, key string) (FieldDef, bool) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return FieldDef{}, false
	}
	f, ok := s.fieldByKey[key]
	if !ok {
		return FieldDef{}, false
	}
	return *f, true
}

// FieldByID returns a definition by id.
func (d *Directory) FieldByID(ctx context.Context, id string) (FieldDef, bool) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return FieldDef{}, false
	}
	f, ok := s.fieldByID[id]
	if !ok {
		return FieldDef{}, false
	}
	return *f, true
}

// FieldValue is a typed stored value (one row of ticket_field_values).
type FieldValue struct {
	Text *string
	Num  *float64
	Date *string // YYYY-MM-DD
	JSON any     // multiselect []string, checkbox bool
}

// Limits for text values (TKT-040).
const (
	MaxTextValue     = 1000
	MaxTextareaValue = 10000
)

// NormalizeValue validates v against the definition. A nil value clears the
// field. The error is a FieldError with field `cf.<key>`.
func (f FieldDef) NormalizeValue(v any) (val *FieldValue, fe *problems.FieldError) {
	bad := func(code, msg string) (*FieldValue, *problems.FieldError) {
		e := problems.Field("cf."+f.Key, code, msg)
		return nil, &e
	}
	if v == nil {
		return nil, nil
	}
	switch f.Type {
	case domain.FieldText, domain.FieldTextarea:
		s, ok := v.(string)
		if !ok {
			return bad("type", "must be a string")
		}
		max := MaxTextValue
		if f.Type == domain.FieldTextarea {
			max = MaxTextareaValue
		}
		if len([]rune(s)) > max {
			return bad("max_length", fmt.Sprintf("at most %d characters", max))
		}
		if strings.TrimSpace(s) == "" {
			return nil, nil
		}
		return &FieldValue{Text: &s}, nil
	case domain.FieldNumber:
		var n float64
		switch x := v.(type) {
		case float64:
			n = x
		case json.Number:
			p, err := x.Float64()
			if err != nil {
				return bad("number", "must be a number")
			}
			n = p
		case string:
			p, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return bad("number", "must be a number")
			}
			n = p
		default:
			return bad("number", "must be a number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e15 {
			return bad("range", "number out of range")
		}
		return &FieldValue{Num: &n}, nil
	case domain.FieldDate:
		s, ok := v.(string)
		if !ok {
			return bad("date", "must be YYYY-MM-DD")
		}
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return bad("date", "must be YYYY-MM-DD")
		}
		return &FieldValue{Date: &s}, nil
	case domain.FieldSelect:
		s, ok := v.(string)
		if !ok || !f.hasOption(s) {
			return bad("option", "not one of the options")
		}
		return &FieldValue{Text: &s}, nil
	case domain.FieldMultiselect:
		arr, ok := v.([]any)
		if !ok {
			if ss, ok2 := v.([]string); ok2 {
				for _, s := range ss {
					arr = append(arr, s)
				}
				ok = true
			}
		}
		if !ok {
			return bad("option", "must be a list of options")
		}
		out := []string{}
		for _, x := range arr {
			s, ok := x.(string)
			if !ok || !f.hasOption(s) {
				return bad("option", "not one of the options")
			}
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		slices.Sort(out)
		if len(out) == 0 {
			return nil, nil
		}
		return &FieldValue{JSON: out}, nil
	case domain.FieldCheckbox:
		b, ok := v.(bool)
		if !ok {
			return bad("type", "must be true or false")
		}
		return &FieldValue{JSON: b}, nil
	}
	return bad("type", "unknown field type")
}

func (f FieldDef) hasOption(v string) bool {
	for _, o := range f.Options {
		if o.Value == v {
			return true
		}
	}
	return false
}

// Present converts a stored value back to its API form.
func (f FieldDef) Present(v FieldValue) any {
	switch f.Type {
	case domain.FieldNumber:
		if v.Num != nil {
			return *v.Num
		}
	case domain.FieldDate:
		if v.Date != nil {
			d := *v.Date
			if len(d) > 10 {
				d = d[:10]
			}
			return d
		}
	case domain.FieldMultiselect:
		switch x := v.JSON.(type) {
		case []any:
			out := make([]string, 0, len(x))
			for _, e := range x {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
			return out
		case []string:
			return x
		}
		return []string{}
	case domain.FieldCheckbox:
		if b, ok := v.JSON.(bool); ok {
			return b
		}
		return false
	default:
		if v.Text != nil {
			return *v.Text
		}
	}
	return nil
}

// SearchText is the indexable text of a value (text-ish fields only).
func (f FieldDef) SearchText(v FieldValue) string {
	switch f.Type {
	case domain.FieldText, domain.FieldTextarea, domain.FieldSelect:
		if v.Text != nil {
			return *v.Text
		}
	case domain.FieldMultiselect:
		if arr, ok := f.Present(v).([]string); ok {
			return strings.Join(arr, " ")
		}
	}
	return ""
}
