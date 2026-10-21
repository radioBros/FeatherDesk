// SPDX-License-Identifier: AGPL-3.0-only

package parse

import (
	"go/parser"
	gotoken "go/token"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/radioBros/SimpleTickets/internal/notify/thread"
)

const reply = "Thanks, that fixed it.\n\nBest\nSam"

// TestStripQuotes: every pattern × en/it/de/fr/es, plus the no-pattern case
// (NOT-026, parsing side).
func TestStripQuotes(t *testing.T) {
	type tc struct {
		name, in, want string
		stripped       bool
	}
	var cases []tc
	// Our marker, in every locale, bare and inside a quote.
	for loc, m := range thread.ReplyMarkers {
		cases = append(cases,
			tc{"marker_" + loc, reply + "\n\n" + m + "\n\nOld ticket text", reply, true},
			tc{"marker_quoted_" + loc, reply + "\n\nOn Mon, Oct 19, 2026 Acme <s@x.com> wrote:\n> " + m + "\n> old", reply, true},
		)
	}
	attributions := map[string]string{
		"en": "On Mon, Oct 19, 2026 at 9:00 AM Acme Support <s@x.com> wrote:",
		"fr": "Le lun. 19 oct. 2026 à 09:00, Acme Support <s@x.com> a écrit :",
		"it": "Il giorno lun 19 ott 2026 alle ore 09:00 Acme Support <s@x.com> ha scritto:",
		"de": "Am Mo., 19. Okt. 2026 um 09:00 Uhr schrieb Acme Support <s@x.com>:",
		"es": "El lun, 19 oct 2026 a las 9:00, Acme Support (<s@x.com>) escribió:",
	}
	for loc, a := range attributions {
		cases = append(cases,
			tc{"attribution_" + loc, reply + "\n\n" + a + "\n> old text\n> more", reply, true},
			tc{"attribution_unquoted_" + loc, reply + "\n\n" + a + "\nold text not quoted", reply, true},
		)
		// Wrapped over two lines (Gmail).
		if i := strings.LastIndex(a, " "); i > 0 {
			cases = append(cases, tc{"attribution_wrapped_" + loc, reply + "\n\n" + a[:i] + "\n" + a[i+1:] + "\n> old", reply, true})
		}
	}
	outlook := map[string][2]string{
		"en": {"From: Acme Support <s@x.com>", "Sent: Monday, October 19, 2026 9:00 AM"},
		"fr": {"De : Acme Support <s@x.com>", "Envoyé : lundi 19 octobre 2026 09:00"},
		"it": {"Da: Acme Support <s@x.com>", "Inviato: lunedì 19 ottobre 2026 09:00"},
		"de": {"Von: Acme Support <s@x.com>", "Gesendet: Montag, 19. Oktober 2026 09:00"},
		"es": {"De: Acme Support <s@x.com>", "Enviado: lunes, 19 de octubre de 2026 9:00"},
	}
	for loc, h := range outlook {
		block := h[0] + "\n" + h[1] + "\nTo: Sam <sam@y.com>\nSubject: RE: [#1042] Printer\n\nOld text"
		cases = append(cases,
			tc{"outlook_" + loc, reply + "\n\n" + block, reply, true},
			tc{"outlook_underscore_" + loc, reply + "\n\n________________________________\n" + block, reply, true},
			tc{"outlook_bold_" + loc, reply + "\n\n**" + strings.Replace(h[0], ":", ":**", 1) + "\n**" + strings.Replace(h[1], ":", ":**", 1) + "\n\nOld", reply, true},
		)
	}
	originals := map[string]string{
		"en": "-----Original Message-----",
		"it": "-----Messaggio originale-----",
		"de": "-----Ursprüngliche Nachricht-----",
		"fr": "-----Message d'origine-----",
		"es": "-----Mensaje original-----",
	}
	for loc, o := range originals {
		cases = append(cases, tc{"original_" + loc, reply + "\n\n" + o + "\nFrom: x\nold", reply, true})
	}
	cases = append(cases,
		tc{"signature", reply + "\n-- \nSam Smith\nACME", reply, true},
		tc{"quoted_tail", reply + "\n\n> old line\n>\n> older", reply, true},
		tc{"interleaved_kept", "> question?\nanswer\n> question 2?\nanswer 2", "> question?\nanswer\n> question 2?\nanswer 2", false},
		tc{"no_pattern", reply + "\n\nP.S. on Monday we wrote: nothing", reply + "\n\nP.S. on Monday we wrote: nothing", false},
		tc{"only_quote_kept_whole", "> all quoted\n> text", "> all quoted\n> text", false},
		tc{"crlf", strings.ReplaceAll(reply+"\n\n"+attributions["en"]+"\n> x", "\n", "\r\n"), reply, true},
		tc{"trailing_ws", reply + "  \n\n\n", reply, false},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, stripped := StripQuotes(c.in)
			if got != c.want || stripped != c.stripped {
				t.Errorf("got %q (%v)\nwant %q (%v)", got, stripped, c.want, c.stripped)
			}
		})
	}
}

// TestAutoReply: loop/noise headers (NOT-029, header side).
func TestAutoReply(t *testing.T) {
	cases := []struct {
		h      map[string]string
		reason string
	}{
		{map[string]string{"Auto-Submitted": "auto-replied"}, "auto_submitted"},
		{map[string]string{"Auto-Submitted": "auto-generated"}, "auto_submitted"},
		{map[string]string{"Auto-Submitted": "no"}, ""},
		{map[string]string{"Auto-Submitted": "No; comment"}, ""},
		{map[string]string{"Precedence": "bulk"}, "precedence"},
		{map[string]string{"Precedence": "List"}, "precedence"},
		{map[string]string{"Precedence": "junk"}, "precedence"},
		{map[string]string{"Precedence": "first-class"}, ""},
		{map[string]string{"X-Autoreply": "yes"}, "x_autoreply"},
		{map[string]string{"X-Autorespond": "x"}, "x_autoreply"},
		{map[string]string{"List-Id": "<devs.lists.example.org>"}, "list_id"},
		{map[string]string{"Return-Path": "<>"}, "null_return_path"},
		{map[string]string{"Return-Path": "<bounce@x.com>"}, ""},
		{map[string]string{"Subject": "hi"}, ""},
	}
	for i, c := range cases {
		var raw strings.Builder
		for k, v := range c.h {
			raw.WriteString(k + ": " + v + "\n")
		}
		raw.WriteString("\nbody")
		m, err := mail.ReadMessage(strings.NewReader(raw.String()))
		if err != nil {
			t.Fatal(err)
		}
		reason, auto := AutoReply(m.Header)
		if reason != c.reason || auto != (c.reason != "") {
			t.Errorf("case %d %v: got %q %v want %q", i, c.h, reason, auto, c.reason)
		}
	}
	if r, ok := AutoReply(nil); ok || r != "" {
		t.Error("nil header")
	}
}

// TestNoForbiddenImports: the package never imports go-message or
// x/net/html directly (D-16, NOT-024/025 dependency gate).
func TestNoForbiddenImports(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fs := gotoken.NewFileSet()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fs, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if strings.HasPrefix(p, "github.com/emersion/go-message") || strings.HasPrefix(p, "golang.org/x/net/html") ||
				strings.HasPrefix(p, "github.com/microcosm-cc/bluemonday") || strings.HasPrefix(p, "github.com/yuin/goldmark") {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}
