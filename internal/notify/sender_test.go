// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/notify/smtptest"
)

func testMail() *Mail {
	return &Mail{From: mail.Address{Name: "Acme", Address: "noreply@acme.test"}, To: mail.Address{Name: "Ada", Address: "ada@example.test"},
		Subject: "[#1042] Hello ü", Text: "plain body", HTML: "<p>html body</p>", MessageID: "<0192f.abc@help.acme.test>",
		References: "<ticket-1@help.acme.test>", InReplyTo: "<ticket-1@help.acme.test>", ReplyTo: "support+t-xyz@acme.test"}
}

func startSMTP(t *testing.T, opt smtptest.Options) *smtptest.Server {
	t.Helper()
	s, err := smtptest.Start(opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func cfgFor(s *smtptest.Server, security, user, pass, auth string) *SMTPConfig {
	return &SMTPConfig{Host: s.Host(), Port: s.Port(), Security: security, Username: user, Password: pass, Auth: auth, Timeout: 5 * time.Second, AllowInsecureTLS: true}
}

// TestSMTPModes: none, STARTTLS and SMTPS against the fake server, with each auth mechanism.
func TestSMTPModes(t *testing.T) {
	cases := []struct {
		name     string
		mode     smtptest.Mode
		security string
		auth     string
	}{
		{"plain-noauth", smtptest.Plain, "none", "none"},
		{"plain-login", smtptest.Plain, "none", "login"},
		{"starttls-plain", smtptest.StartTLS, "starttls", "plain"},
		{"starttls-cram", smtptest.StartTLS, "starttls", "cram-md5"},
		{"smtps-plain", smtptest.TLS, "tls", "plain"},
		{"smtps-login", smtptest.TLS, "tls", "login"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opt := smtptest.Options{Mode: c.mode, RequireTLS: c.mode == smtptest.StartTLS}
			user, pass := "", ""
			if c.auth != "none" {
				opt.Username, opt.Password = "mailer", "s3cret"
				user, pass = "mailer", "s3cret"
			}
			s := startSMTP(t, opt)
			if err := (SMTPSender{}).Send(context.Background(), cfgFor(s, c.security, user, pass, c.auth), testMail()); err != nil {
				t.Fatalf("send: %v (cmds %v)", err, s.Commands())
			}
			msgs := s.Messages()
			if len(msgs) != 1 {
				t.Fatalf("got %d messages", len(msgs))
			}
			h := msgs[0].Header()
			for k, want := range map[string]string{
				"Message-Id": "<0192f.abc@help.acme.test>", "References": "<ticket-1@help.acme.test>", "In-Reply-To": "<ticket-1@help.acme.test>",
				"Auto-Submitted": "auto-replied", "X-Auto-Response-Suppress": "All",
			} {
				if got := h.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			if !strings.Contains(h.Get("Reply-To"), "support+t-xyz@acme.test") {
				t.Errorf("Reply-To = %q", h.Get("Reply-To"))
			}
			body := string(msgs[0].Data)
			if !strings.Contains(body, "plain body") || !strings.Contains(body, "html body") || !strings.Contains(body, "multipart/alternative") {
				t.Errorf("body lacks text+html parts:\n%s", body)
			}
			if c.mode == smtptest.StartTLS && !slicesContains(s.Commands(), "STARTTLS") {
				t.Error("STARTTLS not used")
			}
		})
	}
}

func slicesContains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func codeOf(err error) string {
	if f, ok := err.(*SendFailure); ok {
		return f.Code
	}
	return "unclassified: " + err.Error()
}

// TestSendErrorCodes: readable codes for the admin test button.
func TestSendErrorCodes(t *testing.T) {
	ctx := context.Background()
	t.Run("auth_failed", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.StartTLS, Username: "u", Password: "right"})
		err := (SMTPSender{}).Send(ctx, cfgFor(s, "starttls", "u", "wrong", "plain"), testMail())
		if err == nil || codeOf(err) != "auth_failed" || IsPermanent(err) {
			t.Fatalf("err = %v (%s)", err, codeOf(err))
		}
	})
	t.Run("tls_mismatch_starttls_on_plain", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.Plain})
		err := (SMTPSender{}).Send(ctx, cfgFor(s, "starttls", "", "", "none"), testMail())
		if codeOf(err) != "tls_mismatch" {
			t.Fatalf("err = %v (%s)", err, codeOf(err))
		}
	})
	t.Run("tls_mismatch_smtps_on_plain", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.Plain})
		c := cfgFor(s, "tls", "", "", "none")
		c.Timeout = 2 * time.Second
		err := (SMTPSender{}).Send(ctx, c, testMail())
		if code := codeOf(err); code != "tls_mismatch" && code != "timeout" {
			t.Fatalf("err = %v (%s)", err, code)
		}
	})
	t.Run("tls_mismatch_untrusted_cert", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.TLS})
		c := cfgFor(s, "tls", "", "", "none")
		c.AllowInsecureTLS = false
		if code := codeOf((SMTPSender{}).Send(ctx, c, testMail())); code != "tls_mismatch" {
			t.Fatalf("code = %s", code)
		}
	})
	t.Run("connection_refused", func(t *testing.T) {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		port := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatalf("ln.Close: %v", err)
		}
		err := (SMTPSender{}).Send(ctx, &SMTPConfig{Host: "127.0.0.1", Port: port, Security: "none", Timeout: 2 * time.Second}, testMail())
		if codeOf(err) != "connection_refused" {
			t.Fatalf("err = %v (%s)", err, codeOf(err))
		}
	})
	t.Run("dns", func(t *testing.T) {
		err := (SMTPSender{}).Send(ctx, &SMTPConfig{Host: "no-such-host.invalid", Port: 25, Security: "none", Timeout: 3 * time.Second}, testMail())
		if code := codeOf(err); code != "dns" && code != "timeout" {
			t.Fatalf("err = %v (%s)", err, code)
		}
	})
	t.Run("rejected_550_permanent", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.Plain, Reply: func(stage, _ string) (int, string) {
			if stage == "RCPT" {
				return 550, "5.1.1 no such user"
			}
			return 0, ""
		}})
		err := (SMTPSender{}).Send(ctx, cfgFor(s, "none", "", "", "none"), testMail())
		if codeOf(err) != "rejected" || !IsPermanent(err) {
			t.Fatalf("err = %v (%s) permanent=%v", err, codeOf(err), IsPermanent(err))
		}
	})
	t.Run("temporary_421", func(t *testing.T) {
		s := startSMTP(t, smtptest.Options{Mode: smtptest.Plain, Reply: func(stage, _ string) (int, string) {
			if stage == "MAIL" {
				return 421, "4.3.2 try later"
			}
			return 0, ""
		}})
		err := (SMTPSender{}).Send(ctx, cfgFor(s, "none", "", "", "none"), testMail())
		if err == nil || IsPermanent(err) {
			t.Fatalf("err = %v permanent=%v", err, IsPermanent(err))
		}
	})
	t.Run("timeout", func(t *testing.T) {
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		defer func() {
			if err := ln.Close(); err != nil {
				t.Errorf("ln.Close: %v", err)
			}
		}()
		go func() {
			c, err := ln.Accept()
			if err == nil {
				time.Sleep(3 * time.Second)
				c.Close()
			}
		}()
		port := ln.Addr().(*net.TCPAddr).Port
		err := (SMTPSender{}).Send(ctx, &SMTPConfig{Host: "127.0.0.1", Port: port, Security: "none", Timeout: time.Second}, testMail())
		if codeOf(err) != "timeout" {
			t.Fatalf("err = %v (%s)", err, codeOf(err))
		}
	})
}
