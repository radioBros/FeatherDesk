// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"syscall"
	"time"

	gomail "github.com/wneessen/go-mail"
	gosmtp "github.com/wneessen/go-mail/smtp"

	"github.com/radioBros/SimpleTickets/internal/settings"
)

// SMTPConfig is the effective SMTP configuration (stored settings overlaid by
// ST_SMTP_*, password decrypted). Never log it.
type SMTPConfig struct {
	Host             string
	Port             int
	Security         string // none | starttls | tls
	Username         string
	Password         string
	Auth             string // plain | login | cram-md5 | none
	HELO             string
	Timeout          time.Duration
	AllowInsecureTLS bool
}

// Configured reports whether email can be sent at all.
func (c *SMTPConfig) Configured() bool { return c != nil && strings.TrimSpace(c.Host) != "" }

func configFrom(v settings.SMTP, password string) *SMTPConfig {
	to := time.Duration(v.TimeoutSeconds) * time.Second
	if to <= 0 {
		to = 10 * time.Second
	}
	return &SMTPConfig{Host: strings.TrimSpace(v.Host), Port: v.Port, Security: v.Security, Username: v.Username, Password: password,
		Auth: v.Auth, HELO: v.HELO, Timeout: to, AllowInsecureTLS: v.AllowInsecureTLS}
}

// Mail is one rendered outgoing message.
type Mail struct {
	From       mail.Address      `json:"from"`
	ReplyTo    string            `json:"reply_to,omitempty"`
	To         mail.Address      `json:"to"`
	Subject    string            `json:"subject"`
	Text       string            `json:"text"`
	HTML       string            `json:"html"`
	MessageID  string            `json:"message_id"` // "<…>"
	References string            `json:"references,omitempty"`
	InReplyTo  string            `json:"in_reply_to,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// Sender delivers one mail with the given config.
type Sender interface {
	Send(ctx context.Context, cfg *SMTPConfig, m *Mail) error
}

// SMTPSender sends through go-mail, one connection per mail (low volume).
type SMTPSender struct{}

func (SMTPSender) Send(ctx context.Context, cfg *SMTPConfig, m *Mail) error {
	msg, err := buildMsg(m)
	if err != nil {
		return &SendFailure{Code: "rejected", Permanent: true, Err: err}
	}
	var conns connTracker
	defer conns.closeAll() // go-mail leaks the conn when EHLO/STARTTLS/AUTH fails
	c, err := newClient(cfg, conns.dialer(cfg))
	if err != nil {
		return &SendFailure{Code: "other", Err: err}
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return classify(err)
	}
	return nil
}

// connTracker dials (plain TCP or implicit TLS) and remembers the conns so
// they are always closed after a send attempt.
type connTracker struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (t *connTracker) dialer(cfg *SMTPConfig) gomail.DialContextFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if cfg.Security == "tls" {
			tc := tls.Client(c, tlsConfig(cfg))
			if dl, ok := ctx.Deadline(); ok {
				_ = c.SetDeadline(dl)
			}
			if err := tc.HandshakeContext(ctx); err != nil {
				c.Close()
				return nil, err
			}
			c = tc
		}
		t.mu.Lock()
		t.conns = append(t.conns, c)
		t.mu.Unlock()
		return c, nil
	}
}

func (t *connTracker) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.conns {
		_ = c.Close()
	}
}

func tlsConfig(cfg *SMTPConfig) *tls.Config {
	return &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.AllowInsecureTLS} //nolint:gosec // admin opt-in (D-12)
}

func newClient(cfg *SMTPConfig, dial gomail.DialContextFunc) (*gomail.Client, error) {
	port := cfg.Port
	if port <= 0 {
		switch cfg.Security {
		case "tls":
			port = 465
		case "none":
			port = 25
		default:
			port = 587
		}
	}
	opts := []gomail.Option{
		gomail.WithPort(port),
		gomail.WithTimeout(cfg.Timeout),
		gomail.WithTLSConfig(tlsConfig(cfg)),
	}
	if dial != nil {
		opts = append(opts, gomail.WithDialContextFunc(dial))
	}
	switch cfg.Security {
	case "tls":
		// Implicit TLS is done by our dialer (WithSSL is ignored with a custom dialer).
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if cfg.HELO != "" {
		opts = append(opts, gomail.WithHELO(cfg.HELO))
	}
	if cfg.Username != "" && cfg.Auth != "none" && cfg.Auth != "" {
		plain := cfg.Security == "none"
		var at gomail.SMTPAuthType
		switch cfg.Auth {
		case "login":
			at = gomail.SMTPAuthLogin
			if plain {
				at = gomail.SMTPAuthLoginNoEnc
			}
		case "cram-md5":
			at = gomail.SMTPAuthCramMD5
		default:
			at = gomail.SMTPAuthPlain
			if plain {
				at = gomail.SMTPAuthPlainNoEnc
			}
		}
		opts = append(opts, gomail.WithSMTPAuth(at), gomail.WithUsername(cfg.Username), gomail.WithPassword(cfg.Password))
	}
	return gomail.NewClient(cfg.Host, opts...)
}

func buildMsg(m *Mail) (*gomail.Msg, error) {
	msg := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	if err := msg.FromFormat(m.From.Name, m.From.Address); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if err := msg.AddToFormat(m.To.Name, m.To.Address); err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	if m.ReplyTo != "" {
		if err := msg.ReplyTo(m.ReplyTo); err != nil {
			return nil, fmt.Errorf("reply-to: %w", err)
		}
	}
	msg.Subject(m.Subject)
	if m.MessageID != "" {
		msg.SetMessageIDWithValue(strings.Trim(m.MessageID, "<>"))
	}
	if m.References != "" {
		msg.SetGenHeaderPreformatted(gomail.HeaderReferences, m.References)
	}
	if m.InReplyTo != "" {
		msg.SetGenHeaderPreformatted(gomail.HeaderInReplyTo, m.InReplyTo)
	}
	msg.SetGenHeader("Auto-Submitted", "auto-replied")
	msg.SetGenHeader("X-Auto-Response-Suppress", "All")
	for k, v := range m.Headers {
		msg.SetGenHeader(gomail.Header(k), v)
	}
	msg.SetDate()
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	return msg, nil
}

// SendFailure is a classified delivery error. Code is one of the OpenAPI
// EmailTestResult codes: auth_failed, tls_mismatch, connection_refused,
// timeout, dns, rejected, other.
type SendFailure struct {
	Code      string
	Permanent bool // SMTP 5xx on MAIL/RCPT/DATA: retrying will not help
	SMTPCode  int
	Err       error
}

func (e *SendFailure) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *SendFailure) Unwrap() error { return e.Err }

// IsPermanent reports whether err is a permanent SMTP rejection.
func IsPermanent(err error) bool {
	var f *SendFailure
	return errors.As(err, &f) && f.Permanent
}

// classify maps go-mail/net errors to readable codes.
func classify(err error) error {
	if err == nil {
		return nil
	}
	f := &SendFailure{Code: "other", Err: err}
	var se *gomail.SendError
	var dns *net.DNSError
	var tp *textproto.Error
	var recErr tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	var unk x509.UnknownAuthorityError
	msg := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &se):
		f.SMTPCode = se.ErrorCode()
		switch se.Reason {
		case gomail.ErrSMTPMailFrom, gomail.ErrSMTPRcptTo, gomail.ErrSMTPData, gomail.ErrSMTPDataClose:
			f.Code = "rejected"
		}
		if strings.Contains(msg, "535") || strings.Contains(msg, "530") {
			f.Code = "auth_failed"
		}
		f.Permanent = !se.IsTemp() && f.SMTPCode >= 500 && f.Code == "rejected"
	case errors.As(err, &dns):
		f.Code = "dns"
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		f.Code = "timeout"
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(msg, "connection refused"):
		f.Code = "connection_refused"
	case strings.Contains(msg, "smtp auth failed") || errors.Is(err, gosmtp.ErrUnencrypted) ||
		(errors.As(err, &tp) && (tp.Code == 535 || tp.Code == 534 || tp.Code == 530)) ||
		strings.Contains(msg, "server does not support smtp auth"):
		f.Code = "auth_failed"
	case errors.As(err, &recErr) || errors.As(err, &certErr) || errors.As(err, &unk) ||
		strings.Contains(msg, "starttls") || strings.Contains(msg, "tls:") || strings.Contains(msg, "x509") ||
		errors.Is(err, io.EOF) && strings.Contains(msg, "dial"):
		f.Code = "tls_mismatch"
	case errors.As(err, &tp):
		f.SMTPCode = tp.Code
		if tp.Code >= 500 {
			f.Code = "rejected"
		}
	}
	return f
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// FakeSender records mails in memory (tests). Fail, when set, decides the
// error for each mail.
type FakeSender struct {
	mu    sync.Mutex
	Sent  []*Mail
	Hosts []string // effective SMTP host of each attempt
	Fail  func(m *Mail) error
}

func (f *FakeSender) Send(_ context.Context, cfg *SMTPConfig, m *Mail) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Hosts = append(f.Hosts, cfg.Host)
	if f.Fail != nil {
		if err := f.Fail(m); err != nil {
			return err
		}
	}
	cp := *m
	f.Sent = append(f.Sent, &cp)
	return nil
}

// Mails returns a copy of the sent mails.
func (f *FakeSender) Mails() []*Mail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*Mail(nil), f.Sent...)
}
