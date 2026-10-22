// SPDX-License-Identifier: AGPL-3.0-only

package queue

import "time"

// dbTime parses a timestamp from an aggregate (MIN/MAX), which SQLite returns
// as text because the declared column type is lost; Postgres returns time.Time.
func dbTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), true
	case string:
		return parseTS(t)
	case []byte:
		return parseTS(string(t))
	}
	return time.Time{}, false
}

var tsLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05",
}

func parseTS(s string) (time.Time, bool) {
	for _, l := range tsLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
