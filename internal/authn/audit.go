// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// AuditEntry is one audit_log row (AUTH-021). Data must never carry
// passwords or tokens; keys containing "password" or "token" are dropped.
type AuditEntry struct {
	ActorID    string
	Action     string // e.g. auth.login_success
	TargetType string
	TargetID   string
	Data       map[string]any
	IP         string
}

// Audit inserts e inside tx (append-only table).
func Audit(ctx context.Context, tx *store.Tx, e AuditEntry) error {
	data := map[string]any{}
	for k, v := range e.Data {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || (strings.Contains(lk, "token") && lk != "token_id" && lk != "token_name") {
			continue
		}
		data[k] = v
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.NewID(), store.Nullable(e.ActorID), e.Action, e.TargetType, e.TargetID, store.MustJSON(data), e.IP, store.Now())
	return err
}
