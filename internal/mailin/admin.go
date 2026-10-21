// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ---- DTOs (api/openapi.yaml InboundMailbox, ImapConfig, InboundMessage) ----

type imapDTO struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Security          string `json:"security"`
	Username          string `json:"username"`
	PasswordSet       bool   `json:"password_set"`
	Folder            string `json:"folder"`
	ProcessedAction   string `json:"processed_action"`
	ProcessedFolder   string `json:"processed_folder"`
	Auth              string `json:"auth"`
	AllowInsecureTLS  bool   `json:"allow_insecure_tls"`
	OAuthTokenURL     string `json:"oauth_token_url,omitempty"`
	OAuthClientID     string `json:"oauth_client_id,omitempty"`
	OAuthSecretSet    bool   `json:"oauth_client_secret_set"`
	RefreshTokenSet   bool   `json:"refresh_token_set"`
	TrustedAuthservID string `json:"trusted_authserv_id"`
	AllowListMail     bool   `json:"allow_list_mail"`
}

type mailboxDTO struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Kind        string     `json:"kind"`
	TeamID      *string    `json:"team_id"`
	ProjectID   string     `json:"project_id"`
	Address     string     `json:"address"`
	Active      bool       `json:"active"`
	IMAP        *imapDTO   `json:"imap"`
	WebhookURL  *string    `json:"webhook_url"`
	LockedByEnv []string   `json:"locked_by_env"`
	LastOKAt    *time.Time `json:"last_ok_at"`
	LastError   *string    `json:"last_error"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (m *Module) mailboxDTO(ctx context.Context, mb *mailbox) mailboxDTO {
	d := mailboxDTO{ID: mb.ID, Name: mb.Name, Kind: mb.Kind, ProjectID: mb.ProjectID, Address: mb.Address, Active: mb.Active, LockedByEnv: mb.LockedByEnv, CreatedAt: mb.CreatedAt.UTC()}
	if t := m.effectiveTeam(ctx, mb); t != "" {
		d.TeamID = &t
	}
	if mb.LastOKAt.Valid {
		t := mb.LastOKAt.Time.UTC()
		d.LastOKAt = &t
	}
	if mb.LastError != "" {
		e := mb.LastError
		d.LastError = &e
	}
	if mb.Kind == "imap" {
		c := mb.IMAP
		pw := c.PasswordEnc != "" || (c.EnvManaged && m.d.Getenv("ST_IMAP_PASSWORD") != "")
		d.IMAP = &imapDTO{Host: c.Host, Port: c.Port, Security: c.Security, Username: c.Username, PasswordSet: pw, Folder: c.Folder,
			ProcessedAction: c.ProcessedAction, ProcessedFolder: c.ProcessedFolder, Auth: c.Auth, AllowInsecureTLS: c.AllowInsecureTLS,
			OAuthTokenURL: c.OAuthTokenURL, OAuthClientID: c.OAuthClientID, OAuthSecretSet: c.OAuthClientSecret != "",
			RefreshTokenSet: c.OAuthRefreshEnc != "", TrustedAuthservID: c.TrustedAuthservID, AllowListMail: c.AllowListMail}
	}
	return d
}

// mailboxIn is InboundMailboxCreate (also used for PATCH: absent = keep).
type mailboxIn struct {
	Name              *string `json:"name"`
	Kind              *string `json:"kind"`
	TeamID            *string `json:"team_id"`
	teamSet           bool    // team_id key present (null clears)
	ProjectID         *string `json:"project_id"` // D-20: id or key; omitted on create = the default project
	Address           *string `json:"address"`
	Active            *bool   `json:"active"`
	IMAP              *imapIn `json:"imap"`
	WebhookHMACSecret *string `json:"webhook_hmac_secret"`
}

type imapIn struct {
	Host              *string `json:"host"`
	Port              *int    `json:"port"`
	Security          *string `json:"security"`
	Username          *string `json:"username"`
	Password          *string `json:"password"`
	Folder            *string `json:"folder"`
	ProcessedAction   *string `json:"processed_action"`
	ProcessedFolder   *string `json:"processed_folder"`
	Auth              *string `json:"auth"`
	AllowInsecureTLS  *bool   `json:"allow_insecure_tls"`
	OAuthTokenURL     *string `json:"oauth_token_url"`
	OAuthClientID     *string `json:"oauth_client_id"`
	OAuthClientSecret *string `json:"oauth_client_secret"`
	RefreshToken      *string `json:"refresh_token"`
	TrustedAuthservID *string `json:"trusted_authserv_id"`
	AllowListMail     *bool   `json:"allow_list_mail"`
}

func decodeMailboxIn(r *http.Request) (*mailboxIn, error) {
	var raw map[string]json.RawMessage
	body := json.NewDecoder(r.Body)
	if err := body.Decode(&raw); err != nil {
		return nil, problems.BadRequest("invalid JSON body")
	}
	b, _ := json.Marshal(raw)
	var in mailboxIn
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, problems.BadRequest("invalid JSON body")
	}
	_, in.teamSet = raw["team_id"]
	return &in, nil
}

func validAddr(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}

// apply merges in over mb (create or patch) and validates.
func (m *Module) apply(ctx context.Context, mb *mailbox, in *mailboxIn, create bool) (newToken string, err error) {
	var fe []problems.FieldError
	locked := func(field string) bool { return slices.Contains(mb.LockedByEnv, field) }
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 120 {
			fe = append(fe, problems.Field("name", "invalid", "1..120 characters"))
		}
		mb.Name = n
	}
	if in.Kind != nil {
		if !create && *in.Kind != mb.Kind {
			fe = append(fe, problems.Field("kind", "immutable", "kind cannot change"))
		} else if *in.Kind != "imap" && *in.Kind != "webhook" {
			fe = append(fe, problems.Field("kind", "enum", "imap or webhook"))
		}
		mb.Kind = *in.Kind
	}
	if in.Address != nil {
		a := strings.ToLower(strings.TrimSpace(*in.Address))
		if !validAddr(a) {
			fe = append(fe, problems.Field("address", "email", "invalid address"))
		}
		mb.Address = a
	}
	if in.Active != nil {
		mb.Active = *in.Active
	}
	if in.teamSet {
		t := ""
		if in.TeamID != nil {
			t = *in.TeamID
		}
		if locked("team_id") && t != m.effectiveTeam(ctx, mb) {
			fe = append(fe, problems.Field("team_id", "locked_by_env", "set by ST_IMAP_TEAM"))
		} else if !locked("team_id") {
			if t != "" {
				var one int
				if e := m.d.DB.Read().QueryRowContext(ctx, `SELECT 1 FROM teams WHERE id = ?`, t).Scan(&one); e != nil {
					fe = append(fe, problems.Field("team_id", "not_found", "unknown team"))
				}
			}
			mb.TeamID = t
		}
	}
	if in.ProjectID != nil && *in.ProjectID != "" {
		var id string
		err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ? OR key = ?`, *in.ProjectID, strings.ToUpper(*in.ProjectID)).Scan(&id)
		if err != nil {
			fe = append(fe, problems.Field("project_id", "not_found", "unknown project"))
		} else {
			mb.ProjectID = id
		}
	} else if create || mb.ProjectID == "" {
		mb.ProjectID = m.defaultProject(ctx)
	}
	if t := m.effectiveTeam(ctx, mb); t != "" && mb.ProjectID != "" {
		var one int
		if e := m.d.DB.Read().QueryRowContext(ctx, `SELECT 1 FROM project_teams WHERE project_id = ? AND team_id = ?`, mb.ProjectID, t).Scan(&one); e != nil {
			fe = append(fe, problems.Field("team_id", "wrong_project", "the team does not serve the mailbox's project"))
		}
	}
	if mb.Kind == "imap" && in.IMAP != nil {
		fe = append(fe, m.applyIMAP(mb, in.IMAP)...)
	}
	if mb.Kind == "imap" {
		c := mb.IMAP
		if c.Host == "" {
			fe = append(fe, problems.Field("imap.host", "required", ""))
		}
		if c.Username == "" {
			fe = append(fe, problems.Field("imap.username", "required", ""))
		}
		if c.Port < 1 || c.Port > 65535 {
			fe = append(fe, problems.Field("imap.port", "range", "1..65535"))
		}
		if !slices.Contains([]string{"none", "starttls", "tls"}, c.Security) {
			fe = append(fe, problems.Field("imap.security", "enum", "none, starttls or tls"))
		}
		if !slices.Contains([]string{"move", "flag"}, c.ProcessedAction) {
			fe = append(fe, problems.Field("imap.processed_action", "enum", "move or flag"))
		}
		if !slices.Contains([]string{"password", "xoauth2"}, c.Auth) {
			fe = append(fe, problems.Field("imap.auth", "enum", "password or xoauth2"))
		}
		if c.Auth == "xoauth2" && (c.OAuthTokenURL == "" || c.OAuthClientID == "") {
			fe = append(fe, problems.Field("imap.oauth_token_url", "required", "xoauth2 needs a token URL and client id"))
		}
	}
	if in.WebhookHMACSecret != nil {
		enc, err := m.d.Secrets.SealString(settings.PurposeWebhook, *in.WebhookHMACSecret)
		if err != nil {
			return "", err
		}
		mb.HMACSecretEnc = enc
	}
	if create && mb.Name == "" {
		fe = append(fe, problems.Field("name", "required", ""))
	}
	if create && mb.Address == "" {
		fe = append(fe, problems.Field("address", "required", ""))
	}
	if len(fe) > 0 {
		return "", problems.Validation(fe...)
	}
	if create && mb.Kind == "webhook" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		newToken = base64.RawURLEncoding.EncodeToString(b)
		mb.TokenHash = tokenHash(newToken)
	}
	return newToken, nil
}

// applyIMAP merges the IMAP part; env-locked fields reject a changed value
// (422 locked_by_env) and ignore an echoed one.
func (m *Module) applyIMAP(mb *mailbox, in *imapIn) []problems.FieldError {
	var fe []problems.FieldError
	st, eff := &mb.Stored, &mb.IMAP
	lockedStr := func(field string, v *string, cur string, dst *string) {
		if v == nil {
			return
		}
		if slices.Contains(mb.LockedByEnv, field) {
			if *v != cur {
				fe = append(fe, problems.Field("imap."+field, "locked_by_env", "set by the environment"))
			}
			return
		}
		*dst = strings.TrimSpace(*v)
	}
	lockedStr("host", in.Host, eff.Host, &st.Host)
	lockedStr("security", in.Security, eff.Security, &st.Security)
	lockedStr("username", in.Username, eff.Username, &st.Username)
	lockedStr("folder", in.Folder, eff.Folder, &st.Folder)
	if in.Port != nil {
		if slices.Contains(mb.LockedByEnv, "port") {
			if *in.Port != eff.Port {
				fe = append(fe, problems.Field("imap.port", "locked_by_env", "set by the environment"))
			}
		} else {
			st.Port = *in.Port
		}
	}
	if in.Password != nil {
		if slices.Contains(mb.LockedByEnv, "password") {
			fe = append(fe, problems.Field("imap.password", "locked_by_env", "set by the environment"))
		} else if *in.Password != "" { // empty = keep
			enc, err := m.d.Secrets.SealString(settings.PurposeIMAPPassword, *in.Password)
			if err != nil {
				fe = append(fe, problems.Field("imap.password", "invalid", ""))
			}
			st.PasswordEnc = enc
		}
	}
	set := func(v *string, dst *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(in.ProcessedAction, &st.ProcessedAction)
	set(in.ProcessedFolder, &st.ProcessedFolder)
	set(in.Auth, &st.Auth)
	set(in.OAuthTokenURL, &st.OAuthTokenURL)
	set(in.OAuthClientID, &st.OAuthClientID)
	set(in.TrustedAuthservID, &st.TrustedAuthservID)
	if in.AllowInsecureTLS != nil {
		st.AllowInsecureTLS = *in.AllowInsecureTLS
	}
	if in.AllowListMail != nil {
		st.AllowListMail = *in.AllowListMail
	}
	if in.OAuthClientSecret != nil && *in.OAuthClientSecret != "" {
		enc, _ := m.d.Secrets.SealString(settings.PurposeIMAPRefresh, *in.OAuthClientSecret)
		st.OAuthClientSecret = enc
	}
	if in.RefreshToken != nil && *in.RefreshToken != "" {
		enc, _ := m.d.Secrets.SealString(settings.PurposeIMAPRefresh, *in.RefreshToken)
		st.OAuthRefreshEnc = enc
	}
	// recompute the effective config
	mb.IMAP = mb.Stored
	mb.IMAP.defaults()
	if mb.Stored.EnvManaged {
		m.overlayEnv(mb)
	}
	return fe
}

func (m *Module) audit(ctx context.Context, r *http.Request, action, id string, data map[string]any) {
	actor := ""
	if p := server.PrincipalFrom(ctx); p != nil {
		actor = p.UserID
	}
	if data == nil {
		data = map[string]any{}
	}
	_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			store.NewID(), store.Nullable(actor), action, "inbound_mailbox", id, store.MustJSON(data), server.ClientIP(r), store.Now())
		return err
	})
}

func (m *Module) handleListMailboxes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = m.ensureEnvMailbox(ctx)
	mbs, err := m.listMailboxes(ctx, false)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	out := make([]mailboxDTO, 0, len(mbs))
	for _, mb := range mbs {
		out = append(out, m.mailboxDTO(ctx, mb))
	}
	writeJSON(w, http.StatusOK, out)
}

func (m *Module) handleGetMailbox(w http.ResponseWriter, r *http.Request) {
	mb, err := m.getMailbox(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m.mailboxDTO(r.Context(), mb))
}

func (m *Module) handleCreateMailbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	in, err := decodeMailboxIn(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	mb := &mailbox{Active: true, LockedByEnv: []string{}}
	if in.Kind == nil {
		problems.Write(w, r, problems.Validation(problems.Field("kind", "required", "")))
		return
	}
	mb.Kind = *in.Kind
	if mb.Kind == "imap" {
		mb.Stored.defaults()
		mb.IMAP = mb.Stored
		if in.IMAP == nil {
			problems.Write(w, r, problems.Validation(problems.Field("imap", "required", "")))
			return
		}
	}
	token, err := m.apply(ctx, mb, in, true)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	mb.ID = store.NewID()
	now := store.Now()
	var imapDoc any = "{}"
	if mb.Kind == "imap" {
		imapDoc = store.MustJSON(mb.Stored)
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO inbound_mailboxes (id, name, kind, team_id, address, imap, webhook_token_hash, hmac_secret_enc, active, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			mb.ID, mb.Name, mb.Kind, store.Nullable(mb.TeamID), mb.Address, imapDoc, store.Nullable(mb.TokenHash), mb.HMACSecretEnc, mb.Active, mb.ProjectID, now, now)
		return err
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			problems.Write(w, r, problems.Conflict("mailbox already exists"))
			return
		}
		problems.Write(w, r, err)
		return
	}
	m.audit(ctx, r, "inbound_mailbox.create", mb.ID, map[string]any{"name": mb.Name, "kind": mb.Kind})
	m.sources.reload()
	saved, err := m.getMailbox(ctx, mb.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	dto := m.mailboxDTO(ctx, saved)
	if token != "" {
		u := m.publicURL() + "/api/v1/inbound/mime/" + token
		dto.WebhookURL = &u
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (m *Module) publicURL() string {
	if m.d.Cfg != nil && m.d.Cfg.PublicURL != "" {
		return strings.TrimRight(m.d.Cfg.PublicURL, "/")
	}
	return "http://localhost"
}

func (m *Module) handlePatchMailbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mb, err := m.getMailbox(ctx, chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	in, err := decodeMailboxIn(r)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if _, err := m.apply(ctx, mb, in, false); err != nil {
		problems.Write(w, r, err)
		return
	}
	var imapDoc any = "{}"
	if mb.Kind == "imap" {
		imapDoc = store.MustJSON(mb.Stored)
	}
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inbound_mailboxes SET name = ?, team_id = ?, address = ?, imap = ?, hmac_secret_enc = ?, active = ?, project_id = ?, updated_at = ? WHERE id = ?`,
			mb.Name, store.Nullable(mb.TeamID), mb.Address, imapDoc, mb.HMACSecretEnc, mb.Active, mb.ProjectID, store.Now(), mb.ID)
		return err
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	m.audit(ctx, r, "inbound_mailbox.update", mb.ID, map[string]any{"fields": changedKeys(in)})
	m.sources.reload()
	saved, err := m.getMailbox(ctx, mb.ID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m.mailboxDTO(ctx, saved))
}

func changedKeys(in *mailboxIn) []string {
	var k []string
	if in.Name != nil {
		k = append(k, "name")
	}
	if in.teamSet {
		k = append(k, "team_id")
	}
	if in.ProjectID != nil {
		k = append(k, "project_id")
	}
	if in.Address != nil {
		k = append(k, "address")
	}
	if in.Active != nil {
		k = append(k, "active")
	}
	if in.IMAP != nil {
		k = append(k, "imap")
	}
	if in.WebhookHMACSecret != nil {
		k = append(k, "webhook_hmac_secret")
	}
	return k
}

func (m *Module) handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var n int64
	err := m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM inbound_mailboxes WHERE id = ?`, id)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if n == 0 {
		problems.Write(w, r, problems.NotFound())
		return
	}
	m.audit(ctx, r, "inbound_mailbox.delete", id, nil)
	m.sources.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleTestMailbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mb, err := m.getMailbox(ctx, chi.URLParam(r, "id"))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m.TestMailbox(ctx, mb))
}

// ---- inbound log ----

type messageDTO struct {
	ID        string    `json:"id"`
	MailboxID string    `json:"mailbox_id"`
	MessageID string    `json:"message_id"`
	FromAddr  string    `json:"from_addr"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status"`
	Reason    *string   `json:"reason"`
	TicketID  *string   `json:"ticket_id"`
	Received  time.Time `json:"received_at"`
}

const messageCols = `id, mailbox_id, message_id, from_addr, subject, status, reason, ticket_id, received_at`

func scanMessage(row interface{ Scan(...any) error }) (messageDTO, error) {
	var d messageDTO
	var reason string
	var ticket store.NullString
	if err := row.Scan(&d.ID, &d.MailboxID, &d.MessageID, &d.FromAddr, &d.Subject, &d.Status, &reason, &ticket, &d.Received); err != nil {
		return d, err
	}
	d.Received = d.Received.UTC()
	if reason != "" {
		d.Reason = &reason
	}
	if ticket.Valid {
		d.TicketID = &ticket.String
	}
	return d, nil
}

func (m *Module) handleListMessages(w http.ResponseWriter, r *http.Request) {
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
	where, args := "1=1", []any{}
	if s := q.Get("status"); s != "" {
		if !slices.Contains([]string{statusProcessed, statusQuarantined, statusFailed, statusIgnored}, s) {
			problems.Write(w, r, problems.Validation(problems.Field("status", "enum", "")))
			return
		}
		where += " AND status = ?"
		args = append(args, s)
	}
	if mb := q.Get("mailbox_id"); mb != "" {
		where += " AND mailbox_id = ?"
		args = append(args, mb)
	}
	if c := q.Get("cursor"); c != "" {
		id, err := base64.RawURLEncoding.DecodeString(c)
		if err != nil {
			problems.Write(w, r, problems.Validation(problems.Field("cursor", "invalid", "")))
			return
		}
		where += " AND id < ?"
		args = append(args, string(id))
	}
	args = append(args, limit+1)
	rows, err := m.d.DB.Read().QueryContext(r.Context(), `SELECT `+messageCols+` FROM inbound_messages WHERE `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	defer rows.Close()
	items := []messageDTO{}
	for rows.Next() {
		d, err := scanMessage(rows)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		items = append(items, d)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := base64.RawURLEncoding.EncodeToString([]byte(items[limit-1].ID))
		next = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (m *Module) handleRetryMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if err := m.Process(ctx, id, true); err != nil {
		var rt retryable
		if !errors.As(err, &rt) {
			problems.Write(w, r, err)
			return
		}
	}
	d, err := scanMessage(m.d.DB.Read().QueryRowContext(ctx, `SELECT `+messageCols+` FROM inbound_messages WHERE id = ?`, id))
	if err != nil {
		problems.Write(w, r, store.NotFound(err))
		return
	}
	m.auditTarget(ctx, r, "inbound_message.retry", "inbound_message", id)
	writeJSON(w, http.StatusOK, d)
}

func (m *Module) auditTarget(ctx context.Context, r *http.Request, action, typ, id string) {
	actor := ""
	if p := server.PrincipalFrom(ctx); p != nil {
		actor = p.UserID
	}
	_ = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, ?, ?, '{}', ?, ?)`,
			store.NewID(), store.Nullable(actor), action, typ, id, server.ClientIP(r), store.Now())
		return err
	})
}

// defaultProject is the project of a mailbox created without one (D-20):
// the Default project while active, else the first active project.
func (m *Module) defaultProject(ctx context.Context) string {
	var id string
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM projects WHERE active ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, sort, lower(name), id LIMIT 1`,
		domain.DefaultProjectID).Scan(&id)
	if err != nil {
		return domain.DefaultProjectID
	}
	return id
}
