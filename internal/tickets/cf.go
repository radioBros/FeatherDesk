// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Custom field values on tickets (TKT-040). Values are typed by definition;
// every payload builder filters them through authz.CanSeeField (I-9).

type fieldVals map[string]map[string]directory.FieldValue // ticket → field id → value

// loadFieldValues reads the values of the given tickets in one query per 500 IDs.
func loadFieldValues(ctx context.Context, q store.Querier, ticketIDs []string) (fieldVals, error) {
	out := fieldVals{}
	ids := anyStrings(ticketIDs)
	for len(ids) > 0 {
		n := min(len(ids), 500)
		batch := ids[:n]
		ids = ids[n:]
		rows, err := q.QueryContext(ctx, `SELECT ticket_id, field_id, value_text, value_num, value_date, value_json FROM ticket_field_values WHERE ticket_id IN (`+placeholders(len(batch))+`)`, batch...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var tid, fid string
			var text sql.NullString
			var num sql.NullFloat64
			var date any
			var raw store.RawJSON
			if err := rows.Scan(&tid, &fid, &text, &num, &date, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			var v directory.FieldValue
			if text.Valid {
				s := text.String
				v.Text = &s
			}
			if num.Valid {
				f := num.Float64
				v.Num = &f
			}
			if d := dateString(date); d != "" {
				v.Date = &d
			}
			if len(raw) > 0 {
				var x any
				if json.Unmarshal(raw, &x) == nil {
					v.JSON = x
				}
			}
			if out[tid] == nil {
				out[tid] = map[string]directory.FieldValue{}
			}
			out[tid][fid] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func dateString(v any) string {
	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.DateOnly)
	case string:
		if len(x) >= 10 {
			return x[:10]
		}
		return x
	case []byte:
		if len(x) >= 10 {
			return string(x[:10])
		}
		return string(x)
	}
	return ""
}

// presentFields renders values keyed by field key, keeping only the fields p
// may see (I-9). Archived definitions still render their stored values.
func (s *Service) presentFields(ctx context.Context, p *domain.Principal, vals map[string]directory.FieldValue) map[string]any {
	out := map[string]any{}
	for fid, v := range vals {
		def, ok := s.dir.FieldByID(ctx, fid)
		if !ok || !authz.CanSeeField(p, def.Access()) {
			continue
		}
		if pv := def.Present(v); pv != nil {
			out[def.Key] = pv
		}
	}
	return out
}

// applyFieldValues validates and writes `in` (key → value) for ticket t in tx.
// Unknown, archived, non-applicable or (for customers) non-editable keys are
// 422 errors on `cf.<key>`. When creating as a customer, required
// customer-editable fields that apply to the category must be present (M-9).
// It returns the keys whose value changed.
func (s *Service) applyFieldValues(ctx context.Context, tx *store.Tx, t *ticketRow, p *domain.Principal, in map[string]any, creating bool) ([]string, error) {
	if len(in) == 0 && (!creating || !p.IsCustomer()) {
		return nil, nil
	}
	var errs []problems.FieldError
	current, err := loadFieldValues(ctx, tx, []string{t.ID})
	if err != nil {
		return nil, err
	}
	cur := current[t.ID]
	type write struct {
		def directory.FieldDef
		val *directory.FieldValue
	}
	var writes []write
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		def, ok := s.dir.FieldByKey(ctx, key)
		switch {
		case !ok || !def.Active:
			errs = append(errs, problems.Field("cf."+key, "unknown", "unknown custom field"))
			continue
		case !authz.CanEditField(p, def.Access()):
			if !authz.CanSeeField(p, def.Access()) {
				errs = append(errs, problems.Field("cf."+key, "unknown", "unknown custom field"))
			} else {
				errs = append(errs, problems.Field("cf."+key, "read_only", "this field cannot be edited"))
			}
			continue
		case !def.AppliesTo(t.CategoryID):
			errs = append(errs, problems.Field("cf."+key, "not_applicable", "the field does not apply to this category"))
			continue
		}
		val, fe := def.NormalizeValue(in[key])
		if fe != nil {
			errs = append(errs, *fe)
			continue
		}
		writes = append(writes, write{def, val})
	}
	if creating && p.IsCustomer() {
		defs, err := s.dir.FieldDefs(ctx)
		if err != nil {
			return nil, err
		}
		for _, def := range defs {
			if !def.Active || !def.Required || !def.CustomerEditable || !def.AppliesTo(t.CategoryID) {
				continue
			}
			v, given := in[def.Key]
			if !given || v == nil || v == "" {
				errs = append(errs, problems.Field("cf."+def.Key, "required", "this field is required"))
			}
		}
	}
	if len(errs) > 0 {
		return nil, problems.Validation(errs...)
	}
	var changed []string
	now := s.clock()
	for _, w := range writes {
		old, had := cur[w.def.ID]
		if w.val == nil {
			if had {
				if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_field_values WHERE ticket_id = ? AND field_id = ?`, t.ID, w.def.ID); err != nil {
					return nil, err
				}
				changed = append(changed, w.def.Key)
			}
			continue
		}
		if had && sameValue(w.def, old, *w.val) {
			continue
		}
		var text, date, js any
		var num any
		if w.val.Text != nil {
			text = *w.val.Text
		}
		if w.val.Num != nil {
			num = *w.val.Num
		}
		if w.val.Date != nil {
			date = *w.val.Date
		}
		if w.val.JSON != nil {
			js = store.MustJSON(w.val.JSON)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_field_values (ticket_id, field_id, value_text, value_num, value_date, value_json, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (ticket_id, field_id) DO UPDATE SET value_text = excluded.value_text, value_num = excluded.value_num,
				value_date = excluded.value_date, value_json = excluded.value_json, updated_at = excluded.updated_at`,
			t.ID, w.def.ID, text, num, date, js, now); err != nil {
			return nil, err
		}
		changed = append(changed, w.def.Key)
	}
	return changed, nil
}

func sameValue(def directory.FieldDef, a, b directory.FieldValue) bool {
	x, _ := json.Marshal(def.Present(a))
	y, _ := json.Marshal(def.Present(b))
	return string(x) == string(y)
}

// missingRequiredStaffFields lists required staff fields (not customer
// editable) that apply to t and have no value: M-9 blocks `resolved`.
func (s *Service) missingRequiredStaffFields(ctx context.Context, q store.Querier, t *ticketRow) ([]string, error) {
	defs, err := s.dir.FieldDefs(ctx)
	if err != nil {
		return nil, err
	}
	var need []directory.FieldDef
	for _, d := range defs {
		if d.Active && d.Required && !d.CustomerEditable && d.AppliesTo(t.CategoryID) {
			need = append(need, d)
		}
	}
	if len(need) == 0 {
		return nil, nil
	}
	vals, err := loadFieldValues(ctx, q, []string{t.ID})
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, d := range need {
		v, ok := vals[t.ID][d.ID]
		if !ok || d.Present(v) == nil {
			missing = append(missing, d.Key)
		}
	}
	return missing, nil
}

// customerVisibleKeys filters changed field keys to those customers may see (I-9).
func (s *Service) customerVisibleKeys(ctx context.Context, keys []string) []string {
	out := []string{}
	for _, k := range keys {
		if def, ok := s.dir.FieldByKey(ctx, k); ok && def.CustomerVisible {
			out = append(out, k)
		}
	}
	return out
}

// CustomerUpdateFields is PATCH /portal/tickets/{id}/fields (TKT-040): a
// customer who can see the ticket edits its customer-editable fields while
// it is not closed. Non-editable or staff-only keys are rejected (422,
// staff-only keys look unknown, I-9); one ticket.fields_changed event.
func (s *Service) CustomerUpdateFields(ctx context.Context, p *domain.Principal, id string, in map[string]any) error {
	if len(in) == 0 {
		return problems.Validation(problems.Field("custom_fields", "min_properties", "nothing to change"))
	}
	pol := s.Policy(ctx)
	if err := s.warm(ctx); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if !authz.CanSeeTicket(p, t.view(pol, s.clock())) {
			return problems.NotFound()
		}
		if t.Status == domain.StatusClosed {
			return problems.Conflict("the ticket is closed")
		}
		keys, err := s.applyFieldValues(ctx, tx, t, p, in, false)
		if err != nil {
			return err
		}
		m := &mutation{t: t}
		if len(keys) > 0 {
			m.add(events.TicketFieldsChanged, events.FieldsChange{Keys: keys})
		}
		return s.flush(ctx, tx, m, p.UserID)
	})
}
