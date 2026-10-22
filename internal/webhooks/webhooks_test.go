// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pb33f/libopenapi-validator/schema_validation"

	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// ---- harness ----

type harness struct {
	t          *testing.T
	db         *store.DB
	srv        *server.Server
	m          *Module
	q          *queue.Queue
	adminMails atomic.Int64
	started    bool
}

func fakeAuth(r *http.Request) (*domain.Principal, error) {
	v := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if v == "" {
		return nil, nil
	}
	kind, role, _ := strings.Cut(v, ":")
	return &domain.Principal{UserID: store.NewID(), Kind: domain.UserKind(kind), Role: domain.Role(role), Tier: 1, TokenID: "t"}, nil
}

func newHarness(t *testing.T, db *store.DB) *harness {
	t.Helper()
	envm := map[string]string{
		"FD_SECRET_KEY": strings.Repeat("k", 32), "FD_DATA_DIR": t.TempDir(), "FD_PUBLIC_URL": "http://localhost:8080",
		EnvAllowPrivate: "true", EnvAllowInsecure: "true",
	}
	getenv := func(k string) string { return envm[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	box, err := crypto.NewBox([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(server.Options{Config: cfg, Logger: log, DB: db})
	srv.SetAuthenticator(server.AuthenticatorFunc(fakeAuth))
	q := queue.New(db, queue.Options{Logger: log, Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond,
		BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond, RetryMaxSleep: time.Second})
	m, err := New(Deps{DB: db, Log: log, Queue: q, Secrets: box, Server: srv, Cfg: cfg, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	m.Mount(srv.Groups())
	m.RegisterQueue(q)
	h := &harness{t: t, db: db, srv: srv, m: m, q: q}
	q.Handle(jobAdminMail, func(ctx context.Context, p json.RawMessage) error {
		var am adminMail
		if err := json.Unmarshal(p, &am); err != nil || am.Template != tplStaffWebhookDisabled {
			t.Errorf("admin mail payload: %s", p)
		}
		h.adminMails.Add(1)
		return nil
	})
	return h
}

func (h *harness) start() {
	h.t.Helper()
	if err := h.q.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.started = true
	h.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.q.Stop(ctx)
	})
}

// do performs a request through the real router and validates req + resp
// against the OpenAPI spec.
func (h *harness) do(method, path, auth string, body any) (*http.Response, []byte) {
	h.t.Helper()
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
	h.srv.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	out, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	vreq := httptest.NewRequest(method, path, nil)
	if body != nil {
		b, _ := json.Marshal(body)
		vreq = httptest.NewRequest(method, path, bytes.NewReader(b))
		vreq.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		vreq.Header.Set("Authorization", "Bearer "+auth)
	}
	apispec.ValidateResponse(h.t, vreq, resp)
	return resp, out
}

const admin = "staff:admin"

func (h *harness) createHook(url string, evs []string, internal bool) (id, secret string) {
	h.t.Helper()
	resp, body := h.do("POST", "/api/v1/admin/webhooks", admin, map[string]any{"url": url, "events": evs, "include_internal": internal})
	if resp.StatusCode != 201 {
		h.t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var d webhookDTO
	_ = json.Unmarshal(body, &d)
	return d.ID, d.Secret
}

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// receiver is an httptest server recording requests.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	hdrs   []http.Header
	status atomic.Int64
	hits   atomic.Int64
}

func newReceiver(t *testing.T, status int) *receiver {
	rc := &receiver{}
	rc.status.Store(int64(status))
	rc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.bodies = append(rc.bodies, b)
		rc.hdrs = append(rc.hdrs, r.Header.Clone())
		rc.mu.Unlock()
		rc.hits.Add(1)
		st := int(rc.status.Load())
		if st == 302 {
			w.Header().Set("Location", "http://example.invalid/")
		}
		w.WriteHeader(st)
	}))
	t.Cleanup(rc.Close)
	return rc
}

func (rc *receiver) last() ([]byte, http.Header) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.bodies) == 0 {
		return nil, nil
	}
	return rc.bodies[len(rc.bodies)-1], rc.hdrs[len(rc.hdrs)-1]
}

func ticketEvent(t *testing.T, db *store.DB, typ string, data any) events.Event {
	t.Helper()
	cust := storetest.CreateCustomer(t, db, "")
	team := storetest.CreateTeam(t, db, "")
	tid := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, TeamID: team})
	var num int64
	_ = db.Read().QueryRowContext(context.Background(), `SELECT number FROM tickets WHERE id = ?`, tid).Scan(&num)
	e := events.Event{ID: store.NewID(), TicketID: tid, Type: typ, CreatedAt: store.Now(),
		Snapshot: events.Snapshot{Number: num, Subject: "Printer on fire", Status: "new", Priority: "normal", Tier: 1, TeamID: team, RequesterID: cust, Channel: "portal"}}
	if data != nil {
		e.Data, _ = json.Marshal(data)
	}
	return e
}

// verifySignature is an independent reference implementation of the
// receiver-side check (events_spec §3).
func verifySignature(secret string, header string, body []byte, now time.Time) error {
	var ts, v1 string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(p, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			v1 = v
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return err
	}
	if d := now.Unix() - sec; d > 300 || d < -300 {
		return errors.New("timestamp out of tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	want := mac.Sum(nil)
	got, err := hex.DecodeString(v1)
	if err != nil || !hmac.Equal(want, got) {
		return errors.New("bad signature")
	}
	return nil
}

func validateEnvelope(t *testing.T, body []byte) {
	t.Helper()
	doc, err := apispec.Document()
	if err != nil {
		t.Fatal(err)
	}
	model, err := doc.BuildV3Model()
	if err != nil || model == nil {
		t.Fatalf("build model: %v", err)
	}
	sp := model.Model.Components.Schemas.GetOrZero("WebhookEnvelope")
	if sp == nil {
		t.Fatal("WebhookEnvelope schema missing")
	}
	ok, errs := schema_validation.NewSchemaValidator().ValidateSchemaBytes(sp.Schema(), body)
	if !ok {
		for _, e := range errs {
			msg := e.Message
			for _, se := range e.SchemaValidationErrors {
				msg += "; " + se.FieldPath + ": " + se.Reason
			}
			t.Errorf("envelope does not match WebhookEnvelope: %s", msg)
		}
	}
}

// ---- tests ----

func TestWebhookCRUD(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		id, secret := h.createHook("https://93.184.216.34/hook", []string{events.TicketCreated}, false)
		if len(secret) != 64 {
			t.Fatalf("secret %q", secret)
		}
		// NOT-011: the stored secret is ciphertext, never the plaintext.
		var enc string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT secret_enc FROM webhooks WHERE id = ?`, id).Scan(&enc)
		if enc == secret || strings.Contains(enc, secret) || !crypto.IsSealed(enc) {
			t.Fatalf("secret stored in plaintext: %q", enc)
		}
		resp, body := h.do("GET", "/api/v1/admin/webhooks/"+id, admin, nil)
		if resp.StatusCode != 200 || strings.Contains(string(body), secret) || !strings.Contains(string(body), `"secret_set":true`) {
			t.Fatalf("get: %d %s", resp.StatusCode, body)
		}
		resp, body = h.do("GET", "/api/v1/admin/webhooks", admin, nil)
		if resp.StatusCode != 200 || strings.Contains(string(body), secret) {
			t.Fatalf("list: %d %s", resp.StatusCode, body)
		}
		resp, body = h.do("PATCH", "/api/v1/admin/webhooks/"+id, admin, map[string]any{"rotate_secret": true, "events": []string{events.TicketCreated, events.MessageCreated}})
		var d webhookDTO
		_ = json.Unmarshal(body, &d)
		if resp.StatusCode != 200 || d.Secret == "" || d.Secret == secret || len(d.Events) != 2 {
			t.Fatalf("patch: %d %s", resp.StatusCode, body)
		}
		resp, body = h.do("PATCH", "/api/v1/admin/webhooks/"+id, admin, map[string]any{"events": []string{"bogus.event"}})
		if resp.StatusCode != 422 && resp.StatusCode != 400 {
			t.Fatalf("bad events: %d %s", resp.StatusCode, body)
		}
		resp, _ = h.do("DELETE", "/api/v1/admin/webhooks/"+id, admin, nil)
		if resp.StatusCode != 204 {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
		if resp, _ := h.do("GET", "/api/v1/admin/webhooks/"+id, admin, nil); resp.StatusCode != 404 {
			t.Fatalf("after delete: %d", resp.StatusCode)
		}
		// One audit row per create, update and delete.
		for _, a := range []string{"webhook.create", "webhook.update", "webhook.delete"} {
			if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND target_id = ?`, a, id); n < 1 {
				t.Errorf("audit %s: %d rows", a, n)
			}
		}
		if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE target_id = ?`, id); n != 3 {
			t.Errorf("audit rows %d, want 3 (failed PATCH writes none)", n)
		}
		if n := h.count(`SELECT COUNT(*) FROM audit_log WHERE CAST(data AS TEXT) LIKE ?`, "%"+secret+"%"); n != 0 {
			t.Error("secret leaked into audit_log")
		}
	})
}

func TestWebhookAccess(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		cases := map[string]int{"customer:requester": 404, "staff:agent": 403, "staff:supervisor": 403, "": 401, admin: 200}
		for who, want := range cases {
			req := httptest.NewRequest("GET", "/api/v1/admin/webhooks", nil)
			if who != "" {
				req.Header.Set("Authorization", "Bearer "+who)
			}
			rec := httptest.NewRecorder()
			h.srv.Handler().ServeHTTP(rec, req)
			if rec.Code != want {
				t.Errorf("%q: %d, want %d", who, rec.Code, want)
			}
		}
		resp, _ := h.do("POST", "/api/v1/admin/webhooks", "customer:requester", map[string]any{"url": "https://93.184.216.34/", "events": []string{"ticket.created"}})
		if resp.StatusCode != 404 {
			t.Errorf("customer create: %d", resp.StatusCode)
		}
	})
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

func TestSSRF(t *testing.T) {
	res := fakeResolver{
		"evil.example":   {netip.MustParseAddr("192.168.1.1")},
		"public.example": {netip.MustParseAddr("93.184.216.34")},
		"mixed.example":  {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.1.2.3")},
	}
	private := []string{
		"https://127.0.0.1/", "https://10.0.0.5/", "https://[::1]/", "https://169.254.169.254/latest/meta-data",
		"https://evil.example/", "https://100.64.0.1/", "https://[fd00::1]/", "https://0.0.0.0/", "https://[::ffff:127.0.0.1]/",
		"https://mixed.example/", "https://172.16.0.1/", "https://[fe80::1]/",
	}
	for _, allow := range []bool{false, true} {
		g := NewGuard(allow, false, res)
		for _, u := range private {
			code, _ := g.CheckURL(context.Background(), u)
			if allow && code != "" {
				t.Errorf("allow_private: %s refused (%s)", u, code)
			}
			if !allow && code != "private_address" {
				t.Errorf("%s: code %q, want private_address", u, code)
			}
		}
		if code, _ := g.CheckURL(context.Background(), "https://93.184.216.34/x"); code != "" {
			t.Errorf("public IP refused: %s", code)
		}
		if code, _ := g.CheckURL(context.Background(), "https://public.example/x"); code != "" {
			t.Errorf("public host refused: %s", code)
		}
		if code, _ := g.CheckURL(context.Background(), "http://public.example/x"); code != "insecure_scheme" {
			t.Errorf("http allowed without ALLOW_INSECURE: %q", code)
		}
	}
	// Dial time (DNS rebinding): the hostname now resolves to a private IP,
	// and literal private targets are refused before any connection.
	g := NewGuard(false, true, res)
	for _, addr := range []string{"evil.example:443", "127.0.0.1:80", "10.0.0.5:443", "[::1]:443", "169.254.169.254:80"} {
		_, err := g.DialContext(context.Background(), "tcp", addr)
		if !errors.Is(err, ErrBlocked) {
			t.Errorf("dial %s: %v, want ErrBlocked", addr, err)
		}
	}
	if err := g.control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("control public: %v", err)
	}
	if err := g.control("tcp", "192.168.1.1:443", nil); !errors.Is(err, ErrBlocked) {
		t.Errorf("control private: %v", err)
	}
	// A real local receiver is refused at dial time when private is not allowed …
	rc := newReceiver(t, 200)
	g2 := NewGuard(false, true, nil)
	if _, err := g2.Client(2*time.Second).Post(rc.URL, "application/json", strings.NewReader("{}")); err == nil || !strings.Contains(err.Error(), ErrBlocked.Error()) {
		t.Errorf("dial to loopback receiver: %v", err)
	}
	// … and allowed with FD_WEBHOOK_ALLOW_PRIVATE.
	g2.AllowPrivate = true
	resp, err := g2.Client(2*time.Second).Post(rc.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("allow private: %v", err)
	}
	resp.Body.Close()

	// Create-time refusal through the API (422, field url).
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		h.m.Guard().AllowPrivate = false
		h.m.Guard().Resolver = res
		for _, u := range []string{"https://127.0.0.1/", "https://evil.example/hook", "https://169.254.169.254/"} {
			resp, body := h.do("POST", "/api/v1/admin/webhooks", admin, map[string]any{"url": u, "events": []string{"ticket.created"}})
			if resp.StatusCode != 422 || !strings.Contains(string(body), "private_address") {
				t.Errorf("%s: %d %s", u, resp.StatusCode, body)
			}
		}
	})
}

func TestDeliverySignedEnvelope(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 200)
		_, secret := h.createHook(rc.URL+"/hook", []string{events.TicketCreated, events.MessageCreated}, false)
		e := ticketEvent(t, db, events.TicketCreated, map[string]any{"channel": "portal", "priority": "normal"})
		if err := h.m.handleEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		body, hdr := rc.last()
		if body == nil {
			t.Fatal("no delivery")
		}
		if err := verifySignature(secret, hdr.Get("X-FeatherDesk-Signature"), body, time.Now()); err != nil {
			t.Fatalf("signature: %v (%s)", err, hdr.Get("X-FeatherDesk-Signature"))
		}
		if hdr.Get("X-FeatherDesk-Event") != events.TicketCreated || hdr.Get("X-FeatherDesk-Delivery") == "" {
			t.Fatalf("headers: %v", hdr)
		}
		// Receivers written for SimpleTickets 1.0.0 get the same values under the old names.
		for _, n := range []string{"Event", "Delivery", "Signature"} {
			if hdr.Get("X-SimpleTickets-"+n) != hdr.Get("X-FeatherDesk-"+n) {
				t.Fatalf("X-SimpleTickets-%s = %q, want %q", n, hdr.Get("X-SimpleTickets-"+n), hdr.Get("X-FeatherDesk-"+n))
			}
		}
		if err := verifySignature("wrong", hdr.Get("X-FeatherDesk-Signature"), body, time.Now()); err == nil {
			t.Fatal("wrong secret verified")
		}
		var env map[string]any
		_ = json.Unmarshal(body, &env)
		if env["id"] != e.ID || env["type"] != events.TicketCreated {
			t.Fatalf("envelope: %s", body)
		}
		validateEnvelope(t, body)
		// Success writes no deferred_jobs row (NOT-010).
		if n := h.count(`SELECT COUNT(*) FROM deferred_jobs`); n != 0 {
			t.Fatalf("deferred rows after success: %d", n)
		}
		// Same event id across a retry; delivery id differs per attempt.
		_ = h.m.handleEvent(context.Background(), e)
		b2, h2 := rc.last()
		var env2 map[string]any
		_ = json.Unmarshal(b2, &env2)
		if env2["id"] != e.ID || h2.Get("X-FeatherDesk-Delivery") == hdr.Get("X-FeatherDesk-Delivery") {
			t.Fatal("event id must be stable and delivery id per attempt")
		}

		// message.created carries the public message.
		cust := e.Snapshot.RequesterID
		mid := store.NewID()
		err := db.Tx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.ExecContext(context.Background(), `INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, created_at) VALUES (?, ?, ?, 'public', 'portal', 'Hello there', ?)`, mid, e.TicketID, cust, store.Now())
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		me := e
		me.ID, me.Type, me.ActorID = store.NewID(), events.MessageCreated, cust
		me.Data, _ = json.Marshal(events.MessageRef{MessageID: mid, Kind: "public"})
		if err := h.m.handleEvent(context.Background(), me); err != nil {
			t.Fatal(err)
		}
		b3, _ := rc.last()
		if !strings.Contains(string(b3), "Hello there") {
			t.Fatalf("message missing: %s", b3)
		}
		validateEnvelope(t, b3)
	})
}

func TestFirstRetryUsesClock(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		h.m.Now = func() time.Time { return fixed }
		rc := newReceiver(t, 503)
		h.createHook(rc.URL, []string{events.TicketCreated}, false)
		e := ticketEvent(t, db, events.TicketCreated, nil)
		if err := h.m.handleEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		var next time.Time
		var attempts, max int
		var kind string
		if err := db.Read().QueryRowContext(context.Background(), `SELECT kind, attempts, max_attempts, next_run_at FROM deferred_jobs`).Scan(&kind, &attempts, &max, &next); err != nil {
			t.Fatal(err)
		}
		d := next.Sub(fixed)
		if kind != JobDeliver || attempts != 1 || max != MaxAttempts || d < 24*time.Second || d > 36*time.Second {
			t.Fatalf("deferred row kind=%s attempts=%d max=%d delay=%s", kind, attempts, max, d)
		}
	})
}

func TestRetryScheduleEightAttemptsThenDead(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		h.m.FirstRetry = func() time.Duration { return 0 }
		rc := newReceiver(t, 500)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		e := ticketEvent(t, db, events.TicketCreated, nil)
		h.start()
		if err := h.m.handleEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && h.count(`SELECT COUNT(*) FROM deferred_jobs WHERE status = 'dead'`) == 0 {
			time.Sleep(100 * time.Millisecond)
		}
		var attempts int
		var status string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT attempts, status FROM deferred_jobs`).Scan(&attempts, &status)
		if status != "dead" || attempts != MaxAttempts {
			t.Fatalf("job status=%s attempts=%d", status, attempts)
		}
		if hits := rc.hits.Load(); hits != MaxAttempts {
			t.Fatalf("receiver hits %d, want %d", hits, MaxAttempts)
		}
		// Every failed attempt has a delivery row, numbered 1..8.
		if n := h.count(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND NOT success`, id); n != MaxAttempts {
			t.Fatalf("failure rows %d", n)
		}
		if n := h.count(`SELECT MAX(attempt) FROM webhook_deliveries WHERE webhook_id = ?`, id); n != MaxAttempts {
			t.Fatalf("max attempt %d", n)
		}
	})
}

func TestGone410DisablesAfterOneAttempt(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 410)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		_ = h.m.handleEvent(context.Background(), ticketEvent(t, db, events.TicketCreated, nil))
		_ = h.m.handleEvent(context.Background(), ticketEvent(t, db, events.TicketCreated, nil))
		var active bool
		var reason string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT active, disabled_reason FROM webhooks WHERE id = ?`, id).Scan(&active, &reason)
		if active || reason != "gone" || rc.hits.Load() != 1 {
			t.Fatalf("active=%v reason=%q hits=%d", active, reason, rc.hits.Load())
		}
		if n := h.count(`SELECT COUNT(*) FROM deferred_jobs`); n != 0 {
			t.Fatalf("410 must not retry: %d deferred", n)
		}
	})
}

func TestRedirectIsFailure(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 302)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		_ = h.m.handleEvent(context.Background(), ticketEvent(t, db, events.TicketCreated, nil))
		if rc.hits.Load() != 1 {
			t.Fatalf("hits %d (redirect followed?)", rc.hits.Load())
		}
		if n := h.count(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND NOT success AND status_code = 302`, id); n != 1 {
			t.Fatalf("302 failure rows %d", n)
		}
		if n := h.count(`SELECT COUNT(*) FROM deferred_jobs WHERE kind = ?`, JobDeliver); n != 1 {
			t.Fatalf("deferred %d", n)
		}
		resp, body := h.do("GET", "/api/v1/admin/webhooks/"+id+"/deliveries?limit=1", admin, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"status_code":302`) {
			t.Fatalf("deliveries: %d %s", resp.StatusCode, body)
		}
	})
}

func TestAutoDisableAfter20Failures(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		h.m.FirstRetry = func() time.Duration { return time.Hour }
		h.start()
		rc := newReceiver(t, 500)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		e := ticketEvent(t, db, events.TicketCreated, nil)
		for i := 0; i < DisableAfter+3; i++ {
			e.ID = store.NewID()
			if err := h.m.handleEvent(context.Background(), e); err != nil {
				t.Fatal(err)
			}
		}
		var active bool
		var reason string
		var fails int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT active, disabled_reason, consecutive_failures FROM webhooks WHERE id = ?`, id).Scan(&active, &reason, &fails)
		if active || reason != "failures" || fails != DisableAfter {
			t.Fatalf("active=%v reason=%q fails=%d", active, reason, fails)
		}
		if rc.hits.Load() != DisableAfter {
			t.Fatalf("hits %d, want %d (disabled webhooks get nothing)", rc.hits.Load(), DisableAfter)
		}
		if n := h.count(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND NOT success`, id); n != DisableAfter {
			t.Fatalf("failure rows %d", n)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && h.adminMails.Load() == 0 {
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
		if n := h.adminMails.Load(); n != 1 {
			t.Fatalf("admin mail jobs %d, want exactly 1", n)
		}
		// GET shows disabled_reason; re-activating resets the counter.
		resp, body := h.do("PATCH", "/api/v1/admin/webhooks/"+id, admin, map[string]any{"active": true})
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"consecutive_failures":0`) || !strings.Contains(string(body), `"disabled_reason":null`) {
			t.Fatalf("reactivate: %s", body)
		}
		// A success after failures resets consecutive_failures.
		rc.status.Store(200)
		_ = h.m.handleEvent(context.Background(), e)
		rc.status.Store(500)
		_ = h.m.handleEvent(context.Background(), e)
		rc.status.Store(200)
		_ = h.m.handleEvent(context.Background(), e)
		if n := h.count(`SELECT consecutive_failures FROM webhooks WHERE id = ?`, id); n != 0 {
			t.Fatalf("failures not reset on 2xx: %d", n)
		}
	})
}

func TestSuccessLogIsSampledRing(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 200)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		e := ticketEvent(t, db, events.TicketCreated, nil)
		for i := 0; i < 1000; i++ {
			e.ID = store.NewID()
			if err := h.m.handleEvent(context.Background(), e); err != nil {
				t.Fatal(err)
			}
		}
		n := h.count(`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ? AND success`, id)
		if n == 0 || n > successRing {
			t.Fatalf("success rows %d, want 1..%d", n, successRing)
		}
		if rc.hits.Load() != 1000 {
			t.Fatalf("hits %d", rc.hits.Load())
		}
		if n := h.count(`SELECT COUNT(*) FROM deferred_jobs`); n != 0 {
			t.Fatalf("deferred %d", n)
		}
		// Paging through the log works and validates.
		resp, body := h.do("GET", "/api/v1/admin/webhooks/"+id+"/deliveries?limit=20", admin, nil)
		var page struct {
			Items      []deliveryDTO `json:"items"`
			NextCursor *string       `json:"next_cursor"`
		}
		_ = json.Unmarshal(body, &page)
		if resp.StatusCode != 200 || len(page.Items) != 20 || page.NextCursor == nil {
			t.Fatalf("page 1: %d %d", resp.StatusCode, len(page.Items))
		}
		resp, body = h.do("GET", "/api/v1/admin/webhooks/"+id+"/deliveries?limit=20&cursor="+*page.NextCursor, admin, nil)
		var p2 struct {
			Items []deliveryDTO `json:"items"`
		}
		_ = json.Unmarshal(body, &p2)
		if resp.StatusCode != 200 || len(p2.Items) == 0 || p2.Items[0].ID >= page.Items[19].ID {
			t.Fatalf("page 2: %d %s", resp.StatusCode, body)
		}
	})
}

// sortRows writes 7 rows through insert and returns their ids oldest first.
// Two pairs share a timestamp (one pair straddles a limit-2 page boundary
// ascending, the other descending), and the 4th row has the newest id but a
// middle time, so only a (time, id) order lists them as returned.
func sortRows(t *testing.T, insert func(id string, at time.Time)) []string {
	t.Helper()
	ids := make([]string, 7)
	for i := range ids {
		ids[i] = store.NewID()
	}
	slices.Sort(ids)
	order := []string{ids[0], ids[1], ids[2], ids[6], ids[3], ids[4], ids[5]}
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i, m := range []int{0, 1, 1, 2, 3, 3, 4} {
		insert(order[i], base.Add(time.Duration(m)*time.Minute))
	}
	return order
}

// TestDeliveriesSort: sort=created_at pages a webhook's delivery log oldest
// first and the default (or -created_at) newest first, both by (created_at,
// id) and only for that webhook; the default cursor stays base64url(id), and a
// cursor only works in the direction (and webhook) that issued it.
func TestDeliveriesSort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 200)
		id, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		other, _ := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		ctx := context.Background()
		insert := func(rowID, hook string, at time.Time) {
			if err := db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries (id, webhook_id, event_id, event_type, attempt, success, status_code, response_ms, error, created_at) VALUES (?, ?, ?, ?, 1, TRUE, 200, 5, '', ?)`,
					rowID, hook, store.NewID(), events.TicketCreated, at)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		want := sortRows(t, func(rowID string, at time.Time) { insert(rowID, id, at) })
		otherRow := store.NewID()
		insert(otherRow, other, time.Date(2026, 3, 1, 12, 1, 30, 0, time.UTC)) // another webhook
		rev := slices.Clone(want)
		slices.Reverse(rev)

		path := "/api/v1/admin/webhooks/" + id + "/deliveries?limit=2"
		get := func(url string, code int) (items []string, next string, body []byte) {
			t.Helper()
			var resp *http.Response
			if !strings.Contains(url, "sort=attempt") {
				resp, body = h.do("GET", url, admin, nil)
			} else {
				// Not through h.do: an unknown sort is outside the spec's
				// enum, so the validator would reject the request itself.
				req := httptest.NewRequest("GET", url, nil)
				req.Header.Set("Authorization", "Bearer "+admin)
				rec := httptest.NewRecorder()
				h.srv.Handler().ServeHTTP(rec, req)
				resp, body = rec.Result(), rec.Body.Bytes()
			}
			if resp.StatusCode != code {
				t.Fatalf("GET %s: %d %s", url, resp.StatusCode, body)
			}
			var p struct {
				Items      []deliveryDTO `json:"items"`
				NextCursor *string       `json:"next_cursor"`
			}
			_ = json.Unmarshal(body, &p)
			for _, d := range p.Items {
				items = append(items, d.ID)
			}
			if p.NextCursor != nil {
				next = *p.NextCursor
			}
			return items, next, body
		}
		pageAll := func(query string) (got, cursors []string) {
			t.Helper()
			cursor := ""
			for range 10 {
				items, next, _ := get(path+query+cursor, 200)
				got = append(got, items...)
				if next == "" {
					return got, cursors
				}
				cursors = append(cursors, next)
				cursor = "&cursor=" + next
			}
			t.Fatalf("%s: no last page", query)
			return nil, nil
		}

		asc, ascCursors := pageAll("&sort=created_at")
		if !slices.Equal(asc, want) {
			t.Fatalf("ascending\n got %v\nwant %v", asc, want)
		}
		desc, descCursors := pageAll("")
		if !slices.Equal(desc, rev) {
			t.Fatalf("default\n got %v\nwant %v", desc, rev)
		}
		for i, c := range descCursors {
			if c != base64.RawURLEncoding.EncodeToString([]byte(desc[2*i+1])) {
				t.Fatalf("default cursor %d = %q, want base64url(id)", i, c)
			}
		}
		if explicit, _ := pageAll("&sort=-created_at"); !slices.Equal(explicit, rev) {
			t.Fatalf("-created_at\n got %v\nwant %v", explicit, rev)
		}

		if _, _, body := get(path+"&sort=attempt", 422); !strings.Contains(string(body), `"sort"`) {
			t.Fatalf("bad sort: %s", body)
		}
		get(path+"&cursor="+ascCursors[0], 422)
		get(path+"&sort=-created_at&cursor="+ascCursors[0], 422)
		get(path+"&sort=created_at&cursor="+descCursors[0], 422)
		get(path+"&cursor="+base64.RawURLEncoding.EncodeToString([]byte(otherRow)), 422) // row of another webhook
	})
}

func TestIncludeInternalGating(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		pub := newReceiver(t, 200)
		staff := newReceiver(t, 200)
		evs := []string{events.NoteCreated, events.TicketEscalated, events.TicketFieldsChanged}
		h.createHook(pub.URL, evs, false)
		h.createHook(staff.URL, evs, true)

		note := ticketEvent(t, db, events.NoteCreated, events.MessageRef{MessageID: store.NewID(), Kind: "internal"})
		if err := h.m.handleEvent(context.Background(), note); err != nil {
			t.Fatal(err)
		}
		if pub.hits.Load() != 0 || staff.hits.Load() != 1 {
			t.Fatalf("note.created: public=%d internal=%d", pub.hits.Load(), staff.hits.Load())
		}
		esc := ticketEvent(t, db, events.TicketEscalated, events.TierChange{FromTier: 1, ToTier: 2, Reason: "CANARY-secret-reason"})
		if err := h.m.handleEvent(context.Background(), esc); err != nil {
			t.Fatal(err)
		}
		pb, _ := pub.last()
		sb, _ := staff.last()
		if strings.Contains(string(pb), "reason") || strings.Contains(string(pb), "CANARY") {
			t.Fatalf("public payload has reason: %s", pb)
		}
		if !strings.Contains(string(sb), "CANARY-secret-reason") {
			t.Fatalf("internal payload lacks reason: %s", sb)
		}
		validateEnvelope(t, pb)

		// I-9: fields_changed keys filtered to customer-visible fields.
		err := db.Tx(context.Background(), func(tx *store.Tx) error {
			for _, f := range []struct {
				key string
				vis bool
			}{{"contract_no", true}, {"internal_score", false}} {
				if _, err := tx.ExecContext(context.Background(), `INSERT INTO custom_field_defs (id, key, type, customer_visible, created_at, updated_at) VALUES (?, ?, 'text', ?, ?, ?)`, store.NewID(), f.key, f.vis, store.Now(), store.Now()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		fc := ticketEvent(t, db, events.TicketFieldsChanged, events.FieldsChange{Keys: []string{"contract_no", "internal_score"}})
		if err := h.m.handleEvent(context.Background(), fc); err != nil {
			t.Fatal(err)
		}
		pb, _ = pub.last()
		sb, _ = staff.last()
		if strings.Contains(string(pb), "internal_score") || !strings.Contains(string(pb), "contract_no") {
			t.Fatalf("public fields payload: %s", pb)
		}
		if !strings.Contains(string(sb), "internal_score") {
			t.Fatalf("internal fields payload: %s", sb)
		}
		// The consumer filter ignores non-webhook events.
		if h.m.webhookE[events.TicketWatchersChanged] || !h.m.webhookE[events.NoteCreated] {
			t.Fatal("event filter")
		}
	})
}

func TestTestEndpoint(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		rc := newReceiver(t, 204)
		id, secret := h.createHook(rc.URL, []string{events.TicketCreated}, false)
		resp, body := h.do("POST", "/api/v1/admin/webhooks/"+id+"/test", admin, nil)
		if resp.StatusCode != 200 || !strings.Contains(string(body), `"status_code":204`) || !strings.Contains(string(body), `"error":null`) {
			t.Fatalf("test ok: %d %s", resp.StatusCode, body)
		}
		b, hdr := rc.last()
		if hdr.Get("X-FeatherDesk-Event") != "ping" || verifySignature(secret, hdr.Get("X-FeatherDesk-Signature"), b, time.Now()) != nil {
			t.Fatalf("ping not signed: %v", hdr)
		}
		// Stopped receiver: 200 with the dial error.
		dead := newReceiver(t, 200)
		u := dead.URL
		dead.Close()
		id2, _ := h.createHook(u, []string{events.TicketCreated}, false)
		resp, body = h.do("POST", "/api/v1/admin/webhooks/"+id2+"/test", admin, nil)
		var out WebhookTestResultForTest
		_ = json.Unmarshal(body, &out)
		if resp.StatusCode != 200 || out.Error == nil || !strings.Contains(*out.Error, "connect") || out.StatusCode != nil {
			t.Fatalf("test dead: %d %s", resp.StatusCode, body)
		}
		if resp, _ := h.do("POST", "/api/v1/admin/webhooks/"+store.NewID()+"/test", admin, nil); resp.StatusCode != 404 {
			t.Fatalf("unknown id: %d", resp.StatusCode)
		}
	})
}

type WebhookTestResultForTest struct {
	StatusCode *int    `json:"status_code"`
	Error      *string `json:"error"`
}

func TestRoutesDocumented(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		if db.Dialect != store.SQLite {
			t.Skip("route inventory is engine-independent")
		}
		h := newHarness(t, db)
		apispec.AssertRoutes(t, h.srv.Router())
	})
}

func TestSignReference(t *testing.T) {
	body := []byte(`{"id":"x"}`)
	at := time.Unix(1760954400, 0)
	got := Sign("s3cret", at, body)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("1760954400." + string(body)))
	if want := "t=1760954400,v1=" + hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("%s != %s", got, want)
	}
	if verifySignature("s3cret", got, body, at.Add(301*time.Second)) == nil {
		t.Fatal("stale timestamp accepted by reference receiver")
	}
}

// I-12: a customer message posted on the customer's behalf by an integration
// token carries actor = the customer and data.message.via_integration = true,
// in both webhook scopes; other messages say false.
func TestEnvelope_ViaIntegration(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		h := newHarness(t, db)
		ctx := context.Background()
		owner := storetest.CreateStaff(t, db, domain.RoleAdmin, 3)
		tokID := store.NewID()
		for _, via := range []bool{true, false} {
			e := ticketEvent(t, db, events.MessageCreated, events.MessageRef{Kind: "public"})
			msgID := store.NewID()
			e.ActorID = e.Snapshot.RequesterID
			e.Data, _ = json.Marshal(events.MessageRef{MessageID: msgID, Kind: "public", Channel: "api", ViaIntegration: via})
			err := db.Tx(ctx, func(tx *store.Tx) error {
				var tok any
				if via {
					if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, created_at) VALUES (?, ?, 'Verbalizza', 'abcd1234', ?, ?, ?)`,
						tokID, owner, "hash-"+tokID, `["tickets:act_as_customer"]`, store.Now()); err != nil {
						return err
					}
					tok = tokID
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO ticket_messages (id, ticket_id, author_id, kind, channel, body_md, via_token_id, created_at) VALUES (?, ?, ?, 'public', 'api', 'Thanks', ?, ?)`,
					msgID, e.TicketID, e.ActorID, tok, store.Now())
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, internal := range []bool{false, true} {
				body, err := h.m.envelope(ctx, e, internal)
				if err != nil {
					t.Fatal(err)
				}
				validateEnvelope(t, body)
				var env struct {
					Data struct {
						Actor   struct{ ID, Kind string }
						Message map[string]any
					}
				}
				_ = json.Unmarshal(body, &env)
				if env.Data.Actor.ID != e.ActorID || env.Data.Actor.Kind != "customer" || env.Data.Message["via_integration"] != via {
					t.Fatalf("via=%v internal=%v: %s", via, internal, body)
				}
				if strings.Contains(string(body), "Verbalizza") || strings.Contains(string(body), tokID) {
					t.Fatalf("token name or id leaked: %s", body)
				}
			}
		}
	})
}
