// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"database/sql"
	"time"

	"github.com/radioBros/FeatherDesk/internal/store"
)

// UserRef is the subject as they are now.
type UserRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

// Request is the API shape of a privacy_requests row.
type Request struct {
	ID              string     `json:"id"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	UserID          string     `json:"user_id"`
	User            *UserRef   `json:"user,omitempty"`
	RequestedBy     *string    `json:"requested_by"`
	IncludeInternal bool       `json:"include_internal"`
	Mode            *string    `json:"mode"`
	FileSize        int64      `json:"file_size"`
	Reason          string     `json:"reason"`
	Error           string     `json:"error"`
	DecidedBy       *string    `json:"decided_by"`
	DecidedAt       *time.Time `json:"decided_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	DownloadURL     *string    `json:"download_url"`
	CreatedAt       time.Time  `json:"created_at"`
}

// reqRow is a scanned row plus what the job and the handlers need.
type reqRow struct {
	ID, Type, Status, UserID string
	RequestedBy, Mode        sql.NullString
	DecidedBy                sql.NullString
	IncludeInternal          bool
	FileKey                  string
	FileSize                 int64
	Reason, Error            string
	DecidedAt, CompletedAt   sql.NullTime
	ExpiresAt                sql.NullTime
	CreatedAt                time.Time
	// joined subject
	subjectName, subjectEmail, subjectKind, subjectStatus string
}

const reqCols = `r.id, r.type, r.status, r.user_id, r.requested_by, r.mode, r.decided_by, r.include_internal, r.file_key, r.file_size,
	r.reason, r.error, r.decided_at, r.completed_at, r.expires_at, r.created_at, u.name, u.email, u.kind, u.status`

const reqFrom = ` FROM privacy_requests r JOIN users u ON u.id = r.user_id`

func scanReq(sc interface{ Scan(...any) error }) (reqRow, error) {
	var r reqRow
	err := sc.Scan(&r.ID, &r.Type, &r.Status, &r.UserID, &r.RequestedBy, &r.Mode, &r.DecidedBy, &r.IncludeInternal, &r.FileKey, &r.FileSize,
		&r.Reason, &r.Error, &r.DecidedAt, &r.CompletedAt, &r.ExpiresAt, &r.CreatedAt, &r.subjectName, &r.subjectEmail, &r.subjectKind, &r.subjectStatus)
	return r, err
}

func getReq(ctx context.Context, q store.Querier, id string) (reqRow, error) {
	r, err := scanReq(q.QueryRowContext(ctx, `SELECT `+reqCols+reqFrom+` WHERE r.id = ?`, id))
	return r, store.NotFound(err)
}

func optStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

func optTime(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time.UTC()
	return &t
}

// readyAt reports whether an export can be downloaded at now.
func (r reqRow) readyAt(now time.Time) bool {
	return r.Type == TypeExport && r.Status == StatusReady && r.FileKey != "" && r.ExpiresAt.Valid && now.Before(r.ExpiresAt.Time)
}

// dto renders the row; admin selects the download route and adds the subject.
func (r reqRow) dto(admin bool, now time.Time) Request {
	out := Request{ID: r.ID, Type: r.Type, Status: r.Status, UserID: r.UserID, RequestedBy: optStr(r.RequestedBy),
		IncludeInternal: r.IncludeInternal, Mode: optStr(r.Mode), FileSize: r.FileSize, Reason: r.Reason, Error: r.Error,
		DecidedBy: optStr(r.DecidedBy), DecidedAt: optTime(r.DecidedAt), CompletedAt: optTime(r.CompletedAt),
		ExpiresAt: optTime(r.ExpiresAt), CreatedAt: r.CreatedAt.UTC()}
	if r.readyAt(now) {
		u := "/api/v1/me/privacy/exports/" + r.ID
		if admin {
			u = "/api/v1/admin/privacy/exports/" + r.ID
		}
		out.DownloadURL = &u
	} else if r.Status == StatusReady && r.Type == TypeExport {
		out.Status = StatusExpired // past expires_at, the daily job has not run yet
	}
	if admin {
		out.User = &UserRef{ID: r.UserID, Name: r.subjectName, Email: r.subjectEmail, Kind: r.subjectKind, Status: r.subjectStatus}
	}
	return out
}

func (m *Module) loadRequest(ctx context.Context, q store.Querier, id string) (Request, error) {
	r, err := getReq(ctx, q, id)
	if err != nil {
		return Request{}, err
	}
	return r.dto(true, store.Now()), nil
}
