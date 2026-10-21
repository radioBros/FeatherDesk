// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Business-hours calendars CRUD (TKT-035), supervisor or admin.

type calendarIn struct {
	Name     string                 `json:"name"`
	Timezone string                 `json:"timezone"`
	Week     map[string][]TimeRange `json:"week"`
	Holidays []string               `json:"holidays"`
}

func (in *calendarIn) normalize() []problems.FieldError {
	in.Name = strings.TrimSpace(in.Name)
	if in.Holidays == nil {
		in.Holidays = []string{}
	}
	sort.Strings(in.Holidays)
	errs := ValidateCalendar(in.Name, in.Timezone, in.Week, in.Holidays)
	if in.Week == nil {
		errs = append(errs, problems.Field("week", "required", "weekly hours are required"))
	}
	return errs
}

// refresh rebuilds the snapshot after a committed write (outside any tx, so
// request paths keep serving from memory).
func (d *Directory) refresh(ctx context.Context) {
	d.Invalidate()
	if _, err := d.snapshot(ctx); err != nil {
		d.log.Warn("directory: snapshot reload", "err", err)
	}
}

func (d *Directory) calendarOut(ctx context.Context, id string) (any, error) {
	c, ok := d.CalendarByID(ctx, id)
	if !ok {
		return nil, problems.NotFound()
	}
	return c, nil
}

func (d *Directory) listCalendars(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCalendars, authz.OpRead); err != nil {
		return err
	}
	cals, err := d.Calendars(r.Context())
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, cals)
	return nil
}

func (d *Directory) getCalendar(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCalendars, authz.OpRead); err != nil {
		return err
	}
	c, err := d.calendarOut(r.Context(), urlID(r))
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, c)
	return nil
}

func (d *Directory) createCalendar(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCalendars, authz.OpWrite); err != nil {
		return err
	}
	var in calendarIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	if errs := in.normalize(); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	ctx := r.Context()
	id := store.NewID()
	now := store.Now()
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO business_calendars (id, name, timezone, week, holidays, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, in.Name, in.Timezone, store.MustJSON(in.Week), store.MustJSON(in.Holidays), now, now)
		return err
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	c, err := d.calendarOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusCreated, c)
	return nil
}

func (d *Directory) patchCalendar(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCalendars, authz.OpWrite); err != nil {
		return err
	}
	id := urlID(r)
	var in calendarIn
	if err := dirDecode(r, &in); err != nil {
		return err
	}
	if errs := in.normalize(); len(errs) > 0 {
		return problems.Validation(errs...)
	}
	ctx := r.Context()
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE business_calendars SET name = ?, timezone = ?, week = ?, holidays = ?, updated_at = ? WHERE id = ?`,
			in.Name, in.Timezone, store.MustJSON(in.Week), store.MustJSON(in.Holidays), store.Now(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	d.fireCalendarChanged(ctx, id)
	c, err := d.calendarOut(ctx, id)
	if err != nil {
		return err
	}
	dirJSON(w, http.StatusOK, c)
	return nil
}

func (d *Directory) deleteCalendar(w http.ResponseWriter, r *http.Request) error {
	if err := dirCan(r, authz.ResCalendars, authz.OpWrite); err != nil {
		return err
	}
	id := urlID(r)
	ctx := r.Context()
	err := d.db.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE calendar_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("the calendar is used by a team")
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM business_calendars WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.refresh(ctx)
	w.WriteHeader(http.StatusNoContent)
	return nil
}
