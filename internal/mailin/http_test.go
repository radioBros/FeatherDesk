// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

func TestInboundWebhookWrongToken404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "w@client.example.org", "")
		id, tok := e.webhookMailbox("", "")
		raw := mkMail("w@client.example.org", "x", "x", nil)
		for _, bad := range []string{"nope", tok + "x", strings.ToUpper(tok)} {
			r, body := e.do("POST", "/api/v1/inbound/mime/"+bad, "", raw)
			if r.StatusCode != 404 || !strings.Contains(r.Header.Get("Content-Type"), "problem+json") {
				t.Fatalf("token %q → %d %s", bad, r.StatusCode, body)
			}
		}
		// inactive mailbox → 404 as well (no enumeration)
		if r, d := e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+id, "staff:admin", map[string]any{"active": false}); r.StatusCode != 200 {
			t.Fatalf("deactivate %d %s", r.StatusCode, d)
		}
		if r, _ := e.do("POST", "/api/v1/inbound/mime/"+tok, "", raw); r.StatusCode != 404 {
			t.Fatalf("inactive → %d", r.StatusCode)
		}
		e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+id, "staff:admin", map[string]any{"active": true})
		if r, d := e.do("POST", "/api/v1/inbound/mime/"+tok, "", raw); r.StatusCode != 202 || !strings.Contains(string(d), `"ok":true`) {
			t.Fatalf("good token → %d %s", r.StatusCode, d)
		}
		// wrong content type
		req := httptest.NewRequest("POST", "/api/v1/inbound/mime/"+tok, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 415 {
			t.Fatalf("json body → %d", rec.Code)
		}
	})
}

func TestInboundWebhookHMAC(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "h@client.example.org", "")
		_, tok := e.webhookMailbox("", "s3cret")
		raw := mkMail("h@client.example.org", "x", "x", nil)
		post := func(sig string) int {
			req := httptest.NewRequest("POST", "/api/v1/inbound/mime/"+tok, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "message/rfc822")
			if sig != "" {
				req.Header.Set(SignatureHeader, sig)
			}
			rec := httptest.NewRecorder()
			e.srv.Handler().ServeHTTP(rec, req)
			return rec.Code
		}
		now := time.Now()
		if c := post(""); c != 401 {
			t.Fatalf("missing signature → %d", c)
		}
		if c := post(Sign("wrong", raw, now)); c != 401 {
			t.Fatalf("bad signature → %d", c)
		}
		if c := post(Sign("s3cret", raw, now.Add(-10*time.Minute))); c != 401 {
			t.Fatalf("stale signature → %d", c)
		}
		if c := post(Sign("s3cret", raw, now)); c != 202 {
			t.Fatalf("good signature → %d", c)
		}
		// Senders written for SimpleTickets 1.0.0 use the old header name.
		raw = mkMail("h@client.example.org", "y", "y", nil)
		req := httptest.NewRequest("POST", "/api/v1/inbound/mime/"+tok, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "message/rfc822")
		req.Header.Set("X-SimpleTickets-Inbound-Signature", Sign("s3cret", raw, now))
		rec := httptest.NewRecorder()
		e.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 202 {
			t.Fatalf("legacy signature header → %d", rec.Code)
		}
		// the secret is sealed at rest
		var enc string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT hmac_secret_enc FROM inbound_mailboxes`).Scan(&enc)
		if !crypto.IsSealed(enc) || strings.Contains(enc, "s3cret") {
			t.Fatalf("hmac secret not sealed: %q", enc)
		}
	})
}

type countingReader struct {
	n   int64
	max int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.n >= c.max {
		return 0, io.EOF
	}
	if int64(len(p)) > c.max-c.n {
		p = p[:c.max-c.n]
	}
	for i := range p {
		p[i] = 'a'
	}
	c.n += int64(len(p))
	return len(p), nil
}

func TestInboundWebhookOversize413BeforeFullRead(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, map[string]string{"FD_MAX_ATTACHMENT_MB": "1"}) // cap 10 MiB
		_, tok := e.webhookMailbox("", "")
		limit := int64(10 << 20)
		// declared length over the cap: rejected without reading
		cr := &countingReader{max: 50 << 20}
		req := httptest.NewRequest("POST", "/api/v1/inbound/mime/"+tok, cr)
		req.ContentLength = 50 << 20
		req.Header.Set("Content-Type", "message/rfc822")
		rec := httptest.NewRecorder()
		e.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 413 || cr.n != 0 {
			t.Fatalf("declared oversize → %d after reading %d bytes", rec.Code, cr.n)
		}
		// chunked (unknown length): stops reading just past the cap
		cr = &countingReader{max: 50 << 20}
		req = httptest.NewRequest("POST", "/api/v1/inbound/mime/"+tok, cr)
		req.ContentLength = -1
		req.Header.Set("Content-Type", "message/rfc822")
		rec = httptest.NewRecorder()
		e.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 413 || cr.n > limit+64<<10 {
			t.Fatalf("streamed oversize → %d after reading %d bytes (cap %d)", rec.Code, cr.n, limit)
		}
		var n int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM inbound_messages`).Scan(&n)
		if n != 0 {
			t.Fatalf("oversize message recorded")
		}
	})
}

func TestWebhookTokenShownOnce(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		id, tok := e.webhookMailbox("", "")
		if tok == "" {
			t.Fatal("no webhook_url on create")
		}
		for _, p := range []string{"/api/v1/admin/inbound/mailboxes/" + id, "/api/v1/admin/inbound/mailboxes"} {
			r, d := e.do("GET", p, "staff:admin", nil)
			if r.StatusCode != 200 || strings.Contains(string(d), tok) {
				t.Fatalf("%s leaks the token: %d %s", p, r.StatusCode, d)
			}
		}
		var h string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT webhook_token_hash FROM inbound_mailboxes WHERE id = ?`, id).Scan(&h)
		if h != tokenHash(tok) {
			t.Fatal("token not stored as sha256")
		}
	})
}

func TestMailboxPasswordWriteOnly(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{
			"name": "Box", "kind": "imap", "address": "box@example.test",
			"imap": map[string]any{"host": "imap.example.test", "port": 993, "security": "tls", "username": "box", "password": "hunter2-secret", "refresh_token": "rt-secret"},
		})
		if r.StatusCode != 201 {
			t.Fatalf("%d %s", r.StatusCode, d)
		}
		var mb mailboxDTO
		_ = json.Unmarshal(d, &mb)
		if strings.Contains(string(d), "hunter2") || mb.IMAP == nil || !mb.IMAP.PasswordSet || mb.WebhookURL != nil {
			t.Fatalf("create response: %s", d)
		}
		_, d = e.do("GET", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", nil)
		if strings.Contains(string(d), "hunter2") || strings.Contains(string(d), "rt-secret") || strings.Contains(string(d), "password_enc") {
			t.Fatalf("GET leaks: %s", d)
		}
		var raw string
		_ = db.Read().QueryRowContext(context.Background(), `SELECT imap FROM inbound_mailboxes WHERE id = ?`, mb.ID).Scan(&raw)
		if strings.Contains(raw, "hunter2") || strings.Contains(raw, "rt-secret") || !strings.Contains(raw, "enc:v1:") {
			t.Fatalf("DB holds plaintext: %s", raw)
		}
		// empty password on PATCH keeps the stored one
		e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", map[string]any{"imap": map[string]any{"host": "imap.example.test", "port": 993, "security": "tls", "username": "box", "password": ""}})
		pw, err := e.m.imapPassword(e.mailbox(mb.ID))
		if err != nil || pw != "hunter2-secret" {
			t.Fatalf("password after empty PATCH: %q %v", pw, err)
		}
		// audit rows: create + update
		var n int
		_ = db.Read().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE target_id = ?`, mb.ID).Scan(&n)
		if n != 2 {
			t.Fatalf("audit rows = %d", n)
		}
		// delete
		if r, _ := e.do("DELETE", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", nil); r.StatusCode != 204 {
			t.Fatalf("delete %d", r.StatusCode)
		}
		if r, _ := e.do("GET", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", nil); r.StatusCode != 404 {
			t.Fatalf("get after delete %d", r.StatusCode)
		}
	})
}

func TestMailboxValidation(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		r, d := e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{"name": "x", "kind": "imap", "address": "bad"})
		if r.StatusCode != 422 {
			t.Fatalf("%d %s", r.StatusCode, d)
		}
		r, d = e.do("POST", "/api/v1/admin/inbound/mailboxes", "staff:admin", map[string]any{"name": "x", "kind": "webhook", "address": "a@example.test", "team_id": store.NewID()})
		if r.StatusCode != 422 || !strings.Contains(string(d), "team_id") {
			t.Fatalf("unknown team %d %s", r.StatusCode, d)
		}
	})
}

func TestMailboxNonAdmin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		for who, want := range map[string]int{"customer:requester": 404, "staff:agent": 403, "staff:supervisor": 403, "": 401} {
			for _, p := range []string{"/api/v1/admin/inbound/mailboxes", "/api/v1/admin/inbound/messages"} {
				if r, _ := e.do("GET", p, who, nil); r.StatusCode != want {
					t.Errorf("%q GET %s → %d, want %d", who, p, r.StatusCode, want)
				}
			}
			if r, _ := e.do("POST", "/api/v1/admin/inbound/mailboxes", who, map[string]any{"name": "x", "kind": "webhook", "address": "a@example.test"}); r.StatusCode != want {
				t.Errorf("%q POST → %d, want %d", who, r.StatusCode, want)
			}
		}
	})
}

func TestMailboxEnvLock(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		team := storetest.CreateTeam(t, db, "Billing")
		e := newEnv(t, db, map[string]string{
			"FD_IMAP_HOST": "imap.env.example.test", "FD_IMAP_USERNAME": "envuser", "FD_IMAP_PASSWORD": "envpass", "FD_IMAP_TEAM": "Billing",
		})
		r, d := e.do("GET", "/api/v1/admin/inbound/mailboxes", "staff:admin", nil)
		if r.StatusCode != 200 {
			t.Fatalf("%d %s", r.StatusCode, d)
		}
		var list []mailboxDTO
		_ = json.Unmarshal(d, &list)
		if len(list) != 1 {
			t.Fatalf("env mailbox not created: %s", d)
		}
		mb := list[0]
		for _, f := range []string{"host", "username", "password", "team_id"} {
			if !contains(mb.LockedByEnv, f) {
				t.Fatalf("%s not locked: %v", f, mb.LockedByEnv)
			}
		}
		if mb.IMAP.Host != "imap.env.example.test" || !mb.IMAP.PasswordSet || mb.TeamID == nil || *mb.TeamID != team || strings.Contains(string(d), "envpass") {
			t.Fatalf("env values: %s", d)
		}
		// changing a locked field → 422 locked_by_env
		r, d = e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", map[string]any{"imap": map[string]any{"host": "other.example.test", "port": 993, "security": "tls", "username": "envuser"}})
		if r.StatusCode != 422 || !strings.Contains(string(d), "locked_by_env") {
			t.Fatalf("locked PATCH → %d %s", r.StatusCode, d)
		}
		// echoing the env value is accepted; unlocked fields still change
		r, d = e.do("PATCH", "/api/v1/admin/inbound/mailboxes/"+mb.ID, "staff:admin", map[string]any{"name": "Renamed", "imap": map[string]any{"host": "imap.env.example.test", "port": 993, "security": "tls", "username": "envuser", "processed_action": "flag"}})
		if r.StatusCode != 200 || !strings.Contains(string(d), "Renamed") || !strings.Contains(string(d), `"flag"`) {
			t.Fatalf("echo PATCH → %d %s", r.StatusCode, d)
		}
		pw, _ := e.m.imapPassword(e.mailbox(mb.ID))
		if pw != "envpass" {
			t.Fatalf("env password not effective")
		}
		// listing again does not create a second env mailbox
		_, d = e.do("GET", "/api/v1/admin/inbound/mailboxes", "staff:admin", nil)
		_ = json.Unmarshal(d, &list)
		if len(list) != 1 {
			t.Fatalf("duplicate env mailbox")
		}
	})
}

func TestInboundMessagesPaging(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		customer(t, db, "pg@client.example.org", "")
		mbID, _ := e.webhookMailbox("", "")
		for i := range 3 {
			e.receive(mbID, mkMail("pg@client.example.org", "p"+fmtInt(int64(i)), "x", nil))
		}
		_, d := e.do("GET", "/api/v1/admin/inbound/messages?limit=2&mailbox_id="+mbID, "staff:admin", nil)
		var page struct {
			Items []messageDTO `json:"items"`
			Next  *string      `json:"next_cursor"`
		}
		_ = json.Unmarshal(d, &page)
		if len(page.Items) != 2 || page.Next == nil {
			t.Fatalf("page 1: %s", d)
		}
		_, d = e.do("GET", "/api/v1/admin/inbound/messages?limit=2&cursor="+*page.Next, "staff:admin", nil)
		_ = json.Unmarshal(d, &page)
		if len(page.Items) != 1 || page.Next != nil {
			t.Fatalf("page 2: %s", d)
		}
		if r, _ := e.do("GET", "/api/v1/admin/inbound/messages?status=bogus", "staff:admin", nil); r.StatusCode != 422 && r.StatusCode != 400 {
			t.Fatalf("bad status filter %d", r.StatusCode)
		}
	})
}

// TestInboundMessagesSort: sort=received_at pages the inbound log oldest
// first and the default (or -received_at) newest first, both by
// (received_at, id) and with the status and mailbox filters applied. Seven
// rows: two pairs share a timestamp (one pair straddles a limit-2 page
// boundary each way) and the 4th has the newest id but a middle time. The
// default cursor stays base64url(id); a cursor only works in the direction
// that issued it, and one whose row is gone is rejected.
func TestInboundMessagesSort(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db, nil)
		mbID, _ := e.webhookMailbox("", "")
		ctx := context.Background()
		insert := func(id, status string, at time.Time) {
			if err := db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO inbound_messages (id, mailbox_id, message_id, from_addr, subject, status, received_at, created_at) VALUES (?, ?, ?, 'a@client.example.org', 's', ?, ?, ?)`,
					id, mbID, "<"+id+"@x>", status, at, at)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		ids := make([]string, 7)
		for i := range ids {
			ids[i] = store.NewID()
		}
		slices.Sort(ids)
		want := []string{ids[0], ids[1], ids[2], ids[6], ids[3], ids[4], ids[5]}
		base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		for i, m := range []int{0, 1, 1, 2, 3, 3, 4} {
			insert(want[i], statusProcessed, base.Add(time.Duration(m)*time.Minute))
		}
		insert(store.NewID(), statusFailed, base.Add(90*time.Second)) // filtered out
		rev := slices.Clone(want)
		slices.Reverse(rev)

		path := "/api/v1/admin/inbound/messages?limit=2&status=processed&mailbox_id=" + mbID
		get := func(url string, code int) (items []string, next string, body []byte) {
			t.Helper()
			r, body := e.do("GET", url, "staff:admin", nil)
			if r.StatusCode != code {
				t.Fatalf("GET %s: %d %s", url, r.StatusCode, body)
			}
			var p struct {
				Items []messageDTO `json:"items"`
				Next  *string      `json:"next_cursor"`
			}
			_ = json.Unmarshal(body, &p)
			for _, d := range p.Items {
				items = append(items, d.ID)
			}
			if p.Next != nil {
				next = *p.Next
			}
			return items, next, body
		}
		pageAll := func(query string) (got, cursors []string) {
			t.Helper()
			cursor := ""
			for range 10 {
				items, next, _ := get(path+query+cursor, 200)
				got = append(got, items...)
				if next == "" {
					return got, cursors
				}
				cursors = append(cursors, next)
				cursor = "&cursor=" + next
			}
			t.Fatalf("%s: no last page", query)
			return nil, nil
		}

		asc, ascCursors := pageAll("&sort=received_at")
		if !slices.Equal(asc, want) {
			t.Fatalf("ascending\n got %v\nwant %v", asc, want)
		}
		desc, descCursors := pageAll("")
		if !slices.Equal(desc, rev) {
			t.Fatalf("default\n got %v\nwant %v", desc, rev)
		}
		for i, c := range descCursors {
			if c != base64.RawURLEncoding.EncodeToString([]byte(desc[2*i+1])) {
				t.Fatalf("default cursor %d = %q, want base64url(id)", i, c)
			}
		}
		if explicit, _ := pageAll("&sort=-received_at"); !slices.Equal(explicit, rev) {
			t.Fatalf("-received_at\n got %v\nwant %v", explicit, rev)
		}

		if _, _, body := get(path+"&sort=created_at", 422); !strings.Contains(string(body), `"sort"`) {
			t.Fatalf("bad sort: %s", body)
		}
		get(path+"&cursor="+ascCursors[0], 422)
		get(path+"&sort=-received_at&cursor="+ascCursors[0], 422)
		get(path+"&sort=received_at&cursor="+descCursors[0], 422)
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM inbound_messages WHERE id = ?`, desc[1])
			return err
		}); err != nil {
			t.Fatal(err)
		}
		get(path+"&cursor="+descCursors[0], 422) // its row was pruned
	})
}

var _ = http.StatusOK
