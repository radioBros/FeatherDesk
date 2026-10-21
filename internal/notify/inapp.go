// SPDX-License-Identifier: AGPL-3.0-only

package notify

// In-app notification centre (D-19 §D, NOT-036…NOT-040).
//
// The notify consumer resolves recipients once per event (resolve) and serves
// two channels from that one result: email (Recipients/emailTargets, as
// before) and in-app rows (writeInApp). In-app rows for one event are written
// in ONE transaction: a multi-row INSERT … ON CONFLICT DO NOTHING RETURNING
// (a replayed event inserts nothing) plus one grouped unread-count query.
// After commit, each recipient gets `notification.created` on its `me`
// topic. Preferences live next to the email opt-outs in users.notify_prefs:
//
//	{"opt_out": [...email off...], "in_app_off": [...], "browser_on": [...]}
//
// in_app defaults on, email defaults on, browser is opt-in (and needs in_app,
// because browser notifications are raised from the in-app frame).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Notification types (api/openapi.yaml NotificationType). Deliberately an
// explicit list, not TemplateKeys: auth.* and non-ticket templates never
// become in-app notifications.
var (
	StaffInAppTypes = []string{TplStaffTicketNew, TplStaffCustomerReplied, TplStaffAssigned, TplStaffEscalated,
		TplStaffMentioned, TplStaffSLA, TplStaffChildrenResolved}
	CustomerInAppTypes = []string{TplCustomerTicketReceived, TplCustomerReply, TplCustomerResolved}
	// InAppTypes is the NotificationType enum, in order.
	InAppTypes = append(slices.Clone(StaffInAppTypes), CustomerInAppTypes...)
)

// Pruning (NOT-039): rows older than 90 days, and beyond the newest 500 per user.
const (
	NotificationMaxAge     = 90 * 24 * time.Hour
	NotificationMaxPerUser = 500
	pruneBatch             = 1000
)

// Frame event names on the `me` topic (realtime_spec §3).
const (
	FrameNotificationCreated = "notification.created"
	FrameNotificationRead    = "notification.read"
)

// TypesFor lists the notification types a user of kind ("staff" or
// "customer") can receive.
func TypesFor(kind string) []string {
	if kind == "customer" {
		return CustomerInAppTypes
	}
	return StaffInAppTypes
}

// storedPrefs is the users.notify_prefs document.
type storedPrefs struct {
	OptOut    []string `json:"opt_out"`
	InAppOff  []string `json:"in_app_off"`
	BrowserOn []string `json:"browser_on"`
}

// Channels is one row of the preferences matrix (NotificationChannels).
type Channels struct {
	InApp   bool `json:"in_app"`
	Email   bool `json:"email"`
	Browser bool `json:"browser"`
}

// ChannelsPatch is a partial row (NotificationChannelsPatch).
type ChannelsPatch struct {
	InApp   *bool `json:"in_app"`
	Email   *bool `json:"email"`
	Browser *bool `json:"browser"`
}

// PrefsView renders Me.notification_prefs for a user of kind from the raw
// users.notify_prefs document.
func PrefsView(kind string, raw []byte) map[string]Channels {
	var p storedPrefs
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p)
	}
	out := make(map[string]Channels, len(TypesFor(kind)))
	for _, k := range TypesFor(kind) {
		out[k] = Channels{
			InApp:   !slices.Contains(p.InAppOff, k),
			Email:   !isOptional(k) || !slices.Contains(p.OptOut, k),
			Browser: slices.Contains(p.BrowserOn, k),
		}
	}
	return out
}

// ValidatePrefs checks a PATCH /me notification_prefs object (NOT-037): the
// type must belong to the user's audience ("invalid"), and customer.reply
// email cannot be turned off ("not_optional", like notification_opt_out).
func ValidatePrefs(kind string, in map[string]ChannelsPatch) []problems.FieldError {
	var fe []problems.FieldError
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		field := "notification_prefs." + k
		if !slices.Contains(TypesFor(kind), k) {
			fe = append(fe, problems.Field(field, "invalid", "unknown notification type "+k))
			continue
		}
		if c := in[k]; c.Email != nil && !*c.Email && !isOptional(k) {
			fe = append(fe, problems.Field(field+".email", "not_optional", k+" email cannot be disabled"))
		}
	}
	return fe
}

// ApplyPrefs merges a validated patch into the generic users.notify_prefs
// map (unknown keys are kept) and returns it.
func ApplyPrefs(prefs map[string]any, in map[string]ChannelsPatch) map[string]any {
	if prefs == nil {
		prefs = map[string]any{}
	}
	lists := map[string][]string{"opt_out": nil, "in_app_off": nil, "browser_on": nil}
	for name := range lists {
		lists[name] = strList(prefs[name])
	}
	set := func(name, key string, on bool) {
		l := slices.DeleteFunc(lists[name], func(s string) bool { return s == key })
		if on {
			l = append(l, key)
		}
		lists[name] = l
	}
	for k, c := range in {
		if c.Email != nil {
			set("opt_out", k, !*c.Email)
		}
		if c.InApp != nil {
			set("in_app_off", k, !*c.InApp)
		}
		if c.Browser != nil {
			set("browser_on", k, *c.Browser)
		}
	}
	for name, l := range lists {
		l = slices.Compact(slices.Sorted(slices.Values(l)))
		if l == nil {
			l = []string{}
		}
		prefs[name] = l
	}
	return prefs
}

func strList(v any) []string {
	var out []string
	switch x := v.(type) {
	case []string:
		out = append(out, x...)
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// ---- DTO (api/openapi.yaml Notification) ----

// NotificationParams are the type-specific details (NotificationParams).
// Customer notifications always carry an empty object (I-7): no tier, team,
// SLA or escalation data.
type NotificationParams struct {
	FromTier int    `json:"from_tier,omitempty"`
	ToTier   int    `json:"to_tier,omitempty"`
	SLA      string `json:"sla,omitempty"`
	Breached bool   `json:"breached,omitempty"`
}

// NotificationDTO is the wire shape of one notification.
type NotificationDTO struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	TicketID      *string            `json:"ticket_id"`
	TicketNumber  *int64             `json:"ticket_number"`
	TicketSubject *string            `json:"ticket_subject"`
	ActorName     *string            `json:"actor_name"`
	Params        NotificationParams `json:"params"`
	ReadAt        *time.Time         `json:"read_at"`
	CreatedAt     time.Time          `json:"created_at"`
}

// showsActor: customer notifications name the actor only for a public reply
// (whose author the portal shows anyway); staff always see it (I-9).
func showsActor(typ string) bool {
	return !strings.HasPrefix(typ, "customer.") || typ == TplCustomerReply
}

// staffParams derives the staff-only details of an event.
func staffParams(e events.Event, typ string) NotificationParams {
	var p NotificationParams
	switch typ {
	case TplStaffEscalated:
		var tc events.TierChange
		_ = e.Decode(&tc)
		if tc.FromTier >= 1 && tc.FromTier <= 3 {
			p.FromTier = tc.FromTier
		}
		if tc.ToTier >= 1 && tc.ToTier <= 3 {
			p.ToTier = tc.ToTier
		}
	case TplStaffSLA:
		var sl events.SLA
		_ = e.Decode(&sl)
		if sl.Which == "first_response" || sl.Which == "resolution" {
			p.SLA = sl.Which
		}
		p.Breached = e.Type == events.SLABreached
	}
	return p
}

// ---- consumer: one transaction per event (NOT-036) ----

type inAppRow struct {
	id, userID, typ, actorID string
	params                   NotificationParams
	browser                  bool
}

// writeInApp stores the in-app notifications of one event and, after commit,
// pushes notification.created to each recipient. It reads no row per
// recipient: preferences come with the resolved user rows.
func (m *Module) writeInApp(ctx context.Context, e events.Event, all []Target, users map[string]*userRow) error {
	var rows []inAppRow
	for _, t := range all {
		u := users[t.R.UserID]
		if u == nil || t.R.Mailbox || !slices.Contains(TypesFor(u.Kind), t.Key) || slices.Contains(u.InAppOff, t.Key) {
			continue
		}
		r := inAppRow{id: store.NewID(), userID: u.ID, typ: t.Key, browser: slices.Contains(u.BrowserOn, t.Key)}
		if showsActor(t.Key) {
			r.actorID = e.ActorID
		}
		if !strings.HasPrefix(t.Key, "customer.") {
			r.params = staffParams(e, t.Key)
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil
	}
	actorName := ""
	if e.ActorID != "" {
		if a, err := m.user(ctx, e.ActorID); err == nil {
			actorName = displayName(a)
		}
	}
	now := store.Now()
	inserted := map[string]bool{}
	counts := map[string]int{}
	err := m.db.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		for chunk := range slices.Chunk(rows, 200) {
			var b strings.Builder
			b.WriteString(`INSERT INTO notifications (id, user_id, type, ticket_id, actor_id, event_id, params, read_at, created_at) VALUES `)
			args := make([]any, 0, len(chunk)*8)
			for i, r := range chunk {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("(?, ?, ?, ?, ?, ?, ?, NULL, ?)")
				args = append(args, r.id, r.userID, r.typ, store.Nullable(e.TicketID), store.Nullable(r.actorID), e.ID, store.MustJSON(r.params), now)
			}
			b.WriteString(` ON CONFLICT DO NOTHING RETURNING id`)
			ids, err := scanStrings(tx.QueryContext(ctx, b.String(), args...))
			if err != nil {
				return err
			}
			for _, id := range ids {
				inserted[id] = true
			}
		}
		if len(inserted) == 0 {
			return nil // a replayed event: every row already exists
		}
		var uids []any
		seen := map[string]bool{}
		for _, r := range rows {
			if inserted[r.id] && !seen[r.userID] {
				seen[r.userID] = true
				uids = append(uids, r.userID)
			}
		}
		rs, err := tx.QueryContext(ctx, `SELECT user_id, COUNT(*) FROM notifications WHERE read_at IS NULL AND user_id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(uids)), ",")+`) GROUP BY user_id`, uids...)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var uid string
			var n int
			if err := rs.Scan(&uid, &n); err != nil {
				return err
			}
			counts[uid] = n
		}
		return rs.Err()
	})
	if err != nil {
		return err
	}
	if m.hub == nil || len(inserted) == 0 {
		return nil
	}
	s := e.Snapshot
	for _, r := range rows {
		if !inserted[r.id] {
			continue
		}
		d := NotificationDTO{ID: r.id, Type: r.typ, Params: r.params, CreatedAt: now.UTC()}
		if e.TicketID != "" {
			tid, num, subj := e.TicketID, s.Number, s.Subject
			d.TicketID, d.TicketNumber, d.TicketSubject = &tid, &num, &subj
		}
		if r.actorID != "" && actorName != "" {
			n := actorName
			d.ActorName = &n
		}
		m.hub.SendUser(r.userID, realtime.Frame{Type: "event", Event: FrameNotificationCreated,
			Data: map[string]any{"notification": d, "unread_count": counts[r.userID], "browser": r.browser}})
	}
	return nil
}

// DeliverInApp writes the in-app notifications of one stored event, exactly
// as the notify consumer would, and never sends email. It is for callers that
// never start the queue (`seed --demo` replays its seeded events through it).
func (m *Module) DeliverInApp(ctx context.Context, e events.Event) error {
	if !notifyFilter(e) {
		return nil
	}
	all, users, err := m.resolve(ctx, e)
	if err != nil {
		return err
	}
	return m.writeInApp(ctx, e, all, users)
}

func scanStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- reads ----

// unreadCount is the bell badge of userID.
func (m *Module) unreadCount(ctx context.Context, q store.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

type listQuery struct {
	userID, typ, cursor string
	unread              bool
	limit               int
}

// listNotifications reads one page newest first in a single query: ticket
// number/subject and the actor name are joined at read time (never copied
// into params), so an erased user or deleted ticket never lingers in a row.
func (m *Module) listNotifications(ctx context.Context, lq listQuery) ([]NotificationDTO, string, error) {
	where := []string{"n.user_id = ?"}
	args := []any{lq.userID}
	if lq.unread {
		where = append(where, "n.read_at IS NULL")
	}
	if lq.typ != "" {
		where = append(where, "n.type = ?")
		args = append(args, lq.typ)
	}
	if lq.cursor != "" {
		where = append(where, "n.id < ?")
		args = append(args, lq.cursor)
	}
	args = append(args, lq.limit+1)
	rows, err := m.db.Read().QueryContext(ctx, `SELECT n.id, n.type, n.ticket_id, t.number, t.subject, n.actor_id, u.name, u.email, n.params, n.read_at, n.created_at
		FROM notifications n LEFT JOIN tickets t ON t.id = n.ticket_id LEFT JOIN users u ON u.id = n.actor_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY n.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []NotificationDTO{}
	for rows.Next() {
		var (
			d                          NotificationDTO
			ticketID, subject, actorID sql.NullString
			actorName, actorEmail      sql.NullString
			number                     sql.NullInt64
			params                     store.RawJSON
			readAt                     store.NullTime
		)
		if err := rows.Scan(&d.ID, &d.Type, &ticketID, &number, &subject, &actorID, &actorName, &actorEmail, &params, &readAt, &d.CreatedAt); err != nil {
			return nil, "", err
		}
		d.CreatedAt = d.CreatedAt.UTC()
		if ticketID.Valid && number.Valid {
			tid, num, subj := ticketID.String, number.Int64, subject.String
			d.TicketID, d.TicketNumber, d.TicketSubject = &tid, &num, &subj
		}
		if actorID.Valid && showsActor(d.Type) {
			if n := displayName(&userRow{Name: actorName.String, Email: actorEmail.String}); n != "" {
				d.ActorName = &n
			}
		}
		if !strings.HasPrefix(d.Type, "customer.") && len(params) > 0 {
			_ = json.Unmarshal(params, &d.Params)
		}
		if readAt.Valid {
			t := readAt.Time.UTC()
			d.ReadAt = &t
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > lq.limit {
		out = out[:lq.limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// ---- writes from the API ----

// markRead marks ids (nil = all) of userID read in one transaction and
// returns how many rows changed and the new unread count.
func (m *Module) markRead(ctx context.Context, userID, id string) (changed int64, unread int, err error) {
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		q, args := `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, []any{store.Now(), userID}
		if id != "" {
			q += ` AND id = ?`
			args = append(args, id)
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		changed, _ = res.RowsAffected()
		if changed == 0 && id != "" {
			if err := m.mustOwn(ctx, tx, userID, id); err != nil {
				return err
			}
		}
		unread, err = m.unreadCount(ctx, tx, userID)
		return err
	})
	return changed, unread, err
}

// deleteOne removes one notification of userID.
func (m *Module) deleteOne(ctx context.Context, userID, id string) (unread int, err error) {
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM notifications WHERE id = ? AND user_id = ?`, id, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return problems.NotFound()
		}
		unread, err = m.unreadCount(ctx, tx, userID)
		return err
	})
	return unread, err
}

func (m *Module) mustOwn(ctx context.Context, q store.Querier, userID, id string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM notifications WHERE id = ? AND user_id = ?`, id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return problems.NotFound()
	}
	return err
}

// sendRead syncs the other tabs of userID (notification.read).
func (m *Module) sendRead(userID string, ids []string, all bool, unread int) {
	if m.hub == nil {
		return
	}
	d := map[string]any{"unread_count": unread}
	if all {
		d["all"] = true
	} else {
		d["ids"] = ids
	}
	m.hub.SendUser(userID, realtime.Frame{Type: "event", Event: FrameNotificationRead, Data: d})
}

// ---- pruning (NOT-039) ----

// PruneNotifications deletes notifications older than 90 days and those
// beyond the newest 500 of each user, in bounded batches (each its own short
// transaction, so the SQLite writer is never held long).
func PruneNotifications(ctx context.Context, db *store.DB, now time.Time) (int64, error) {
	var total int64
	cutoff := now.UTC().Add(-NotificationMaxAge)
	for {
		var n int64
		err := db.Tx(ctx, func(tx *store.Tx) error {
			res, err := tx.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE created_at < ? LIMIT ?)`, cutoff, pruneBatch)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			return nil
		})
		total += n
		if err != nil || n < pruneBatch {
			if err != nil {
				return total, err
			}
			break
		}
	}
	over, err := scanStrings(db.Read().QueryContext(ctx, `SELECT user_id FROM notifications GROUP BY user_id HAVING COUNT(*) > ?`, NotificationMaxPerUser))
	if err != nil {
		return total, err
	}
	for _, uid := range over {
		err := db.Tx(ctx, func(tx *store.Tx) error {
			var keep string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM notifications WHERE user_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?`, uid, NotificationMaxPerUser-1).Scan(&keep); err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `DELETE FROM notifications WHERE user_id = ? AND id < ?`, uid, keep)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			total += n
			return nil
		})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return total, err
		}
	}
	return total, nil
}

// RegisterRealtime implements app.RealtimeProvider: the hub that carries the
// notification frames on each user's `me` topic (authorized by tickets).
func (m *Module) RegisterRealtime(h *realtime.Hub) { m.hub = h }
