// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// route is one slack_routes row (the SlackRoute schema).
type route struct {
	ID             string     `json:"id"`
	ChannelID      string     `json:"channel_id"`
	ChannelName    string     `json:"channel_name"`
	ChannelPrivate bool       `json:"channel_private"`
	Events         []string   `json:"events"`
	ProjectIDs     []string   `json:"project_ids"`
	Priorities     []string   `json:"priorities"`
	Threads        bool       `json:"threads"`
	IncludeBody    bool       `json:"include_body"`
	Internal       bool       `json:"include_internal"`
	Active         bool       `json:"active"`
	LastError      *string    `json:"last_error"`
	LastErrorAt    *time.Time `json:"last_error_at"`
	LastPostedAt   *time.Time `json:"last_posted_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const selectRoute = `SELECT id, channel_id, channel_name, channel_private, events, project_ids, priorities, threads, include_body, include_internal, active, last_error, last_error_at, last_posted_at, created_at, updated_at FROM slack_routes`

func scanRoute(row interface{ Scan(...any) error }) (*route, error) {
	var rt route
	var evs, projects, prios store.JSON[[]string]
	var lastErr string
	var errAt, postedAt sql.NullTime
	if err := row.Scan(&rt.ID, &rt.ChannelID, &rt.ChannelName, &rt.ChannelPrivate, &evs, &projects, &prios, &rt.Threads, &rt.IncludeBody,
		&rt.Internal, &rt.Active, &lastErr, &errAt, &postedAt, &rt.CreatedAt, &rt.UpdatedAt); err != nil {
		return nil, err
	}
	rt.Events, rt.ProjectIDs, rt.Priorities = nonNil(evs.V), nonNil(projects.V), nonNil(prios.V)
	if lastErr != "" {
		rt.LastError = &lastErr
	}
	if errAt.Valid {
		t := errAt.Time.UTC()
		rt.LastErrorAt = &t
	}
	if postedAt.Valid {
		t := postedAt.Time.UTC()
		rt.LastPostedAt = &t
	}
	rt.CreatedAt, rt.UpdatedAt = rt.CreatedAt.UTC(), rt.UpdatedAt.UTC()
	return &rt, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (m *Module) loadRoute(ctx context.Context, id string) (*route, error) {
	rt, err := scanRoute(m.db.Read().QueryRowContext(ctx, selectRoute+` WHERE id = ?`, id))
	if err != nil {
		return nil, store.NotFound(err)
	}
	return rt, nil
}

// activeRoutes returns the cached active routes, loading them once.
func (m *Module) activeRoutes(ctx context.Context) ([]*route, error) {
	m.mu.Lock()
	if m.routes != nil {
		c := m.routes
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()
	rows, err := m.db.Read().QueryContext(ctx, selectRoute+` WHERE active ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*route{}
	for rows.Next() {
		rt, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.routes = out
	m.mu.Unlock()
	return out, nil
}

func (m *Module) invalidate() {
	m.mu.Lock()
	m.routes = nil
	m.mu.Unlock()
}

func (m *Module) listRoutes(w http.ResponseWriter, r *http.Request) {
	rows, err := m.db.Read().QueryContext(r.Context(), selectRoute+` ORDER BY created_at, id`)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	out := []*route{}
	for rows.Next() {
		rt, err := scanRoute(rows)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		out = append(out, rt)
	}
	if err := rows.Err(); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type routeInput struct {
	ChannelID      *string   `json:"channel_id"`
	ChannelName    *string   `json:"channel_name"`
	ChannelPrivate *bool     `json:"channel_private"`
	Events         *[]string `json:"events"`
	ProjectIDs     *[]string `json:"project_ids"`
	Priorities     *[]string `json:"priorities"`
	Threads        *bool     `json:"threads"`
	IncludeBody    *bool     `json:"include_body"`
	Internal       *bool     `json:"include_internal"`
	Active         *bool     `json:"active"`
}

// channelIDPattern: public (C…) and private/legacy group (G…) channel IDs.
var channelIDPattern = regexp.MustCompile(`^[CG][A-Z0-9]{2,30}$`)

// apply validates in over rt (create: rt is the zero route with defaults).
func (m *Module) apply(ctx context.Context, rt *route, in routeInput, create bool) error {
	var fe []problems.FieldError
	if in.ChannelID != nil {
		v := strings.TrimSpace(*in.ChannelID)
		if v == "" {
			fe = append(fe, problems.Field("channel_id", "required", "pick a channel"))
		} else if !channelIDPattern.MatchString(v) {
			fe = append(fe, problems.Field("channel_id", "pattern", "a Slack channel ID such as C0123ABCD"))
		}
		rt.ChannelID = v
	} else if create {
		fe = append(fe, problems.Field("channel_id", "required", "pick a channel"))
	}
	if in.ChannelName != nil {
		v := strings.TrimPrefix(strings.TrimSpace(*in.ChannelName), "#")
		if len(v) > 80 {
			fe = append(fe, problems.Field("channel_name", "max_length", "at most 80 characters"))
		}
		rt.ChannelName = v
	}
	if in.ChannelPrivate != nil {
		rt.ChannelPrivate = *in.ChannelPrivate
	}
	for _, b := range []struct {
		in  *bool
		dst *bool
	}{{in.Threads, &rt.Threads}, {in.IncludeBody, &rt.IncludeBody}, {in.Internal, &rt.Internal}, {in.Active, &rt.Active}} {
		if b.in != nil {
			*b.dst = *b.in
		}
	}
	if in.Events != nil {
		evs := slices.Clone(*in.Events)
		slices.Sort(evs)
		rt.Events = slices.Compact(evs)
		if len(rt.Events) == 0 {
			fe = append(fe, problems.Field("events", "required", "choose at least one event"))
		}
		for _, e := range rt.Events {
			if !slices.Contains(Triggers, e) {
				fe = append(fe, problems.Field("events", "enum", "unknown event "+e))
				break
			}
		}
	} else if create {
		fe = append(fe, problems.Field("events", "required", "choose at least one event"))
	}
	if slices.Contains(rt.Events, TrigNote) && !rt.Internal {
		fe = append(fe, problems.Field("events", "not_allowed", "internal notes need include_internal"))
	}
	if in.Priorities != nil {
		ps := slices.Clone(*in.Priorities)
		slices.Sort(ps)
		rt.Priorities = slices.Compact(ps)
		for _, p := range rt.Priorities {
			if !domain.Priority(p).Valid() {
				fe = append(fe, problems.Field("priorities", "enum", "low, normal, high or urgent"))
				break
			}
		}
	}
	if in.ProjectIDs != nil {
		ids := []string{}
		for i, ref := range *in.ProjectIDs {
			var id string
			if err := m.db.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ? OR key = ?`, ref, strings.ToUpper(ref)).Scan(&id); err != nil {
				fe = append(fe, problems.Field("project_ids["+strconv.Itoa(i)+"]", "not_found", "unknown project"))
				continue
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		rt.ProjectIDs = ids
	}
	if len(fe) > 0 {
		return problems.Validation(fe...)
	}
	return nil
}

func (rt *route) auditData() map[string]any {
	return map[string]any{"channel_id": rt.ChannelID, "channel": rt.ChannelName, "events": rt.Events, "project_ids": rt.ProjectIDs,
		"priorities": rt.Priorities, "include_internal": rt.Internal, "active": rt.Active}
}

func (m *Module) createRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in routeInput
	if err := decode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	rt := &route{Threads: true, IncludeBody: true, Active: true, ProjectIDs: []string{}, Priorities: []string{}}
	if err := m.apply(ctx, rt, in, true); err != nil {
		problems.Write(w, r, err)
		return
	}
	rt.ID = store.NewID()
	now := store.Now()
	err := m.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO slack_routes (id, channel_id, channel_name, channel_private, events, project_ids, priorities, threads, include_body, include_internal, active, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			rt.ID, rt.ChannelID, rt.ChannelName, rt.ChannelPrivate, store.MustJSON(rt.Events), store.MustJSON(rt.ProjectIDs), store.MustJSON(rt.Priorities),
			rt.Threads, rt.IncludeBody, rt.Internal, rt.Active, now, now)
		return err
	})
	if err == nil {
		err = m.audit(r, "", "slack.route_created", rt.ID, rt.auditData())
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.invalidate()
	m.writeRoute(w, r, rt.ID, http.StatusCreated)
}

func (m *Module) writeRoute(w http.ResponseWriter, r *http.Request, id string, status int) {
	rt, err := m.loadRoute(r.Context(), id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, status, rt)
}

func (m *Module) patchRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rt, err := m.loadRoute(ctx, chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	var in routeInput
	if err := decode(r, &in); err != nil {
		problems.Write(w, r, err)
		return
	}
	prevChannel := rt.ChannelID
	if err := m.apply(ctx, rt, in, false); err != nil {
		problems.Write(w, r, err)
		return
	}
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		// A new channel (or a re-enable) clears the error; a new channel
		// also drops the thread roots, which live in the old channel.
		clear := rt.ChannelID != prevChannel || (in.Active != nil && *in.Active)
		_, err := tx.ExecContext(ctx, `UPDATE slack_routes SET channel_id = ?, channel_name = ?, channel_private = ?, events = ?, project_ids = ?, priorities = ?, threads = ?, include_body = ?, include_internal = ?, active = ?,
			last_error = CASE WHEN ? THEN '' ELSE last_error END, last_error_at = CASE WHEN ? THEN NULL ELSE last_error_at END, updated_at = ? WHERE id = ?`,
			rt.ChannelID, rt.ChannelName, rt.ChannelPrivate, store.MustJSON(rt.Events), store.MustJSON(rt.ProjectIDs), store.MustJSON(rt.Priorities),
			rt.Threads, rt.IncludeBody, rt.Internal, rt.Active, clear, clear, store.Now(), rt.ID)
		if err != nil || rt.ChannelID == prevChannel {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM slack_threads WHERE route_id = ?`, rt.ID)
		return err
	})
	if err == nil {
		err = m.audit(r, "", "slack.route_updated", rt.ID, rt.auditData())
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.invalidate()
	m.writeRoute(w, r, rt.ID, http.StatusOK)
}

func (m *Module) deleteRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rt, err := m.loadRoute(ctx, chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	err = m.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM slack_routes WHERE id = ?`, rt.ID)
		return err
	})
	if err == nil {
		err = m.audit(r, "", "slack.route_deleted", rt.ID, map[string]any{"channel_id": rt.ChannelID, "channel": rt.ChannelName})
	}
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// connected returns the bot token or a 409 problem.
func (m *Module) connected(ctx context.Context) (string, error) {
	d, err := m.load(ctx)
	if err != nil {
		return "", err
	}
	tok, err := m.token(d)
	if err != nil {
		return "", err
	}
	if tok == "" {
		return "", problems.Conflict("Slack is not connected")
	}
	return tok, nil
}

// slackProblem turns a Slack API error into a response (auth errors also
// mark the connection broken).
func (m *Module) slackProblem(ctx context.Context, err error) error {
	e, ok := AsAPIError(err)
	if !ok {
		return err
	}
	if e.Auth() {
		m.markBroken(context.WithoutCancel(ctx), e.Code)
		return problems.Conflict("Slack rejected the token (" + e.Code + "): reconnect the workspace")
	}
	return problems.New(problems.SlugUnavailable, "Slack: "+e.Code)
}

// channelTTL: the picker list is cached; refresh=true bypasses it.
const channelTTL = 60 * time.Second

func (m *Module) listChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok, err := m.connected(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.mu.Lock()
	c := m.channels
	m.mu.Unlock()
	if c == nil || r.URL.Query().Get("refresh") == "true" || m.Now().Sub(c.at) > channelTTL {
		list, err := m.api.Channels(ctx, tok)
		if err != nil {
			problems.Write(w, r, m.slackProblem(ctx, err))
			return
		}
		slices.SortFunc(list, func(a, b Channel) int { return strings.Compare(a.Name, b.Name) })
		c = &channelCache{at: m.Now(), list: list}
		m.mu.Lock()
		m.channels = c
		m.mu.Unlock()
	}
	out := make([]map[string]any, 0, len(c.list))
	for _, ch := range c.list {
		out = append(out, map[string]any{"id": ch.ID, "name": ch.Name, "is_private": ch.IsPrivate, "is_member": ch.IsMember, "num_members": ch.NumMembers})
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) testRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rt, err := m.loadRoute(ctx, chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	tok, err := m.connected(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	msg := Message{Channel: rt.ChannelID, Text: "FeatherDesk test message",
		Blocks: []Block{{"type": "section", "text": mrkdwn(":white_check_mark: *Test message from FeatherDesk.* Ticket notifications for this rule will appear in this channel.")}}}
	_, perr := m.api.Post(ctx, tok, msg)
	res := map[string]any{"ok": perr == nil, "error": nil}
	if perr != nil {
		e, ok := AsAPIError(perr)
		if !ok {
			problems.Write(w, r, perr)
			return
		}
		if e.Auth() {
			m.markBroken(context.WithoutCancel(ctx), e.Code)
		}
		res["error"] = e.Code
	}
	m.routeResult(context.WithoutCancel(ctx), rt.ID, perr)
	if err := m.audit(r, "", "slack.route_tested", rt.ID, map[string]any{"ok": perr == nil}); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// routeResult records a post outcome on the route: success clears the
// error, a channel error sets it.
func (m *Module) routeResult(ctx context.Context, id string, err error) {
	now := m.Now().UTC().Truncate(time.Microsecond)
	var q string
	var args []any
	if err == nil {
		q, args = `UPDATE slack_routes SET last_error = '', last_error_at = NULL, last_posted_at = ? WHERE id = ?`, []any{now, id}
	} else if e, ok := AsAPIError(err); ok && e.Channel() {
		q, args = `UPDATE slack_routes SET last_error = ?, last_error_at = ? WHERE id = ?`, []any{e.Code, now, id}
	} else {
		return
	}
	// The route cache holds no error fields, so it stays valid.
	if err := m.db.Tx(ctx, func(tx *store.Tx) error { _, err := tx.ExecContext(ctx, q, args...); return err }); err != nil {
		m.log.Error("slack: record route result", "route", id, "err", err)
	}
}
