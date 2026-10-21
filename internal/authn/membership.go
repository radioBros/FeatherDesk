// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"sync"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// MembershipFunc re-validates M-4 for users whose team membership, tier or
// role changed, inside the caller's transaction (teamID "" = every team).
// app/modules.go plugs in directory.MembershipChanged (authn cannot import
// directory: directory imports authn).
type MembershipFunc func(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error

var (
	membershipMu sync.RWMutex
	membershipFn MembershipFunc
)

// SetMembershipChanged installs the M-4 re-validation used by the OIDC role,
// tier and team sync (AUTH-006, AUTH-031 → TKT-017). nil disables it.
func SetMembershipChanged(fn MembershipFunc) {
	membershipMu.Lock()
	membershipFn = fn
	membershipMu.Unlock()
}

func membershipChanged(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error {
	membershipMu.RLock()
	fn := membershipFn
	membershipMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, tx, actorID, teamID, userIDs)
}
