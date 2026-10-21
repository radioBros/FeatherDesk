// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// webhookDTO is the Webhook / WebhookCreated schema.
type webhookDTO struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	URL                 string    `json:"url"`
	Events              []string  `json:"events"`
	Active              bool      `json:"active"`
	IncludeInternal     bool      `json:"include_internal"`
	SecretSet           bool      `json:"secret_set"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	DisabledReason      *string   `json:"disabled_reason"`
	ProjectIDs          []string  `json:"project_ids"` // D-20: [] = every project
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Secret              string    `json:"secret,omitempty"` // only on create / rotate
}

const selectWebhook = `SELECT id, name, url, secret_enc, events, active, include_internal, consecutive_failures, disabled_reason, created_at, updated_at, project_ids FROM webhooks`

func scanDTO(row interface{ Scan(...any) error }) (webhookDTO, error) {
	var d webhookDTO
	var enc, reason string
	var evs, projects store.JSON[[]string]
	if err := row.Scan(&d.ID, &d.Name, &d.URL, &enc, &evs, &d.Active, &d.IncludeInternal, &d.ConsecutiveFailures, &reason, &d.CreatedAt, &d.UpdatedAt, &projects); err != nil {
		return d, err
	}
	d.Events, d.ProjectIDs = evs.V, projects.V
	if d.ProjectIDs == nil {
		d.ProjectIDs = []string{}
	}
	if d.Events == nil {
		d.Events = []string{}
	}
	d.SecretSet = enc != ""
	if reason != "" {
		d.DisabledReason = &reason
	}
	d.CreatedAt, d.UpdatedAt = d.CreatedAt.UTC(), d.UpdatedAt.UTC()
	return d, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Module) load(ctx context.Context, id string) (webhookDTO, error) {
	d, err := scanDTO(m.db.Read().QueryRowContext(ctx, selectWebhook+` WHERE id = ?`, id))
	if err != nil {
		return d, store.NotFound(err)
	}
	return d, nil
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	rows, err := m.db.Read().QueryContext(r.Context(), selectWebhook+` ORDER BY created_at, id`)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	out := []webhookDTO{}
	for rows.Next() {
		d, err := scanDTO(rows)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	d, err := m.load(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type webhookInput struct {
	Name            *string   `json:"name"`
	URL             *string   `json:"url"`
	Events          *[]string `json:"events"`
	Active          *bool     `json:"active"`
	IncludeInternal *bool     `json:"include_internal"`
	RotateSecret    bool      `json:"rotate_secret"`
	ProjectIDs      *[]string `json:"project_ids"` // D-20: ids or keys; [] = every project
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return problems.PayloadTooLarge()
		}
		return problems.BadRequest("invalid JSON body")
	}
	return nil
}

// validate checks the input; create requires url and events.
func (m *Module) validate(ctx context.Context, in webhookInput, create bool) error {
	var fields []problems.FieldError
	if in.URL == nil {
		if create {
			fields = append(fields, problems.Field("url", "required", "URL is required"))
		}
	} else if code, err := m.guard.CheckURL(ctx, *in.URL); code != "" {
		fields = append(fields, problems.Field("url", code, err.Error()))
	}
	if in.Events == nil {
		if create {
			fields = append(fields, problems.Field("events", "required", "at least one event is required"))
		}
	} else if len(*in.Events) == 0 {
		fields = append(fields, problems.Field("events", "required", "at least one event is required"))
	} else {
		for _, e := range *in.Events {
			if !m.webhookE[e] {
				fields = append(fields, problems.Field("events", "invalid", "unknown event type "+e))
				break
			}
		}
	}
	if in.Name != nil && len(*in.Name) > 120 {
		fields = append(fields, problems.Field("name", "max_length", "at most 120 characters"))
	}
	if in.ProjectIDs != nil {
		ids := []string{}
		for i, ref := range *in.ProjectIDs {
			var id string
			err := m.db.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ? OR key = ?`, ref, strings.ToUpper(ref)).Scan(&id)
			if err != nil {
				fields = append(fields, problems.Field("project_ids["+strconv.Itoa(i)+"]", "not_found", "unknown project"))
				continue
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		*in.ProjectIDs = ids
	}
	if len(fields) > 0 {
		return problems.Validation(fields...)
	}
	return nil
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func dedupe(evs []string) []string {
	out := slices.Clone(evs)
	slices.Sort(out)
	return slices.Compact(out)
}

func audit(ctx context.Context, tx *store.Tx, r *http.Request, action, id string, data map[string]any) error {
	actor := ""
	if p := server.PrincipalFrom(r.Context()); p != nil {
		actor = p.UserID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, 'webhook', ?, ?, ?, ?)`,
		store.NewID(), store.Nullable(actor), action, id, store.MustJSON(data), server.ClientIP(r), store.Now())
	return err
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in webhookInput
	if err := decode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if err := m.validate(ctx, in, true); err != nil {
		problems.Write(w, r, err)
		return
	}
	secret := newSecret()
	enc, err := m.box.SealString(settings.PurposeWebhook, secret)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	id := store.NewID()
	name := ""
	if in.Name != nil {
		name = *in.Name
	}
	active := in.Active == nil || *in.Active
	internal := in.IncludeInternal != nil && *in.IncludeInternal
	evs := dedupe(*in.Events)
	projects := []string{}
	if in.ProjectIDs != nil {
		projects = *in.ProjectIDs
	}
	now := store.Now()
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhooks (id, name, url, secret_enc, events, include_internal, active, consecutive_failures, disabled_reason, project_ids, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?, ?)`,
			id, name, *in.URL, enc, store.MustJSON(evs), internal, active, store.MustJSON(projects), now, now); err != nil {
			return err
		}
		return audit(ctx, tx, r, "webhook.create", id, map[string]any{"url": *in.URL, "events": evs, "active": active, "include_internal": internal})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.invalidate()
	d, err := m.load(ctx, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	d.Secret = secret
	writeJSON(w, http.StatusCreated, d)
}

func (m *Module) patch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	cur, err := m.load(ctx, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var in webhookInput
	if err := decode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	if err := m.validate(ctx, in, false); err != nil {
		problems.Write(w, r, err)
		return
	}
	upd := cur
	changed := map[string]any{}
	if in.Name != nil {
		upd.Name, changed["name"] = *in.Name, *in.Name
	}
	if in.URL != nil {
		upd.URL, changed["url"] = *in.URL, *in.URL
	}
	if in.Events != nil {
		upd.Events = dedupe(*in.Events)
		changed["events"] = upd.Events
	}
	if in.IncludeInternal != nil {
		upd.IncludeInternal, changed["include_internal"] = *in.IncludeInternal, *in.IncludeInternal
	}
	if in.ProjectIDs != nil {
		upd.ProjectIDs, changed["project_ids"] = *in.ProjectIDs, *in.ProjectIDs
	}
	reactivate := false
	if in.Active != nil {
		upd.Active, changed["active"] = *in.Active, *in.Active
		reactivate = *in.Active && !cur.Active
	}
	var secret, enc string
	if in.RotateSecret {
		secret = newSecret()
		if enc, err = m.box.SealString(settings.PurposeWebhook, secret); err != nil {
			problems.Write(w, r, err)
			return
		}
		changed["rotate_secret"] = true
	}
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		q := `UPDATE webhooks SET name = ?, url = ?, events = ?, include_internal = ?, active = ?, project_ids = ?, updated_at = ?`
		args := []any{upd.Name, upd.URL, store.MustJSON(upd.Events), upd.IncludeInternal, upd.Active, store.MustJSON(upd.ProjectIDs), store.Now()}
		if enc != "" {
			q += `, secret_enc = ?`
			args = append(args, enc)
		}
		if reactivate {
			q += `, consecutive_failures = 0, disabled_reason = ''`
		}
		q += ` WHERE id = ?`
		args = append(args, id)
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
		return audit(ctx, tx, r, "webhook.update", id, changed)
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if reactivate {
		m.mu.Lock()
		delete(m.state, id)
		m.mu.Unlock()
	}
	m.invalidate()
	d, err := m.load(ctx, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	d.Secret = secret
	writeJSON(w, http.StatusOK, d)
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := m.load(ctx, id); err != nil {
		problems.Write(w, r, err)
		return
	}
	err := m.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM webhooks WHERE id = ?`, id); err != nil {
			return err
		}
		return audit(ctx, tx, r, "webhook.delete", id, map[string]any{})
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.mu.Lock()
	delete(m.state, id)
	m.mu.Unlock()
	m.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// test sends a synchronous ping and reports status and latency. Errors
// (refused dial, SSRF block, timeout) are returned in "error" with 200.
func (m *Module) test(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	row := m.db.Read().QueryRowContext(ctx, `SELECT id, name, url, secret_enc, events, include_internal, consecutive_failures, project_ids FROM webhooks WHERE id = ?`, id)
	h, err := m.scanHook(row)
	if err != nil {
		problems.Write(w, r, store.NotFound(err))
		return
	}
	res := m.send(ctx, h, "ping", m.pingEnvelope())
	out := map[string]any{"status_code": nil, "response_ms": res.MS, "error": nil}
	if res.Status != 0 {
		out["status_code"] = res.Status
	}
	if !res.OK {
		out["error"] = res.errText()
	}
	writeJSON(w, http.StatusOK, out)
}

type deliveryDTO struct {
	ID         string    `json:"id"`
	WebhookID  string    `json:"webhook_id"`
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Success    bool      `json:"success"`
	StatusCode *int      `json:"status_code"`
	ResponseMS *int      `json:"response_ms"`
	Error      *string   `json:"error"`
	Attempt    int       `json:"attempt"`
	CreatedAt  time.Time `json:"created_at"`
}

// deliveries pages the delivery log newest first; cursor = base64url(id).
func (m *Module) deliveries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if _, err := m.load(ctx, id); err != nil {
		problems.Write(w, r, err)
		return
	}
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			problems.Write(w, r, problems.Validation(problems.Field("limit", "range", "1..200")))
			return
		}
		limit = n
	}
	where, args := "webhook_id = ?", []any{id}
	if c := q.Get("cursor"); c != "" {
		cid, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problems.Write(w, r, problems.Validation(problems.Field("cursor", "invalid", "")))
			return
		}
		where += " AND id < ?"
		args = append(args, string(cid))
	}
	args = append(args, limit+1)
	rows, err := m.db.Read().QueryContext(ctx, `SELECT id, webhook_id, event_id, event_type, success, status_code, response_ms, error, attempt, created_at FROM webhook_deliveries WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	items := []deliveryDTO{}
	for rows.Next() {
		var d deliveryDTO
		var sc, ms sql.NullInt64
		var e string
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventID, &d.EventType, &d.Success, &sc, &ms, &e, &d.Attempt, &d.CreatedAt); err != nil {
			problems.Write(w, r, err)
			return
		}
		if sc.Valid {
			v := int(sc.Int64)
			d.StatusCode = &v
		}
		if ms.Valid {
			v := int(ms.Int64)
			d.ResponseMS = &v
		}
		if e != "" {
			d.Error = &e
		}
		d.CreatedAt = d.CreatedAt.UTC()
		items = append(items, d)
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
