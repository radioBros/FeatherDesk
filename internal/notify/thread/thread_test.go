// SPDX-License-Identifier: AGPL-3.0-only

package thread

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/radioBros/FeatherDesk/internal/crypto"
)

func box(t *testing.T) *crypto.Box {
	b, err := crypto.NewBox([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlusTokenRoundTrip(t *testing.T) {
	b := box(t)
	id := uuid.Must(uuid.NewV7()).String()
	tok, err := PlusToken(b, id, "Ada@Example.test")
	if err != nil {
		t.Fatal(err)
	}
	addr := PlusAddress("support@acme.test", tok)
	if len(strings.SplitN(addr, "@", 2)[0]) > 64 {
		t.Errorf("local part too long: %s", addr)
	}
	got, ok := TokenFromAddress(addr)
	if !ok || got != tok {
		t.Fatalf("TokenFromAddress(%q) = %q, %v", addr, got, ok)
	}
	tid, rh, ok := VerifyPlusToken(b, got)
	if !ok || tid != id {
		t.Fatalf("verify: %q %v", tid, ok)
	}
	if !RecipientMatches(rh, "ada@example.test") || RecipientMatches(rh, "eve@example.test") {
		t.Error("recipient prefix mismatch")
	}
}

func TestPlusTokenTamperRejected(t *testing.T) {
	b := box(t)
	tok, _ := PlusToken(b, uuid.Must(uuid.NewV7()).String(), "a@b.test")
	raw, err := b32.DecodeString(strings.ToUpper(tok))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(raw)*8; i++ {
		c := append([]byte(nil), raw...)
		c[i/8] ^= 1 << (i % 8)
		if _, _, ok := VerifyPlusToken(b, strings.ToLower(b32.EncodeToString(c))); ok {
			t.Fatalf("bit %d flip accepted", i)
		}
	}
	other, _ := crypto.NewBox([]byte(strings.Repeat("x", 32)))
	if _, _, ok := VerifyPlusToken(other, tok); ok {
		t.Error("token accepted under another key")
	}
	if _, _, ok := VerifyPlusToken(b, "not-a-token"); ok {
		t.Error("garbage accepted")
	}
}

func TestParseOwnID(t *testing.T) {
	ev := uuid.Must(uuid.NewV7()).String()
	tk := uuid.Must(uuid.NewV7()).String()
	mid := MessageID(ev, "A@b.test", "help.acme.test")
	if mid != MessageID(ev, "a@b.test ", "help.acme.test") {
		t.Error("MessageID not deterministic / case-insensitive")
	}
	r, ok := ParseOwnID(mid, "help.acme.test")
	if !ok || r.EventID != ev || r.RecipientHash != RecipientHash("a@b.test") {
		t.Fatalf("ParseOwnID(%s) = %+v %v", mid, r, ok)
	}
	r, ok = ParseOwnID(TicketThreadID(tk, "help.acme.test"), "HELP.acme.test")
	if !ok || r.TicketID != tk {
		t.Fatalf("thread id: %+v %v", r, ok)
	}
	for _, bad := range []string{"<" + ev + ".abc@help.acme.test>", mid[:len(mid)-1] + "x>", "<foo@gmail.com>", "<ticket-nope@help.acme.test>", ""} {
		if _, ok := ParseOwnID(bad, "help.acme.test"); ok {
			t.Errorf("ParseOwnID(%q) accepted", bad)
		}
	}
	refs := SplitReferences("<a@x> <b@y>\r\n <c@z>")
	if len(refs) != 3 || refs[2] != "<c@z>" {
		t.Errorf("SplitReferences = %v", refs)
	}
	if HostFromURL("https://Help.Acme.test:8443/x") != "help.acme.test" || HostFromURL("::") != "localhost" {
		t.Error("HostFromURL")
	}
	for l := range ReplyMarkers {
		if ReplyMarker(l) == "" {
			t.Error(l)
		}
	}
	if ReplyMarker("xx") != ReplyMarkers["en"] {
		t.Error("fallback")
	}
}
