// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMailpitTLSIntegration (NOT-001) sends through SMTPSender to a real Mailpit
// in its TLS modes and checks the message arrives through Mailpit's API. It runs
// only when the servers are given:
//
//	ST_TEST_SMTP_STARTTLS=smtp://127.0.0.1:1027?api=http://127.0.0.1:8027   (mailpit --smtp-require-starttls)
//	ST_TEST_SMTP_TLS=smtps://127.0.0.1:1028?api=http://127.0.0.1:8028       (mailpit --smtp-require-tls)
//
// Both with a self-signed --smtp-tls-cert/--smtp-tls-key and --smtp-auth-accept-any,
// so AUTH PLAIN runs inside TLS. A plain-text send to the TLS-only server must fail.
func TestMailpitTLSIntegration(t *testing.T) {
	cases := []struct{ env, security string }{{"ST_TEST_SMTP_STARTTLS", "starttls"}, {"ST_TEST_SMTP_TLS", "tls"}}
	ran := false
	for _, c := range cases {
		raw := os.Getenv(c.env)
		if raw == "" {
			continue
		}
		ran = true
		t.Run(c.security, func(t *testing.T) {
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			host, portS, _ := net.SplitHostPort(u.Host)
			port, _ := strconv.Atoi(portS)
			api := u.Query().Get("api")
			cfg := &SMTPConfig{Host: host, Port: port, Security: c.security, Username: "st", Password: "st-password",
				Auth: "plain", Timeout: 10 * time.Second, AllowInsecureTLS: true}
			subject := fmt.Sprintf("NOT-001 %s %d", c.security, time.Now().UnixNano())
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			m := &Mail{From: mail.Address{Address: "desk@example.test"}, To: mail.Address{Address: "tls@example.test"}, Subject: subject, Text: "over " + c.security}
			if err := (SMTPSender{}).Send(ctx, cfg, m); err != nil {
				t.Fatalf("send (%s): %v", c.security, err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				resp, err := http.Get(api + "/api/v1/search?query=" + url.QueryEscape(`subject:"`+subject+`"`))
				if err == nil {
					var r struct {
						MessagesCount int `json:"messages_count"`
					}
					_ = json.NewDecoder(resp.Body).Decode(&r)
					resp.Body.Close()
					if r.MessagesCount == 1 {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("%q not found in Mailpit (%s)", subject, api)
				}
				time.Sleep(200 * time.Millisecond)
			}
			if c.security == "tls" {
				plain := *cfg
				plain.Security, plain.Timeout = "none", 3*time.Second
				if err := (SMTPSender{}).Send(ctx, &plain, m); err == nil {
					t.Fatal("plain-text send to a TLS-only server succeeded")
				}
			}
		})
	}
	if !ran {
		t.Skip("ST_TEST_SMTP_STARTTLS / ST_TEST_SMTP_TLS not set")
	}
}
