// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/radioBros/SimpleTickets/api"
	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/crypto"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/events"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// logBuf is a concurrency-safe log sink.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type env struct {
	t      *testing.T
	db     *store.DB
	set    *settings.Service
	box    *crypto.Box
	m      *Module
	fake   *FakeSender
	logs   *logBuf
	envs   map[string]string
	cfg    *config.Config
	srv    *server.Server
	commit atomic.Int64

	org, team, team2                              string
	agent1, agent2, agentOther, supervisor, admin string
	customer, customer2                           string
}

func (e *env) getenv(k string) string { return e.envs[k] }

// newEnv builds a module on db with a fake sender, configured SMTP (unless
// smtpHost is ""), fixture users and a team.
func newEnv(t *testing.T, db *store.DB, envs map[string]string) *env {
	t.Helper()
	if envs == nil {
		envs = map[string]string{}
	}
	if _, ok := envs["ST_NOTIFY_COALESCE_SECONDS"]; !ok {
		envs["ST_NOTIFY_COALESCE_SECONDS"] = "0"
	}
	e := &env{t: t, db: db, envs: envs, logs: &logBuf{}, fake: &FakeSender{}}
	box, err := crypto.NewBox([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	e.box = box
	e.set = settings.New(db, box, e.getenv)
	cfgEnv := map[string]string{"ST_SECRET_KEY": strings.Repeat("k", 32), "ST_DATA_DIR": t.TempDir(), "ST_PUBLIC_URL": "https://help.example.test",
		"ST_PORTAL_URL": "https://help.example.test", "ST_AGENT_URL": "https://help.example.test/agent"}
	e.cfg, err = config.Load(func(k string) string { return cfgEnv[k] })
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(Deps{DB: db, Log: slog.New(slog.NewTextHandler(e.logs, nil)), Settings: e.set, Secrets: box, Cfg: e.cfg, Getenv: e.getenv})
	if err != nil {
		t.Fatal(err)
	}
	m.SetSender(e.fake)
	e.m = m
	db.OnCommit(func() { e.commit.Add(1) })

	e.org = storetest.CreateOrg(t, db, "")
	e.team = storetest.CreateTeam(t, db, "Billing")
	e.team2 = storetest.CreateTeam(t, db, "Network")
	e.agent1 = storetest.CreateStaff(t, db, domain.RoleAgent, 1, e.team)
	e.agent2 = storetest.CreateStaff(t, db, domain.RoleAgent, 2, e.team)
	e.agentOther = storetest.CreateStaff(t, db, domain.RoleAgent, 3, e.team2)
	e.supervisor = storetest.CreateStaff(t, db, domain.RoleSupervisor, 2)
	e.admin = storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
	e.customer = storetest.CreateCustomer(t, db, e.org)
	e.customer2 = storetest.CreateCustomer(t, db, e.org)
	e.setEmail(func(em *settings.Email) {
		em.CustomerFrom = settings.Identity{Name: "Acme Support", Address: "support@acme.test", ReplyTo: "support@acme.test"}
		em.StaffFrom = settings.Identity{Name: "Acme Helpdesk", Address: "noreply@acme.test"}
	})
	e.setSMTP("smtp.acme.test")
	return e
}

func (e *env) setSMTP(host string) {
	e.t.Helper()
	s := settings.DefaultSMTP()
	s.Host = host
	if err := e.set.PutSMTP(context.Background(), s, nil, ""); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) setEmail(fn func(*settings.Email)) {
	e.t.Helper()
	em := settings.Email{DisabledTemplates: []string{}, InboundBlocklist: []string{}}
	if err := e.set.Get(context.Background(), settings.KeyEmail, &em); err != nil {
		e.t.Fatal(err)
	}
	fn(&em)
	if err := e.set.PutEmail(context.Background(), em, ""); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if err := e.db.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.ExecContext(context.Background(), q, args...)
		return err
	}); err != nil {
		e.t.Fatalf("%v: %s", err, q)
	}
}

func (e *env) email(userID string) string {
	var s string
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, userID).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// ticket creates a ticket and returns its id and snapshot.
func (e *env) ticket(tk storetest.Ticket) (string, events.Snapshot) {
	e.t.Helper()
	if tk.Requester == "" {
		tk.Requester = e.customer
		tk.OrgID = e.org
	}
	id := storetest.CreateTicket(e.t, e.db, tk)
	var snap events.Snapshot
	var team, cat, asg, org *string
	err := e.db.Read().QueryRowContext(context.Background(), `SELECT number, subject, status, priority, tier, team_id, category_id, assignee_id, requester_id, organization_id, channel FROM tickets WHERE id = ?`, id).
		Scan(&snap.Number, &snap.Subject, &snap.Status, &snap.Priority, &snap.Tier, &team, &cat, &asg, &snap.RequesterID, &org, &snap.Channel)
	if err != nil {
		e.t.Fatal(err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	snap.TeamID, snap.CategoryID, snap.AssigneeID, snap.OrganizationID = deref(team), deref(cat), deref(asg), deref(org)
	return id, snap
}

// message inserts a ticket message and returns its id.
func (e *env) message(ticketID, authorID, kind, body string) string {
	e.t.Helper()
	id := store.NewID()
	e.exec(`INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, created_at) VALUES (?, ?, ?, ?, 'agent', ?, ?)`, id, ticketID, authorID, kind, body, store.Now())
	return id
}

// event writes a ticket_events row and returns the event (as the dispatcher would read it).
func (e *env) event(ticketID, actor, typ string, snap events.Snapshot, data any) events.Event {
	e.t.Helper()
	ev := events.Event{TicketID: ticketID, ActorID: actor, Type: typ, Snapshot: snap}
	if err := e.db.Tx(context.Background(), func(tx *store.Tx) error { return events.Write(context.Background(), tx, &ev, data) }); err != nil {
		e.t.Fatal(err)
	}
	return ev
}

func (e *env) handle(ev events.Event) {
	e.t.Helper()
	if err := e.m.handleEvent(context.Background(), ev); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) countJobs(where string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM deferred_jobs WHERE `+where, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// sentTo returns mails (template detection by subject not needed) addressed to addr.
func sentTo(ms []*Mail, addr string) []*Mail {
	var out []*Mail
	for _, m := range ms {
		if strings.EqualFold(m.To.Address, addr) {
			out = append(out, m)
		}
	}
	return out
}

// ---- HTTP ----

type userAuth struct{ db *store.DB }

// Authenticate maps "Bearer <user id>" to the fixture user's principal.
func (a userAuth) Authenticate(r *http.Request) (*domain.Principal, error) {
	id := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if id == "" {
		return nil, nil
	}
	var kind, role string
	var tier *int
	err := a.db.Read().QueryRowContext(r.Context(), `SELECT kind, role, tier FROM users WHERE id = ?`, id).Scan(&kind, &role, &tier)
	if err != nil {
		return nil, nil
	}
	p := &domain.Principal{UserID: id, Kind: domain.UserKind(kind), Role: domain.Role(role), TokenID: "t"}
	if tier != nil {
		p.Tier = *tier
	}
	return p, nil
}

func (e *env) server() *server.Server {
	if e.srv != nil {
		return e.srv
	}
	e.srv = server.New(server.Options{Config: e.cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: e.db, Spec: api.Spec})
	e.srv.SetAuthenticator(userAuth{db: e.db})
	e.m.Mount(e.srv.Groups())
	return e.srv
}

// do performs a request through the real router and validates the response
// against the OpenAPI spec.
func (e *env) do(method, path, auth string, body any) (*http.Response, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rec := httptest.NewRecorder()
	e.server().Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	vreq := httptest.NewRequest(method, path, nil)
	if body != nil {
		b, _ := json.Marshal(body)
		vreq = httptest.NewRequest(method, path, bytes.NewReader(b))
		vreq.Header.Set("Content-Type", "application/json")
	}
	apispec.ValidateResponse(e.t, vreq, resp)
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}
