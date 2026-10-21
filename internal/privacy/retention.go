// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Retention (D-19 §C). The daily job and the dry-run preview share one rule
// builder, so the preview count is exactly what the next run processes.

// Preview holds per-rule counts (dry run) or processed counts (a run).
type Preview struct {
	ClosedTickets        int64     `json:"closed_tickets"`
	ClosedTicketsAction  string    `json:"closed_tickets_action"`
	InboundEmails        int64     `json:"inbound_emails"`
	AuditEntries         int64     `json:"audit_entries"`
	ExpiredExports       int64     `json:"expired_exports"`
	ErasureAutoApprovals int64     `json:"erasure_auto_approvals"`
	AsOf                 time.Time `json:"as_of"`
}

// rule is one WHERE clause over one table; off rules match nothing.
type rule struct {
	table, where string
	args         []any
	off          bool
}

type rules struct {
	tickets, inbound, audit, exports, approvals rule
	auditCutoff                                 time.Time
}

const retentionBatch = 100

func buildRules(set Settings, now time.Time) rules {
	r := set.Retention
	now = now.UTC()
	var out rules
	tw := `status = 'closed' AND closed_at IS NOT NULL AND closed_at < ?`
	if r.ClosedTicketsAction == ActionAnonymize {
		tw += ` AND anonymized_at IS NULL`
	}
	out.tickets = rule{table: "tickets", where: tw, args: []any{now.AddDate(0, -r.ClosedTicketsMonths, 0)}, off: r.ClosedTicketsMonths <= 0}
	out.inbound = rule{table: "inbound_messages", where: `received_at < ? AND status <> 'quarantined'`, args: []any{now.AddDate(0, 0, -r.InboundEmailDays)}}
	out.auditCutoff = now.AddDate(0, -r.AuditLogMonths, 0)
	out.audit = rule{table: "audit_log", where: `created_at < ?`, args: []any{out.auditCutoff}, off: r.AuditLogMonths <= 0}
	out.exports = rule{table: "privacy_requests", where: `type = 'export' AND status = 'ready' AND expires_at <= ?`, args: []any{now}}
	out.approvals = rule{table: "privacy_requests", where: `type = 'erasure' AND status = 'pending' AND created_at <= ?`,
		args: []any{now.Add(-time.Duration(set.ErasureAutoApproveDays) * 24 * time.Hour)}, off: set.ErasureAutoApproveDays <= 0}
	return out
}

func count(ctx context.Context, q store.Querier, r rule) (int64, error) {
	if r.off {
		return 0, nil
	}
	var n int64
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+r.table+` WHERE `+r.where, r.args...).Scan(&n)
	return n, err
}

// PreviewRetention counts what a run with set would process at now.
func (m *Module) PreviewRetention(ctx context.Context, set Settings, now time.Time) (Preview, error) {
	rs := buildRules(set, now)
	p := Preview{ClosedTicketsAction: set.Retention.ClosedTicketsAction, AsOf: now.UTC()}
	q := m.d.DB.Read()
	for _, c := range []struct {
		r   rule
		dst *int64
	}{{rs.tickets, &p.ClosedTickets}, {rs.inbound, &p.InboundEmails}, {rs.audit, &p.AuditEntries}, {rs.exports, &p.ExpiredExports}, {rs.approvals, &p.ErasureAutoApprovals}} {
		n, err := count(ctx, q, c.r)
		if err != nil {
			return p, err
		}
		*c.dst = n
	}
	return p, nil
}

// RunRetention applies the saved policy now (the daily job).
func (m *Module) RunRetention(ctx context.Context) (Preview, error) {
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		return Preview{}, err
	}
	now := store.Now()
	rs := buildRules(set, now)
	p := Preview{ClosedTicketsAction: set.Retention.ClosedTicketsAction, AsOf: now.UTC()}

	// Exports first: cheap, and they hold the most concentrated PII.
	var keys []string
	if err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		k, err := expireExports(ctx, tx, `status = 'ready' AND expires_at <= ?`, now, now)
		keys = k
		return err
	}); err != nil {
		return p, err
	}
	p.ExpiredExports = int64(len(keys))
	m.deleteBlobs(ctx, keys)

	if !rs.approvals.off {
		ids, err := queryStrings(ctx, m.d.DB.Read(), `SELECT id FROM privacy_requests WHERE `+rs.approvals.where+` ORDER BY created_at`, rs.approvals.args...)
		if err != nil {
			return p, err
		}
		for _, id := range ids {
			r, err := getReq(ctx, m.d.DB.Read(), id)
			if err != nil {
				continue
			}
			if _, err := m.Erase(ctx, EraseInput{UserID: r.UserID, Mode: set.ErasureAutoApproveMode, RequestID: id}); err != nil {
				m.log.Warn("privacy: auto-approval failed", "request", id, "err", err)
				continue
			}
			p.ErasureAutoApprovals++
		}
	}

	if !rs.tickets.off {
		n, err := m.retainTickets(ctx, rs.tickets, set.Retention.ClosedTicketsAction, now)
		p.ClosedTickets = n
		if err != nil {
			return p, err
		}
	}

	n, err := m.pruneInbound(ctx, rs.inbound)
	p.InboundEmails = n
	if err != nil {
		return p, err
	}

	if !rs.audit.off {
		if p.AuditEntries, err = store.PruneAuditLog(ctx, m.d.DB, now, now.Sub(rs.auditCutoff)); err != nil {
			return p, err
		}
	}

	if p.ClosedTickets+p.InboundEmails+p.AuditEntries+p.ExpiredExports+p.ErasureAutoApprovals > 0 {
		err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			return authn.Audit(ctx, tx, authn.AuditEntry{Action: "privacy.retention_run", TargetType: "system",
				Data: map[string]any{"closed_tickets": p.ClosedTickets, "closed_tickets_action": p.ClosedTicketsAction, "inbound_emails": p.InboundEmails,
					"audit_entries": p.AuditEntries, "expired_exports": p.ExpiredExports, "erasure_auto_approvals": p.ErasureAutoApprovals}})
		})
		m.log.Info("privacy: retention applied", "closed_tickets", p.ClosedTickets, "action", p.ClosedTicketsAction, "inbound", p.InboundEmails,
			"audit", p.AuditEntries, "exports", p.ExpiredExports, "auto_approvals", p.ErasureAutoApprovals)
	}
	return p, err
}

// retainTickets purges or anonymizes matching closed tickets in batches.
func (m *Module) retainTickets(ctx context.Context, r rule, action string, now time.Time) (int64, error) {
	var total int64
	for {
		ids, err := queryStrings(ctx, m.d.DB.Read(), `SELECT id FROM tickets WHERE `+r.where+` ORDER BY closed_at, id LIMIT ?`, append(append([]any{}, r.args...), retentionBatch)...)
		if err != nil || len(ids) == 0 {
			return total, err
		}
		var keys []string
		err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			var candidates []string
			for _, id := range ids {
				k, err := queryStrings(ctx, tx, `SELECT storage_key FROM attachments WHERE ticket_id = ?`, id)
				if err != nil {
					return err
				}
				candidates = append(candidates, k...)
				inb, err := scrubInbound(ctx, tx, `ticket_id = ?`, true, id)
				if err != nil {
					return err
				}
				keys = append(keys, inb...)
				if action == ActionPurge {
					err = purgeTicket(ctx, tx, id)
				} else {
					err = anonymizeTicket(ctx, tx, id, now)
				}
				if err != nil {
					return err
				}
			}
			k, err := unreferenced(ctx, tx, candidates)
			keys = append(keys, k...)
			return err
		})
		if err != nil {
			return total, err
		}
		total += int64(len(ids))
		m.deleteBlobs(ctx, keys)
		if action == ActionAnonymize {
			m.reindex(ctx, ids)
		}
		if len(ids) < retentionBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// purgeTicket hard-deletes one ticket like TKT-031 (events through purge_allow).
func purgeTicket(ctx context.Context, tx *store.Tx, id string) error {
	for _, st := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO purge_allow (tbl, key) VALUES ('ticket_events', ?) ON CONFLICT DO NOTHING`, []any{id}},
		{`DELETE FROM ticket_events WHERE ticket_id = ?`, []any{id}},
		{`DELETE FROM purge_allow WHERE tbl = 'ticket_events' AND key = ?`, []any{id}},
		{`UPDATE tickets SET followup_of_id = NULL WHERE followup_of_id = ?`, []any{id}},
		{`UPDATE ticket_messages SET split_to_ticket_id = NULL WHERE split_to_ticket_id = ?`, []any{id}},
		{`DELETE FROM tickets WHERE id = ?`, []any{id}},
	} {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return store.UnindexTicket(ctx, tx, id)
}

// anonymizeTicket keeps the ticket's metadata (numbers, dates, status, team,
// requester id) for reports and removes its content.
func anonymizeTicket(ctx context.Context, tx *store.Tx, id string, now time.Time) error {
	for _, st := range []struct {
		q    string
		args []any
	}{
		{`UPDATE tickets SET subject = ?, csat_comment = NULL, anonymized_at = ? WHERE id = ?`, []any{redacted, now, id}},
		{`UPDATE ticket_messages SET body_md = ? WHERE ticket_id = ?`, []any{redacted, id}},
		{`DELETE FROM attachments WHERE ticket_id = ?`, []any{id}},
		{`DELETE FROM ticket_field_values WHERE ticket_id = ?`, []any{id}},
	} {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return err
		}
	}
	return nil
}

// pruneInbound deletes matching inbound log rows and their raw mail.
func (m *Module) pruneInbound(ctx context.Context, r rule) (int64, error) {
	var total int64
	for {
		var keys []string
		var n int
		err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
			ids, err := queryStrings(ctx, tx, `SELECT id FROM inbound_messages WHERE `+r.where+` ORDER BY received_at, id LIMIT ?`, append(append([]any{}, r.args...), store.PruneBatch)...)
			if err != nil {
				return err
			}
			n = len(ids)
			for _, id := range ids {
				k, err := queryStrings(ctx, tx, `SELECT raw_blob_key FROM inbound_messages WHERE id = ?`, id)
				if err != nil {
					return err
				}
				keys = append(keys, k...)
				if _, err := tx.ExecContext(ctx, `DELETE FROM inbound_messages WHERE id = ?`, id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += int64(n)
		m.deleteBlobs(ctx, keys)
		if n < store.PruneBatch {
			return total, nil
		}
	}
}
