// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func corpus(t testing.TB) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob("testdata/mail/*.eml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".eml")] = b
	}
	return out
}

func summary(m *Message) string {
	var b strings.Builder
	from := ""
	if m.From != nil {
		from = fmt.Sprintf("%q <%s>", m.From.Name, m.From.Address)
	}
	fmt.Fprintf(&b, "subject: %s\nfrom: %s\nmessage_id: %s\nin_reply_to: %v\nreferences: %v\n", m.Subject, from, m.MessageID, m.InReplyTo, m.References)
	var to, cc []string
	for _, a := range m.To {
		to = append(to, a.Address)
	}
	for _, a := range m.Cc {
		cc = append(cc, fmt.Sprintf("%q <%s>", a.Name, a.Address))
	}
	date := ""
	if !m.Date.IsZero() {
		date = m.Date.Format("2006-01-02T15:04:05Z")
	}
	fmt.Fprintf(&b, "to: %v\ncc: %v\ndate: %s\nfrom_html: %t\nhas_html: %t\nunknown_charset: %t\nattachments:\n", to, cc, date, m.FromHTML, m.HTML != "", m.UnknownCharset)
	for _, a := range m.Attachments {
		fmt.Fprintf(&b, "- %s (%s, %d bytes, cid=%q, inline=%t)\n", a.Filename, a.ContentType, len(bytes.ReplaceAll(a.Data, []byte("\r\n"), []byte("\n"))), a.ContentID, a.Inline)
	}
	body, stripped := StripQuotes(m.Text)
	fmt.Fprintf(&b, "--- text ---\n%s\n--- stripped (%t) ---\n%s\n", m.Text, stripped, body)
	return b.String()
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// TestParseCorpus: golden subject, from, body text and attachment list for
// every fixture, identical for LF and CRLF line endings (NOT-024).
func TestParseCorpus(t *testing.T) {
	for name, raw := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			m, err := Parse(raw, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := summary(m)
			golden(t, filepath.Join("testdata", "golden", name+".txt"), got)
			crlf := bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
			m2, err := Parse(crlf, Options{})
			if err != nil {
				t.Fatalf("crlf: %v", err)
			}
			if got2 := summary(m2); got2 != got {
				t.Errorf("CRLF result differs:\n%s\nvs\n%s", got2, got)
			}
		})
	}
}

// TestParseSpecifics pins the behaviours the golden files encode.
func TestParseSpecifics(t *testing.T) {
	c := corpus(t)
	m, _ := Parse(c["gmail_alternative"], Options{})
	if m.Subject != "Re: [#1042] Printer not working – still" || m.From.Name != "Jürgen Müller" || m.HTML == "" || m.FromHTML {
		t.Errorf("gmail: %+v", m)
	}
	if len(m.References) != 2 || len(m.InReplyTo) != 1 || len(m.Cc) != 2 || m.Cc[1].Name != "Bob, Jr." {
		t.Errorf("gmail refs/cc: %v %v %v", m.References, m.InReplyTo, m.Cc)
	}
	m, _ = Parse(c["windows1252"], Options{})
	if m.Subject != "Facture € 100 – “urgent”" || m.From.Name != "René €uro" || !strings.Contains(m.Text, "€ 10 “quoted” – naïve café") {
		t.Errorf("windows-1252: %q %q %q", m.Subject, m.From.Name, m.Text)
	}
	m, _ = Parse(c["outlook_html_only"], Options{})
	if !m.FromHTML || strings.Contains(m.Text, "alert") || strings.Contains(m.Text, "javascript") {
		t.Errorf("outlook: %q", m.Text)
	}
	m, _ = Parse(c["related_inline"], Options{})
	if len(m.Attachments) != 2 || m.Attachments[0].ContentID != "logo123@example.com" || !m.Attachments[0].Inline || m.Attachments[1].Filename != "réport.csv" {
		t.Errorf("related: %+v", m.Attachments)
	}
	m, _ = Parse(c["rfc2231_attachment"], Options{})
	if len(m.Attachments) != 1 || m.Attachments[0].Filename != "résumé final.pdf" {
		t.Errorf("rfc2231: %+v", m.Attachments)
	}
	m, _ = Parse(c["unknown_charset"], Options{})
	if !m.UnknownCharset || m.Text != "plain ascii body" {
		t.Errorf("unknown charset: %+v", m)
	}
	m, _ = Parse(c["list_digest"], Options{})
	if r, ok := AutoReply(m.Header); !ok || r != "precedence" {
		t.Errorf("digest auto: %q %v", r, ok)
	}
	if len(m.Attachments) != 2 || m.Attachments[0].ContentType != "message/rfc822" {
		t.Errorf("digest parts: %+v", m.Attachments)
	}
}

func TestParseCaps(t *testing.T) {
	raw := corpus(t)["apple_mail"]
	if _, err := Parse(raw, Options{MaxBytes: 100}); err != ErrTooLarge {
		t.Errorf("size cap: %v", err)
	}
	m, err := Parse(raw, Options{MaxAttachBytes: 10})
	if err != nil || len(m.Attachments) != 0 {
		t.Errorf("attachment cap: %v %+v", err, m)
	}
	// Deep nesting stops at MaxDepth without error.
	var b strings.Builder
	b.WriteString("From: a@b.c\nContent-Type: multipart/mixed; boundary=b0\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "--b%d\nContent-Type: multipart/mixed; boundary=b%d\n\n", i, i+1)
	}
	b.WriteString("--b30\nContent-Type: text/plain\n\ndeep\n")
	m, err = Parse([]byte(b.String()), Options{MaxDepth: 5})
	if err != nil || m.Text != "" {
		t.Errorf("depth cap: %v %q", err, m.Text)
	}
	m, err = Parse([]byte(b.String()), Options{MaxDepth: 40})
	if err != nil || m.Text != "deep" {
		t.Errorf("deep ok: %v %q", err, m.Text)
	}
}

// TestParseMalformed: truncated and garbage inputs never panic (NOT-024).
func TestParseMalformed(t *testing.T) {
	inputs := [][]byte{
		nil, []byte(""), []byte("\x00\x01\x02"), []byte("garbage without headers"),
		[]byte("From: x\nContent-Type: multipart/mixed; boundary=\n\n--\n"),
		[]byte("From: x\nContent-Type: multipart/mixed\n\nno boundary"),
		[]byte("From: x\nContent-Type: text/plain; charset=\"\nContent-Transfer-Encoding: base64\n\n!!!@@@"),
		[]byte("From: =?utf-8?B?####?= <\nSubject: =?bogus?Q?x?=\n\nbody"),
		[]byte("Content-Type: multipart/alternative; boundary=x\n\n--x\nContent-Type: text/html\n\n<a href=\"javascript:1\"><<<<\n--x"),
		[]byte("Content-Type: text/plain\nContent-Transfer-Encoding: quoted-printable\n\n=ZZ=\n=4"),
	}
	for _, raw := range corpus(t) {
		for _, cut := range []int{1, 10, len(raw) / 3, len(raw) / 2, len(raw) - 5} {
			if cut > 0 && cut < len(raw) {
				inputs = append(inputs, raw[:cut])
			}
		}
	}
	for i, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("input %d panicked: %v", i, r)
				}
			}()
			_, _ = Parse(in, Options{})
		}()
	}
}

func FuzzParse(f *testing.F) {
	for _, raw := range corpus(f) {
		f.Add(raw)
	}
	f.Add([]byte("From: a@b\n\nx"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		m, err := Parse(raw, Options{MaxBytes: 1 << 20})
		if err == nil && m == nil {
			t.Fatal("nil message without error")
		}
		if err != nil && strings.Contains(err.Error(), "malformed message") {
			t.Fatalf("recovered panic: %v", err) // bounds must be checked, not recovered
		}
		if m != nil {
			_, _ = StripQuotes(m.Text)
			_ = HTMLToMarkdown(m.HTML)
		}
	})
}

func TestDecodeCharset(t *testing.T) {
	cases := []struct {
		cs   string
		in   []byte
		want string
		ok   bool
	}{
		{"windows-1252", []byte{0x80, 0x93, 'x', 0x94, 0xe9}, "€“x”é", true},
		{"ISO-8859-15", []byte{0xa4, 0xbd, 0xe9}, "€œé", true},
		{"latin1", []byte{0xa4, 0xe9}, "¤é", true},
		{"US-ASCII", []byte("abc"), "abc", true},
		{"utf-8", []byte("caf\xc3\xa9"), "café", true},
		{"", []byte("x\xffy"), "x�y", true},
		{"koi8-r", []byte("abc"), "abc", false},
	}
	for _, c := range cases {
		got, ok := DecodeCharset(c.cs, c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q %v want %q %v", c.cs, got, ok, c.want, c.ok)
		}
	}
}
