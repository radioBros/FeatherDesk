// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
	"github.com/radioBros/SimpleTickets/internal/tickets"
)

// env mounts the privacy module on the real router with a fake bearer
// authenticator ("Authorization: Bearer <who>"). Exports run inline and mails
// are captured, so every test is deterministic. Every response is validated
// against api/openapi.yaml.
type env struct {
	t    *testing.T
	db   *store.DB
	srv  *server.Server
	m    *Module
	set  *settings.Service
	blob blob.Store
	who  map[string]*domain.Principal

	mu    sync.Mutex
	mails []notify.AuthMail
	w     world
}

func (e *env) Authenticate(r *http.Request) (*domain.Principal, error) {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p, ok := e.who[v]; ok && v != "" {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}

// world is the seeded data: the subject customer "Zelda Quixote", another
// customer "Otto Other", staff, two tickets, fields, notes and files.
type world struct {
	org, team                          string
	admin, admin2, agent, cust, other  string
	t1, t2                             string
	fPublic, fStaff                    string
	custMsg, agentReply, note, ottoMsg string
	ccMsg                              string
	attKey, noteAttKey, rawKey         string
}

const (
	zeldaName  = "Zelda Quixote"
	zeldaEmail = "zelda.quixote@example.test"
	ottoName   = "Otto Other"
	ottoEmail  = "otto.other@example.test"
)

func newEnv(t *testing.T, db *store.DB) *env {
	t.Helper()
	vars := map[string]string{"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": t.TempDir(), "ST_PUBLIC_URL": "https://help.example.com"}
	cfg, err := config.Load(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	bl, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, blob: bl, who: map[string]*domain.Principal{}}
	e.srv = server.New(server.Options{Config: cfg, DB: db, Blob: bl, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.srv.SetAuthenticator(e)
	e.set = settings.New(db, nil, func(string) string { return "" })
	m, err := New(Deps{Cfg: cfg, DB: db, Blob: bl, Settings: e.set, Server: e.srv, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Reindex: func(ctx context.Context, _ []string) error { _, err := tickets.Reindex(ctx, db, nil); return err }})
	if err != nil {
		t.Fatal(err)
	}
	m.queueExport = func(ctx context.Context, id string) {
		if err := m.RunExport(ctx, id); err != nil {
			t.Errorf("RunExport: %v", err)
		}
	}
	m.mail = func(_ context.Context, a notify.AuthMail) {
		e.mu.Lock()
		e.mails = append(e.mails, a)
		e.mu.Unlock()
	}
	e.m = m
	m.Mount(e.srv.Groups())
	e.seed()
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if err := e.db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	}); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return s
}

func (e *env) message(ticket, author, kind, body string) string {
	id := store.NewID()
	e.exec(`INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, created_at) VALUES (?, ?, ?, ?, 'portal', ?, ?)`,
		id, ticket, author, kind, body, store.Now())
	return id
}

func (e *env) attachment(ticket, msg, uploader, name, content string) string {
	key := blob.NewKey("attachments", time.Now())
	sum, err := e.blob.Put(context.Background(), key, strings.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`INSERT INTO attachments (id, ticket_id, message_id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at)
		VALUES (?, ?, ?, ?, ?, 'text/plain', ?, ?, ?, ?)`, store.NewID(), ticket, store.Nullable(msg), uploader, name, len(content), sum, key, store.Now())
	return key
}

func (e *env) field(key string, visible bool) string {
	id := store.NewID()
	e.exec(`INSERT INTO custom_field_defs (id, key, label, type, customer_visible, created_at, updated_at) VALUES (?, ?, '{"en":"F"}', 'text', ?, ?, ?)`,
		id, key, visible, store.Now(), store.Now())
	return id
}

func (e *env) seed() {
	t, db := e.t, e.db
	w := &e.w
	w.org = storetest.CreateOrg(t, db, "")
	w.team = storetest.CreateTeam(t, db, "")
	w.admin = storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
	w.admin2 = storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
	w.agent = storetest.CreateStaff(t, db, domain.RoleAgent, 1, w.team)
	w.cust = storetest.CreateCustomer(t, db, w.org)
	w.other = storetest.CreateCustomer(t, db, w.org)
	e.exec(`UPDATE users SET name = ?, email = ?, password_hash = 'x', signature_md = '' WHERE id = ?`, zeldaName, zeldaEmail, w.cust)
	e.exec(`UPDATE users SET name = ?, email = ? WHERE id = ?`, ottoName, ottoEmail, w.other)
	e.who["admin"] = &domain.Principal{UserID: w.admin, Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3, TokenID: "t-admin"}
	e.who["admin2"] = &domain.Principal{UserID: w.admin2, Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3, TokenID: "t-admin2"}
	e.who["agent"] = &domain.Principal{UserID: w.agent, Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1, TokenID: "t-agent"}
	e.who["cust"] = &domain.Principal{UserID: w.cust, Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: w.org, TokenID: "t-cust"}
	e.who["other"] = &domain.Principal{UserID: w.other, Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: w.org, TokenID: "t-other"}
	pat := *e.who["cust"]
	pat.Scopes = []string{domain.ScopeTicketsRead}
	e.who["cust-pat"] = &pat

	// Credentials and identities of the subject.
	e.exec(`INSERT INTO user_identities (id, user_id, audience, issuer, subject, email_at_link, created_at) VALUES (?, ?, 'customer', 'https://idp.test', 'zelda-sub', ?, ?)`,
		store.NewID(), w.cust, zeldaEmail, store.Now())
	e.exec(`INSERT INTO sessions (id_hash, user_id, audience, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent) VALUES (?, ?, 'customer', 'c', ?, ?, ?, '198.51.100.7', 'ZeldaBrowser')`,
		"h-"+w.cust, w.cust, store.Now(), store.Now(), store.Now().Add(time.Hour))
	e.exec(`INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, created_at) VALUES (?, ?, 'zelda-token', 'st_x', ?, '["tickets:read"]', ?)`,
		store.NewID(), w.cust, "hash-"+w.cust, store.Now())
	e.exec(`INSERT INTO user_consents (id, user_id, kind, policy_url, policy_version, ip, accepted_at) VALUES (?, ?, 'privacy_policy', 'https://acme.test/privacy', 'v1', '198.51.100.7', ?)`,
		store.NewID(), w.cust, store.Now())

	w.fPublic = e.field("order_ref", true)
	w.fStaff = e.field("risk_note", false)
	w.t1 = storetest.CreateTicket(t, db, storetest.Ticket{Subject: "Printer zeldasubject fails", Requester: w.cust, OrgID: w.org, TeamID: w.team, Status: domain.StatusOpen})
	w.t2 = storetest.CreateTicket(t, db, storetest.Ticket{Subject: "Otto ottosubject question", Requester: w.other, OrgID: w.org, TeamID: w.team})
	w.custMsg = e.message(w.t1, w.cust, "public", "My zeldasecret serial is ZQ-123")
	w.agentReply = e.message(w.t1, w.agent, "public", "Thanks, agentreplyword, please retry")
	w.note = e.message(w.t1, w.agent, "internal", "internalonlygossip about the customer")
	w.ottoMsg = e.message(w.t1, w.other, "public", "ottosecretcc on Zelda's ticket")
	w.ccMsg = e.message(w.t2, w.cust, "public", "zeldaccmessage on Otto's ticket")
	e.message(w.t2, w.other, "public", "ottoword unrelated")
	e.exec(`INSERT INTO ticket_field_values (ticket_id, field_id, value_text, updated_at) VALUES (?, ?, 'ORDZELDA77', ?)`, w.t1, w.fPublic, store.Now())
	e.exec(`INSERT INTO ticket_field_values (ticket_id, field_id, value_text, updated_at) VALUES (?, ?, 'staffonlyrisk', ?)`, w.t1, w.fStaff, store.Now())
	e.exec(`UPDATE tickets SET csat_score = 4, csat_comment = 'zeldacsatcomment' WHERE id = ?`, w.t1)
	w.attKey = e.attachment(w.t1, w.custMsg, w.cust, "../../passport zelda.txt", "zelda-attachment-bytes")
	w.noteAttKey = e.attachment(w.t1, w.note, w.agent, "internal.txt", "internal-attachment-bytes")

	// Raw inbound mail from her address.
	mb := store.NewID()
	e.exec(`INSERT INTO inbound_mailboxes (id, name, kind, created_at, updated_at) VALUES (?, 'Support', 'webhook', ?, ?)`, mb, store.Now(), store.Now())
	w.rawKey = blob.NewKey("inbound", time.Now())
	if _, err := e.blob.Put(context.Background(), w.rawKey, strings.NewReader("From: "+zeldaEmail+"\r\n\r\nraw"), -1, "message/rfc822"); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO inbound_messages (id, mailbox_id, message_id, from_addr, subject, status, ticket_id, raw_blob_key, received_at, created_at)
		VALUES (?, ?, '<m1@x>', ?, 'Printer zeldasubject fails', 'processed', ?, ?, ?, ?)`, store.NewID(), mb, zeldaEmail, w.t1, w.rawKey, store.Now(), store.Now())

	// Audit: one entry about her (by an admin), one by her, one by Otto.
	if err := db.Tx(context.Background(), func(tx *store.Tx) error {
		for _, a := range []authn.AuditEntry{
			{ActorID: w.admin, Action: "auth.user_enabled", TargetType: "user", TargetID: w.cust, IP: "203.0.113.9"},
			{ActorID: w.cust, Action: "auth.login_success", TargetType: "user", TargetID: w.cust, IP: "198.51.100.7"},
			{ActorID: w.other, Action: "auth.login_success", TargetType: "user", TargetID: w.other, IP: "192.0.2.55"},
		} {
			if err := authn.Audit(context.Background(), tx, a); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tickets.Reindex(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
}

type resp struct {
	*httptest.ResponseRecorder
	req *http.Request
}

func (r resp) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v\n%s", r.req.URL.Path, err, r.Body.String())
	}
}

// do sends a request through the real router; who "" is anonymous. The
// request body and the response are validated against the spec.
func (e *env) do(method, path, who string, body any) resp {
	e.t.Helper()
	return e.send(method, path, who, body, true)
}

// invalid sends a deliberately invalid body (only the response is validated).
func (e *env) invalid(method, path, who string, body any) resp {
	e.t.Helper()
	return e.send(method, path, who, body, false)
}

func (e *env) send(method, path, who string, body any, validateReq bool) resp {
	e.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if who != "" {
		req.Header.Set("Authorization", "Bearer "+who)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	vreq := httptest.NewRequest(method, path, bytes.NewReader(raw))
	vreq.Header = req.Header.Clone()
	if body != nil && validateReq {
		apispec.ValidateRequest(e.t, vreq)
		vreq = httptest.NewRequest(method, path, bytes.NewReader(raw))
		vreq.Header = req.Header.Clone()
	}
	apispec.ValidateResponse(e.t, vreq, rec.Result())
	return resp{rec, req}
}

func (e *env) want(r resp, code int) resp {
	e.t.Helper()
	if r.Code != code {
		e.t.Fatalf("%s %s: status %d, want %d\n%s", r.req.Method, r.req.URL.String(), r.Code, code, r.Body.String())
	}
	return r
}

// unzip reads an archive into name → content.
func unzip(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(c)
	}
	return out
}

func (e *env) mailsCopy() []notify.AuthMail {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]notify.AuthMail(nil), e.mails...)
}

func (e *env) putSettings(mut func(*Settings)) {
	e.t.Helper()
	s, err := LoadSettings(context.Background(), e.set)
	if err != nil {
		e.t.Fatal(err)
	}
	mut(&s)
	e.want(e.do("PUT", "/api/v1/admin/privacy/settings", "admin", s), 200)
}
