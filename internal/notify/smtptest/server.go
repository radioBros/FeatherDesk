// SPDX-License-Identifier: AGPL-3.0-only

// Package smtptest is a small net-based fake SMTP server for tests: EHLO,
// AUTH PLAIN/LOGIN/CRAM-MD5, STARTTLS and implicit TLS (self-signed cert),
// MAIL/RCPT/DATA/RSET/NOOP/QUIT, scripted failures and captured messages.
// It is not a real MTA and is only imported by tests.
package smtptest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mode selects the transport security of the listener.
type Mode int

const (
	Plain    Mode = iota // no TLS offered
	StartTLS             // STARTTLS advertised (and required before AUTH when RequireTLS)
	TLS                  // implicit TLS (SMTPS)
)

// Message is one accepted mail.
type Message struct {
	From string
	To   []string
	Data []byte
}

// Header parses the captured message headers.
func (m Message) Header() mail.Header {
	msg, err := mail.ReadMessage(strings.NewReader(string(m.Data)))
	if err != nil {
		return mail.Header{}
	}
	return msg.Header
}

// Options configure a Server.
type Options struct {
	Mode       Mode
	Username   string // "" = no AUTH required/advertised
	Password   string
	RequireTLS bool // refuse AUTH/MAIL before STARTTLS
	// Reply lets a test script failures: called before answering MAIL, RCPT,
	// DATA-end with the command argument; a non-zero code is sent instead of
	// the success reply.
	Reply func(stage, arg string) (code int, text string)
}

// Server is a running fake SMTP server.
type Server struct {
	opt  Options
	ln   net.Listener
	cert tls.Certificate

	mu       sync.Mutex
	msgs     []Message
	cmds     []string
	authOK   int
	authFail int
	wg       sync.WaitGroup
}

// Start listens on 127.0.0.1:0.
func Start(opt Options) (*Server, error) {
	cert, err := selfSigned()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{opt: opt, cert: cert}
	if opt.Mode == TLS {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	s.ln = ln
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Addr is "127.0.0.1:port".
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host is "127.0.0.1".
func (s *Server) Host() string { h, _, _ := net.SplitHostPort(s.Addr()); return h }

// Port is the listening port.
func (s *Server) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr())
	n, _ := strconv.Atoi(p)
	return n
}

// Close stops the listener.
func (s *Server) Close() { _ = s.ln.Close(); s.wg.Wait() }

// Messages returns the captured mails.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

// Commands returns every command verb received (upper-case), in order.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

// Reset forgets captured messages and commands.
func (s *Server) Reset() {
	s.mu.Lock()
	s.msgs, s.cmds = nil, nil
	s.mu.Unlock()
}

// SetReply replaces the failure script.
func (s *Server) SetReply(f func(stage, arg string) (int, string)) {
	s.mu.Lock()
	s.opt.Reply = f
	s.mu.Unlock()
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(c)
		}()
	}
}

type session struct {
	s      *Server
	c      net.Conn
	r      *bufio.Reader
	w      *bufio.Writer
	tls    bool
	authed bool
	from   string
	to     []string
}

func (ss *session) reply(code int, text string) {
	fmt.Fprintf(ss.w, "%d %s\r\n", code, text)
	_ = ss.w.Flush()
}

func (ss *session) line() (string, error) {
	_ = ss.c.SetReadDeadline(time.Now().Add(30 * time.Second))
	l, err := ss.r.ReadString('\n')
	return strings.TrimRight(l, "\r\n"), err
}

func (s *Server) scripted(stage, arg string) (int, string) {
	s.mu.Lock()
	f := s.opt.Reply
	s.mu.Unlock()
	if f == nil {
		return 0, ""
	}
	return f(stage, arg)
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	ss := &session{s: s, c: c, r: bufio.NewReader(c), w: bufio.NewWriter(c), tls: s.opt.Mode == TLS}
	ss.reply(220, "fake ESMTP ready")
	for {
		l, err := ss.line()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(l, " ")
		verb = strings.ToUpper(verb)
		s.mu.Lock()
		s.cmds = append(s.cmds, verb)
		s.mu.Unlock()
		switch verb {
		case "EHLO", "HELO":
			ext := []string{"fake.test", "8BITMIME", "SMTPUTF8"}
			if s.opt.Mode == StartTLS && !ss.tls {
				ext = append(ext, "STARTTLS")
			}
			if s.opt.Username != "" && (ss.tls || !s.opt.RequireTLS) {
				ext = append(ext, "AUTH PLAIN LOGIN CRAM-MD5")
			}
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				fmt.Fprintf(ss.w, "250%s%s\r\n", sep, e)
			}
			_ = ss.w.Flush()
		case "STARTTLS":
			if s.opt.Mode != StartTLS || ss.tls {
				ss.reply(502, "not supported")
				continue
			}
			ss.reply(220, "go ahead")
			tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			ss.c, ss.r, ss.w, ss.tls = tc, bufio.NewReader(tc), bufio.NewWriter(tc), true
		case "AUTH":
			ss.auth(arg)
		case "MAIL":
			if s.opt.RequireTLS && !ss.tls {
				ss.reply(530, "must issue STARTTLS first")
				continue
			}
			if s.opt.Username != "" && !ss.authed {
				ss.reply(530, "authentication required")
				continue
			}
			if code, text := s.scripted("MAIL", arg); code != 0 {
				ss.reply(code, text)
				continue
			}
			ss.from, ss.to = addrArg(arg), nil
			ss.reply(250, "ok")
		case "RCPT":
			if code, text := s.scripted("RCPT", arg); code != 0 {
				ss.reply(code, text)
				continue
			}
			ss.to = append(ss.to, addrArg(arg))
			ss.reply(250, "ok")
		case "DATA":
			ss.reply(354, "end with <CRLF>.<CRLF>")
			var b strings.Builder
			for {
				dl, err := ss.line()
				if err != nil {
					return
				}
				if dl == "." {
					break
				}
				dl = strings.TrimPrefix(dl, ".")
				b.WriteString(dl)
				b.WriteString("\r\n")
			}
			if code, text := s.scripted("DATA", b.String()); code != 0 {
				ss.reply(code, text)
				continue
			}
			s.mu.Lock()
			s.msgs = append(s.msgs, Message{From: ss.from, To: ss.to, Data: []byte(b.String())})
			s.mu.Unlock()
			ss.reply(250, "queued")
		case "RSET":
			ss.from, ss.to = "", nil
			ss.reply(250, "ok")
		case "NOOP":
			ss.reply(250, "ok")
		case "QUIT":
			ss.reply(221, "bye")
			return
		default:
			ss.reply(502, "command not implemented")
		}
	}
}

func addrArg(arg string) string {
	_, a, _ := strings.Cut(arg, ":")
	a = strings.TrimSpace(a)
	if i := strings.IndexByte(a, '>'); i >= 0 {
		a = a[:i]
	}
	return strings.TrimPrefix(a, "<")
}

func (ss *session) auth(arg string) {
	s := ss.s
	mech, initial, _ := strings.Cut(arg, " ")
	var user, pass string
	switch strings.ToUpper(mech) {
	case "PLAIN":
		if initial == "" {
			ss.reply(334, "")
			l, _ := ss.line()
			initial = l
		}
		raw, _ := base64.StdEncoding.DecodeString(initial)
		parts := strings.Split(string(raw), "\x00")
		if len(parts) == 3 {
			user, pass = parts[1], parts[2]
		}
	case "LOGIN":
		if initial == "" {
			ss.reply(334, base64.StdEncoding.EncodeToString([]byte("Username:")))
			initial, _ = ss.line()
		}
		u, _ := base64.StdEncoding.DecodeString(initial)
		ss.reply(334, base64.StdEncoding.EncodeToString([]byte("Password:")))
		l, _ := ss.line()
		p, _ := base64.StdEncoding.DecodeString(l)
		user, pass = string(u), string(p)
	case "CRAM-MD5":
		challenge := fmt.Sprintf("<%d.fake@test>", time.Now().UnixNano())
		ss.reply(334, base64.StdEncoding.EncodeToString([]byte(challenge)))
		l, _ := ss.line()
		raw, _ := base64.StdEncoding.DecodeString(l)
		u, digest, _ := strings.Cut(string(raw), " ")
		h := hmac.New(md5.New, []byte(s.opt.Password))
		h.Write([]byte(challenge))
		if u == s.opt.Username && hex.EncodeToString(h.Sum(nil)) == digest {
			user, pass = s.opt.Username, s.opt.Password
		}
	default:
		ss.reply(504, "unrecognized mechanism")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if user == s.opt.Username && pass == s.opt.Password && user != "" {
		s.authOK++
		ss.authed = true
		fmt.Fprintf(ss.w, "235 2.7.0 Authentication successful\r\n")
	} else {
		s.authFail++
		fmt.Fprintf(ss.w, "535 5.7.8 Authentication credentials invalid\r\n")
	}
	_ = ss.w.Flush()
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
