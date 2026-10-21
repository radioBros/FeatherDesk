// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"slices"
	"strconv"

	"github.com/radioBros/SimpleTickets/internal/problems"
)

// ValidateOptOut checks a PATCH /me notification_opt_out list (NOT-015).
// auth.* and customer.reply cannot be opted out of (code not_optional);
// unknown keys are "invalid". Storage: users.notify_prefs = {"opt_out":[…]}.
func ValidateOptOut(keys []string) []problems.FieldError {
	var fe []problems.FieldError
	for i, k := range keys {
		field := "notification_opt_out[" + strconv.Itoa(i) + "]"
		switch {
		case !slices.Contains(TemplateKeys, k):
			fe = append(fe, problems.Field(field, "invalid", "unknown template key "+k))
		case !isOptional(k):
			fe = append(fe, problems.Field(field, "not_optional", k+" cannot be disabled"))
		}
	}
	return fe
}
