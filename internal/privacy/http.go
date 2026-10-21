// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeOptional decodes a JSON body into dst; an empty body leaves dst as is.
func decodeOptional(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return problems.BadRequest("unreadable body")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return problems.Validation(problems.Field("body", "invalid_json", "malformed JSON body"))
	}
	return nil
}

func decodeRequired(r *http.Request, dst any) error {
	if r.Body == nil {
		return problems.Validation(problems.Field("body", "required", "request body required"))
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return problems.BadRequest("unreadable body")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return problems.Validation(problems.Field("body", "required", "request body required"))
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return problems.Validation(problems.Field("body", "invalid_json", "malformed JSON body"))
	}
	return nil
}

func principal(r *http.Request) *domain.Principal { return server.PrincipalFrom(r.Context()) }

func (m *Module) audit(ctx context.Context, e authn.AuditEntry) error {
	return m.d.DB.Tx(ctx, func(tx *store.Tx) error { return authn.Audit(ctx, tx, e) })
}

// ---- self-service ----

type myPrivacy struct {
	Requests          []Request `json:"requests"`
	Consents          []Consent `json:"consents"`
	CanRequestErasure bool      `json:"can_request_erasure"`
	ExportDays        int       `json:"export_days"`
}

func (m *Module) handleMyPrivacy(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	now := store.Now()
	out := myPrivacy{Requests: []Request{}}
	pendingErasure := false
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT `+reqCols+reqFrom+` WHERE r.user_id = ? AND (r.type = 'erasure' OR r.requested_by = ?)
		ORDER BY r.created_at DESC, r.id DESC LIMIT 20`, p.UserID, p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	for rows.Next() {
		rr, err := scanReq(rows)
		if err != nil {
			rows.Close()
			problems.Write(w, r, err)
			return
		}
		if rr.Type == TypeErasure && rr.Status == StatusPending {
			pendingErasure = true
		}
		out.Requests = append(out.Requests, rr.dto(false, now))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	if out.Consents, err = m.consents(ctx, p.UserID); err != nil {
		problems.Write(w, r, err)
		return
	}
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	out.ExportDays = set.Retention.ExportDays
	out.CanRequestErasure = p.Kind == domain.KindCustomer && !pendingErasure
	writeJSON(w, http.StatusOK, out)
}

// createExport inserts a pending export and queues it. 409 while one of the
// same subject (and, for self-service, the same requester) is unfinished.
func (m *Module) createExport(ctx context.Context, userID, requestedBy string, internal bool, self bool, ip string) (Request, error) {
	id := store.NewID()
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		q := `SELECT COUNT(*) FROM privacy_requests WHERE user_id = ? AND type = 'export' AND status IN ('pending', 'processing')`
		args := []any{userID}
		if self {
			q += ` AND requested_by = ?`
			args = append(args, requestedBy)
		}
		var n int
		if err := tx.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("an export is already being prepared")
		}
		now := store.Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO privacy_requests (id, user_id, type, status, requested_by, include_internal, created_at, updated_at)
			VALUES (?, ?, 'export', 'pending', ?, ?, ?, ?)`, id, userID, requestedBy, internal, now, now); err != nil {
			return err
		}
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: requestedBy, Action: "privacy.export_requested", TargetType: "user", TargetID: userID, IP: ip,
			Data: map[string]any{"request_id": id, "include_internal": internal}})
	})
	if err != nil {
		return Request{}, err
	}
	m.enqueueExport(ctx, id)
	rr, err := getReq(ctx, m.d.DB.Read(), id)
	if err != nil {
		return Request{}, err
	}
	return rr.dto(!self, store.Now()), nil
}

func (m *Module) handleMyExport(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	out, err := m.createExport(r.Context(), p.UserID, p.UserID, false, true, server.ClientIP(r))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

type erasureCreate struct {
	Reason string `json:"reason"`
}

func (m *Module) handleMyErasure(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	if p.Kind != domain.KindCustomer {
		problems.Write(w, r, problems.Forbidden("only customers can request the deletion of their account; ask an admin"))
		return
	}
	var in erasureCreate
	if err := decodeOptional(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if utf8.RuneCountInString(in.Reason) > 1000 {
		problems.Write(w, r, problems.Validation(problems.Field("reason", "max_length", "at most 1000 characters")))
		return
	}
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	id := store.NewID()
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM privacy_requests WHERE user_id = ? AND type = 'erasure' AND status = 'pending'`, p.UserID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return problems.Conflict("a deletion request is already pending")
		}
		now := store.Now()
		var auto any
		if set.ErasureAutoApproveDays > 0 {
			auto = now.Add(time.Duration(set.ErasureAutoApproveDays) * 24 * time.Hour)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO privacy_requests (id, user_id, type, status, requested_by, reason, expires_at, created_at, updated_at)
			VALUES (?, ?, 'erasure', 'pending', ?, ?, ?, ?, ?)`, id, p.UserID, p.UserID, in.Reason, auto, now, now); err != nil {
			return err
		}
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: p.UserID, Action: "privacy.erasure_requested", TargetType: "user", TargetID: p.UserID,
			IP: server.ClientIP(r), Data: map[string]any{"request_id": id}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	rr, err := getReq(ctx, m.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, rr.dto(false, store.Now()))
}

func (m *Module) handleMyDownload(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rr, err := getReq(r.Context(), m.d.DB.Read(), chi.URLParam(r, "id"))
	if err != nil || rr.UserID != p.UserID || !rr.RequestedBy.Valid || rr.RequestedBy.String != p.UserID {
		problems.Write(w, r, problems.NotFound())
		return
	}
	m.serveExport(w, r, rr)
}

// serveExport streams a ready ZIP (404 once expired).
func (m *Module) serveExport(w http.ResponseWriter, r *http.Request, rr reqRow) {
	ctx, p := r.Context(), principal(r)
	if !rr.readyAt(store.Now()) {
		problems.Write(w, r, problems.NotFound())
		return
	}
	rc, info, err := m.d.Blob.Open(ctx, rr.FileKey)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rc.Close()
	if err := m.audit(ctx, authn.AuditEntry{ActorID: p.UserID, Action: "privacy.export_downloaded", TargetType: "user", TargetID: rr.UserID,
		IP: server.ClientIP(r), Data: map[string]any{"request_id": rr.ID}}); err != nil {
		problems.Write(w, r, err)
		return
	}
	name := "data-export-" + rr.CreatedAt.UTC().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if info.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// ---- admin ----

type requestPage struct {
	Items      []Request `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

func (m *Module) handleListRequests(w http.ResponseWriter, r *http.Request) {
	ctx, qv := r.Context(), r.URL.Query()
	limit := 50
	var fe []problems.FieldError
	if v := qv.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fe = append(fe, problems.Field("limit", "range", "1 to 200"))
		}
		limit = n
	}
	where, args := `1 = 1`, []any{}
	if t := qv.Get("type"); t != "" {
		if t != TypeExport && t != TypeErasure {
			fe = append(fe, problems.Field("type", "enum", "export or erasure"))
		}
		where += ` AND r.type = ?`
		args = append(args, t)
	}
	if s := qv.Get("status"); s != "" {
		switch s {
		case StatusPending, StatusProcessing, StatusReady, StatusFailed, StatusExpired, StatusCompleted, StatusRejected:
		default:
			fe = append(fe, problems.Field("status", "enum", ""))
		}
		where += ` AND r.status = ?`
		args = append(args, s)
	}
	if u := qv.Get("user_id"); u != "" {
		where += ` AND r.user_id = ?`
		args = append(args, u)
	}
	if c := qv.Get("cursor"); c != "" {
		where += ` AND r.id < ?`
		args = append(args, c)
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	now := store.Now()
	out := requestPage{Items: []Request{}}
	rows, err := m.d.DB.Read().QueryContext(ctx, `SELECT `+reqCols+reqFrom+` WHERE `+where+` ORDER BY r.id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		rr, err := scanReq(rows)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		out.Items = append(out.Items, rr.dto(true, now))
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := out.Items[limit-1].ID
		out.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, out)
}

type modeBody struct {
	Mode string `json:"mode"`
}

func readMode(r *http.Request) (string, error) {
	var in modeBody
	if err := decodeRequired(r, &in); err != nil {
		return "", err
	}
	if in.Mode == "" {
		return "", problems.Validation(problems.Field("mode", "required", "anonymize or anonymize_and_redact"))
	}
	if !validMode(in.Mode) {
		return "", problems.Validation(problems.Field("mode", "enum", "anonymize or anonymize_and_redact"))
	}
	return in.Mode, nil
}

func (m *Module) handleApprove(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	rr, err := getReq(ctx, m.d.DB.Read(), chi.URLParam(r, "id"))
	if err != nil || rr.Type != TypeErasure {
		problems.Write(w, r, problems.NotFound())
		return
	}
	mode, err := readMode(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if rr.Status != StatusPending {
		problems.Write(w, r, problems.Conflict("the request is no longer pending"))
		return
	}
	res, err := m.Erase(ctx, EraseInput{UserID: rr.UserID, Mode: mode, ActorID: p.UserID, IP: server.ClientIP(r), RequestID: rr.ID})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type rejectBody struct {
	Reason string `json:"reason"`
}

func (m *Module) handleReject(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	id := chi.URLParam(r, "id")
	rr, err := getReq(ctx, m.d.DB.Read(), id)
	if err != nil || rr.Type != TypeErasure {
		problems.Write(w, r, problems.NotFound())
		return
	}
	var in rejectBody
	if err := decodeRequired(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(in.Reason); n < 1 || n > 1000 {
		code := "length"
		if n == 0 {
			code = "required"
		}
		problems.Write(w, r, problems.Validation(problems.Field("reason", code, "1 to 1000 characters")))
		return
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		now := store.Now()
		res, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'rejected', reason = ?, decided_by = ?, decided_at = ?, updated_at = ?
			WHERE id = ? AND type = 'erasure' AND status = 'pending'`, in.Reason, p.UserID, now, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.Conflict("the request is no longer pending")
		}
		return authn.Audit(ctx, tx, authn.AuditEntry{ActorID: p.UserID, Action: "privacy.erasure_rejected", TargetType: "user", TargetID: rr.UserID,
			IP: server.ClientIP(r), Data: map[string]any{"request_id": id}})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := m.loadRequest(ctx, m.d.DB.Read(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleAdminDownload(w http.ResponseWriter, r *http.Request) {
	rr, err := getReq(r.Context(), m.d.DB.Read(), chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, problems.NotFound())
		return
	}
	m.serveExport(w, r, rr)
}

type adminExportBody struct {
	IncludeInternal bool `json:"include_internal"`
}

func (m *Module) handleAdminExport(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	id := chi.URLParam(r, "id")
	if _, err := loadSubject(ctx, m.d.DB.Read(), id); err != nil {
		problems.Write(w, r, err)
		return
	}
	var in adminExportBody
	if err := decodeOptional(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	out, err := m.createExport(ctx, id, p.UserID, in.IncludeInternal, false, server.ClientIP(r))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

func (m *Module) handleErase(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	id := chi.URLParam(r, "id")
	if _, err := loadSubject(ctx, m.d.DB.Read(), id); err != nil {
		problems.Write(w, r, err)
		return
	}
	mode, err := readMode(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	res, err := m.Erase(ctx, EraseInput{UserID: id, Mode: mode, ActorID: p.UserID, IP: server.ClientIP(r)})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (m *Module) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	set, err := LoadSettings(r.Context(), m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (m *Module) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx, p := r.Context(), principal(r)
	var in Settings
	if err := decodeRequired(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	in.PolicyVersion = strings.TrimSpace(in.PolicyVersion)
	if fe := in.validate(); len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	if m.d.Settings == nil {
		problems.Write(w, r, problems.Internal(errors.New("privacy: no settings service")))
		return
	}
	if err := saveSettings(ctx, m.d.Settings, in, p.UserID); err != nil {
		problems.Write(w, r, err)
		return
	}
	if err := m.audit(ctx, authn.AuditEntry{ActorID: p.UserID, Action: "privacy.settings_updated", TargetType: "settings", TargetID: KeyRetention,
		IP: server.ClientIP(r), Data: map[string]any{"closed_tickets_months": in.Retention.ClosedTicketsMonths, "closed_tickets_action": in.Retention.ClosedTicketsAction,
			"inbound_email_days": in.Retention.InboundEmailDays, "audit_log_months": in.Retention.AuditLogMonths, "export_days": in.Retention.ExportDays,
			"erasure_auto_approve_days": in.ErasureAutoApproveDays}}); err != nil {
		problems.Write(w, r, err)
		return
	}
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (m *Module) handlePreview(w http.ResponseWriter, r *http.Request) {
	ctx, qv := r.Context(), r.URL.Query()
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var fe []problems.FieldError
	intParam := func(name string, dst *int, lo, hi int) {
		v := qv.Get(name)
		if v == "" {
			return
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			fe = append(fe, problems.Field(name, "range", strconv.Itoa(lo)+" to "+strconv.Itoa(hi)))
			return
		}
		*dst = n
	}
	rt := &set.Retention
	intParam("closed_tickets_months", &rt.ClosedTicketsMonths, 0, 240)
	intParam("inbound_email_days", &rt.InboundEmailDays, 1, 90)
	intParam("audit_log_months", &rt.AuditLogMonths, 0, 120)
	intParam("export_days", &rt.ExportDays, 1, 30)
	if a := qv.Get("closed_tickets_action"); a != "" {
		if a != ActionPurge && a != ActionAnonymize {
			fe = append(fe, problems.Field("closed_tickets_action", "enum", "purge or anonymize"))
		}
		rt.ClosedTicketsAction = a
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	p, err := m.PreviewRetention(ctx, set, store.Now())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
