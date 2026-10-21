// SPDX-License-Identifier: AGPL-3.0-only

package directory_test

import (
	"context"
	"time"
)

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
