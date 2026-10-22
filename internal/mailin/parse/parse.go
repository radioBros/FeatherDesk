// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/notify/thread"
)

// Errors returned by Parse.
var (
	ErrTooLarge = errors.New("parse: message exceeds size cap")
	ErrEmpty    = errors.New("parse: empty message")
)

const (
	defMaxBytes = 50 << 20
	defMaxParts = 200
	defMaxDepth = 10
)

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

// Parse parses a raw RFC 5322 message with the stdlib only. It never panics
// on malformed input; structural damage degrades to best-effort output.
func Parse(raw []byte, opt Options) (msg *Message, err error) {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = defMaxBytes
	}
	if opt.MaxParts <= 0 {
		opt.MaxParts = defMaxParts
	}
	if opt.MaxDepth <= 0 {
		opt.MaxDepth = defMaxDepth
	}
	if opt.MaxAttachBytes <= 0 {
		opt.MaxAttachBytes = opt.MaxBytes
	}
	if int64(len(raw)) > opt.MaxBytes {
		return nil, ErrTooLarge
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrEmpty
	}
	defer func() {
		if r := recover(); r != nil { // last resort; bounds are checked explicitly
			msg, err = nil, fmt.Errorf("parse: malformed message: %v", r)
		}
	}()
	mm, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse: headers: %w", err)
	}
	m := &Message{Header: mm.Header}
	h := mm.Header
	m.MessageID = strings.TrimSpace(h.Get("Message-Id"))
	m.InReplyTo = thread.SplitReferences(h.Get("In-Reply-To"))
	m.References = thread.SplitReferences(strings.Join(h["References"], " "))
	m.Subject = decodeHeader(h.Get("Subject"))
	if froms := parseAddrs(h.Get("From")); len(froms) > 0 {
		m.From = froms[0]
	}
	m.ReplyTo = parseAddrs(h.Get("Reply-To"))
	m.To = parseAddrs(strings.Join(h["To"], ", "))
	m.Cc = parseAddrs(strings.Join(h["Cc"], ", "))
	if d, err := mail.ParseDate(h.Get("Date")); err == nil {
		m.Date = d.UTC()
	}
	body, _ := io.ReadAll(io.LimitReader(mm.Body, opt.MaxBytes))

	w := &walker{opt: opt, m: m}
	w.part(map[string][]string(h), body, 0, "text/plain")
	if len(w.texts) > 0 {
		m.Text = strings.Join(w.texts, "\n\n")
	}
	if len(w.htmls) > 0 {
		m.HTML = w.htmls[0]
	}
	if strings.TrimSpace(m.Text) == "" && m.HTML != "" {
		m.Text = HTMLToMarkdown(m.HTML)
		m.FromHTML = true
	}
	m.Text = strings.TrimRight(m.Text, " \t\r\n")
	return m, nil
}

type walker struct {
	opt   Options
	m     *Message
	parts int
	texts []string
	htmls []string
}

func hget(h map[string][]string, k string) string {
	if v := h[k]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// part handles one MIME entity. def is the default content type (text/plain,
// or message/rfc822 inside multipart/digest).
func (w *walker) part(h map[string][]string, raw []byte, depth int, def string) {
	w.parts++
	if w.parts > w.opt.MaxParts || depth > w.opt.MaxDepth {
		return
	}
	ctRaw := hget(h, "Content-Type")
	mediatype, params := def, map[string]string{}
	if strings.TrimSpace(ctRaw) != "" {
		mt, p, err := mime.ParseMediaType(ctRaw)
		if mt != "" {
			mediatype = strings.ToLower(mt)
		}
		if p != nil {
			params = p
		}
		_ = err
	}
	if strings.HasPrefix(mediatype, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			// No boundary: treat the body as text.
			w.leaf(h, "text/plain", params, raw)
			return
		}
		childDef := "text/plain"
		if mediatype == "multipart/digest" {
			childDef = "message/rfc822"
		}
		mr := multipart.NewReader(bytes.NewReader(raw), boundary)
		for {
			p, err := mr.NextRawPart()
			if err != nil {
				return
			}
			data, err := io.ReadAll(io.LimitReader(p, w.opt.MaxBytes))
			ph := map[string][]string(p.Header)
			_ = p.Close() // in-memory part reader: Close never fails
			if err != nil && len(data) == 0 {
				return
			}
			w.part(ph, data, depth+1, childDef)
			if w.parts > w.opt.MaxParts {
				return
			}
		}
	}
	w.leaf(h, mediatype, params, raw)
}

func (w *walker) leaf(h map[string][]string, mediatype string, params map[string]string, raw []byte) {
	data := decodeCTE(hget(h, "Content-Transfer-Encoding"), raw)
	disp, dparams := "", map[string]string{}
	if d := hget(h, "Content-Disposition"); strings.TrimSpace(d) != "" {
		dt, p, _ := mime.ParseMediaType(d)
		disp = strings.ToLower(dt)
		if p != nil {
			dparams = p
		}
		if disp == "" {
			// Unparseable disposition: look at the raw token.
			tok, _, _ := strings.Cut(d, ";")
			disp = strings.ToLower(strings.TrimSpace(tok))
		}
	}
	filename := dparams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	filename = sanitizeFilename(decodeHeader(filename))
	cid := strings.TrimSpace(hget(h, "Content-Id"))
	cid = strings.TrimSuffix(strings.TrimPrefix(cid, "<"), ">")

	isText := mediatype == "text/plain" || mediatype == "text/html"
	if isText && disp != "attachment" && filename == "" {
		s, ok := DecodeCharset(params["charset"], data)
		if !ok {
			w.m.UnknownCharset = true
		}
		s = strings.ReplaceAll(s, "\r\n", "\n")
		if mediatype == "text/plain" {
			if strings.EqualFold(params["format"], "flowed") {
				s = unflow(s, strings.EqualFold(params["delsp"], "yes"))
			}
			w.texts = append(w.texts, s)
		} else {
			w.htmls = append(w.htmls, s)
		}
		return
	}
	if int64(len(data)) > w.opt.MaxAttachBytes {
		return // over the per-attachment cap: dropped (original.eml keeps it)
	}
	if filename == "" {
		filename = defaultName(mediatype, len(w.m.Attachments)+1)
	}
	w.m.Attachments = append(w.m.Attachments, Attachment{
		Filename:    filename,
		ContentType: mediatype,
		ContentID:   cid,
		Inline:      disp == "inline" || (disp == "" && cid != ""),
		Data:        data,
	})
}

func defaultName(mediatype string, n int) string {
	ext := ".bin"
	switch mediatype {
	case "message/rfc822":
		ext = ".eml"
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "text/plain":
		ext = ".txt"
	case "text/html":
		ext = ".html"
	case "application/pdf":
		ext = ".pdf"
	}
	return fmt.Sprintf("attachment-%d%s", n, ext)
}

func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 255 {
		s = s[:255]
	}
	if s == "." || s == ".." {
		return ""
	}
	return s
}

func decodeCTE(cte string, raw []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(cte)) {
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil && len(out) == 0 {
			return raw
		}
		return out
	case "base64":
		clean := make([]byte, 0, len(raw))
		for _, c := range raw {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
				clean = append(clean, c)
			}
		}
		if len(clean)%4 == 1 { // a lone trailing sextet carries no full byte
			clean = clean[:len(clean)-1]
		}
		out := make([]byte, base64.RawStdEncoding.DecodedLen(len(clean)))
		n, _ := base64.RawStdEncoding.Decode(out, clean)
		return out[:max(n, 0)]
	}
	return raw
}

// unflow joins RFC 3676 format=flowed soft line breaks.
func unflow(s string, delsp bool) string {
	lines := strings.Split(s, "\n")
	var out strings.Builder
	for i, l := range lines {
		if l != "-- " && strings.HasSuffix(l, " ") && i < len(lines)-1 {
			if delsp {
				l = l[:len(l)-1]
			}
			out.WriteString(l)
			continue
		}
		out.WriteString(l)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func decodeHeader(s string) string {
	if s == "" {
		return ""
	}
	d, err := wordDecoder.DecodeHeader(s)
	if err != nil {
		return strings.ToValidUTF8(s, "�")
	}
	return strings.ToValidUTF8(d, "�")
}

var addrParser = &mail.AddressParser{WordDecoder: wordDecoder}

// parseAddrs parses an address list leniently: a broken list falls back to
// per-item parsing and finally to a bare "x@y" scan.
func parseAddrs(s string) []*mail.Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if l, err := addrParser.ParseList(s); err == nil {
		return l
	}
	var out []*mail.Address
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if a, err := addrParser.Parse(item); err == nil {
			out = append(out, a)
			continue
		}
		if a := bareAddr(item); a != nil {
			out = append(out, a)
		}
	}
	return out
}

func bareAddr(s string) *mail.Address {
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j > 0 {
			a := strings.TrimSpace(s[i+1 : i+j])
			if strings.Count(a, "@") == 1 && !strings.ContainsAny(a, " \t") {
				return &mail.Address{Name: strings.Trim(strings.TrimSpace(decodeHeader(s[:i])), `"`), Address: a}
			}
		}
	}
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, "<>\"'(),;")
		if strings.Count(f, "@") == 1 && !strings.HasPrefix(f, "@") && !strings.HasSuffix(f, "@") {
			return &mail.Address{Address: f}
		}
	}
	return nil
}
