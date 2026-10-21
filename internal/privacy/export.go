// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

// Export (data subject access, D-19 §C). The job streams a ZIP of JSON files
// straight into blob storage (no full copy in memory):
//
//	README.txt        what each file holds
//	profile.json      the account, organization, teams, identities, sessions, API tokens
//	tickets.json      tickets the user requested, with their conversation and field values
//	messages.json     every message the user wrote
//	attachments.json  metadata of the files the user uploaded; files under attachments/
//	consents.json     consent records
//	audit.json        audit entries about the user or by the user
//	requests.json     the user's privacy requests
//
// Without include_internal (every self-service export) the archive never
// holds internal notes, system messages, staff-only fields, messages written
// by other customers or other actors' identities and IPs.

// exportScope controls what an archive may contain.
type exportScope struct {
	userID   string
	internal bool // admin export with include_internal
}

// RunExport builds the archive of one pending export request. It is
// idempotent: a request that is no longer pending/processing is left alone.
func (m *Module) RunExport(ctx context.Context, id string) error {
	now := store.Now()
	var claimed bool
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'processing', updated_at = ? WHERE id = ? AND type = 'export' AND status IN ('pending', 'processing')`, now, id)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		claimed = n == 1
		return nil
	})
	if err != nil || !claimed {
		return err
	}
	r, err := getReq(ctx, m.d.DB.Read(), id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	set, err := LoadSettings(ctx, m.d.Settings)
	if err != nil {
		return err
	}
	key := blob.NewKey(exportPrefix, now)
	size, err := m.writeArchive(ctx, key, exportScope{userID: r.UserID, internal: r.IncludeInternal})
	if err != nil {
		m.log.Error("privacy: export failed", "request", id, "err", err)
		m.deleteBlobs(ctx, []string{key})
		return m.d.DB.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'failed', error = 'export failed; request a new one', updated_at = ? WHERE id = ?`, store.Now(), id)
			return err
		})
	}
	done := store.Now()
	expires := done.Add(time.Duration(set.Retention.ExportDays) * 24 * time.Hour)
	var updated bool
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE privacy_requests SET status = 'ready', file_key = ?, file_size = ?, error = '', completed_at = ?, expires_at = ?, updated_at = ?
			WHERE id = ? AND status = 'processing'`, key, size, done, expires, done, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		updated = n == 1
		return nil
	})
	if err != nil || !updated {
		// The user was erased or deleted meanwhile: drop the file.
		m.deleteBlobs(ctx, []string{key})
		return err
	}
	m.sendReadyMail(ctx, r, set.Retention.ExportDays)
	return nil
}

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// writeArchive streams the ZIP into blob storage and returns its size.
func (m *Module) writeArchive(ctx context.Context, key string, sc exportScope) (int64, error) {
	pr, pw := io.Pipe()
	werr := make(chan error, 1)
	go func() {
		err := m.writeZip(ctx, pw, sc)
		_ = pw.CloseWithError(err)
		werr <- err
	}()
	cr := &countingReader{r: pr}
	_, perr := m.d.Blob.Put(ctx, key, cr, -1, "application/zip")
	_ = pr.CloseWithError(errors.New("privacy: blob write ended"))
	if err := <-werr; err != nil {
		return 0, err
	}
	if perr != nil {
		return 0, perr
	}
	return cr.n.Load(), nil
}

const readme = `This archive holds the personal data this help desk stores about you.

profile.json      your account, organization, teams, sign-in identities, sessions and API tokens
tickets.json      the tickets you opened, with their conversation and field values
messages.json     every message you wrote
attachments.json  the files you uploaded (the files are in attachments/)
consents.json     the privacy policies you accepted
audit.json        security and administration events about your account or done by you
requests.json     your data export and deletion requests

Times are UTC (RFC 3339). Messages are Markdown.
`

func (m *Module) writeZip(ctx context.Context, w io.Writer, sc exportScope) error {
	zw := zip.NewWriter(w)
	file := func(name string) (io.Writer, error) {
		return zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: store.Now()})
	}
	f, err := file("README.txt")
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, readme); err != nil {
		return err
	}
	steps := []struct {
		name string
		fn   func(context.Context, *arrayWriter, exportScope) error
	}{
		{"tickets.json", m.exportTickets},
		{"messages.json", m.exportMessages},
		{"consents.json", m.exportConsents},
		{"audit.json", m.exportAudit},
		{"requests.json", m.exportRequests},
	}
	f, err = file("profile.json")
	if err != nil {
		return err
	}
	if err := m.exportProfile(ctx, f, sc); err != nil {
		return err
	}
	for _, s := range steps {
		f, err := file(s.name)
		if err != nil {
			return err
		}
		aw := newArrayWriter(f)
		if err := s.fn(ctx, aw, sc); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		if err := aw.close(); err != nil {
			return err
		}
	}
	files, err := m.exportAttachmentsMeta(ctx, file, sc)
	if err != nil {
		return err
	}
	for _, a := range files {
		if err := m.copyAttachment(ctx, file, a); err != nil {
			return err
		}
	}
	return zw.Close()
}

// arrayWriter streams a JSON array one element at a time.
type arrayWriter struct {
	w     io.Writer
	n     int
	err   error
	begun bool
}

func newArrayWriter(w io.Writer) *arrayWriter { return &arrayWriter{w: w} }

func (a *arrayWriter) add(v any) error {
	if a.err != nil {
		return a.err
	}
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		a.err = err
		return err
	}
	sep := ",\n  "
	if !a.begun {
		sep, a.begun = "[\n  ", true
	}
	if _, err := io.WriteString(a.w, sep); err != nil {
		a.err = err
		return err
	}
	_, a.err = a.w.Write(b)
	a.n++
	return a.err
}

func (a *arrayWriter) close() error {
	if a.err != nil {
		return a.err
	}
	end := "\n]\n"
	if !a.begun {
		end = "[]\n"
	}
	_, err := io.WriteString(a.w, end)
	return err
}

// each runs fn for every row of query.
func each(ctx context.Context, q store.Querier, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func ts(t sql.NullTime) *time.Time { return optTime(t) }

func (m *Module) exportProfile(ctx context.Context, w io.Writer, sc exportScope) error {
	q := m.d.DB.Read()
	var p struct {
		ID           string          `json:"id"`
		Kind         string          `json:"kind"`
		Email        string          `json:"email"`
		Name         string          `json:"name"`
		Role         string          `json:"role"`
		Tier         *int            `json:"tier,omitempty"`
		Organization *string         `json:"organization"`
		Status       string          `json:"status"`
		Locale       string          `json:"locale"`
		Signature    string          `json:"signature_md,omitempty"`
		NotifyPrefs  json.RawMessage `json:"notification_preferences"`
		LastLoginAt  *time.Time      `json:"last_login_at"`
		CreatedAt    time.Time       `json:"created_at"`
		UpdatedAt    time.Time       `json:"updated_at"`
		Teams        []string        `json:"teams,omitempty"`
		Identities   []any           `json:"sign_in_identities"`
		Sessions     []any           `json:"sessions"`
		APITokens    []any           `json:"api_tokens"`
	}
	var tier sql.NullInt64
	var org sql.NullString
	var prefs store.RawJSON
	var last sql.NullTime
	err := q.QueryRowContext(ctx, `SELECT u.id, u.kind, u.email, u.name, u.role, u.tier, o.name, u.status, u.locale, u.signature_md, u.notify_prefs,
		u.last_login_at, u.created_at, u.updated_at FROM users u LEFT JOIN organizations o ON o.id = u.organization_id WHERE u.id = ?`, sc.userID).
		Scan(&p.ID, &p.Kind, &p.Email, &p.Name, &p.Role, &tier, &org, &p.Status, &p.Locale, &p.Signature, &prefs, &last, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return store.NotFound(err)
	}
	if tier.Valid {
		t := int(tier.Int64)
		p.Tier = &t
	}
	p.Organization, p.LastLoginAt = optStr(org), ts(last)
	p.NotifyPrefs = json.RawMessage(prefs)
	if len(p.NotifyPrefs) == 0 {
		p.NotifyPrefs = json.RawMessage(`{}`)
	}
	if p.Teams, err = queryStrings(ctx, q, `SELECT t.name FROM team_members m JOIN teams t ON t.id = m.team_id WHERE m.user_id = ? ORDER BY t.name`, sc.userID); err != nil {
		return err
	}
	p.Identities, p.Sessions, p.APITokens = []any{}, []any{}, []any{}
	if err := each(ctx, q, `SELECT audience, issuer, subject, email_at_link, created_at FROM user_identities WHERE user_id = ? ORDER BY created_at`, []any{sc.userID}, func(r *sql.Rows) error {
		var aud, iss, sub, email string
		var at time.Time
		if err := r.Scan(&aud, &iss, &sub, &email, &at); err != nil {
			return err
		}
		p.Identities = append(p.Identities, map[string]any{"audience": aud, "issuer": iss, "subject": sub, "email_at_link": email, "created_at": at.UTC()})
		return nil
	}); err != nil {
		return err
	}
	if err := each(ctx, q, `SELECT audience, ip, user_agent, created_at, last_seen_at, expires_at FROM sessions WHERE user_id = ? ORDER BY created_at`, []any{sc.userID}, func(r *sql.Rows) error {
		var aud, ip, ua string
		var c, l, e time.Time
		if err := r.Scan(&aud, &ip, &ua, &c, &l, &e); err != nil {
			return err
		}
		p.Sessions = append(p.Sessions, map[string]any{"audience": aud, "ip": ip, "user_agent": ua, "created_at": c.UTC(), "last_seen_at": l.UTC(), "expires_at": e.UTC()})
		return nil
	}); err != nil {
		return err
	}
	if err := each(ctx, q, `SELECT name, prefix, scopes, created_at, last_used_at, expires_at, revoked_at FROM api_tokens WHERE user_id = ? ORDER BY created_at`, []any{sc.userID}, func(r *sql.Rows) error {
		var name, prefix string
		var scopes store.RawJSON
		var c time.Time
		var used, exp, rev sql.NullTime
		if err := r.Scan(&name, &prefix, &scopes, &c, &used, &exp, &rev); err != nil {
			return err
		}
		p.APITokens = append(p.APITokens, map[string]any{"name": name, "prefix": prefix, "scopes": json.RawMessage(scopes), "created_at": c.UTC(),
			"last_used_at": ts(used), "expires_at": ts(exp), "revoked_at": ts(rev)})
		return nil
	}); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// fieldValue picks the stored value of a custom field.
func fieldValue(text sql.NullString, num sql.NullFloat64, date any, js store.RawJSON) any {
	switch {
	case text.Valid:
		return text.String
	case num.Valid:
		return num.Float64
	case len(js) > 0 && string(js) != "null":
		return json.RawMessage(js)
	}
	switch d := date.(type) {
	case time.Time:
		return d.UTC().Format("2006-01-02")
	case string:
		return strings.TrimSpace(d)
	case []byte:
		return string(d)
	}
	return nil
}

func (m *Module) exportTickets(ctx context.Context, aw *arrayWriter, sc exportScope) error {
	q := m.d.DB.Read()
	type ticket struct {
		ID, Subject, Status, Channel string
		Number                       int64
		Priority                     string
		Created, Updated             time.Time
		Resolved, Closed             sql.NullTime
		CSAT                         sql.NullInt64
		CSATComment                  sql.NullString
	}
	var list []ticket
	if err := each(ctx, q, `SELECT id, number, subject, status, priority, channel, created_at, updated_at, resolved_at, closed_at, csat_score, csat_comment
		FROM tickets WHERE requester_id = ? ORDER BY number`, []any{sc.userID}, func(r *sql.Rows) error {
		var t ticket
		if err := r.Scan(&t.ID, &t.Number, &t.Subject, &t.Status, &t.Priority, &t.Channel, &t.Created, &t.Updated, &t.Resolved, &t.Closed, &t.CSAT, &t.CSATComment); err != nil {
			return err
		}
		list = append(list, t)
		return nil
	}); err != nil {
		return err
	}
	for _, t := range list {
		out := map[string]any{"number": t.Number, "subject": t.Subject, "channel": t.Channel, "created_at": t.Created.UTC(), "updated_at": t.Updated.UTC(),
			"resolved_at": ts(t.Resolved), "closed_at": ts(t.Closed)}
		if sc.internal {
			out["id"], out["status"], out["priority"] = t.ID, t.Status, t.Priority
		} else {
			out["status"] = tickets.CustomerStatus(domain.TicketStatus(t.Status))
		}
		if t.CSAT.Valid {
			out["satisfaction"] = map[string]any{"score": t.CSAT.Int64, "comment": optStr(t.CSATComment)}
		}
		msgs := []any{}
		if err := each(ctx, q, `SELECT m.id, m.kind, m.channel, COALESCE(m.author_id, ''), COALESCE(u.kind, ''), m.body_md, m.created_at, m.edited_at
			FROM ticket_messages m LEFT JOIN users u ON u.id = m.author_id WHERE m.ticket_id = ? ORDER BY m.created_at, m.id`, []any{t.ID}, func(r *sql.Rows) error {
			var id, kind, ch, author, akind, body string
			var at time.Time
			var edited sql.NullTime
			if err := r.Scan(&id, &kind, &ch, &author, &akind, &body, &at, &edited); err != nil {
				return err
			}
			from := "system"
			switch {
			case author == sc.userID:
				from = "you"
			case akind == string(domain.KindStaff):
				from = "staff"
			case akind == string(domain.KindCustomer):
				from = "customer"
			}
			if !sc.internal && (kind != string(domain.MessagePublic) || from == "customer" || from == "system") {
				return nil
			}
			msg := map[string]any{"from": from, "channel": ch, "body_md": body, "created_at": at.UTC(), "edited_at": ts(edited)}
			if sc.internal {
				msg["id"], msg["kind"], msg["author_id"] = id, kind, author
			}
			msgs = append(msgs, msg)
			return nil
		}); err != nil {
			return err
		}
		out["messages"] = msgs
		fields := map[string]any{}
		if err := each(ctx, q, `SELECT f.key, f.customer_visible, v.value_text, v.value_num, v.value_date, v.value_json
			FROM ticket_field_values v JOIN custom_field_defs f ON f.id = v.field_id WHERE v.ticket_id = ? ORDER BY f.key`, []any{t.ID}, func(r *sql.Rows) error {
			var key string
			var visible bool
			var text sql.NullString
			var num sql.NullFloat64
			var date any
			var js store.RawJSON
			if err := r.Scan(&key, &visible, &text, &num, &date, &js); err != nil {
				return err
			}
			if visible || sc.internal {
				fields[key] = fieldValue(text, num, date, js)
			}
			return nil
		}); err != nil {
			return err
		}
		out["fields"] = fields
		if err := aw.add(out); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) exportMessages(ctx context.Context, aw *arrayWriter, sc exportScope) error {
	where := `m.author_id = ?`
	if !sc.internal {
		where += ` AND m.kind = 'public'`
	}
	return each(ctx, m.d.DB.Read(), `SELECT t.number, m.kind, m.channel, m.body_md, m.created_at, m.edited_at FROM ticket_messages m
		JOIN tickets t ON t.id = m.ticket_id WHERE `+where+` ORDER BY m.created_at, m.id`, []any{sc.userID}, func(r *sql.Rows) error {
		var num int64
		var kind, ch, body string
		var at time.Time
		var edited sql.NullTime
		if err := r.Scan(&num, &kind, &ch, &body, &at, &edited); err != nil {
			return err
		}
		return aw.add(map[string]any{"ticket_number": num, "kind": kind, "channel": ch, "body_md": body, "created_at": at.UTC(), "edited_at": ts(edited)})
	})
}

func (m *Module) exportConsents(ctx context.Context, aw *arrayWriter, sc exportScope) error {
	return each(ctx, m.d.DB.Read(), `SELECT kind, policy_url, policy_version, ip, accepted_at FROM user_consents WHERE user_id = ? ORDER BY accepted_at`, []any{sc.userID}, func(r *sql.Rows) error {
		var kind, url, ver, ip string
		var at time.Time
		if err := r.Scan(&kind, &url, &ver, &ip, &at); err != nil {
			return err
		}
		return aw.add(map[string]any{"kind": kind, "policy_url": url, "policy_version": ver, "ip": ip, "accepted_at": at.UTC()})
	})
}

// exportAudit: entries about the user (with their data) and entries the user
// caused (without the other party's id or data). Other actors are never named.
func (m *Module) exportAudit(ctx context.Context, aw *arrayWriter, sc exportScope) error {
	return each(ctx, m.d.DB.Read(), `SELECT action, COALESCE(actor_id, ''), target_type, target_id, data, ip, created_at FROM audit_log
		WHERE (target_type = 'user' AND target_id = ?) OR actor_id = ? ORDER BY created_at, id`, []any{sc.userID, sc.userID}, func(r *sql.Rows) error {
		var action, actor, ttype, tid, ip string
		var data store.RawJSON
		var at time.Time
		if err := r.Scan(&action, &actor, &ttype, &tid, &data, &ip, &at); err != nil {
			return err
		}
		about := ttype == "user" && tid == sc.userID
		by := "staff"
		switch actor {
		case sc.userID:
			by = "you"
		case "":
			by = "system"
		}
		e := map[string]any{"action": action, "at": at.UTC(), "by": by, "about_you": about, "target_type": ttype}
		if by == "you" {
			e["ip"] = ip
		}
		if about && len(data) > 0 {
			e["data"] = json.RawMessage(data)
		}
		return aw.add(e)
	})
}

func (m *Module) exportRequests(ctx context.Context, aw *arrayWriter, sc exportScope) error {
	return each(ctx, m.d.DB.Read(), `SELECT type, status, created_at, completed_at FROM privacy_requests WHERE user_id = ? ORDER BY created_at, id`, []any{sc.userID}, func(r *sql.Rows) error {
		var typ, status string
		var at time.Time
		var done sql.NullTime
		if err := r.Scan(&typ, &status, &at, &done); err != nil {
			return err
		}
		return aw.add(map[string]any{"type": typ, "status": status, "created_at": at.UTC(), "completed_at": ts(done)})
	})
}

type attFile struct {
	key, path string
}

// exportAttachmentsMeta writes attachments.json and returns the files to copy.
func (m *Module) exportAttachmentsMeta(ctx context.Context, file func(string) (io.Writer, error), sc exportScope) ([]attFile, error) {
	f, err := file("attachments.json")
	if err != nil {
		return nil, err
	}
	aw := newArrayWriter(f)
	var files []attFile
	err = each(ctx, m.d.DB.Read(), `SELECT a.id, COALESCE(t.number, 0), COALESCE(msg.kind, ''), a.filename, a.content_type, a.size_bytes, a.sha256, a.storage_key, a.created_at
		FROM attachments a LEFT JOIN tickets t ON t.id = a.ticket_id LEFT JOIN ticket_messages msg ON msg.id = a.message_id
		WHERE a.uploader_id = ? ORDER BY a.created_at, a.id`, []any{sc.userID}, func(r *sql.Rows) error {
		var id, kind, name, ct, sum, key string
		var num, size int64
		var at time.Time
		if err := r.Scan(&id, &num, &kind, &name, &ct, &size, &sum, &key, &at); err != nil {
			return err
		}
		if !sc.internal && kind != "" && kind != string(domain.MessagePublic) {
			return nil
		}
		p := "attachments/" + id + "/" + safeName(name)
		e := map[string]any{"filename": name, "content_type": ct, "size_bytes": size, "sha256": sum, "created_at": at.UTC(), "file": p}
		if num > 0 {
			e["ticket_number"] = num
		}
		files = append(files, attFile{key: key, path: p})
		return aw.add(e)
	})
	if err != nil {
		return nil, err
	}
	return files, aw.close()
}

func (m *Module) copyAttachment(ctx context.Context, file func(string) (io.Writer, error), a attFile) error {
	rc, _, err := m.d.Blob.Open(ctx, a.key)
	if errors.Is(err, blob.ErrNotFound) {
		f, err := file(a.path + ".missing.txt")
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, "The stored file is no longer available.\n")
		return err
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := file(a.path)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, rc)
	return err
}

// safeName keeps a ZIP entry name inside its folder (no zip-slip): path
// separators, control characters and dot-only names are replaced.
func safeName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == ':' || unicode.IsControl(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" || strings.Trim(s, ".") == "" {
		s = "file"
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	return s
}
