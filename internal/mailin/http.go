// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// SignatureHeader carries the optional inbound HMAC (same scheme as outbound
// webhooks: t=<unix>,v1=<hex hmac_sha256(secret, t + "." + body)>).
const SignatureHeader = "X-SimpleTickets-Inbound-Signature"

// Mount registers the inbound webhook (Public) and the admin API.
func (m *Module) Mount(g server.Groups) {
	mw := []func(http.Handler) http.Handler{server.MaxBody(m.maxBytes)}
	if m.d.Server != nil {
		mw = append(mw, m.d.Server.Limit("inbound-mime", 5, 50))
	}
	g.Public.With(mw...).Post("/inbound/mime/{mailbox_token}", m.handleInbound)

	a := g.Admin
	a.Get("/inbound/mailboxes", m.handleListMailboxes)
	a.Post("/inbound/mailboxes", m.handleCreateMailbox)
	a.Get("/inbound/mailboxes/{id}", m.handleGetMailbox)
	a.Patch("/inbound/mailboxes/{id}", m.handlePatchMailbox)
	a.Delete("/inbound/mailboxes/{id}", m.handleDeleteMailbox)
	a.Post("/inbound/mailboxes/{id}/test", m.handleTestMailbox)
	a.Get("/inbound/messages", m.handleListMessages)
	a.Post("/inbound/messages/{id}/retry", m.handleRetryMessage)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// handleInbound: POST /api/v1/inbound/mime/{mailbox_token} (NOT-023).
func (m *Module) handleInbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h := tokenHash(chi.URLParam(r, "mailbox_token"))
	var id, stored string
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id, webhook_token_hash FROM inbound_mailboxes WHERE webhook_token_hash = ? AND active AND kind = 'webhook'`, h).Scan(&id, &stored)
	if err != nil || subtle.ConstantTimeCompare([]byte(h), []byte(stored)) != 1 {
		if err != nil && !errors.Is(store.NotFound(err), store.ErrNotFound) {
			problems.Write(w, r, err)
			return
		}
		problems.Write(w, r, problems.NotFound())
		return
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "message/rfc822" && ct != "application/octet-stream" {
		problems.Write(w, r, problems.UnsupportedMedia(ct))
		return
	}
	if r.ContentLength > m.maxBytes {
		problems.Write(w, r, problems.PayloadTooLarge())
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, m.maxBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			problems.Write(w, r, problems.PayloadTooLarge())
			return
		}
		problems.Write(w, r, problems.BadRequest("cannot read body"))
		return
	}
	if int64(len(raw)) > m.maxBytes {
		problems.Write(w, r, problems.PayloadTooLarge())
		return
	}
	mb, err := m.getMailbox(ctx, id)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if mb.HMACSecretEnc != "" {
		secret, err := m.d.Secrets.OpenString(settings.PurposeWebhook, mb.HMACSecretEnc)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		if !VerifySignature(secret, r.Header.Get(SignatureHeader), raw, m.now()) {
			problems.Write(w, r, problems.Unauthenticated())
			return
		}
	}
	if len(raw) == 0 {
		problems.Write(w, r, problems.Validation(problems.Field("body", "required", "empty message")))
		return
	}
	if _, err := m.Receive(ctx, mb, raw); err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// Sign returns the signature header value for body at time t.
func Sign(secret string, body []byte, t time.Time) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a "t=…,v1=…" header within ±300 s of now.
func VerifySignature(secret, header string, body []byte, now time.Time) bool {
	var ts string
	var sigs []string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return false
	}
	if d := now.Unix() - sec; d > 300 || d < -300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}
