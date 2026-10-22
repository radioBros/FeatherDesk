// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"fmt"
	"strings"
)

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }
func sprint(a ...any) string            { return fmt.Sprint(a...) }

func stringsContains(s, sub string) bool { return strings.Contains(s, sub) }
