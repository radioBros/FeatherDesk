// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ---- GET /admin/audit-log (listAuditLog) ----

type auditActor struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	AvatarURL *string `json:"avatar_url"`
}

type auditEntry struct {
	ID         string         `json:"id"`
	Actor      *auditActor    `json:"actor"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   *string        `json:"target_id"`
	Data       map[string]any `json:"data"`
	IP         *string        `json:"ip"`
	CreatedAt  time.Time      `json:"created_at"`
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// auditLog pages audit_log newest first (ids are UUIDv7, so id order is time
// order); cursor = base64(id). Filters: action (exact, or a prefix ending in
// "."), actor_id, since.
func (m *coreModule) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var fe []problems.FieldError
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fe = append(fe, problems.Field("limit", "range", "1..200"))
		}
		limit = n
	}
	where, args := "1=1", []any{}
	if a := strings.TrimSpace(q.Get("action")); a != "" {
		if strings.HasSuffix(a, ".") {
			where += " AND a.action LIKE ? ESCAPE '\\'"
			args = append(args, strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(a)+"%")
		} else {
			where += " AND a.action = ?"
			args = append(args, a)
		}
	}
	if id := q.Get("actor_id"); id != "" {
		where += " AND a.actor_id = ?"
		args = append(args, id)
	}
	if s := q.Get("since"); s != "" {
		ts, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			fe = append(fe, problems.Field("since", "date_time", "RFC 3339 timestamp"))
		} else {
			where += " AND a.created_at >= ?"
			args = append(args, ts.UTC())
		}
	}
	if c := q.Get("cursor"); c != "" {
		id, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil || len(id) == 0 {
			fe = append(fe, problems.Field("cursor", "invalid", ""))
		} else {
			where += " AND a.id < ?"
			args = append(args, string(id))
		}
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	args = append(args, limit+1)
	rows, err := m.d.DB.Read().QueryContext(r.Context(), `SELECT a.id, a.actor_id, u.name, u.kind, a.action, a.target_type, a.target_id, a.data, a.ip, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id WHERE `+where+` ORDER BY a.id DESC LIMIT ?`, args...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	items := []auditEntry{}
	for rows.Next() {
		var e auditEntry
		var actorID, name, kind sql.NullString
		var targetID, ip string
		var data store.JSON[map[string]any]
		if err := rows.Scan(&e.ID, &actorID, &name, &kind, &e.Action, &e.TargetType, &targetID, &data, &ip, &e.CreatedAt); err != nil {
			problems.Write(w, r, err)
			return
		}
		if actorID.Valid && actorID.String != "" && kind.Valid {
			e.Actor = &auditActor{ID: actorID.String, Name: name.String, Kind: kind.String}
		}
		e.TargetID, e.IP = nullIfEmpty(targetID), nullIfEmpty(ip)
		e.Data = data.V
		if e.Data == nil {
			e.Data = map[string]any{}
		}
		e.CreatedAt = e.CreatedAt.UTC()
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	var next any
	if len(items) > limit {
		items = items[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(items[limit-1].ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// ---- GET /admin/storage (getStorageStatus) ----

// storage reports the blob backend and its health. Usage comes from the
// database (attachments, KB images and branding assets record their size),
// never from listing the bucket or walking the data directory, so the call
// is cheap on S3 and on large local stores.
func (m *coreModule) storage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	doc := map[string]any{"backend": m.d.Blob.Backend(), "bucket": nil, "endpoint": nil, "used_bytes": nil, "object_count": nil, "ok": true, "last_error": nil}
	if s3, ok := m.d.Blob.(*blob.S3); ok {
		doc["bucket"] = s3.Bucket
		if s3.Endpoint != nil {
			doc["endpoint"] = s3.Endpoint.Scheme + "://" + s3.Endpoint.Host
		}
	}
	if used, count, err := blobUsage(ctx, m.d.DB); err == nil {
		doc["used_bytes"], doc["object_count"] = used, count
	}
	if _, err := m.d.Blob.Stat(ctx, "readyz-probe"); err != nil && !errors.Is(err, blob.ErrNotFound) {
		doc["ok"], doc["last_error"] = false, err.Error()
	}
	writeJSON(w, http.StatusOK, doc)
}

func blobUsage(ctx context.Context, db *store.DB) (used, count int64, err error) {
	// SUM over BIGINT is NUMERIC on Postgres: cast back for the scan.
	err = db.Read().QueryRowContext(ctx, `SELECT
		CAST(COALESCE((SELECT SUM(size_bytes) FROM attachments), 0) + COALESCE((SELECT SUM(size_bytes) FROM kb_assets), 0) + COALESCE((SELECT SUM(size_bytes) FROM branding_assets), 0) AS BIGINT),
		CAST((SELECT COUNT(*) FROM attachments) + (SELECT COUNT(*) FROM kb_assets) + (SELECT COUNT(*) FROM branding_assets) AS BIGINT)`).Scan(&used, &count)
	return used, count, err
}
