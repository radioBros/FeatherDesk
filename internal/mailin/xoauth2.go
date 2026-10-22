// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"

	"github.com/emersion/go-imap/v2"
	"golang.org/x/oauth2"

	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// xoauth2Client is the XOAUTH2 SASL mechanism (Gmail, Microsoft 365). It
// satisfies go-sasl's Client interface structurally, so go-sasl stays an
// indirect dependency.
type xoauth2Client struct {
	user, token string
}

func (x *xoauth2Client) Start() (mech string, ir []byte, err error) {
	return "XOAUTH2", []byte("user=" + x.user + "\x01auth=Bearer " + x.token + "\x01\x01"), nil
}

// Next answers an error challenge with an empty response, as the mechanism
// requires; the server then fails the command with the real reason.
func (x *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	return []byte{}, nil
}

// oauthTokens refreshes the access token in memory from the sealed refresh
// token; a rotated refresh token is sealed and stored again.
type oauthTokens struct {
	m  *Module
	mb *mailbox

	mu  sync.Mutex
	src oauth2.TokenSource
}

func (m *Module) oauthFor(mb *mailbox) *oauthTokens { return &oauthTokens{m: m, mb: mb} }

func (o *oauthTokens) Token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.mb.IMAP
	if o.src == nil {
		refresh, err := o.m.d.Secrets.OpenString(settings.PurposeIMAPRefresh, c.OAuthRefreshEnc)
		if err != nil || refresh == "" {
			return "", errors.New("no refresh token configured")
		}
		secret := ""
		if c.OAuthClientSecret != "" {
			secret, _ = o.m.d.Secrets.OpenString(settings.PurposeIMAPRefresh, c.OAuthClientSecret)
		}
		cfg := &oauth2.Config{ClientID: c.OAuthClientID, ClientSecret: secret, Endpoint: oauth2.Endpoint{TokenURL: c.OAuthTokenURL}}
		o.src = oauth2.ReuseTokenSource(nil, cfg.TokenSource(context.WithoutCancel(ctx), &oauth2.Token{RefreshToken: refresh}))
	}
	tok, err := o.src.Token()
	if err != nil {
		return "", err
	}
	if tok.RefreshToken != "" {
		if cur, _ := o.m.d.Secrets.OpenString(settings.PurposeIMAPRefresh, c.OAuthRefreshEnc); cur != tok.RefreshToken {
			o.persistRefresh(ctx, tok.RefreshToken)
		}
	}
	return tok.AccessToken, nil
}

func (o *oauthTokens) persistRefresh(ctx context.Context, refresh string) {
	enc, err := o.m.d.Secrets.SealString(settings.PurposeIMAPRefresh, refresh)
	if err != nil {
		return
	}
	mb, err := o.m.getMailbox(ctx, o.mb.ID)
	if err != nil {
		return
	}
	mb.Stored.OAuthRefreshEnc = enc
	o.mb.Stored.OAuthRefreshEnc, o.mb.IMAP.OAuthRefreshEnc = enc, enc
	_ = o.m.d.DB.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inbound_mailboxes SET imap = ?, updated_at = ? WHERE id = ?`, store.MustJSON(mb.Stored), store.Now(), mb.ID)
		return err
	})
}

// ---- POST /admin/inbound/mailboxes/{id}/test ----

// TestResult is InboundTestResult.
type TestResult struct {
	OK           bool    `json:"ok"`
	Error        *string `json:"error"`
	MessageCount *int    `json:"message_count"`
}

// TestMailbox logs in, selects the folder and checks IDLE. Codes:
// auth_failed, tls_mismatch, connection_refused, dns, timeout,
// folder_missing, no_idle (ok=true: polling fallback works), other.
func (m *Module) TestMailbox(ctx context.Context, mb *mailbox) TestResult {
	if mb.Kind != "imap" {
		return TestResult{OK: true}
	}
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	s := m.NewIMAPSource(mb)
	fail := func(code string) TestResult { return TestResult{OK: false, Error: &code} }
	cl, err := s.dial(ctx, nil)
	if err != nil {
		return fail(classifyIMAPError(ctx, err))
	}
	defer func() { _ = cl.Close() }() // connectivity probe: close error is irrelevant
	sel, err := cl.Select(mb.IMAP.Folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return fail("folder_missing")
	}
	n := int(sel.NumMessages)
	res := TestResult{OK: true, MessageCount: &n}
	if !cl.Caps().Has(imap.CapIdle) {
		code := "no_idle"
		res.Error = &code
	}
	_ = cl.Logout().Wait()
	return res
}

func classifyIMAPError(ctx context.Context, err error) string {
	var ie *imap.Error
	var dnsErr *net.DNSError
	var recErr tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	var ne net.Error
	s := strings.ToLower(err.Error())
	switch {
	case errors.As(err, &ie) && (ie.Code == imap.ResponseCodeAuthenticationFailed || ie.Code == imap.ResponseCodeAuthorizationFailed || ie.Type == imap.StatusResponseTypeNo):
		return "auth_failed"
	case strings.Contains(s, "xoauth2 token"):
		return "auth_failed"
	case errors.As(err, &dnsErr):
		return "dns"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.As(err, &recErr), errors.As(err, &certErr), strings.Contains(s, "tls:"), strings.Contains(s, "x509"), strings.Contains(s, "starttls"):
		return "tls_mismatch"
	case errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil, errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	}
	return "other"
}
