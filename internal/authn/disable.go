// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// DisableUser is I-5 (AUTH-017), inside the caller's transaction: the user
// becomes `disabled`, every session is deleted and every PAT revoked, an
// audit entry is written, and after commit every open WebSocket of the user
// is closed with 4401 and the principal cache is dropped. The next request
// with any of the user's credentials is rejected (no cache window).
//
// Called by the staff admin API, the customer directory (TKT-015), org
// managers and the OIDC role sync.
func DisableUser(ctx context.Context, tx *store.Tx, userID, actorID, reason, ip string) error {
	now := store.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, string(domain.UserDisabled), now, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, userID); err != nil {
		return err
	}
	if err := Audit(ctx, tx, AuditEntry{ActorID: actorID, Action: "auth.user_disabled", TargetType: "user", TargetID: userID, IP: ip,
		Data: map[string]any{"reason": reason}}); err != nil {
		return err
	}
	tx.AfterCommit(func() {
		InvalidateTeams(userID)
		closeUserSockets(userID, "user disabled")
	})
	return nil
}

// EnableUser re-activates a disabled user (sessions and tokens stay revoked).
func EnableUser(ctx context.Context, tx *store.Tx, userID, actorID, ip string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, string(domain.UserActive), store.Now(), userID); err != nil {
		return err
	}
	return Audit(ctx, tx, AuditEntry{ActorID: actorID, Action: "auth.user_enabled", TargetType: "user", TargetID: userID, IP: ip})
}
