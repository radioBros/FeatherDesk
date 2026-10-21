// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/apispec"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

func multipartBody(filename string, content []byte) ([]byte, string) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = w.Close()
	return b.Bytes(), w.FormDataContentType()
}

// upload posts a file as p to its area and returns the attachment id.
func (e *env) upload(p *domain.Principal, filename string, content []byte) resp {
	e.t.Helper()
	area := "agent"
	if p.IsCustomer() {
		area = "portal"
	}
	body, ct := multipartBody(filename, content)
	return e.do(p, "POST", "/api/v1/"+area+"/attachments", body, "Content-Type", ct)
}

func (e *env) uploadID(p *domain.Principal, filename string, content []byte) string {
	e.t.Helper()
	return e.upload(p, filename, content).expect(e.t, 201).m(e.t)["id"].(string)
}

// countingReader counts bytes handed to the server.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestUpload_OverLimit413BeforeFullRead(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db) // MaxUploadMB = 1
		agent := e.staff(domain.RoleAgent, 1)
		var head bytes.Buffer
		w := multipart.NewWriter(&head)
		_, _ = w.CreateFormFile("file", "big.bin")
		body := &countingReader{r: io.MultiReader(&head, io.LimitReader(zeroReader{}, 50<<20))}
		req := httptest.NewRequest("POST", "/api/v1/agent/attachments", body)
		req.ContentLength = -1 // no Content-Length: the limit must hold while streaming
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+agent.UserID)
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("X-CSRF-Token", "csrf-ok")
		rec := httptest.NewRecorder()
		e.srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 413 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		if body.n > 3<<20 {
			t.Fatalf("read %d bytes of a 50 MiB upload", body.n)
		}
		if n := e.count(`SELECT COUNT(*) FROM attachments`); n != 0 {
			t.Fatalf("%d rows", n)
		}
	})
}

func TestUpload_SniffedTypeStored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		cust := e.customer("", domain.RoleRequester)
		png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00")
		body, ct := multipartBody("evil.html", png)
		// The client claims text/html for the part; the server ignores it.
		body = bytes.Replace(body, []byte("application/octet-stream"), []byte("text/html"), 1)
		r := e.do(cust, "POST", "/api/v1/portal/attachments", body, "Content-Type", ct).expect(t, 201).m(t)
		if r["content_type"] != "image/png" || r["url"] != "/api/v1/portal/attachments/"+r["id"].(string) {
			t.Fatalf("stored %v", r)
		}
	})
}

func TestAttach_OtherUsersDraft422(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		a, b := e.customer("", domain.RoleRequester), e.customer("", domain.RoleRequester)
		draft := e.uploadID(a, "a.txt", []byte("hello"))
		e.do(b, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "attachment_ids": []string{draft}}).expect(t, 422)
		r := e.do(a, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "attachment_ids": []string{draft}}).expect(t, 201).m(t)
		if n := len(r["attachments"].([]any)); n != 1 {
			t.Fatalf("attachments %d", n)
		}
		// Already attached: cannot be reused.
		e.do(a, "POST", "/api/v1/portal/tickets", map[string]any{"subject": "x", "body_md": "y", "attachment_ids": []string{draft}}).expect(t, 422)
	})
}

type attFix struct {
	*lifecycle
	public, internal string
}

func newAttFix(t *testing.T, db *store.DB) *attFix {
	l := newLifecycle(t, db)
	f := &attFix{lifecycle: l}
	ctx := context.Background()
	f.public = l.e.uploadID(l.agent, "public.txt", []byte("public file"))
	f.internal = l.e.uploadID(l.agent, "internal.txt", []byte("SECRET internal file"))
	if _, err := l.e.svc.AddStaffMessage(ctx, l.agent, l.ticketID, MessageInput{Kind: "public", BodyMD: "see file", AttachmentIDs: []string{f.public}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.e.svc.AddStaffMessage(ctx, l.agent, l.ticketID, MessageInput{Kind: "internal", BodyMD: "log", AttachmentIDs: []string{f.internal}}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDownload_CustomerInternalAttachment404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newAttFix(t, db)
		f.e.do(f.cust, "GET", "/api/v1/portal/attachments/"+f.internal, nil).expect(t, 404)
		r := f.e.do(f.cust, "GET", "/api/v1/portal/attachments/"+f.public, nil).expect(t, 200)
		if string(r.body) != "public file" {
			t.Fatalf("body %q", r.body)
		}
		f.e.do(f.agent, "GET", "/api/v1/agent/attachments/"+f.internal, nil).expect(t, 200)
	})
}

func TestPortalDetail_NoInternalAttachments(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newAttFix(t, db)
		r := f.e.do(f.cust, "GET", "/api/v1/portal/tickets/"+f.ticketID, nil).expect(t, 200)
		if containsBytes(r.body, f.internal) || containsBytes(r.body, "internal.txt") {
			t.Fatal("internal attachment listed on the portal")
		}
		if !containsBytes(r.body, f.public) {
			t.Fatal("public attachment missing")
		}
		s := f.e.do(f.agent, "GET", "/api/v1/agent/tickets/"+f.ticketID, nil).expect(t, 200)
		if !containsBytes(s.body, f.internal) {
			t.Fatal("staff detail lacks the internal attachment")
		}
	})
}

func TestDownload_OtherOrg404(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newAttFix(t, db)
		stranger := f.e.customer(storetest.CreateOrg(t, db, ""), domain.RoleOrgManager)
		f.e.do(stranger, "GET", "/api/v1/portal/attachments/"+f.public, nil).expect(t, 404)
		otherAgent := f.e.staff(domain.RoleAgent, 1, storetest.CreateTeam(t, db, ""))
		f.e.do(otherAgent, "GET", "/api/v1/agent/attachments/"+f.public, nil).expect(t, 404)
		// Drafts are visible to their uploader only.
		d := f.e.uploadID(f.cust, "draft.txt", []byte("d"))
		f.e.do(f.cust, "GET", "/api/v1/portal/attachments/"+d, nil).expect(t, 200)
		f.e.do(f.agent, "GET", "/api/v1/agent/attachments/"+d, nil).expect(t, 404)
	})
}

func TestDownload_SVGForcedAttachment(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		for name, content := range map[string][]byte{
			"x.svg":  []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
			"x.html": []byte(`<html><script>alert(1)</script></html>`),
		} {
			id := e.uploadID(agent, name, content)
			r := e.do(agent, "GET", "/api/v1/agent/attachments/"+id, nil).expect(t, 200)
			if cd := r.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
				t.Fatalf("%s disposition %q", name, cd)
			}
			if r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("Content-Security-Policy") != "sandbox" {
				t.Fatalf("%s headers %v", name, r.Header)
			}
		}
		png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
		id := e.uploadID(agent, "p.png", png)
		if cd := e.do(agent, "GET", "/api/v1/agent/attachments/"+id, nil).Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
			t.Fatalf("png disposition %q", cd)
		}
	})
}

func TestDownload_RangeSupported(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		id := e.uploadID(agent, "r.txt", []byte("0123456789"))
		r := e.do(agent, "GET", "/api/v1/agent/attachments/"+id, nil, "Range", "bytes=2-5")
		if r.StatusCode != http.StatusPartialContent || string(r.body) != "2345" {
			t.Fatalf("range: %d %q", r.StatusCode, r.body)
		}
	})
}

// TestUpload_StreamsWithoutBuffering uploads 25 MiB (the default limit)
// through a pipe; the handler must stream it into the blob store.
func TestUpload_StreamsWithoutBuffering(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	e := newEnvMax(t, db, 25)
	agent := e.staff(domain.RoleAgent, 1)
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		fw, _ := w.CreateFormFile("file", "big.bin")
		_, _ = io.Copy(fw, io.LimitReader(zeroReader{}, 25<<20-1024))
		_ = w.Close()
		_ = pw.Close()
	}()
	req := httptest.NewRequest("POST", "/api/v1/agent/attachments", pr)
	req.ContentLength = -1
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+agent.UserID)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	rec := httptest.NewRecorder()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	e.srv.Handler().ServeHTTP(rec, req)
	runtime.ReadMemStats(&after)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Streaming: the heap never holds the 25 MiB file.
	if grow := int64(after.HeapInuse) - int64(before.HeapInuse); grow > 8<<20 {
		t.Fatalf("heap grew by %d MiB", grow>>20)
	}
}

func TestDelete_AdminOnly403(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		l := newLifecycle(t, db)
		l.e.do(l.sup, "DELETE", "/api/v1/agent/tickets/"+l.ticketID, nil).expect(t, 403)
		l.e.do(l.agent, "DELETE", "/api/v1/agent/tickets/"+l.ticketID, nil).expect(t, 403)
		adm := l.e.staff(domain.RoleAdmin, 1)
		l.e.do(l.e.pat(adm, domain.ScopeTicketsWrite), "DELETE", "/api/v1/agent/tickets/"+l.ticketID, nil).expect(t, 403)
		l.e.do(adm, "DELETE", "/api/v1/agent/tickets/"+l.ticketID, nil).expect(t, 204)
		l.e.do(adm, "GET", "/api/v1/agent/tickets/"+l.ticketID, nil).expect(t, 404)
	})
}

func TestDelete_RemovesBlobsAndFTS(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		f := newAttFix(t, db)
		ctx := context.Background()
		var key string
		_ = db.Read().QueryRowContext(ctx, `SELECT storage_key FROM attachments WHERE id = ?`, f.public).Scan(&key)
		if err := db.Tx(ctx, func(tx *store.Tx) error {
			return store.IndexTicket(ctx, tx, store.TicketDoc{TicketID: f.ticketID, Public: "printer"})
		}); err != nil {
			t.Fatal(err)
		}
		follow := storetest.CreateTicket(t, db, storetest.Ticket{Requester: f.cust.UserID})
		f.e.exec(`UPDATE tickets SET followup_of_id = ? WHERE id = ?`, f.ticketID, follow)
		adm := f.e.staff(domain.RoleAdmin, 1)
		f.e.do(adm, "DELETE", "/api/v1/agent/tickets/"+f.ticketID, nil).expect(t, 204)
		if _, err := f.e.blob.Stat(ctx, key); err == nil {
			t.Fatal("blob still present")
		}
		idx := "tickets_fts"
		if db.Dialect == store.Postgres {
			idx = "ticket_search"
		}
		for _, q := range []string{`SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ?`, `SELECT COUNT(*) FROM attachments WHERE ticket_id = ?`,
			`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ?`, `SELECT COUNT(*) FROM ` + idx + ` WHERE ticket_id = ?`, `SELECT COUNT(*) FROM tickets WHERE id = ?`} {
			if n := f.e.count(q, f.ticketID); n != 0 {
				t.Fatalf("%s: %d", q, n)
			}
		}
		if n := f.e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'ticket.deleted' AND target_id = ?`, f.ticketID); n != 1 {
			t.Fatal("no audit row")
		}
		if n := f.e.count(`SELECT COUNT(*) FROM purge_allow`); n != 0 {
			t.Fatal("purge_allow row left behind")
		}
	})
}

// TestOrphanDraft_PrunedWithBlobKey: the platform's hourly
// prune.orphan_attachments (app.registerMaintenance) deletes drafts older than
// 24 h and hands back their blob keys for deletion after commit.
func TestOrphanDraft_PrunedWithBlobKey(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		agent := e.staff(domain.RoleAgent, 1)
		id := e.uploadID(agent, "old.txt", []byte("x"))
		e.exec(`UPDATE attachments SET created_at = ? WHERE id = ?`, store.Now().Add(-25*time.Hour), id)
		n, keys, err := store.PruneOrphanAttachments(context.Background(), db, time.Now())
		if err != nil || n != 1 || len(keys) != 1 {
			t.Fatalf("prune: %d %v %v", n, keys, err)
		}
	})
}

var _ = apispec.ValidateResponse
var _ = fmt.Sprint

// TestDelete_EmailTicketAndDeferredBlobDelete: a ticket referenced by
// inbound_messages deletes cleanly, and a blob delete that fails after
// commit is retried through the queue's job handler.
func TestDelete_EmailTicketAndDeferredBlobDelete(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.mod.registerAttachJobs(e.q)
		cust := e.customer("", domain.RoleRequester)
		id, err := e.svc.CreateFromEmail(context.Background(), EmailTicket{InboundMessageID: "im-1", RequesterID: cust.UserID, Subject: "x", BodyMD: "y",
			Attachments: []EmailAttachment{{Filename: "a.txt", Data: []byte("a")}}})
		if err != nil {
			t.Fatal(err)
		}
		mb := store.NewID()
		e.exec(`INSERT INTO inbound_mailboxes (id, name, kind) VALUES (?, 'mb', 'webhook')`, mb)
		e.exec(`INSERT INTO inbound_messages (id, mailbox_id, message_id, status, ticket_id) VALUES (?, ?, '<m@x>', 'processed', ?)`, store.NewID(), mb, id)
		e.mod.q = nil // no queue: the failed delete is only logged
		adm := e.staff(domain.RoleAdmin, 1)
		e.do(adm, "DELETE", "/api/v1/agent/tickets/"+id, nil).expect(t, 204)
		if n := e.count(`SELECT COUNT(*) FROM inbound_messages WHERE ticket_id IS NULL`); n != 1 {
			t.Fatal("inbound log row not detached")
		}
		// The job handler deletes a leftover blob.
		key := "attachments/leftover"
		if _, err := e.blob.Put(context.Background(), key, strings.NewReader("x"), 1, "text/plain"); err != nil {
			t.Fatal(err)
		}
		e.mod.registerAttachJobsHandlerForTest(t, key)
		if _, err := e.blob.Stat(context.Background(), key); err == nil {
			t.Fatal("deferred blob delete did not remove the blob")
		}
	})
}

func (m *Module) registerAttachJobsHandlerForTest(t *testing.T, key string) {
	t.Helper()
	if err := m.blobDeleteJob(context.Background(), []byte(`{"key":"`+key+`"}`)); err != nil {
		t.Fatal(err)
	}
}
