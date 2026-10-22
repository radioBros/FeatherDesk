// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"testing"
	"time"
)

func cal(t *testing.T, tz string, week map[string][]TimeRange, holidays ...string) *Calendar {
	t.Helper()
	c := &Calendar{ID: "c", Name: "c", Timezone: tz, Week: week, Holidays: holidays}
	if err := c.Prepare(); err != nil {
		t.Fatal(err)
	}
	return c
}

func weekdays(start, end string) map[string][]TimeRange {
	w := map[string][]TimeRange{}
	for _, d := range []string{"mon", "tue", "wed", "thu", "fri"} {
		w[d] = []TimeRange{{start, end}}
	}
	return w
}

func everyDay(ranges ...TimeRange) map[string][]TimeRange {
	w := map[string][]TimeRange{}
	for _, d := range WeekDays {
		w[d] = ranges
	}
	return w
}

func at(t *testing.T, tz, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBusiness_AddMinutesTable(t *testing.T) {
	const tz = "Europe/Rome"
	c := cal(t, tz, weekdays("09:00", "18:00"))
	cases := []struct {
		start string
		min   int
		want  string
	}{
		{"2026-10-23 17:30", 60, "2026-10-26 09:30"}, // Fri 17:30 + 60 → Mon 09:30
		{"2026-10-21 10:00", 30, "2026-10-21 10:30"},
		{"2026-10-21 07:00", 30, "2026-10-21 09:30"}, // before opening
		{"2026-10-24 12:00", 60, "2026-10-26 10:00"}, // Saturday
		{"2026-10-21 18:00", 1, "2026-10-22 09:01"},  // exactly at closing
		{"2026-10-21 09:00", 540, "2026-10-21 18:00"},
		{"2026-10-21 09:00", 541, "2026-10-22 09:01"},
	}
	for _, tc := range cases {
		got := AddBusinessMinutes(c, at(t, tz, tc.start), tc.min)
		if want := at(t, tz, tc.want); !got.Equal(want) {
			t.Errorf("%s + %d: got %s want %s", tc.start, tc.min, got.In(want.Location()), want)
		}
	}
}

// TestBusiness_DSTSpringForward: on the spring-forward night an interval that
// spans the missing hour is one hour shorter in real time.
func TestBusiness_DSTSpringForward(t *testing.T) {
	for _, tc := range []struct{ tz, day string }{{"Europe/Rome", "2026-03-29"}, {"America/New_York", "2026-03-08"}} {
		c := cal(t, tc.tz, everyDay(TimeRange{"01:00", "04:00"}))
		start := at(t, tc.tz, tc.day+" 01:00")
		// Only 2 real hours exist between 01:00 and 04:00 that night.
		if d := BusinessDuration(c, start, at(t, tc.tz, tc.day+" 04:00")); d != 2*time.Hour {
			t.Errorf("%s: business duration %s", tc.tz, d)
		}
		got := AddBusinessMinutes(c, start, 150) // 120 that night + 30 the next
		next := at(t, tc.tz, tc.day+" 01:00").AddDate(0, 0, 1)
		if want := next.Add(30 * time.Minute); !got.Equal(want) {
			t.Errorf("%s: got %s want %s", tc.tz, got.In(want.Location()), want)
		}
	}
}

func TestBusiness_DSTFallBack(t *testing.T) {
	for _, tc := range []struct{ tz, day string }{{"Europe/Rome", "2026-10-25"}, {"America/New_York", "2026-11-01"}} {
		c := cal(t, tc.tz, everyDay(TimeRange{"00:00", "24:00"}))
		start := at(t, tc.tz, tc.day+" 00:00")
		end := at(t, tc.tz, tc.day+" 00:00").AddDate(0, 0, 1)
		if d := BusinessDuration(c, start, end); d != 25*time.Hour {
			t.Errorf("%s: fall-back day has %s of business time", tc.tz, d)
		}
		if got := AddBusinessMinutes(c, start, 25*60); !got.Equal(end) {
			t.Errorf("%s: got %s want %s", tc.tz, got, end)
		}
		// 09–18 on the fall-back Sunday is still 9 real hours.
		c2 := cal(t, tc.tz, everyDay(TimeRange{"09:00", "18:00"}))
		if d := BusinessDuration(c2, start, end); d != 9*time.Hour {
			t.Errorf("%s: 09-18 = %s", tc.tz, d)
		}
	}
}

func TestBusiness_HolidaySkipped(t *testing.T) {
	const tz = "Europe/Rome"
	c := cal(t, tz, weekdays("09:00", "18:00"), "2026-12-25")
	got := AddBusinessMinutes(c, at(t, tz, "2026-12-24 17:00"), 120) // Thu 1 h, Fri holiday, Mon 1 h
	if want := at(t, tz, "2026-12-28 10:00"); !got.Equal(want) {
		t.Fatalf("got %s want %s", got.In(want.Location()), want)
	}
}

func TestBusiness_PauseSubtractsBusinessMinutesOnly(t *testing.T) {
	const tz = "Europe/Rome"
	c := cal(t, tz, weekdays("09:00", "18:00"))
	// Paused Fri 17:00 → Mon 10:00: only Fri 17–18 and Mon 09–10 count.
	if d := BusinessDuration(c, at(t, tz, "2026-10-23 17:00"), at(t, tz, "2026-10-26 10:00")); d != 2*time.Hour {
		t.Fatalf("paused business time %s", d)
	}
	if d := BusinessDuration(nil, at(t, tz, "2026-10-23 17:00"), at(t, tz, "2026-10-26 10:00")); d != 66*time.Hour { // the weekend includes the fall-back hour
		t.Fatalf("24x7 paused %s", d)
	}
}

func TestBusiness_24x7ModeUnchanged(t *testing.T) {
	start := time.Date(2026, 10, 23, 17, 30, 0, 0, time.UTC)
	if got := AddBusinessMinutes(nil, start, 60); !got.Equal(start.Add(time.Hour)) {
		t.Fatalf("nil calendar: %s", got)
	}
	empty := cal(t, "UTC", map[string][]TimeRange{})
	if got := AddBusinessMinutes(empty, start, 60); !got.Equal(start.Add(time.Hour)) {
		t.Fatalf("calendar without hours: %s", got)
	}
}

func TestCalendar_InvalidIntervals422(t *testing.T) {
	cases := []map[string][]TimeRange{
		{"mon": {{"18:00", "09:00"}}},
		{"mon": {{"09:00", "13:00"}, {"12:00", "18:00"}}},
		{"mon": {{"9:00", "18:00"}}},
		{"mon": {{"09:00", "24:01"}}},
		{"xyz": {{"09:00", "18:00"}}},
	}
	for _, w := range cases {
		if errs := ValidateCalendar("x", "Europe/Rome", w, nil); len(errs) == 0 {
			t.Errorf("accepted %v", w)
		}
	}
	if errs := ValidateCalendar("x", "Mars/Olympus", weekdays("09:00", "18:00"), nil); len(errs) == 0 {
		t.Error("accepted unknown zone")
	}
	if errs := ValidateCalendar("x", "UTC", weekdays("09:00", "18:00"), []string{"2026-02-30"}); len(errs) == 0 {
		t.Error("accepted invalid holiday")
	}
	if errs := ValidateCalendar("x", "UTC", everyDay(TimeRange{"00:00", "24:00"}), []string{"2026-12-25"}); len(errs) != 0 {
		t.Errorf("rejected a valid calendar: %v", errs)
	}
}
