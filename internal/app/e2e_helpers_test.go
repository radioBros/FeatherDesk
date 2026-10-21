// SPDX-License-Identifier: AGPL-3.0-only

package app_test

// Helpers for the cross-module tests: the FULL app (app.Registry, as
// `simpletickets serve` builds it) on a storetest database, a fake SMTP sink,
// an HTTP client that validates every /api/v1 response against the spec, a
// WebSocket client, a signed-webhook receiver and MIME decoding.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/app"
	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/authn/authntest"
	"github.com/radioBros/SimpleTickets/internal/config"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/notify/smtptest"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

const e2ePublicURL = "http://localhost:8080"

// stack is one full-app instance.
type stack struct {
	t    *testing.T
	a    *app.App
	db   *store.DB // the app's own handle
	sink *smtptest.Server
	vars map[string]string
	logs *authntest.SyncBuffer

	base    string // set by run
	stopped bool
}

// e2eEnv is the environment of every instance on db.
func e2eEnv(t *testing.T, db *store.DB, sink *smtptest.Server, dataDir string) map[string]string {
	return map[string]string{
		"ST_SECRET_KEY": strings.Repeat("e", 32), "ST_DATA_DIR": dataDir, "ST_PUBLIC_URL": e2ePublicURL,
		"ST_DATABASE_URL": db.URL, "ST_LOG_FORMAT": "text",
		"ST_SMTP_HOST": sink.Host(), "ST_SMTP_PORT": strconv.Itoa(sink.Port()), "ST_SMTP_SECURITY": "none", "ST_SMTP_AUTH": "none",
		"ST_SMTP_FROM_CUSTOMER": "support@example.test", "ST_SMTP_FROM_STAFF": "desk@example.test",
		"ST_SELF_REGISTRATION": "true", "ST_AUTH_STAFF_LOCAL": "true",
		"ST_WEBHOOK_ALLOW_PRIVATE": "true", "ST_WEBHOOK_ALLOW_INSECURE": "true",
		"ST_NOTIFY_COALESCE_SECONDS": "0",
	}
}

// newSink starts the fake SMTP server.
func newSink(t *testing.T) *smtptest.Server {
	t.Helper()
	s, err := smtptest.Start(smtptest.Options{Mode: smtptest.Plain})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// buildStack builds the full app (default Registry) on vars; it is not running.
func buildStack(t *testing.T, sink *smtptest.Server, vars map[string]string) *stack {
	t.Helper()
	authntest.FastArgon()
	getenv := func(k string) string { return vars[k] }
	cfg, err := config.Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	logs := &authntest.SyncBuffer{}
	a, err := app.Build(context.Background(), cfg, app.BuildOptions{
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})), Getenv: getenv,
		SPA:     fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}},
		Version: "e2e", QueueOptions: &queue.Options{Fallback: 50 * time.Millisecond, CursorEvery: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("app.Build: %v", err)
	}
	s := &stack{t: t, a: a, db: a.Deps.DB, sink: sink, vars: vars, logs: logs}
	t.Cleanup(func() {
		if !s.stopped && s.base == "" {
			_ = a.Close()
		}
	})
	return s
}

// run serves the app on a random port until the returned stop is called
// (also on cleanup).
func (s *stack) run() (stop func()) {
	t := s.t
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.a.Run(ctx, ln) }()
	s.base = "http://" + ln.Addr().String()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(s.base + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("run: %v", err)
			}
			s.stopped = true
		})
	}
	t.Cleanup(stop)
	return stop
}

// close releases a stack that was never run.
func (s *stack) close() {
	if !s.stopped {
		_ = s.a.Close()
		s.stopped = true
	}
}

// ---- HTTP client (in-process through the full router) ----

type client struct {
	s      *stack
	cookie string
	csrf   string
	ip     string
}

func (s *stack) client() *client { return &client{s: s, ip: "192.0.2.10"} }

func (c *client) do(method, path string, body any, hdr ...string) (int, []byte) {
	t := c.s.t
	t.Helper()
	var raw []byte
	ct := ""
	switch b := body.(type) {
	case nil:
	case []byte:
		raw, ct = b, "message/rfc822"
	default:
		raw, _ = json.Marshal(b)
		ct = "application/json"
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.RemoteAddr = c.ip + ":40000"
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if method != "GET" {
		req.Header.Set("Origin", e2ePublicURL)
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: authn.CookieName, Value: c.cookie})
	}
	rec := httptest.NewRecorder()
	c.s.a.Server.Handler().ServeHTTP(rec, req)
	resp := rec.Result()
	data, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(data))
	if strings.HasPrefix(path, "/api/v1") {
		vreq := httptest.NewRequest(method, path, bytes.NewReader(raw))
		vreq.Header = req.Header.Clone()
		apispec.ValidateResponse(t, vreq, resp)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == authn.CookieName {
			c.cookie = ck.Value
			if ck.MaxAge < 0 {
				c.cookie = ""
			}
		}
	}
	if resp.StatusCode < 300 && strings.Contains(resp.Header.Get("Content-Type"), "json") {
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			if v, ok := m["csrf_token"].(string); ok && v != "" {
				c.csrf = v
			}
		}
	}
	return resp.StatusCode, data
}

// must asserts the status and decodes a JSON object body.
func (c *client) must(code int, method, path string, body any, hdr ...string) map[string]any {
	c.s.t.Helper()
	got, data := c.do(method, path, body, hdr...)
	if got != code {
		c.s.t.Fatalf("%s %s: want %d, got %d: %s", method, path, code, got, data)
	}
	var m map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

// staff creates an active staff user with a local password and signs in.
func (s *stack) staff(role domain.Role, tier int, teams ...string) (*client, string) {
	s.t.Helper()
	id := storetest.CreateStaff(s.t, s.db, role, tier, teams...)
	return s.loginFixture(id, domain.KindStaff), id
}

func (s *stack) loginFixture(id string, kind domain.UserKind) *client {
	t := s.t
	t.Helper()
	h, err := authn.HashPassword(context.Background(), authntest.TestPassword)
	if err != nil {
		t.Fatal(err)
	}
	var email string
	err = s.db.Tx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.ExecContext(context.Background(), `UPDATE users SET password_hash = ? WHERE id = ?`, h, id); err != nil {
			return err
		}
		return tx.QueryRowContext(context.Background(), `SELECT email FROM users WHERE id = ?`, id).Scan(&email)
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.login(email, kind)
}

func (s *stack) login(email string, kind domain.UserKind) *client {
	s.t.Helper()
	c := s.client()
	c.must(200, "POST", "/api/v1/auth/login", map[string]any{"audience": string(kind), "email": email, "password": authntest.TestPassword})
	return c
}

// ---- mail ----

type sentMail struct {
	To        []string
	Subject   string
	MessageID string
	Text      string // every decoded text/* part joined
	Raw       string
}

// decodeMail parses a captured message and decodes every text part
// (quoted-printable, base64) so assertions see the real content.
func decodeMail(t *testing.T, m smtptest.Message) sentMail {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(m.Data))
	if err != nil {
		t.Fatalf("parse mail: %v", err)
	}
	dec := new(mime.WordDecoder)
	subj, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		subj = msg.Header.Get("Subject")
	}
	var parts []string
	walkPart(t, msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, &parts)
	return sentMail{To: m.To, Subject: subj, MessageID: msg.Header.Get("Message-Id"), Text: strings.Join(parts, "\n----\n"), Raw: string(m.Data)}
}

func walkPart(t *testing.T, ctype, cte string, body io.Reader, out *[]string) {
	mt, params, err := mime.ParseMediaType(ctype)
	if err != nil {
		mt = "text/plain"
	}
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return
			}
			walkPart(t, p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p, out)
		}
	}
	r := body
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		r = quotedprintable.NewReader(body)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, &newlineStripper{r: body})
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decode %s part: %v", mt, err)
	}
	if strings.HasPrefix(mt, "text/") {
		*out = append(*out, string(b))
	}
}

type newlineStripper struct{ r io.Reader }

func (n *newlineStripper) Read(p []byte) (int, error) {
	buf := make([]byte, len(p))
	k, err := n.r.Read(buf)
	j := 0
	for _, c := range buf[:k] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}

// mailsTo returns the decoded mails sent to addr.
func (s *stack) mailsTo(addr string) []sentMail {
	s.t.Helper()
	var out []sentMail
	for _, m := range s.sink.Messages() {
		for _, to := range m.To {
			if strings.EqualFold(strings.Trim(to, "<>"), addr) {
				out = append(out, decodeMail(s.t, m))
				break
			}
		}
	}
	return out
}

// waitMail polls until a mail to addr matches pred.
func (s *stack) waitMail(addr string, within time.Duration, pred func(sentMail) bool) sentMail {
	s.t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, m := range s.mailsTo(addr) {
			if pred(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			var subj []string
			for _, m := range s.sink.Messages() {
				d := decodeMail(s.t, m)
				subj = append(subj, strings.Join(d.To, ",")+": "+d.Subject)
			}
			s.t.Fatalf("no matching mail to %s within %s; sink has %d mails: %v\nlogs:\n%s", addr, within, len(subj), subj, tail(s.logs.String(), 4000))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

var tokenURL = regexp.MustCompile(`https?://[^\s<>()"\]]*token=[A-Za-z0-9_\-%=.]+`)

// ---- WebSocket ----

type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	mu     sync.Mutex
	frames []map[string]any
}

// ws dials /api/v1/ws on the running stack with c's session cookie.
func (s *stack) ws(c *client) *wsClient {
	t := s.t
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Cookie", authn.CookieName+"="+c.cookie)
	h.Set("Origin", e2ePublicURL)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.base, "http")+"/api/v1/ws", &websocket.DialOptions{Subprotocols: []string{"simpletickets.v1"}, HTTPHeader: h})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	w := &wsClient{t: t, c: conn}
	go func() {
		for {
			_, b, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			w.mu.Lock()
			w.frames = append(w.frames, m)
			w.mu.Unlock()
		}
	}()
	t.Cleanup(func() { _ = conn.CloseNow() })
	return w
}

func (w *wsClient) waitFor(d time.Duration, pred func(map[string]any) bool) map[string]any {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, f := range w.frames {
			if pred(f) {
				w.mu.Unlock()
				return f
			}
		}
		w.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (w *wsClient) subscribe(id string, topics ...string) {
	w.t.Helper()
	b, _ := json.Marshal(map[string]any{"op": "subscribe", "id": id, "topics": topics})
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
	ack := w.waitFor(3*time.Second, func(f map[string]any) bool { return f["type"] == "ack" && f["id"] == id })
	if ack == nil {
		w.t.Fatal("no subscribe ack")
	}
	if d, ok := ack["denied"].([]any); ok && len(d) > 0 {
		w.t.Fatalf("topics denied: %v", d)
	}
}

func (w *wsClient) dump() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, _ := json.Marshal(w.frames)
	return string(b)
}

// ---- webhook receiver ----

type delivery struct {
	Event string
	Body  []byte
	Sig   string
}

type receiver struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []delivery
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, delivery{Event: req.Header.Get("X-SimpleTickets-Event"), Body: b, Sig: req.Header.Get("X-SimpleTickets-Signature")})
		r.mu.Unlock()
		w.WriteHeader(204)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) deliveries() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivery(nil), r.got...)
}

func (r *receiver) waitEvent(t *testing.T, typ string, within time.Duration, pred func(map[string]any) bool) delivery {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, d := range r.deliveries() {
			if d.Event != typ {
				continue
			}
			var env map[string]any
			_ = json.Unmarshal(d.Body, &env)
			if pred == nil || pred(env) {
				return d
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	var evs []string
	for _, d := range r.deliveries() {
		evs = append(evs, d.Event)
	}
	t.Fatalf("webhook %s not delivered within %s (got %v)", typ, within, evs)
	return delivery{}
}

// verifySig checks `t=<unix>,v1=<hex hmac-sha256(secret, t + "." + body)>`.
func verifySig(secret string, d delivery) bool {
	var ts, v1 string
	for _, part := range strings.Split(d.Sig, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			v1 = v
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(sec, 0)).Abs() > 300*time.Second {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(d.Body)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(v1))
}

// poll retries fn until it returns true or the timeout ends.
func poll(t *testing.T, within time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
