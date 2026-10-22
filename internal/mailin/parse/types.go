// SPDX-License-Identifier: AGPL-3.0-only

// Package parse is the stdlib-only inbound MIME parser (NOT-024), the
// HTML→Markdown converter (NOT-025) and quote stripping (NOT-026).
// It never imports github.com/emersion/go-message or golang.org/x/net/html.
package parse

import (
	"net/mail"
	"time"
)

// Message is a parsed RFC 5322 message.
type Message struct {
	Header     mail.Header // raw headers (canonical keys); encoded words NOT decoded
	MessageID  string      // "<…>" as found ("" when missing)
	InReplyTo  []string    // "<…>" ids
	References []string    // "<…>" ids
	Subject    string      // RFC 2047 decoded
	From       *mail.Address
	ReplyTo    []*mail.Address
	To         []*mail.Address
	Cc         []*mail.Address
	Date       time.Time // zero when missing/unparseable

	// Text is the plain-text body (UTF-8). When only HTML exists, Text is
	// HTMLToMarkdown(HTML) and FromHTML is true.
	Text     string
	HTML     string // the text/html body when present (UTF-8)
	FromHTML bool

	Attachments []Attachment
	// UnknownCharset is set when a part used a charset we cannot decode; the
	// bytes were kept raw.
	UnknownCharset bool
}

// Attachment is a non-body part (including inline images with a Content-ID).
type Attachment struct {
	Filename    string
	ContentType string // media type without params, lowercase
	ContentID   string // without angle brackets
	Inline      bool
	Data        []byte
}

// Options bound the parser.
type Options struct {
	MaxBytes       int64 // total raw size cap (0 = 50 MiB)
	MaxParts       int   // multipart parts cap (0 = 200)
	MaxDepth       int   // nesting cap (0 = 10)
	MaxAttachBytes int64 // per-attachment cap (0 = MaxBytes)
}

// Functions implemented in this package:
//
//	func Parse(raw []byte, opt Options) (*Message, error)
//	    Never panics on malformed input (FuzzParse).
//	func HTMLToMarkdown(html string) string
//	    Allowlist converter with a hand tokenizer; drops script/style/on*/javascript:.
//	func StripQuotes(text string) (body string, stripped bool)
//	    Cuts at a thread.ReplyMarkers line, else at known client quote
//	    patterns in en/it/de/fr/es, Outlook header blocks, ">" tails and "-- ".
//	func DecodeCharset(charset string, b []byte) (string, bool)
//	    utf-8, us-ascii, iso-8859-1, iso-8859-15, windows-1252; ok=false otherwise.
//	func AutoReply(h mail.Header) (reason string, auto bool)
//	    Loop/noise headers (NOT-029): Auto-Submitted≠no, Precedence bulk|list|junk,
//	    X-Autoreply, X-Autorespond, List-Id, Return-Path <>. reason is a short code.
