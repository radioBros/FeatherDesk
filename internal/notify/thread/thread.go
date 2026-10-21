// SPDX-License-Identifier: AGPL-3.0-only

// Package thread holds the identifiers shared by outbound mail (notify) and
// inbound mail (mailin) so replies can be matched to tickets (events_spec §2,
// D-16 "Threading"):
//
//   - Message-ID of every outgoing mail: <{event_id}.{recipient_hash}@host>
//   - ticket thread id used in References/In-Reply-To: <ticket-{ticket_id}@host>
//   - plus-address reply token: local+t-<token>@domain (NOT-032)
//   - the localized "reply above this line" marker (NOT-026)
//
// It has no dependencies on the notify or mailin packages.
package thread

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/radioBros/SimpleTickets/internal/crypto"
)

// PurposePlusToken is the crypto.Box MAC purpose for reply tokens.
const PurposePlusToken = "plus-reply-token"

// HostFromURL returns the host part (no port) of a base URL such as
// ST_PUBLIC_URL, used as the Message-ID domain. Falls back to "localhost".
func HostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "localhost"
	}
	return strings.ToLower(u.Hostname())
}

// RecipientHash is the first 12 hex chars of sha256(lower(trimmed address)).
func RecipientHash(addr string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return hex.EncodeToString(sum[:])[:12]
}

// MessageID returns "<{eventID}.{RecipientHash(addr)}@host>". It is
// deterministic so a replayed event produces the same id (dedupe).
func MessageID(eventID, addr, host string) string {
	return "<" + eventID + "." + RecipientHash(addr) + "@" + host + ">"
}

// TicketThreadID returns "<ticket-{ticketID}@host>", the References root.
func TicketThreadID(ticketID, host string) string {
	return "<ticket-" + ticketID + "@" + host + ">"
}

// Ref is a parsed reference to one of our own message ids.
type Ref struct {
	EventID       string // set for <event.hash@host>
	RecipientHash string
	TicketID      string // set for <ticket-id@host>
}

// ParseOwnID parses a Message-ID / References entry that we generated for
// host. ok is false for foreign ids. Angle brackets are optional.
func ParseOwnID(id, host string) (Ref, bool) {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	local, domain, found := strings.Cut(id, "@")
	if !found || !strings.EqualFold(domain, host) {
		return Ref{}, false
	}
	if t, ok := strings.CutPrefix(local, "ticket-"); ok {
		if _, err := uuid.Parse(t); err == nil {
			return Ref{TicketID: t}, true
		}
		return Ref{}, false
	}
	ev, h, ok := strings.Cut(local, ".")
	if !ok || len(h) != 12 {
		return Ref{}, false
	}
	if _, err := uuid.Parse(ev); err != nil {
		return Ref{}, false
	}
	return Ref{EventID: ev, RecipientHash: h}, true
}

// SplitReferences splits a References / In-Reply-To header value into ids.
func SplitReferences(v string) []string {
	var out []string
	for {
		i := strings.IndexByte(v, '<')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(v[i:], '>')
		if j < 0 {
			return out
		}
		out = append(out, v[i:i+j+1])
		v = v[i+j+1:]
	}
}

// ---- plus-address reply token (NOT-032) ----

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

const (
	tokRecipientBytes = 4
	tokMACBytes       = 10 // 80-bit truncated HMAC-SHA256
	tokLen            = 16 + tokRecipientBytes + tokMACBytes
)

// PlusToken returns the lowercase base32 token for (ticketID, recipient).
// Layout: uuid(16) | sha256(lower(addr))[:4] | HMAC-SHA256(key, first 20)[:10].
func PlusToken(box *crypto.Box, ticketID, recipient string) (string, error) {
	u, err := uuid.Parse(ticketID)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, tokLen)
	buf = append(buf, u[:]...)
	rh := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(recipient))))
	buf = append(buf, rh[:tokRecipientBytes]...)
	mac := box.MAC(PurposePlusToken, buf)
	buf = append(buf, mac[:tokMACBytes]...)
	return strings.ToLower(b32.EncodeToString(buf)), nil
}

// VerifyPlusToken checks a token and returns the ticket id and the 8-hex
// recipient hash prefix. Any tampering (one flipped bit) yields ok=false.
func VerifyPlusToken(box *crypto.Box, token string) (ticketID, recipientPrefix string, ok bool) {
	raw, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(token)))
	if err != nil || len(raw) != tokLen {
		return "", "", false
	}
	body, mac := raw[:16+tokRecipientBytes], raw[16+tokRecipientBytes:]
	want := box.MAC(PurposePlusToken, body)
	if !hmac.Equal(want[:tokMACBytes], mac) {
		return "", "", false
	}
	var u uuid.UUID
	copy(u[:], body[:16])
	return u.String(), hex.EncodeToString(body[16:]), true
}

// RecipientMatches reports whether addr matches a recipient prefix returned
// by VerifyPlusToken.
func RecipientMatches(prefix, addr string) bool {
	rh := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return hex.EncodeToString(rh[:tokRecipientBytes]) == prefix
}

// PlusAddress builds "local+t-<token>@domain" from a base address.
func PlusAddress(base, token string) string {
	local, domain, ok := strings.Cut(base, "@")
	if !ok {
		return base
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	return local + "+t-" + token + "@" + domain
}

// TokenFromAddress extracts the token from "local+t-<token>@domain".
func TokenFromAddress(addr string) (string, bool) {
	local, _, ok := strings.Cut(strings.TrimSpace(addr), "@")
	if !ok {
		return "", false
	}
	i := strings.Index(local, "+t-")
	if i < 0 {
		return "", false
	}
	tok := local[i+3:]
	return tok, tok != ""
}

// ---- reply marker (NOT-026) ----

// ReplyMarkers is the localized marker line placed at the top of every
// outgoing ticket mail. Inbound mail is cut at the first marker found.
var ReplyMarkers = map[string]string{
	"en": "##- Reply above this line -##",
	"it": "##- Rispondi sopra questa riga -##",
	"de": "##- Bitte oberhalb dieser Zeile antworten -##",
	"fr": "##- Répondez au-dessus de cette ligne -##",
	"es": "##- Responda por encima de esta línea -##",
}

// ReplyMarker returns the marker for locale (English fallback).
func ReplyMarker(locale string) string {
	if m, ok := ReplyMarkers[locale]; ok {
		return m
	}
	return ReplyMarkers["en"]
}
