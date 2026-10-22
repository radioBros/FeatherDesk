// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// SSO-only accounts (D-04 §4/§5, AUTH-044). An account that belongs to the
// identity provider — an OIDC identity for its audience, or idp_managed —
// signs in only through the provider, so the provider's MFA, group mapping
// and blocking always apply. Local password login, sign-in links, invites,
// password reset and password change are refused for it. The one exception
// is the break-glass staff admin (D-04 §5): it keeps local login, and with
// it FeatherDesk' own two-step verification (D-19 §B).

// breakGlassAdmin reports whether u is a break-glass staff admin.
func breakGlassAdmin(u userRow) bool {
	return u.Kind == domain.KindStaff && u.BreakGlass && u.Role == domain.RoleAdmin
}

// hasIdentity reports whether userID has an OIDC identity for the audience
// of kind. An empty userID (unknown address) runs the same query, so the
// login timing does not depend on whether the account exists.
func hasIdentity(ctx context.Context, q store.Querier, userID string, kind domain.UserKind) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_identities WHERE user_id = ? AND audience = ?`, userID, string(kind)).Scan(&n)
	return n > 0, err
}

// ssoOnlyWith is ssoOnly with the identity lookup already done.
func ssoOnlyWith(u userRow, linked bool) bool {
	return !breakGlassAdmin(u) && (u.IDPManaged || linked)
}

// ssoOnly reports whether u may sign in only through its identity provider.
func ssoOnly(ctx context.Context, q store.Querier, u userRow) (bool, error) {
	if breakGlassAdmin(u) {
		return false, nil
	}
	if u.IDPManaged {
		return true, nil
	}
	return hasIdentity(ctx, q, u.ID, u.Kind)
}

// markSSOLinked runs when an account becomes SSO-only (first link by email,
// idp_managed set by the role sync, or loss of break-glass): the local
// password hash is removed, every session not created by an OIDC sign-in is
// revoked (sockets closed after commit), open MFA challenges and unused
// password-reset and sign-in links are dropped, and the change is audited.
// PATs are unaffected (D-19 §B). Never call it for a break-glass admin.
func markSSOLinked(ctx context.Context, tx *store.Tx, u userRow, via, ip string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = NULL, updated_at = ? WHERE id = ?`, store.Now(), u.ID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id_hash FROM sessions WHERE user_id = ? AND oidc_id_token IS NULL`, u.ID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, h := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h); err != nil {
			return err
		}
	}
	tx.AfterCommit(func() {
		for _, h := range ids {
			closeSessionSockets(h, "session revoked")
		}
	})
	if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_challenges WHERE user_id = ?`, u.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id = ? AND used_at IS NULL AND purpose IN (?, ?)`,
		u.ID, PurposeResetPassword, PurposeMagicLink); err != nil {
		return err
	}
	return Audit(ctx, tx, AuditEntry{ActorID: u.ID, Action: "auth.sso_linked", TargetType: "user", TargetID: u.ID, IP: ip,
		Data: map[string]any{"via": via, "password_removed": u.hasPassword(), "sessions_revoked": len(ids)}})
}
