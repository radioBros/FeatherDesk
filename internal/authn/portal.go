// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"net/http"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/realtime"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// The customer portal switch (support.portal_enabled). While it is off, only
// staff may sign in: every customer entry point answers 404 `portal-disabled`,
// customer PATs are refused, customer sockets are closed with 4403 and the
// existing customer sessions are deleted the moment the switch flips. Mail
// still goes out to customers and inbound mail still threads (those paths
// never use a customer principal).

func errPortalDisabled() error {
	return problems.New(problems.SlugPortalDisabled, "the customer portal is disabled")
}

// PortalEnabled reports the cached support.portal_enabled value.
func (m *Module) PortalEnabled() bool { return m.portalOn.Load() }

// Admit implements realtime.Admitter: customers may not hold a socket while
// the portal is disabled.
func (m *Module) Admit(p *domain.Principal) bool { return m.portalOn.Load() || !p.IsCustomer() }

// portalRefused writes the 404 and reports true while the portal is disabled.
// Call it first in every customer-only handler.
func (m *Module) portalRefused(w http.ResponseWriter, r *http.Request) bool {
	if m.portalOn.Load() {
		return false
	}
	problems.Write(w, r, errPortalDisabled())
	return true
}

// portalGateUser is the in-transaction variant for flows whose user kind is
// only known from the token (invite accept): a customer is refused and the
// transaction rolls back, so the token stays valid for when the portal is on.
func (m *Module) portalGateUser(u userRow) error {
	if u.Kind == domain.KindCustomer && !m.portalOn.Load() {
		return errPortalDisabled()
	}
	return nil
}

// syncPortal re-reads the setting; on an on to off flip it revokes every
// customer session and closes every customer socket (4403).
func (m *Module) syncPortal(ctx context.Context) {
	if m.d.Settings == nil {
		return
	}
	sup, err := m.d.Settings.Support(ctx)
	if err != nil {
		m.log.Error("portal setting", "err", err)
		return
	}
	was := m.portalOn.Swap(sup.PortalEnabled)
	if was && !sup.PortalEnabled {
		m.revokeCustomerAccess(ctx)
	}
}

func (m *Module) revokeCustomerAccess(ctx context.Context) {
	n := 0
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE audience = ?`, string(domain.AudienceCustomer))
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		n = int(c)
		if _, err := tx.ExecContext(ctx, `DELETE FROM mfa_challenges WHERE user_id IN (SELECT id FROM users WHERE kind = 'customer')`); err != nil {
			return err
		}
		return Audit(ctx, tx, AuditEntry{Action: "auth.portal_disabled", TargetType: "settings", TargetID: settings.KeySupport,
			Data: map[string]any{"sessions_revoked": n}})
	})
	if err != nil {
		m.log.Error("revoke customer sessions", "err", err)
		return
	}
	hubs.Range(func(k, _ any) bool {
		k.(*realtime.Hub).CloseWhere(func(p *domain.Principal) bool { return p.IsCustomer() }, realtime.CloseForbidden, "portal disabled")
		return true
	})
}
