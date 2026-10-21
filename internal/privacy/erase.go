// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ErasureResult is the API shape of an erasure.
type ErasureResult struct {
	UserID              string  `json:"user_id"`
	Mode                string  `json:"mode"`
	Name                string  `json:"name"`
	TicketsKept         int64   `json:"tickets_kept"`
	MessagesRedacted    int64   `json:"messages_redacted"`
	AttachmentsDeleted  int64   `json:"attachments_deleted"`
	FieldValuesRedacted int64   `json:"field_values_redacted"`
	Request             Request `json:"request"`
}

// optionalUserTables hold per-user rows of features built in parallel
// (D-19 §B MFA, §D notification centre). They are cleared when present.
var optionalUserTables = []string{"user_mfa", "mfa_recovery_codes", "mfa_challenges", "notifications"}

// EraseInput describes one erasure.
type EraseInput struct {
	UserID    string
	Mode      string
	ActorID   string // "" = the system (auto-approval)
	IP        string
	RequestID string // the pending erasure request being approved, if any
}

// ErasedEmail is the address an erased user keeps (unique, undeliverable).
func ErasedEmail(userID string) string { return "erased+" + userID + "@invalid" }

// IsErasedEmail reports whether the address is an erasure placeholder.
func IsErasedEmail(email string) bool {
	return strings.HasPrefix(email, "erased+") && strings.HasSuffix(email, "@invalid")
}

type subject struct {
	ID, Kind, Role, Status, Email, Name string
	BreakGlass                          bool
}

func loadSubject(ctx context.Context, q store.Querier, id string) (subject, error) {
	var s subject
	err := q.QueryRowContext(ctx, `SELECT id, kind, role, status, email, name, break_glass FROM users WHERE id = ?`, id).
		Scan(&s.ID, &s.Kind, &s.Role, &s.Status, &s.Email, &s.Name, &s.BreakGlass)
	return s, store.NotFound(err)
}

// Erase anonymizes (and in redact mode redacts) one user in a single write
// transaction, then deletes the freed blobs and refreshes the search index of
// the touched tickets. Tickets stay: they are business records (D-19 §C).
func (m *Module) Erase(ctx context.Context, in EraseInput) (ErasureResult, error) {
	res := ErasureResult{UserID: in.UserID, Mode: in.Mode}
	if !validMode(in.Mode) {
		return res, problems.Validation(problems.Field("mode", "enum", "anonymize or anonymize_and_redact"))
	}
	var blobKeys []string
	var touched []string
	var reqID string
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		s, err := loadSubject(ctx, tx, in.UserID)
		if err != nil {
			return err
		}
		if err := m.guardErase(ctx, tx, s, in.ActorID); err != nil {
			return err
		}
		now := store.Now()
		if in.RequestID != "" {
			r, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'completed', mode = ?, decided_by = ?, decided_at = ?, completed_at = ?, updated_at = ?
				WHERE id = ? AND user_id = ? AND type = 'erasure' AND status = 'pending'`,
				in.Mode, store.Nullable(in.ActorID), now, now, now, in.RequestID, in.UserID)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n == 0 {
				return problems.Conflict("the request is no longer pending")
			}
			reqID = in.RequestID
		}
		// Display name: keep the number of a user erased before (re-run, e.g.
		// anonymize → anonymize_and_redact).
		name := s.Name
		if !IsErasedEmail(s.Email) {
			var n int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO counters (name, int_value, text_value) VALUES ('erased_user', 1, '')
				ON CONFLICT (name) DO UPDATE SET int_value = counters.int_value + 1 RETURNING int_value`).Scan(&n); err != nil {
				return err
			}
			name = "Deleted user #" + strconv.FormatInt(n, 10)
		}
		res.Name = name
		if s.Status != string(domain.UserDisabled) {
			// I-5: status, sessions, PAT revocation, socket close 4401, audit.
			if err := authn.DisableUser(ctx, tx, s.ID, in.ActorID, "privacy_erasure", in.IP); err != nil {
				return err
			}
		}
		if s.Kind == string(domain.KindStaff) {
			// M-4: tickets they own are unassigned with an event (TKT-017).
			if err := directory.MembershipChanged(ctx, tx, in.ActorID, "", []string{s.ID}); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, email = ?, password_hash = NULL, signature_md = '', notify_prefs = '{}',
			last_login_at = NULL, status = 'disabled', updated_at = ? WHERE id = ?`, name, ErasedEmail(s.ID), now, s.ID); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM user_identities WHERE user_id = ?`,
			`DELETE FROM sessions WHERE user_id = ?`,
			`DELETE FROM api_tokens WHERE user_id = ?`,
			`DELETE FROM auth_tokens WHERE user_id = ?`,
			`DELETE FROM idempotency_keys WHERE principal_id = ?`,
			`DELETE FROM ticket_watchers WHERE user_id = ?`,
			`DELETE FROM saved_views WHERE user_id = ? AND NOT shared`,
			`DELETE FROM team_members WHERE user_id = ?`,
			`UPDATE user_consents SET ip = '' WHERE user_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, s.ID); err != nil {
				return err
			}
		}
		if err := clearOptionalTables(ctx, m.d.DB, tx, s.ID); err != nil {
			return err
		}
		// Raw inbound mail from their address carries the address and body.
		keys, err := scrubInbound(ctx, tx, `lower(from_addr) = lower(?) AND from_addr <> ''`, in.Mode == ModeRedact, s.Email)
		if err != nil {
			return err
		}
		blobKeys = append(blobKeys, keys...)
		// Exports of this user hold their data: delete the files now.
		keys, err = expireExports(ctx, tx, `user_id = ? AND file_key <> ''`, now, s.ID)
		if err != nil {
			return err
		}
		blobKeys = append(blobKeys, keys...)
		// An export still being built would hold pre-erasure data: cancel it
		// (RunExport then drops its file).
		if _, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'failed', error = 'cancelled by an erasure', updated_at = ?
			WHERE user_id = ? AND type = 'export' AND status IN ('pending', 'processing')`, now, s.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE requester_id = ?`, s.ID).Scan(&res.TicketsKept); err != nil {
			return err
		}
		if in.Mode == ModeRedact {
			t, keys, err := m.redactAuthored(ctx, tx, s.ID, &res)
			if err != nil {
				return err
			}
			touched, blobKeys = t, append(blobKeys, keys...)
		}
		if reqID == "" {
			// Complete any pending request of the user; a direct erasure with
			// none records a completed row so the history is in one place.
			r, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'completed', mode = ?, decided_by = ?, decided_at = ?, completed_at = ?, updated_at = ?
				WHERE user_id = ? AND type = 'erasure' AND status = 'pending'`, in.Mode, store.Nullable(in.ActorID), now, now, now, s.ID)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n == 0 {
				reqID = store.NewID()
				if _, err := tx.ExecContext(ctx, `INSERT INTO privacy_requests (id, user_id, type, status, requested_by, mode, decided_by, decided_at, completed_at, created_at, updated_at)
					VALUES (?, ?, 'erasure', 'completed', ?, ?, ?, ?, ?, ?, ?)`, reqID, s.ID, store.Nullable(in.ActorID), in.Mode, store.Nullable(in.ActorID), now, now, now, now); err != nil {
					return err
				}
			} else if err := tx.QueryRowContext(ctx, `SELECT id FROM privacy_requests WHERE user_id = ? AND type = 'erasure' AND status = 'completed' ORDER BY completed_at DESC, id DESC LIMIT 1`, s.ID).Scan(&reqID); err != nil {
				return err
			}
		}
		// No PII: ids, mode and counts only.
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: in.ActorID, Action: "privacy.user_erased", TargetType: "user", TargetID: s.ID, IP: in.IP,
			Data: map[string]any{"mode": in.Mode, "request_id": reqID, "tickets_kept": res.TicketsKept, "messages_redacted": res.MessagesRedacted,
				"attachments_deleted": res.AttachmentsDeleted, "field_values_redacted": res.FieldValuesRedacted}})
	})
	if err != nil {
		return res, err
	}
	m.deleteBlobs(ctx, blobKeys)
	m.reindex(ctx, touched)
	r, err := m.loadRequest(ctx, m.d.DB.Read(), reqID)
	if err != nil {
		return res, err
	}
	res.Request = r
	return res, nil
}

// guardErase refuses the caller themselves, break-glass admins and the last
// active admin (the instance must stay administrable).
func (m *Module) guardErase(ctx context.Context, tx *store.Tx, s subject, actorID string) error {
	if actorID != "" && actorID == s.ID {
		return problems.Conflict("you cannot erase your own account")
	}
	if s.BreakGlass {
		return problems.Conflict("break-glass admins cannot be erased")
	}
	if s.Kind == string(domain.KindStaff) && s.Role == string(domain.RoleAdmin) && s.Status == string(domain.UserActive) {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE kind = 'staff' AND role = 'admin' AND status = 'active' AND id <> ?`, s.ID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return problems.Conflict("the last active admin cannot be erased")
		}
	}
	return nil
}

// redactAuthored is the anonymize_and_redact part: bodies of messages the
// user wrote, their attachments (rows and, once unreferenced, files), and the
// custom-field values and CSAT comments of tickets they requested.
func (m *Module) redactAuthored(ctx context.Context, tx *store.Tx, userID string, res *ErasureResult) (touched, keys []string, err error) {
	add := func(id string) {
		if id != "" && !slices.Contains(touched, id) {
			touched = append(touched, id)
		}
	}
	ids, err := queryStrings(ctx, tx, `SELECT DISTINCT ticket_id FROM ticket_messages WHERE author_id = ? AND body_md <> ?`, userID, redacted)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		add(id)
	}
	r, err := tx.ExecContext(ctx, `UPDATE ticket_messages SET body_md = ? WHERE author_id = ? AND body_md <> ?`, redacted, userID, redacted)
	if err != nil {
		return nil, nil, err
	}
	res.MessagesRedacted, _ = r.RowsAffected()

	type att struct{ id, key, ticket string }
	var atts []att
	rows, err := tx.QueryContext(ctx, `SELECT id, storage_key, COALESCE(ticket_id, '') FROM attachments WHERE uploader_id = ?`, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var a att
		if err := rows.Scan(&a.id, &a.key, &a.ticket); err != nil {
			rows.Close()
			return nil, nil, err
		}
		atts = append(atts, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var candidates []string
	for _, a := range atts {
		if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, a.id); err != nil {
			return nil, nil, err
		}
		add(a.ticket)
		if !slices.Contains(candidates, a.key) {
			candidates = append(candidates, a.key)
		}
	}
	res.AttachmentsDeleted = int64(len(atts))
	if keys, err = unreferenced(ctx, tx, candidates); err != nil {
		return nil, nil, err
	}

	ids, err = queryStrings(ctx, tx, `SELECT DISTINCT ticket_id FROM ticket_field_values WHERE ticket_id IN (SELECT id FROM tickets WHERE requester_id = ?)`, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		add(id)
	}
	r, err = tx.ExecContext(ctx, `DELETE FROM ticket_field_values WHERE ticket_id IN (SELECT id FROM tickets WHERE requester_id = ?)`, userID)
	if err != nil {
		return nil, nil, err
	}
	res.FieldValuesRedacted, _ = r.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET csat_comment = NULL WHERE requester_id = ? AND csat_comment IS NOT NULL`, userID); err != nil {
		return nil, nil, err
	}
	return touched, keys, nil
}

// unreferenced returns the keys no attachment row points at any more (split
// copies share blobs, TKT-031).
func unreferenced(ctx context.Context, q store.Querier, keys []string) ([]string, error) {
	var out []string
	for _, k := range keys {
		if k == "" {
			continue
		}
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE storage_key = ?`, k).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, k)
		}
	}
	return out, nil
}

// scrubInbound clears the sender address (and, when full, the subject) of the
// matching inbound log rows and returns their raw-mail blob keys.
func scrubInbound(ctx context.Context, tx *store.Tx, where string, full bool, args ...any) ([]string, error) {
	keys, err := queryStrings(ctx, tx, `SELECT raw_blob_key FROM inbound_messages WHERE `+where+` AND raw_blob_key <> ''`, args...)
	if err != nil {
		return nil, err
	}
	set := `from_addr = '', raw_blob_key = ''`
	if full {
		set += `, subject = ''`
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inbound_messages SET `+set+` WHERE `+where, args...); err != nil {
		return nil, err
	}
	return keys, nil
}

// expireExports marks matching export rows expired and returns their files.
func expireExports(ctx context.Context, tx *store.Tx, where string, now any, args ...any) ([]string, error) {
	keys, err := queryStrings(ctx, tx, `SELECT file_key FROM privacy_requests WHERE type = 'export' AND `+where, args...)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	a := append([]any{now}, args...)
	_, err = tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'expired', file_key = '', updated_at = ? WHERE type = 'export' AND `+where, a...)
	return keys, err
}

// clearOptionalTables deletes the user's rows from tables that may not exist
// (features shipped by other D-19 sections).
func clearOptionalTables(ctx context.Context, db *store.DB, tx *store.Tx, userID string) error {
	tables, err := store.Tables(ctx, db)
	if err != nil {
		return err
	}
	for _, t := range optionalUserTables {
		if !slices.Contains(tables, t) {
			continue
		}
		cols, err := store.Columns(ctx, db, t)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(cols, func(c store.Column) bool { return c.Name == "user_id" }) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id = ?`, userID); err != nil {
			return err
		}
	}
	return nil
}

func queryStrings(ctx context.Context, q store.Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s.Valid && s.String != "" {
			out = append(out, s.String)
		}
	}
	return out, rows.Err()
}

// deleteBlobs removes files after commit; a missing file is fine.
func (m *Module) deleteBlobs(ctx context.Context, keys []string) {
	ctx = context.WithoutCancel(ctx)
	for _, k := range keys {
		if err := m.d.Blob.Delete(ctx, k); err != nil && !errors.Is(err, blob.ErrNotFound) {
			m.log.Warn("privacy: blob delete failed", "key", k, "err", err)
		}
	}
}

// reindex refreshes the search entries of the touched tickets.
func (m *Module) reindex(ctx context.Context, ids []string) {
	if len(ids) == 0 || m.d.Reindex == nil {
		return
	}
	if err := m.d.Reindex(context.WithoutCancel(ctx), ids); err != nil {
		m.log.Error("privacy: search reindex failed; run `simpletickets reindex`", "tickets", len(ids), "err", err)
	}
}
