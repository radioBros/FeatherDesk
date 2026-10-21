// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/radioBros/SimpleTickets/api"
	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// fakeSink records calls and returns synthetic ids (or real tickets when
// createTicket is set).
type fakeSink struct {
	mu      sync.Mutex
	creates []EmailTicket
	replies []EmailReply
	err     error
	// followup maps a ticket id to the follow-up id the sink "creates".
	followup map[string]string
}

func (s *fakeSink) CreateFromEmail(ctx context.Context, in EmailTicket) (EmailResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return EmailResult{}, s.err
	}
	s.creates = append(s.creates, in)
	return EmailResult{TicketID: "", MessageID: store.NewID()}, nil
}

func (s *fakeSink) ReplyFromEmail(ctx context.Context, in EmailReply) (EmailResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return EmailResult{}, s.err
	}
	s.replies = append(s.replies, in)
	t := in.TicketID
	if f, ok := s.followup[t]; ok {
		t = f
	}
	return EmailResult{TicketID: t, MessageID: store.NewID()}, nil
}

func (s *fakeSink) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.creates), len(s.replies)
}

type env struct {
	t    *testing.T
	db   *store.DB
	m    *Module
	srv  *server.Server
	sink *fakeSink
	box  *crypto.Box
	set  *settings.Service
	vars map[string]string
}

func testBox(t *testing.T) *crypto.Box {
	b, err := crypto.NewBox([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newEnv builds the module on db with a real router and a bearer authenticator
// ("Bearer staff:admin", "Bearer customer:requester", …).
func newEnv(t *testing.T, db *store.DB, vars map[string]string) *env {
	t.Helper()
	if vars == nil {
		vars = map[string]string{}
	}
	dir := t.TempDir()
	base := map[string]string{"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": dir, "ST_PUBLIC_URL": "https://help.example.test"}
	getenv := func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return base[k]
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	bl, err := blob.NewLocal(getenv("ST_DATA_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	box := testBox(t)
	set := settings.New(db, box, getenv)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(server.Options{Config: cfg, Logger: log, DB: db, Blob: bl, Spec: api.Spec})
	srv.SetAuthenticator(server.AuthenticatorFunc(func(r *http.Request) (*domain.Principal, error) {
		v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if v == "" {
			return nil, nil
		}
		kind, role, _ := strings.Cut(v, ":")
		return &domain.Principal{UserID: "u-" + v, Kind: domain.UserKind(kind), Role: domain.Role(role), Tier: 1, TokenID: "t"}, nil
	}))
	m, err := New(Deps{DB: db, Log: log, Blob: bl, Settings: set, Secrets: box, Server: srv, Cfg: cfg, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{followup: map[string]string{}}
	m.SetTicketSink(sink)
	m.Mount(srv.Groups())
	return &env{t: t, db: db, m: m, srv: srv, sink: sink, box: box, set: set, vars: vars}
}

// do performs a request through the real router and validates the response
// against the OpenAPI spec.
func (e *env) do(method, path, auth string, body any) (*http.Response, []byte) {
	e.t.Helper()
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
		ct = "message/rfc822"
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
		ct = "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	data, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(data))
	vreq := httptest.NewRequest(method, path, nil)
	apispec.ValidateResponse(e.t, vreq, resp)
	return resp, data
}

// webhookMailbox creates a webhook mailbox via the API and returns (id, token).
func (e *env) webhookMailbox(teamID string, hmacSecret string) (string, string) {
	e.t.Helper()
	body := map[string]any{"name": "Hook", "kind": "webhook", "address": "support@example.test"}
	if teamID != "" {
		body["team_id"] = teamID
	}
	if hmacSecret != "" {
		body["webhook_hmac_secret"] = hmacSecret
	}
	resp, data := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", body)
	if resp.StatusCode != 201 {
		e.t.Fatalf("create mailbox: %d %s", resp.StatusCode, data)
	}
	var d struct {
		ID         string `json:"id"`
		WebhookURL string `json:"webhook_url"`
	}
	_ = json.Unmarshal(data, &d)
	i := strings.LastIndex(d.WebhookURL, "/")
	return d.ID, d.WebhookURL[i+1:]
}

func (e *env) mailbox(id string) *mailbox {
	e.t.Helper()
	mb, err := e.m.getMailbox(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return mb
}

// msgStatus returns (status, reason, ticket_id) of the single row with msgID.
func (e *env) msgStatus(id string) (string, string, string) {
	e.t.Helper()
	var st, reason string
	var tid store.NullString
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT status, reason, ticket_id FROM inbound_messages WHERE id = ?`, id).Scan(&st, &reason, &tid); err != nil {
		e.t.Fatal(err)
	}
	return st, reason, tid.String
}

// receive records + processes raw on mailbox id synchronously and returns the row id.
func (e *env) receive(mailboxID string, raw []byte) string {
	e.t.Helper()
	rec, err := e.m.Receive(context.Background(), e.mailbox(mailboxID), raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return rec.ID
}

var msgSeq int

// mail builds a raw message. headers override defaults (key "" removes).
func mkMail(from, subject, body string, headers map[string]string) []byte {
	msgSeq++
	h := map[string]string{
		"From":         from,
		"To":           "support@example.test",
		"Subject":      subject,
		"Message-ID":   fmt.Sprintf("<m%d.%d@client.example.org>", msgSeq, len(body)),
		"Date":         "Mon, 19 Oct 2026 10:00:00 +0000",
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=utf-8",
	}
	for k, v := range headers {
		if v == "" {
			delete(h, k)
		} else {
			h[k] = v
		}
	}
	var b strings.Builder
	for _, k := range []string{"From", "To", "Cc", "Subject", "Message-ID", "In-Reply-To", "References", "Date", "MIME-Version", "Content-Type"} {
		if v, ok := h[k]; ok {
			b.WriteString(k + ": " + v + "\r\n")
			delete(h, k)
		}
	}
	for k, v := range h {
		b.WriteString(k + ": " + v + "\r\n")
	}
	b.WriteString("\r\n" + strings.ReplaceAll(body, "\n", "\r\n") + "\r\n")
	return []byte(b.String())
}

// customer creates an active customer with a known address.
func customer(t *testing.T, db *store.DB, email, org string) string {
	t.Helper()
	id := storetest.CreateCustomer(t, db, org)
	exec(t, db, `UPDATE users SET email = ? WHERE id = ?`, email, id)
	return id
}

func staff(t *testing.T, db *store.DB, email string) string {
	t.Helper()
	id := storetest.CreateStaff(t, db, domain.RoleAgent, 1)
	exec(t, db, `UPDATE users SET email = ? WHERE id = ?`, email, id)
	return id
}

func exec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	if err := db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setSelfReg(t *testing.T, s *settings.Service, on bool) {
	t.Helper()
	sup, _ := s.Support(context.Background())
	sup.SelfRegistration = on
	if err := s.Put(context.Background(), settings.KeySupport, sup, ""); err != nil {
		t.Fatal(err)
	}
}
