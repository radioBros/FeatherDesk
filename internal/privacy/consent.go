// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// Consent is the API shape of a user_consents row.
type Consent struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	PolicyURL     string    `json:"policy_url"`
	PolicyVersion string    `json:"policy_version"`
	IP            string    `json:"ip"`
	AcceptedAt    time.Time `json:"accepted_at"`
}

// RecordConsent stores the acceptance of the privacy policy inside the
// registration transaction (authn's register hook, wired in app/modules.go).
// The policy version is read through the same transaction, so the hook needs
// no state of its own.
func RecordConsent(ctx context.Context, tx *store.Tx, userID, policyURL, ip string) error {
	o := DefaultOptions()
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, KeyPrivacy).Scan(&raw)
	switch {
	case err == nil:
		_ = json.Unmarshal([]byte(raw), &o)
	case !errors.Is(store.NotFound(err), store.ErrNotFound):
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_consents (id, user_id, kind, policy_url, policy_version, ip, accepted_at) VALUES (?, ?, 'privacy_policy', ?, ?, ?, ?)`,
		store.NewID(), userID, policyURL, o.PolicyVersion, ip, store.Now())
	return err
}

func (m *Module) consents(ctx context.Context, userID string) ([]Consent, error) {
	out := []Consent{}
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT id, kind, policy_url, policy_version, ip, accepted_at FROM user_consents WHERE user_id = ? ORDER BY accepted_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Consent
		if err := rows.Scan(&c.ID, &c.Kind, &c.PolicyURL, &c.PolicyVersion, &c.IP, &c.AcceptedAt); err != nil {
			return nil, err
		}
		c.AcceptedAt = c.AcceptedAt.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}
