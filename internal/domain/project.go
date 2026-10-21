// SPDX-License-Identifier: AGPL-3.0-only

package domain

import (
	"strconv"
	"strings"
)

// DefaultProjectID is the id of the Default project created by migration
// 0140 (D-20). It is a fixed value so the migration, store.WipeData and the
// column defaults agree; the project itself can be renamed and re-keyed.
const DefaultProjectID = "00000000-0000-7000-8000-000000000001"

// ProjectRole is a staff member's role in one project (project_members.role).
type ProjectRole string

const (
	ProjectMember ProjectRole = "member"
	ProjectAdmin  ProjectRole = "project_admin"
)

// ProjectRoles lists the values of project_members.role.
var ProjectRoles = []ProjectRole{ProjectMember, ProjectAdmin}

func (r ProjectRole) Valid() bool { return r == ProjectMember || r == ProjectAdmin }

// ValidProjectKey: 2–10 characters, an upper-case letter then upper-case
// letters or digits (`^[A-Z][A-Z0-9]{1,9}$`).
func ValidProjectKey(k string) bool {
	if len(k) < 2 || len(k) > 10 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// TicketRef formats a ticket reference: KEY-123.
func TicketRef(key string, seq int64) string {
	return key + "-" + strconv.FormatInt(seq, 10)
}

// ParseTicketRef splits "KEY-123" (case-insensitive key, upper-cased on
// return) into key and sequence. ok is false for anything else.
func ParseTicketRef(s string) (key string, seq int64, ok bool) {
	i := strings.LastIndexByte(s, '-')
	if i <= 0 || i == len(s)-1 {
		return "", 0, false
	}
	key = strings.ToUpper(s[:i])
	if !ValidProjectKey(key) {
		return "", 0, false
	}
	num := s[i+1:]
	if len(num) > 12 || num[0] == '0' {
		return "", 0, false
	}
	for j := 0; j < len(num); j++ {
		if num[j] < '0' || num[j] > '9' {
			return "", 0, false
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return key, n, true
}
