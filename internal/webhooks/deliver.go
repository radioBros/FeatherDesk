// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/mdrender"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Sign returns the X-FeatherDesk-Signature value for body at time t:
// "t=<unix>,v1=<hex hmac_sha256(secret, t + "." + body)>".
func Sign(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// result of one HTTP attempt.
type result struct {
	Status int
	MS     int64
	Err    string
	OK     bool // 2xx
	Gone   bool // 410
}

func (r result) errText() string {
	if r.Err != "" {
		return r.Err
	}
	return fmt.Sprintf("HTTP %d", r.Status)
}

// send POSTs one signed body. Redirects are not followed (3xx = failure).
func (m *Module) send(ctx context.Context, h *hook, typ string, body []byte) result {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return result{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "FeatherDesk-Webhook/1")
	id, sig := store.NewID(), Sign(h.Secret, m.Now(), body)
	req.Header.Set("X-FeatherDesk-Event", typ)
	req.Header.Set("X-FeatherDesk-Delivery", id)
	req.Header.Set("X-FeatherDesk-Signature", sig)
	// The SimpleTickets 1.0.0 names, so receivers written for them keep working.
	req.Header.Set("X-SimpleTickets-Event", typ)
	req.Header.Set("X-SimpleTickets-Delivery", id)
	req.Header.Set("X-SimpleTickets-Signature", sig)
	start := time.Now()
	resp, err := m.client.Do(req)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return result{MS: ms, Err: err.Error()}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	r := result{Status: resp.StatusCode, MS: ms}
	r.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	r.Gone = resp.StatusCode == http.StatusGone
	return r
}

// handleEvent is the durable consumer: first attempt for every matching
// webhook runs here, in memory. Failures become one deferred_jobs row each.
// It returns nil once every webhook got its first attempt (or was deferred),
// so a failing receiver never replays the event to the others.
func (m *Module) handleEvent(ctx context.Context, e events.Event) error {
	hooks, err := m.activeHooks(ctx)
	if err != nil {
		return err
	}
	envs := map[bool][]byte{}
	for i, h := range hooks {
		if !h.wants(e.Type) || (e.Type == events.NoteCreated && !h.IncludeInternal) || !h.inScope(e) {
			continue
		}
		// Keep 15 s of budget: defer whatever is left instead of timing out,
		// which would replay the whole event.
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 15*time.Second {
			for _, rest := range hooks[i:] {
				if rest.wants(e.Type) && (e.Type != events.NoteCreated || rest.IncludeInternal) && rest.inScope(e) {
					if err := m.deferJob(context.WithoutCancel(ctx), rest.ID, e, 0, "deferred: consumer time budget", 0); err != nil {
						return err
					}
				}
			}
			return nil
		}
		body, ok := envs[h.IncludeInternal]
		if !ok {
			body, err = m.envelope(ctx, e, h.IncludeInternal)
			if err != nil {
				return err
			}
			envs[h.IncludeInternal] = body
		}
		res := m.send(ctx, h, e.Type, body)
		m.record(context.WithoutCancel(ctx), h, e.ID, e.Type, 1, res)
		if !res.OK && !res.Gone {
			if err := m.deferJob(context.WithoutCancel(ctx), h.ID, e, 1, res.errText(), m.FirstRetry()); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleRetry runs one deferred attempt. Inactive or deleted webhooks are
// skipped (nil = done). An error lets the queue back off and retry until
// MaxAttempts, then mark the row dead.
func (m *Module) handleRetry(ctx context.Context, payload json.RawMessage) error {
	var p retryPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil // malformed: nothing to retry
	}
	h, err := m.loadActive(ctx, p.WebhookID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if (p.Event.Type == events.NoteCreated && !h.IncludeInternal) || !h.inScope(p.Event) {
		return nil
	}
	body, err := m.envelope(ctx, p.Event, h.IncludeInternal)
	if err != nil {
		return err
	}
	var prior int
	_ = m.db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND event_id = ? AND NOT success`, h.ID, p.Event.ID).Scan(&prior)
	res := m.send(ctx, h, p.Event.Type, body)
	m.record(context.WithoutCancel(ctx), h, p.Event.ID, p.Event.Type, prior+1, res)
	if res.OK || res.Gone {
		return nil
	}
	return errors.New(res.errText())
}

func truncErr(s string) string {
	if len(s) > 1024 {
		s = s[:1024]
	}
	return s
}

// record updates failure counters and the delivery log.
func (m *Module) record(ctx context.Context, h *hook, eventID, typ string, attempt int, res result) {
	if err := m.recordErr(ctx, h, eventID, typ, attempt, res); err != nil {
		m.log.Error("webhooks: record delivery", "webhook", h.ID, "err", err)
	}
}

func nullInt(v int, valid bool) any {
	if !valid {
		return nil
	}
	return v
}

func (m *Module) recordErr(ctx context.Context, h *hook, eventID, typ string, attempt int, res result) error {
	now := store.Now()
	if res.OK {
		m.mu.Lock()
		s := m.st(h.ID)
		wasFailing := s.failing
		s.failing = false
		n := s.successes
		s.successes++
		m.mu.Unlock()
		sample := n == 0 || wasFailing || n%10 == 0
		if !wasFailing && !sample {
			return nil
		}
		return m.db.Tx(ctx, func(tx *store.Tx) error {
			if wasFailing {
				if _, err := tx.ExecContext(ctx, `UPDATE webhooks SET consecutive_failures = 0 WHERE id = ? AND consecutive_failures > 0`, h.ID); err != nil {
					return err
				}
			}
			if !sample {
				return nil
			}
			var cnt int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND success`, h.ID).Scan(&cnt); err != nil {
				return err
			}
			if cnt < successRing {
				_, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, attempt, success, status_code, response_ms, error, created_at) VALUES (?, ?, ?, ?, ?, TRUE, ?, ?, '', ?)`,
					store.NewID(), h.ID, eventID, typ, attempt, res.Status, res.MS, now)
				return err
			}
			// Ring: overwrite the oldest success row in place (new id keeps id order = time order).
			var oldest string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM webhook_deliveries WHERE webhook_id = ? AND success ORDER BY id LIMIT 1`, h.ID).Scan(&oldest); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET id = ?, event_id = ?, event_type = ?, attempt = ?, status_code = ?, response_ms = ?, error = '', created_at = ? WHERE id = ?`,
				store.NewID(), eventID, typ, attempt, res.Status, res.MS, now, oldest)
			return err
		})
	}

	m.mu.Lock()
	m.st(h.ID).failing = true
	m.mu.Unlock()
	disabled := false
	var reason string
	err := m.db.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, attempt, success, status_code, response_ms, error, created_at) VALUES (?, ?, ?, ?, ?, FALSE, ?, ?, ?, ?)`,
			store.NewID(), h.ID, eventID, typ, attempt, nullInt(res.Status, res.Status != 0), res.MS, truncErr(res.errText()), now); err != nil {
			return err
		}
		var fails int
		if err := tx.QueryRowContext(ctx, `UPDATE webhooks SET consecutive_failures = consecutive_failures + 1, updated_at = ? WHERE id = ? RETURNING consecutive_failures`, now, h.ID).Scan(&fails); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil // deleted meanwhile
			}
			return err
		}
		switch {
		case res.Gone:
			reason = "gone"
		case fails >= DisableAfter:
			reason = "failures"
		default:
			return nil
		}
		r, err := tx.ExecContext(ctx, `UPDATE webhooks SET active = FALSE, disabled_reason = ?, updated_at = ? WHERE id = ? AND active`, reason, now, h.ID)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		disabled = n == 1
		return nil
	})
	if err != nil {
		return err
	}
	if disabled {
		m.invalidate()
		m.log.Warn("webhooks: webhook disabled", "webhook", h.ID, "reason", reason)
		if reason == "failures" && m.q != nil {
			name := h.Name
			if name == "" {
				name = h.URL
			}
			if err := m.q.Enqueue(ctx, jobAdminMail, adminMail{
				Template: tplStaffWebhookDisabled,
				Vars:     map[string]string{"name": name, "url": h.URL, "reason": reason, "failures": strconv.Itoa(DisableAfter)},
			}); err != nil {
				m.log.Error("webhooks: admin mail", "err", err)
			}
		}
	}
	return nil
}

// ---- envelope (events_spec §3) ----

// Renderer builds the webhook envelope of an event. Slack (D-21) renders its
// posts from the same envelope, so both share the visibility rules (I-7, I-9).
type Renderer struct {
	db      *store.DB
	baseURL string
}

// NewRenderer returns a renderer reading db; baseURL is FD_PUBLIC_URL.
func NewRenderer(db *store.DB, baseURL string) *Renderer {
	return &Renderer{db: db, baseURL: baseURL}
}

// Envelope is the JSON body a webhook receives for e. internal includes
// notes and staff-only fields (include_internal).
func (m *Renderer) Envelope(ctx context.Context, e events.Event, internal bool) ([]byte, error) {
	return m.envelope(ctx, e, internal)
}

type userRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type envelope struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      envData   `json:"data"`
}

type envData struct {
	Ticket  map[string]any `json:"ticket,omitempty"`
	Actor   *userRef       `json:"actor"`
	Changes map[string]any `json:"changes"`
	Message map[string]any `json:"message,omitempty"`
}

func (m *Renderer) userRef(ctx context.Context, id string) (*userRef, error) {
	if id == "" {
		return nil, nil
	}
	var u userRef
	err := m.db.Read().QueryRowContext(ctx, `SELECT id, name, kind FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Name, &u.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func ts(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ticketSummary builds a TicketSummary: state fields from the event snapshot
// (state at event time), timestamps and names from the DB.
func (m *Renderer) ticketSummary(ctx context.Context, e events.Event) (map[string]any, error) {
	s := e.Snapshot
	t := map[string]any{
		"id": e.TicketID, "number": s.Number, "subject": s.Subject, "status": s.Status, "priority": s.Priority,
		"tier": max(s.Tier, 1), "team_id": nullStr(s.TeamID), "category_id": nullStr(s.CategoryID),
		"organization_id": nullStr(s.OrganizationID), "channel": s.Channel, "tags": []string{},
		"created_at": e.CreatedAt.UTC(), "updated_at": e.CreatedAt.UTC(),
	}
	var created, updated time.Time
	var frd, rd, lca, lsa sql.NullTime
	var channel string
	err := m.db.Read().QueryRowContext(ctx, `SELECT channel, first_response_due_at, resolution_due_at, last_customer_activity_at, last_staff_activity_at, created_at, updated_at FROM tickets WHERE id = ?`, e.TicketID).
		Scan(&channel, &frd, &rd, &lca, &lsa, &created, &updated)
	switch {
	case err == nil:
		t["created_at"], t["updated_at"] = created.UTC(), updated.UTC()
		t["first_response_due_at"], t["resolution_due_at"] = ts(frd), ts(rd)
		t["last_customer_activity_at"], t["last_staff_activity_at"] = ts(lca), ts(lsa)
		if s.Channel == "" {
			t["channel"] = channel
		}
		rows, err := m.db.Read().QueryContext(ctx, `SELECT g.name FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id WHERE tt.ticket_id = ? ORDER BY g.name`, e.TicketID)
		if err == nil {
			tags := []string{}
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil {
					tags = append(tags, n)
				}
			}
			rows.Close()
			t["tags"] = tags
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return nil, err
	}
	// D-20: the ref and project of the snapshot (pre-D-20 events: the
	// Default project and the ticket's current sequence).
	pid := s.Project()
	project := map[string]any{"id": pid, "key": "", "name": "", "color": ""}
	var key, name, color string
	if err := m.db.Read().QueryRowContext(ctx, `SELECT key, name, color FROM projects WHERE id = ?`, pid).Scan(&key, &name, &color); err == nil {
		project["key"], project["name"], project["color"] = key, name, color
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	ref := s.Ref
	if ref == "" {
		var seq int64
		if err := m.db.Read().QueryRowContext(ctx, `SELECT project_seq FROM tickets WHERE id = ?`, e.TicketID).Scan(&seq); err == nil && key != "" {
			ref = domain.TicketRef(key, seq)
		} else {
			ref = "#" + strconv.FormatInt(s.Number, 10)
		}
	}
	t["ref"], t["project_id"], t["project"] = ref, pid, project
	if t["channel"] == "" {
		t["channel"] = "portal"
	}
	if s.Status == "" {
		t["status"] = "new"
	}
	if s.Priority == "" {
		t["priority"] = "normal"
	}
	req, err := m.userRef(ctx, s.RequesterID)
	if err != nil {
		return nil, err
	}
	if req == nil {
		req = &userRef{ID: s.RequesterID, Kind: "customer"}
	}
	t["requester"] = req
	asg, err := m.userRef(ctx, s.AssigneeID)
	if err != nil {
		return nil, err
	}
	if asg != nil {
		t["assignee"] = asg
	} else {
		t["assignee"] = nil
	}
	return t, nil
}

// changes returns the event data filtered for the webhook scope (I-7, I-9).
func (m *Renderer) changes(ctx context.Context, e events.Event, internal bool) (map[string]any, error) {
	ch := map[string]any{}
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &ch); err != nil {
			ch = map[string]any{}
		}
	}
	if internal {
		return ch, nil
	}
	switch e.Type {
	case events.TicketEscalated, events.TicketDeescalated:
		delete(ch, "reason")
	case events.TicketFieldsChanged:
		keys, _ := ch["keys"].([]any)
		if len(keys) > 0 {
			visible, err := m.customerVisibleFields(ctx)
			if err != nil {
				return nil, err
			}
			kept := []any{}
			for _, k := range keys {
				if ks, ok := k.(string); ok && visible[ks] {
					kept = append(kept, ks)
				}
			}
			ch["keys"] = kept
		}
		// Value maps (if present) are filtered the same way.
		for _, f := range []string{"from", "to", "values"} {
			if mv, ok := ch[f].(map[string]any); ok {
				visible, err := m.customerVisibleFields(ctx)
				if err != nil {
					return nil, err
				}
				for k := range mv {
					if !visible[k] {
						delete(mv, k)
					}
				}
			}
		}
	}
	for k := range ch {
		if strings.Contains(strings.ToLower(k), "internal") {
			delete(ch, k)
		}
	}
	return ch, nil
}

func (m *Renderer) customerVisibleFields(ctx context.Context) (map[string]bool, error) {
	rows, err := m.db.Read().QueryContext(ctx, `SELECT key FROM custom_field_defs WHERE customer_visible`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// message loads the message referenced by a message.created / note.created event.
func (m *Renderer) message(ctx context.Context, e events.Event, internal bool) (map[string]any, error) {
	var ref events.MessageRef
	if err := e.Decode(&ref); err != nil || ref.MessageID == "" {
		return nil, nil
	}
	var id, ticketID, kind, channel, body string
	var author, via sql.NullString
	var created time.Time
	var edited sql.NullTime
	err := m.db.Read().QueryRowContext(ctx, `SELECT id, ticket_id, author_id, kind, channel, body_md, created_at, edited_at, via_token_id FROM ticket_messages WHERE id = ?`, ref.MessageID).
		Scan(&id, &ticketID, &author, &kind, &channel, &body, &created, &edited, &via)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if kind != "public" && !internal {
		return nil, nil
	}
	au, err := m.userRef(ctx, author.String)
	if err != nil {
		return nil, err
	}
	msg := map[string]any{"id": id, "ticket_id": ticketID, "kind": kind, "channel": channel, "body_md": body, "body_html": mdrender.Render(body),
		"created_at": created.UTC(), "edited_at": ts(edited), "attachments": []any{},
		"via_integration": via.Valid} // I-12: posted on the customer's behalf by an integration
	if au != nil {
		msg["author"] = au
	} else {
		msg["author"] = nil
	}
	rows, err := m.db.Read().QueryContext(ctx, `SELECT id, filename, content_type, size_bytes, sha256, created_at FROM attachments WHERE message_id = ? ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	atts := []any{}
	for rows.Next() {
		var aid, fn, ct, sha string
		var size int64
		var at time.Time
		if err := rows.Scan(&aid, &fn, &ct, &size, &sha, &at); err != nil {
			return nil, err
		}
		atts = append(atts, map[string]any{"id": aid, "ticket_id": ticketID, "message_id": id, "filename": fn, "content_type": ct,
			"size_bytes": size, "sha256": sha, "url": strings.TrimRight(m.baseURL, "/") + "/api/v1/agent/attachments/" + aid, "created_at": at.UTC()})
	}
	msg["attachments"] = atts
	return msg, rows.Err()
}

// envelope renders the JSON body for one scope (include_internal or not).
func (m *Renderer) envelope(ctx context.Context, e events.Event, internal bool) ([]byte, error) {
	env := envelope{ID: e.ID, Type: e.Type, CreatedAt: e.CreatedAt.UTC()}
	var err error
	if env.Data.Ticket, err = m.ticketSummary(ctx, e); err != nil {
		return nil, err
	}
	if env.Data.Actor, err = m.userRef(ctx, e.ActorID); err != nil {
		return nil, err
	}
	if env.Data.Changes, err = m.changes(ctx, e, internal); err != nil {
		return nil, err
	}
	if e.Type == events.MessageCreated || e.Type == events.NoteCreated {
		if env.Data.Message, err = m.message(ctx, e, internal); err != nil {
			return nil, err
		}
	}
	return json.Marshal(env)
}

// pingEnvelope is the body of POST /admin/webhooks/{id}/test.
func (m *Module) pingEnvelope() []byte {
	b, _ := json.Marshal(map[string]any{"id": store.NewID(), "type": "ping", "created_at": m.Now().UTC().Truncate(time.Second),
		"data": map[string]any{"actor": nil, "changes": map[string]any{}}})
	return b
}
