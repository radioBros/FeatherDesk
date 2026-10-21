// SPDX-License-Identifier: AGPL-3.0-only

package app

import "time"

// SetAdminSystemEvery shortens the admin:system check for tests; call before
// Build. It returns a restore func.
func SetAdminSystemEvery(d time.Duration) func() {
	old := adminSystemEvery
	adminSystemEvery = d
	return func() { adminSystemEvery = old }
}
