// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	osexec "os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/queue"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

type literal struct {
	*bytes.Reader
}

func (l literal) Size() int64 { return int64(l.Len()) }

// memIMAP is an in-process IMAP server (go-imap/v2 imapmemserver, same module).
type memIMAP struct {
	t    *testing.T
	mem  *imapmemserver.Server
	user *imapmemserver.User
	srv  *imapserver.Server
	addr string
}

func startIMAP(t *testing.T) *memIMAP {
	t.Helper()
	m := &memIMAP{t: t, mem: imapmemserver.New(), user: imapmemserver.NewUser("u", "p")}
	if err := m.user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	m.mem.AddUser(m.user)
	m.listen("127.0.0.1:0")
	t.Cleanup(func() {
		if err := m.srv.Close(); err != nil {
			t.Errorf("m.srv.Close: %v", err)
		}
	})
	return m
}

func (m *memIMAP) listen(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.t.Fatal(err)
	}
	m.addr = ln.Addr().String()
	m.srv = imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return m.mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
		Logger:       log.New(ioDiscard{}, "", 0),
	})
	go func() { _ = m.srv.Serve(ln) }()
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func (m *memIMAP) appendMsg(folder string, raw []byte) {
	m.t.Helper()
	if _, err := m.user.Append(folder, literal{bytes.NewReader(raw)}, &imap.AppendOptions{}); err != nil {
		m.t.Fatal(err)
	}
}

func (m *memIMAP) port() int {
	_, p, _ := net.SplitHostPort(m.addr)
	n, _ := strconv.Atoi(p)
	return n
}

// count returns (messages, unseen) of folder (-1 when missing).
func (m *memIMAP) count(folder string) (int, int) {
	st, err := m.user.Status(folder, &imap.StatusOptions{NumMessages: true, NumUnseen: true})
	if err != nil {
		return -1, -1
	}
	return int(*st.NumMessages), int(*st.NumUnseen)
}

func (e *env) imapMailbox(srv *memIMAP, extra map[string]any) *mailbox {
	e.t.Helper()
	cfg := map[string]any{"host": "127.0.0.1", "port": srv.port(), "security": "none", "username": "u", "password": "p"}
	for k, v := range extra {
		cfg[k] = v
	}
	r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{"name": "IMAP", "kind": "imap", "address": "imap@example.test", "imap": cfg})
	if r.StatusCode != 201 {
		e.t.Fatalf("%d %s", r.StatusCode, d)
	}
	var dto mailboxDTO
	_ = json.Unmarshal(d, &dto)
	return e.mailbox(dto.ID)
}

// cmdCounter counts tagged client commands (lines like "T12 IDLE").
type cmdCounter struct {
	net.Conn
	mu   sync.Mutex
	cmds []string
}

func (c *cmdCounter) Write(p []byte) (int, error) {
	c.mu.Lock()
	for _, line := range strings.Split(string(p), "\r\n") {
		if f := strings.Fields(line); len(f) >= 2 && strings.HasPrefix(f[0], "T") {
			c.cmds = append(c.cmds, strings.ToUpper(f[1]))
		} else if strings.TrimSpace(line) == "DONE" {
			c.cmds = append(c.cmds, "DONE")
		}
	}
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *cmdCounter) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.cmds...)
}

type delivered struct {
	mu   sync.Mutex
	raws [][]byte
	ch   chan struct{}
}

func newDelivered() *delivered { return &delivered{ch: make(chan struct{}, 100)} }

func (d *delivered) fn(ctx context.Context, raw []byte, _ Meta) error {
	d.mu.Lock()
	d.raws = append(d.raws, raw)
	d.mu.Unlock()
	d.ch <- struct{}{}
	return nil
}

func (d *delivered) wait(t *testing.T, timeout time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	select {
	case <-d.ch:
		return time.Since(start)
	case <-time.After(timeout):
		t.Fatalf("no delivery within %s", timeout)
		return 0
	}
}

// closeClient closes an IMAP client; an error that only says the server had
// already ended the session (closed, reset, EOF) is benign, anything else fails.
func closeClient(t *testing.T, cl *imapclient.Client) {
	t.Helper()
	err := cl.Close()
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) {
		return
	}
	// go-imap may flatten the cause into text (no %w) when it fails reading
	// the tagged response of the already-ended session.
	if msg := err.Error(); strings.Contains(msg, "connection reset by peer") || strings.Contains(msg, "use of closed network connection") || strings.HasSuffix(msg, "EOF") {
		return
	}
	t.Errorf("cl.Close: %v", err)
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	eventuallyWithin(t, 30*time.Second, what, f)
}

func eventuallyWithin(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestIMAPIdleDeliversAndMoves(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		oldSettle := idleSettle
		idleSettle = time.Hour // this test asserts silence while idle; the settle re-check is separate
		defer func() { idleSettle = oldSettle }()
		srv := startIMAP(t)
		srv.appendMsg("INBOX", mkMail("a@client.example.org", "existing", "x", nil))
		src := e.m.NewIMAPSource(e.imapMailbox(srv, nil))
		var counter *cmdCounter
		src.wrapConn = func(c net.Conn) net.Conn { counter = &cmdCounter{Conn: c}; return counter }
		d := newDelivered()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = src.Run(ctx, d.fn); close(done) }()
		defer func() { cancel(); <-done }()

		d.wait(t, 30*time.Second) // the pre-existing unseen message
		eventually(t, "move to processed", func() bool { n, _ := srv.count("FeatherDesk/Processed"); return n == 1 })
		// let the client settle into IDLE, then measure idle traffic
		eventually(t, "idle", func() bool { c := counter.snapshot(); return len(c) > 0 && c[len(c)-1] == "IDLE" })
		before := len(counter.snapshot())
		time.Sleep(1500 * time.Millisecond)
		if idleCmds := counter.snapshot()[before:]; len(idleCmds) != 0 {
			t.Fatalf("commands while idle: %v (no polling allowed)", idleCmds)
		}
		// a new message is pushed through IDLE quickly
		srv.appendMsg("INBOX", mkMail("b@client.example.org", "new", "y", nil))
		if lat := d.wait(t, 30*time.Second); lat > 10*time.Second {
			t.Fatalf("IDLE latency %s > 10s (pushed, not polled)", lat)
		}
		eventually(t, "second move", func() bool { n, _ := srv.count("FeatherDesk/Processed"); return n == 2 })
		if n, _ := srv.count("INBOX"); n != 0 {
			t.Fatalf("INBOX still has %d", n)
		}
		if _, unseen := srv.count("FeatherDesk/Processed"); unseen != 0 {
			t.Fatalf("processed messages should be \\Seen")
		}
	})
}

func TestIMAPFlagActionNeverDeletes(t *testing.T) {
	e := newEnv(t, storetest.Open(t, store.SQLite), nil)
	srv := startIMAP(t)
	srv.appendMsg("INBOX", mkMail("a@client.example.org", "flag me", "x", nil))
	src := e.m.NewIMAPSource(e.imapMailbox(srv, map[string]any{"processed_action": "flag"}))
	d := newDelivered()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = src.Run(ctx, d.fn); close(done) }()
	d.wait(t, 30*time.Second)
	eventually(t, "seen flag", func() bool { n, unseen := srv.count("INBOX"); return n == 1 && unseen == 0 })
	cancel()
	<-done
	if n, _ := srv.count("FeatherDesk/Processed"); n != -1 && n != 0 {
		t.Fatalf("flag action moved mail")
	}
}

func TestNoIdleFallbackPollsAtLeast60s(t *testing.T) {
	if pollInterval < 60*time.Second {
		t.Fatalf("production poll interval %s < 60s", pollInterval)
	}
	old := pollInterval
	pollInterval = 300 * time.Millisecond // scaled down for the test
	defer func() { pollInterval = old }()
	e := newEnv(t, storetest.Open(t, store.SQLite), nil)
	srv := startIMAP(t)
	src := e.m.NewIMAPSource(e.imapMailbox(srv, nil))
	src.noIdle = true
	var counter *cmdCounter
	src.wrapConn = func(c net.Conn) net.Conn { counter = &cmdCounter{Conn: c}; return counter }
	d := newDelivered()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = src.Run(ctx, d.fn); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(100 * time.Millisecond)
	srv.appendMsg("INBOX", mkMail("a@client.example.org", "polled", "x", nil))
	d.wait(t, 30*time.Second)
	start, t0 := src.polls.Load(), time.Now()
	eventually(t, "three fallback polls", func() bool { return src.polls.Load()-start >= 3 })
	// never faster than the interval: ≤ elapsed/interval (+1 for edges)
	elapsed := time.Since(t0)
	if polls := src.polls.Load() - start; polls > int64(elapsed/pollInterval)+1 {
		t.Fatalf("%d polls in %s at %s interval", polls, elapsed, pollInterval)
	}
	for _, c := range counter.snapshot() {
		if c == "IDLE" {
			t.Fatal("IDLE used although unsupported")
		}
	}
}

func TestIMAPReconnectAfterRestart(t *testing.T) {
	oldMin := backoffMin
	backoffMin = 50 * time.Millisecond
	defer func() { backoffMin = oldMin }()
	e := newEnv(t, storetest.Open(t, store.SQLite), nil)
	srv := startIMAP(t)
	mb := e.imapMailbox(srv, nil)
	src := e.m.NewIMAPSource(mb)
	d := newDelivered()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = src.Run(ctx, d.fn); close(done) }()
	defer func() { cancel(); <-done }()
	srv.appendMsg("INBOX", mkMail("a@client.example.org", "one", "x", nil))
	d.wait(t, 30*time.Second)
	eventually(t, "first move", func() bool { n, _ := srv.count("FeatherDesk/Processed"); return n == 1 })
	// kill the server mid-session; mail arrives while it is down
	if err := srv.srv.Close(); err != nil {
		t.Fatalf("srv.srv.Close: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	srv.appendMsg("INBOX", mkMail("a@client.example.org", "two", "x", nil))
	srv.listen(srv.addr)
	d.wait(t, 30*time.Second)
	eventually(t, "no message lost", func() bool { n, _ := srv.count("FeatherDesk/Processed"); return n == 2 })
	var lastErr string
	_ = e.db.Read().QueryRowContext(context.Background(), `SELECT last_error FROM inbound_mailboxes WHERE id = ?`, mb.ID).Scan(&lastErr)
	_ = lastErr // reported while down, cleared on the next healthy round
}

func TestMailboxTestCodes(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		srv := startIMAP(t)
		test := func(extra map[string]any) TestResult {
			mb := e.imapMailbox(srv, extra)
			r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes/"+mb.ID+"/test", "staff:admin", nil)
			if r.StatusCode != 200 {
				t.Fatalf("%d %s", r.StatusCode, d)
			}
			var res TestResult
			_ = json.Unmarshal(d, &res)
			return res
		}
		srv.appendMsg("INBOX", mkMail("a@client.example.org", "x", "x", nil))
		if r := test(nil); !r.OK || r.MessageCount == nil || *r.MessageCount != 1 || r.Error != nil {
			t.Fatalf("good config: %+v", r)
		}
		if r := test(map[string]any{"password": "wrong"}); r.OK || r.Error == nil || *r.Error != "auth_failed" {
			t.Fatalf("wrong password: %+v", r)
		}
		if r := test(map[string]any{"folder": "Nope"}); r.OK || *r.Error != "folder_missing" {
			t.Fatalf("missing folder: %+v", r)
		}
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		closed := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatalf("ln.Close: %v", err)
		}
		if r := test(map[string]any{"port": closed}); r.OK || *r.Error != "connection_refused" {
			t.Fatalf("refused: %+v", r)
		}
		if r := test(map[string]any{"security": "tls"}); r.OK || *r.Error != "tls_mismatch" {
			t.Fatalf("tls against plaintext: %+v", r)
		}
	})
}

// fakeXOAUTH2Server is a minimal IMAP server accepting AUTHENTICATE XOAUTH2
// with one access token (go-sasl stays out of our imports).
func fakeXOAUTH2Server(t *testing.T, wantToken func() string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("ln.Close: %v", err)
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				fmt.Fprintf(c, "* OK ready\r\n")
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					f := strings.Fields(line)
					if len(f) < 2 {
						continue
					}
					tag, cmd := f[0], strings.ToUpper(f[1])
					switch cmd {
					case "CAPABILITY":
						fmt.Fprintf(c, "* CAPABILITY IMAP4rev1 AUTH=XOAUTH2 SASL-IR IDLE\r\n%s OK done\r\n", tag)
					case "AUTHENTICATE":
						ir, _ := base64.StdEncoding.DecodeString(f[3])
						if strings.Contains(string(ir), "auth=Bearer "+wantToken()+"\x01") && strings.HasPrefix(string(ir), "user=u\x01") {
							fmt.Fprintf(c, "%s OK authenticated\r\n", tag)
						} else {
							fmt.Fprintf(c, "%s NO [AUTHENTICATIONFAILED] bad token\r\n", tag)
						}
					case "LOGOUT":
						fmt.Fprintf(c, "* BYE\r\n%s OK bye\r\n", tag)
						return
					default:
						fmt.Fprintf(c, "%s BAD unsupported\r\n", tag)
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func TestXOAUTH2TokenRefresh(t *testing.T) {
	e := newEnv(t, storetest.Open(t, store.SQLite), nil)
	var calls atomic.Int64
	var gotRefresh atomic.Value
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotRefresh.Store(r.Form.Get("refresh_token") + "|" + r.Form.Get("grant_type"))
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// short-lived token; the provider rotates the refresh token
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"Bearer","expires_in":1,"refresh_token":"rt-%d"}`, n, n+1)
	}))
	defer tokenSrv.Close()
	addr := fakeXOAUTH2Server(t, func() string { return "at-" + strconv.FormatInt(calls.Load(), 10) })
	host, port, _ := net.SplitHostPort(addr)
	pn, _ := strconv.Atoi(port)
	r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{"name": "Gmail", "kind": "imap", "address": "g@example.test",
		"imap": map[string]any{"host": host, "port": pn, "security": "none", "username": "u", "auth": "xoauth2",
			"oauth_token_url": tokenSrv.URL, "oauth_client_id": "cid", "oauth_client_secret": "csecret", "refresh_token": "rt-1"}})
	if r.StatusCode != 201 {
		t.Fatalf("%d %s", r.StatusCode, d)
	}
	var dto mailboxDTO
	_ = json.Unmarshal(d, &dto)
	src := e.m.NewIMAPSource(e.mailbox(dto.ID))
	login := func() {
		t.Helper()
		cl, err := src.dial(context.Background(), nil)
		if err != nil {
			t.Fatalf("xoauth2 login: %v", err)
		}
		_ = cl.Logout().Wait()
		closeClient(t, cl)
	}
	login()
	if calls.Load() != 1 || gotRefresh.Load().(string) != "rt-1|refresh_token" {
		t.Fatalf("first refresh: calls=%d form=%v", calls.Load(), gotRefresh.Load())
	}
	// the rotated refresh token is sealed and stored
	var raw string
	_ = e.db.Read().QueryRowContext(context.Background(), `SELECT imap FROM inbound_mailboxes WHERE id = ?`, dto.ID).Scan(&raw)
	var cfg IMAPConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	if rt, _ := e.box.OpenString(settings.PurposeIMAPRefresh, cfg.OAuthRefreshEnc); rt != "rt-2" || strings.Contains(raw, "rt-2") {
		t.Fatalf("rotated refresh token not sealed/stored: %q", rt)
	}
	// the access token expired: the next login refreshes in memory
	time.Sleep(1100 * time.Millisecond)
	login()
	if calls.Load() != 2 {
		t.Fatalf("expected a refresh on expiry, calls=%d", calls.Load())
	}
	if c := (&xoauth2Client{user: "u", token: "t"}); true {
		mech, ir, _ := c.Start()
		if mech != "XOAUTH2" || string(ir) != "user=u\x01auth=Bearer t\x01\x01" {
			t.Fatalf("sasl initial response %q %q", mech, ir)
		}
	}
}

// TestDovecotIntegration runs against a real Dovecot (NOT-033) only when
// FD_TEST_IMAP_URL is set, e.g. imap://user:pass@localhost:143/INBOX
// (deploy/compose.test.yml). It covers: the mailbox test (good and wrong
// password), APPEND → ticket, the move to the processed folder, an IDLE-pushed
// reply threaded by plus token, and — when FD_TEST_IMAP_RESTART holds a shell
// command that restarts the server (e.g. `docker restart <container>`) — a
// reconnect after the restart with no message lost. With FD_TEST_IMAP_NO_IDLE=1
// the server must not offer IDLE (Dovecot `imap_capability { IDLE = no }`): the
// reply then arrives through the fallback poll, which the test asserts ran.
func TestDovecotIntegration(t *testing.T) {
	raw := os.Getenv("FD_TEST_IMAP_URL")
	if raw == "" {
		t.Skip("FD_TEST_IMAP_URL not set")
	}
	noIdle := os.Getenv("FD_TEST_IMAP_NO_IDLE") == "1"
	oldMin, oldPoll := backoffMin, pollInterval
	backoffMin, pollInterval = 200*time.Millisecond, time.Second
	defer func() { backoffMin, pollInterval = oldMin, oldPoll }()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = 143
	}
	security := "none"
	if u.Scheme == "imaps" {
		security = "tls"
	}
	addr := net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	e := newEnv(t, storetest.Open(t, store.SQLite), nil)
	cust := customer(t, e.db, "dovecot@client.example.org", "")
	folder := "INBOX"
	const processed = "FeatherDesk/Processed"

	// A plain client (fresh per call: the server may restart) to APPEND and count.
	withClient := func(fn func(cl *imapclient.Client)) {
		t.Helper()
		cl, err := imapclient.DialInsecure(addr, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			closeClient(t, cl)
		}()
		if err := cl.Login(u.User.Username(), pw).Wait(); err != nil {
			t.Fatal(err)
		}
		fn(cl)
	}
	appendMsg := func(msg []byte) {
		t.Helper()
		withClient(func(cl *imapclient.Client) {
			ap := cl.Append(folder, int64(len(msg)), nil)
			_, _ = ap.Write(msg)
			_ = ap.Close()
			if _, err := ap.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
	count := func(mailbox string) int {
		n := -1
		withClient(func(cl *imapclient.Client) {
			st, err := cl.Status(mailbox, &imap.StatusOptions{NumMessages: true}).Wait()
			if err == nil && st.NumMessages != nil {
				n = int(*st.NumMessages)
			}
		})
		return n
	}
	// Leftovers from an earlier run must not count.
	base := max(count(processed), 0)
	if n := count(folder); n != 0 {
		t.Fatalf("INBOX of the test account is not empty (%d messages): use a fresh Dovecot", n)
	}

	r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{"name": "Dovecot", "kind": "imap", "address": "dovecot@example.test",
		"imap": map[string]any{"host": u.Hostname(), "port": port, "security": security, "username": u.User.Username(), "password": pw, "folder": folder, "allow_insecure_tls": true}})
	if r.StatusCode != 201 {
		t.Fatalf("%d %s", r.StatusCode, d)
	}
	var dto mailboxDTO
	_ = json.Unmarshal(d, &dto)
	mb := e.mailbox(dto.ID)
	if res := e.m.TestMailbox(context.Background(), mb); !res.OK {
		t.Fatalf("test: %+v", res)
	}
	bad := *mb
	bad.IMAP.Auth = "password"
	bad.Stored.PasswordEnc, _ = e.box.SealString(settings.PurposeIMAPPassword, "definitely-wrong")
	if res := e.m.TestMailbox(context.Background(), &bad); res.OK || *res.Error != "auth_failed" {
		t.Fatalf("wrong password: %+v", res)
	}

	// 1. APPEND → a new ticket; the message moves to the processed folder.
	appendMsg(mkMail("dovecot@client.example.org", "From Dovecot", "hello", nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	src := e.m.NewIMAPSource(mb)
	go func() { _ = src.Run(ctx, e.m.deliverFor(mb)); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "ticket from dovecot", func() bool { c, _ := e.sink.counts(); return c >= 1 })
	eventually(t, "moved to "+processed, func() bool { return count(processed) == base+1 && count(folder) == 0 })

	// 2. While the source idles, a reply addressed to the ticket's plus address is
	//    pushed through IDLE and threads onto that ticket.
	tk := storetest.CreateTicket(t, e.db, storetest.Ticket{Requester: cust, Subject: "Threaded via Dovecot"})
	tok, err := thread.PlusToken(e.box, tk, "dovecot@client.example.org")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	appendMsg(mkMail("dovecot@client.example.org", "Re: no number", "via plus token", map[string]string{"To": thread.PlusAddress("dovecot@example.test", tok)}))
	eventually(t, "threaded reply", func() bool { _, n := e.sink.counts(); return n == 1 })
	if lat := time.Since(start); lat > 5*time.Second {
		t.Fatalf("delivery took %s", lat)
	}
	if polls := src.polls.Load(); noIdle != (polls > 0) {
		t.Fatalf("server without IDLE=%v, but fallback polls=%d", noIdle, polls)
	}
	e.sink.mu.Lock()
	got := e.sink.replies[0].TicketID
	e.sink.mu.Unlock()
	if got != tk {
		t.Fatalf("reply threaded to %q, want %q", got, tk)
	}
	eventually(t, "reply moved", func() bool { return count(processed) == base+2 })

	// 3. Server restart: the source reconnects (backoff) and picks up mail that
	//    arrived after the restart.
	if cmd := os.Getenv("FD_TEST_IMAP_RESTART"); cmd != "" {
		if out, err := osexec.Command("sh", "-c", cmd).CombinedOutput(); err != nil {
			t.Fatalf("restart: %v %s", err, out)
		}
		// A container port proxy may accept TCP before Dovecot listens: wait for a login.
		eventuallyWithin(t, 30*time.Second, "dovecot back", func() bool {
			cl, err := imapclient.DialInsecure(addr, nil)
			if err != nil {
				return false
			}
			defer func() {
				closeClient(t, cl)
			}()
			return cl.Login(u.User.Username(), pw).Wait() == nil
		})
		appendMsg(mkMail("dovecot@client.example.org", "After restart", "still here", nil))
		eventuallyWithin(t, 30*time.Second, "ticket after restart", func() bool { c, _ := e.sink.counts(); return c >= 2 })
	}
}

// TestStartEndToEnd: Start runs the IMAP source for an active mailbox; a mail
// appended to INBOX becomes a ticket through the queue job; deactivating the
// mailbox stops its source (hot reload).
func TestStartEndToEnd(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "e2e@client.example.org", "")
		q := queue.New(db, queue.Options{Fallback: 50 * time.Millisecond, Logger: e.m.log})
		e.m.RegisterQueue(q)
		ctx, cancel := context.WithCancel(context.Background())
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		srv := startIMAP(t)
		mb := e.imapMailbox(srv, nil)
		done := make(chan struct{})
		go func() { _ = e.m.Start(ctx); close(done) }()
		defer func() { cancel(); <-done; _ = q.Stop(context.Background()) }()
		srv.appendMsg("INBOX", mkMail("e2e@client.example.org", "Via IMAP", "hello", nil))
		eventually(t, "ticket via IMAP", func() bool { c, _ := e.sink.counts(); return c == 1 })
		eventually(t, "processed status", func() bool {
			var st string
			_ = db.Read().QueryRowContext(context.Background(), `SELECT status FROM inbound_messages WHERE mailbox_id = ?`, mb.ID).Scan(&st)
			return st == "processed"
		})
		if r, d := e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", map[string]any{"active": false}); r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, d)
		}
		eventually(t, "source stopped", func() bool {
			e.m.sources.mu.Lock()
			defer e.m.sources.mu.Unlock()
			return len(e.m.sources.running) == 0
		})
		srv.appendMsg("INBOX", mkMail("e2e@client.example.org", "After stop", "x", nil))
		time.Sleep(300 * time.Millisecond)
		if c, _ := e.sink.counts(); c != 1 {
			t.Fatalf("inactive mailbox still delivering")
		}
	})
}
