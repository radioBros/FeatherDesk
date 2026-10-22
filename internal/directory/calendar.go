// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // distroless images have no zoneinfo (TKT-035)

	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// TimeRange is one business interval of a weekday, "HH:MM"–"HH:MM" (end may be 24:00).
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// WeekDays are the keys of Calendar.Week, Monday first.
var WeekDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var weekdayKey = map[time.Weekday]string{
	time.Monday: "mon", time.Tuesday: "tue", time.Wednesday: "wed", time.Thursday: "thu",
	time.Friday: "fri", time.Saturday: "sat", time.Sunday: "sun",
}

// Calendar is a business-hours calendar (TKT-035): weekly wall-clock
// intervals in an IANA zone plus holidays.
type Calendar struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Timezone  string                 `json:"timezone"`
	Week      map[string][]TimeRange `json:"week"`
	Holidays  []string               `json:"holidays"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`

	loc      *time.Location
	spans    [7][]span // by time.Weekday
	holidays map[string]bool
}

type span struct{ start, end int } // minutes since local midnight, end ≤ 1440

// Prepare parses the calendar; call after loading or building one by hand.
func (c *Calendar) Prepare() error {
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("calendar %s: timezone: %w", c.ID, err)
	}
	c.loc = loc
	c.holidays = map[string]bool{}
	for _, h := range c.Holidays {
		c.holidays[h] = true
	}
	for wd, key := range weekdayKey {
		var ss []span
		for _, r := range c.Week[key] {
			s, ok1 := parseHHMM(r.Start, false)
			e, ok2 := parseHHMM(r.End, true)
			if ok1 && ok2 && e > s {
				ss = append(ss, span{s, e})
			}
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].start < ss[j].start })
		c.spans[wd] = ss
	}
	return nil
}

func (c *Calendar) hasHours() bool {
	for _, s := range c.spans {
		if len(s) > 0 {
			return true
		}
	}
	return false
}

func parseHHMM(s string, allow24 bool) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || mm < 0 || mm > 59 || hh < 0 {
		return 0, false
	}
	if hh == 24 && mm == 0 && allow24 {
		return 1440, true
	}
	if hh > 23 {
		return 0, false
	}
	return hh*60 + mm, true
}

// maxCalendarDays bounds every walk (a calendar with hours but only holidays
// would otherwise loop forever).
const maxCalendarDays = 3 * 366

// AddBusinessMinutes returns the instant `minutes` business minutes after
// start (TKT-035). A nil calendar, or one without hours, is 24×7. Intervals
// are built per day from wall-clock times in the calendar's zone, so DST days
// simply have shorter or longer intervals; holidays are skipped.
func AddBusinessMinutes(c *Calendar, start time.Time, minutes int) time.Time {
	return AddBusiness(c, start, time.Duration(minutes)*time.Minute)
}

// AddBusiness is AddBusinessMinutes with a duration.
func AddBusiness(c *Calendar, start time.Time, d time.Duration) time.Time {
	if d <= 0 {
		return start
	}
	if c == nil || c.loc == nil || !c.hasHours() {
		return start.Add(d)
	}
	rem := d
	cur := start.In(c.loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, c.loc)
	for i := 0; i < maxCalendarDays; i++ {
		y, m, dd := day.Date()
		if !c.holidays[day.Format(time.DateOnly)] {
			for _, sp := range c.spans[day.Weekday()] {
				s := time.Date(y, m, dd, sp.start/60, sp.start%60, 0, 0, c.loc)
				e := time.Date(y, m, dd, sp.end/60, sp.end%60, 0, 0, c.loc)
				if !e.After(cur) {
					continue
				}
				if s.Before(cur) {
					s = cur
				}
				avail := e.Sub(s)
				if rem <= avail {
					return s.Add(rem).UTC()
				}
				rem -= avail
			}
		}
		day = time.Date(y, m, dd+1, 0, 0, 0, 0, c.loc)
	}
	return start.Add(d) // degenerate calendar: fall back to 24×7
}

// BusinessDuration is the business time between a and b (0 when b ≤ a). Used
// for SLA pause subtraction and business-minute reports.
func BusinessDuration(c *Calendar, a, b time.Time) time.Duration {
	if !b.After(a) {
		return 0
	}
	if c == nil || c.loc == nil || !c.hasHours() {
		return b.Sub(a)
	}
	var total time.Duration
	cur := a.In(c.loc)
	end := b.In(c.loc)
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, c.loc)
	for i := 0; i < maxCalendarDays && day.Before(end); i++ {
		y, m, dd := day.Date()
		if !c.holidays[day.Format(time.DateOnly)] {
			for _, sp := range c.spans[day.Weekday()] {
				s := time.Date(y, m, dd, sp.start/60, sp.start%60, 0, 0, c.loc)
				e := time.Date(y, m, dd, sp.end/60, sp.end%60, 0, 0, c.loc)
				if s.Before(cur) {
					s = cur
				}
				if e.After(end) {
					e = end
				}
				if e.After(s) {
					total += e.Sub(s)
				}
			}
		}
		day = time.Date(y, m, dd+1, 0, 0, 0, 0, c.loc)
	}
	return total
}

// ValidateCalendar checks a calendar definition (422 field errors, TKT-035).
func ValidateCalendar(name, tz string, week map[string][]TimeRange, holidays []string) []problems.FieldError {
	var errs []problems.FieldError
	if n := strings.TrimSpace(name); n == "" || len([]rune(n)) > 120 {
		errs = append(errs, problems.Field("name", "required", "1..120 characters"))
	}
	if tz == "" {
		errs = append(errs, problems.Field("timezone", "required", "IANA time zone"))
	} else if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
		errs = append(errs, problems.Field("timezone", "timezone", "unknown IANA time zone"))
	}
	for key, ranges := range week {
		if !dirContains(WeekDays, key) {
			errs = append(errs, problems.Field("week."+key, "invalid", "unknown weekday"))
			continue
		}
		var ss []span
		for i, r := range ranges {
			f := fmt.Sprintf("week.%s[%d]", key, i)
			s, ok1 := parseHHMM(r.Start, false)
			e, ok2 := parseHHMM(r.End, true)
			switch {
			case !ok1 || !ok2:
				errs = append(errs, problems.Field(f, "time", "HH:MM"))
				continue
			case e <= s:
				errs = append(errs, problems.Field(f, "range", "end must be after start"))
				continue
			}
			ss = append(ss, span{s, e})
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].start < ss[j].start })
		for i := 1; i < len(ss); i++ {
			if ss[i].start < ss[i-1].end {
				errs = append(errs, problems.Field("week."+key, "overlap", "intervals overlap"))
				break
			}
		}
	}
	for i, h := range holidays {
		if _, err := time.Parse(time.DateOnly, h); err != nil {
			errs = append(errs, problems.Field(fmt.Sprintf("holidays[%d]", i), "date", "YYYY-MM-DD"))
		}
	}
	return errs
}

func dirContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func loadCalendars(ctx context.Context, q store.Querier) ([]Calendar, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, timezone, week, holidays, created_at, updated_at FROM business_calendars ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Calendar
	for rows.Next() {
		var c Calendar
		var week store.JSON[map[string][]TimeRange]
		var hol store.JSON[[]string]
		if err := rows.Scan(&c.ID, &c.Name, &c.Timezone, &week, &hol, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Week, c.Holidays = week.V, hol.V
		normalizeCalendar(&c)
		c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
		if err := c.Prepare(); err != nil {
			continue // invalid zone stored by hand: treat the team as 24×7
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func normalizeCalendar(c *Calendar) {
	if c.Week == nil {
		c.Week = map[string][]TimeRange{}
	}
	for _, k := range WeekDays {
		if c.Week[k] == nil {
			c.Week[k] = []TimeRange{}
		}
	}
	if c.Holidays == nil {
		c.Holidays = []string{}
	}
}

// Calendars returns every valid calendar (snapshot).
func (d *Directory) Calendars(ctx context.Context) ([]Calendar, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Calendar, 0, len(s.calendars))
	for _, c := range s.calendars {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) || (strings.EqualFold(out[i].Name, out[j].Name) && out[i].ID < out[j].ID)
	})
	return out, nil
}

// CalendarByID returns one calendar from the snapshot.
func (d *Directory) CalendarByID(ctx context.Context, id string) (*Calendar, bool) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, false
	}
	c, ok := s.calendars[id]
	return c, ok
}
