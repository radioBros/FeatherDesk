// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// ConsentRecorder stores the acceptance of the privacy policy inside the
// registration transaction (D-19 §C, internal/privacy). authn cannot import
// privacy, so app/modules.go plugs it in.
type ConsentRecorder func(ctx context.Context, tx *store.Tx, userID, policyURL, ip string) error

var registerConsent atomic.Pointer[ConsentRecorder]

// SetRegisterConsent installs the process-wide consent recorder.
func SetRegisterConsent(fn ConsentRecorder) { registerConsent.Store(&fn) }

func recordConsent(ctx context.Context, tx *store.Tx, userID, policyURL, ip string) error {
	if fn := registerConsent.Load(); fn != nil && *fn != nil {
		return (*fn)(ctx, tx, userID, policyURL, ip)
	}
	return nil
}

// privacyPolicyURL is branding.legal.privacy_url; when set, self-registration
// requires accept_privacy = true.
func (m *Module) privacyPolicyURL(ctx context.Context) string {
	if m.d.Settings == nil {
		return ""
	}
	b, err := m.d.Settings.Branding(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(b.Legal.PrivacyURL)
}
