// SPDX-License-Identifier: AGPL-3.0-only

package privacy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/store"
	"github.com/radioBros/SimpleTickets/internal/store/storetest"
)

// readyExport fetches the caller's export list and returns the newest request.
func (e *env) myLatest(who string) Request {
	e.t.Helper()
	var mp myPrivacy
	e.want(e.do("GET", "/api/v1/me/privacy", who, nil), 200).decode(e.t, &mp)
	if len(mp.Requests) == 0 {
		e.t.Fatal("no requests")
	}
	return mp.Requests[0]
}

// TestExport_SelfServiceContainsExactlyTheSubjectsData: a customer's own
// export holds her profile, tickets, messages, files, consents and audit
// entries, and never internal notes, staff-only fields, other customers'
// messages or anyone else's identity.
func TestExport_SelfServiceContainsExactlyTheSubjectsData(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		var r Request
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 202).decode(t, &r)
		if r.Type != TypeExport || r.UserID != e.w.cust || r.IncludeInternal {
			t.Fatalf("request = %+v", r)
		}
		got := e.myLatest("cust")
		if got.Status != StatusReady || got.DownloadURL == nil || got.ExpiresAt == nil {
			t.Fatalf("export not ready: %+v", got)
		}
		if d := got.ExpiresAt.Sub(*got.CompletedAt); d != 7*24*time.Hour {
			t.Errorf("expires after %s, want 7 days", d)
		}
		dl := e.want(e.do("GET", *got.DownloadURL, "cust", nil), 200)
		if ct := dl.Header().Get("Content-Type"); ct != "application/zip" {
			t.Errorf("content type %q", ct)
		}
		if !strings.Contains(dl.Header().Get("Content-Disposition"), ".zip") {
			t.Errorf("disposition %q", dl.Header().Get("Content-Disposition"))
		}
		files := unzip(t, dl.Body.Bytes())
		for _, f := range []string{"README.txt", "profile.json", "tickets.json", "messages.json", "attachments.json", "consents.json", "audit.json", "requests.json"} {
			if _, ok := files[f]; !ok {
				t.Errorf("archive lacks %s", f)
			}
		}
		all := strings.Join(func() []string {
			var s []string
			for n, c := range files {
				s = append(s, n, c)
			}
			return s
		}(), "\n")
		for _, want := range []string{zeldaName, zeldaEmail, "zelda-sub", "ZeldaBrowser", "zelda-token", "zeldasubject", "zeldasecret",
			"agentreplyword", "ORDZELDA77", "zeldacsatcomment", "zeldaccmessage", "https://acme.test/privacy", "auth.login_success", "198.51.100.7"} {
			if !strings.Contains(all, want) {
				t.Errorf("export lacks %q", want)
			}
		}
		for _, leak := range []string{"internalonlygossip", "staffonlyrisk", "risk_note", "ottosecretcc", "ottoword", "ottosubject", ottoName, ottoEmail,
			"internal-attachment-bytes", "203.0.113.9", "192.0.2.55", e.w.admin, e.w.agent, e.w.other} {
			if strings.Contains(all, leak) {
				t.Errorf("customer self-export leaks %q", leak)
			}
		}
		// The uploaded file is inside, under a safe name (no zip-slip).
		var found bool
		for name, c := range files {
			if strings.HasPrefix(name, "attachments/") && strings.HasSuffix(name, "passport zelda.txt") {
				found = c == "zelda-attachment-bytes"
			}
			if strings.Contains(name, "..") {
				t.Errorf("unsafe entry %q", name)
			}
		}
		if !found {
			t.Error("attachment file missing from the archive")
		}
		// The ready mail goes to the requester with the portal link.
		mails := e.mailsCopy()
		if len(mails) != 1 || mails[0].Template != notify.TplPrivacyExportReady || mails[0].UserID != e.w.cust ||
			mails[0].Vars["url"] != "https://help.example.com/profile/data" || mails[0].Vars["expires_days"] != "7" {
			t.Fatalf("mails = %+v", mails)
		}
		// One at a time: a second request while pending is 409 (seen through a
		// pending row; the inline runner already finished the first).
		e.exec(`UPDATE privacy_requests SET status = 'pending' WHERE id = ?`, r.ID)
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 409)
	})
}

// TestExport_AdminIncludeInternal: an admin export can include internal notes
// and staff-only fields; it is downloadable by admins only, and the subject
// never sees it.
func TestExport_AdminIncludeInternal(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		var r Request
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+e.w.cust+"/export", "admin", map[string]any{"include_internal": true}), 202).decode(t, &r)
		var page requestPage
		e.want(e.do("GET", "/api/v1/admin/privacy/requests?type=export&user_id="+e.w.cust, "admin", nil), 200).decode(t, &page)
		if len(page.Items) != 1 || page.Items[0].DownloadURL == nil || page.Items[0].User == nil || page.Items[0].User.Email != zeldaEmail {
			t.Fatalf("admin list = %+v", page.Items)
		}
		files := unzip(t, e.want(e.do("GET", *page.Items[0].DownloadURL, "admin", nil), 200).Body.Bytes())
		for _, want := range []string{"internalonlygossip", "staffonlyrisk"} {
			if !strings.Contains(files["tickets.json"], want) {
				t.Errorf("include_internal export lacks %q", want)
			}
		}
		// The subject cannot see or download it.
		var mp myPrivacy
		e.want(e.do("GET", "/api/v1/me/privacy", "cust", nil), 200).decode(t, &mp)
		if len(mp.Requests) != 0 {
			t.Fatalf("customer sees the admin export: %+v", mp.Requests)
		}
		e.want(e.do("GET", "/api/v1/me/privacy/exports/"+r.ID, "cust", nil), 404)
		mails := e.mailsCopy()
		if len(mails) != 1 || mails[0].UserID != e.w.admin || mails[0].Vars["url"] != "https://help.example.com/agent/admin/privacy" {
			t.Fatalf("mails = %+v", mails)
		}
	})
}

// TestExport_FileExpires: after export_days the download is 404, and the
// retention run deletes the file and marks the request expired.
func TestExport_FileExpires(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 202)
		got := e.myLatest("cust")
		key := e.str(`SELECT file_key FROM privacy_requests WHERE id = ?`, got.ID)
		if _, err := e.blob.Stat(context.Background(), key); err != nil {
			t.Fatalf("file missing before expiry: %v", err)
		}
		e.want(e.do("GET", *got.DownloadURL, "cust", nil), 200)
		restore := store.SetClock(func() time.Time { return time.Now().Add(7*24*time.Hour + time.Minute) })
		defer restore()
		e.want(e.do("GET", *got.DownloadURL, "cust", nil), 404)
		if r := e.myLatest("cust"); r.Status != StatusExpired || r.DownloadURL != nil {
			t.Fatalf("after expiry: %+v", r)
		}
		var pv Preview
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &pv)
		if pv.ExpiredExports != 1 {
			t.Fatalf("preview expired_exports = %d", pv.ExpiredExports)
		}
		p, err := e.m.RunRetention(context.Background())
		if err != nil || p.ExpiredExports != 1 {
			t.Fatalf("run = %+v, %v", p, err)
		}
		if _, err := e.blob.Stat(context.Background(), key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatalf("file still there: %v", err)
		}
		if s := e.str(`SELECT status FROM privacy_requests WHERE id = ?`, got.ID); s != StatusExpired {
			t.Fatalf("status %s", s)
		}
	})
}

// TestErase_AnonymizeAndRedact: PII is gone from users, identities, messages,
// field values, files, inbound mail and search; tickets stay; the audit has
// no PII.
func TestErase_AnonymizeAndRedact(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		ctx := context.Background()
		w := e.w
		search := func(term string) int {
			sub, args, ok := store.TicketMatch(db.Dialect, term, true)
			if !ok {
				t.Fatal("no terms")
			}
			return e.count(`SELECT COUNT(*) FROM tickets t WHERE t.id IN (`+sub+`)`, args...)
		}
		if search("zeldasecret") != 1 || search("ORDZELDA77") != 1 {
			t.Fatal("precondition: the index finds her text")
		}
		// A finished export of hers is deleted too.
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 202)
		exportKey := e.str(`SELECT file_key FROM privacy_requests WHERE user_id = ? AND type = 'export'`, w.cust)
		ticketsBefore := e.count(`SELECT COUNT(*) FROM tickets`)
		messagesBefore := e.count(`SELECT COUNT(*) FROM ticket_messages`)

		var res ErasureResult
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.cust+"/erase", "admin", map[string]string{"mode": ModeRedact}), 200).decode(t, &res)
		if !strings.HasPrefix(res.Name, "Deleted user #") || res.MessagesRedacted != 2 || res.AttachmentsDeleted != 1 || res.FieldValuesRedacted != 2 ||
			res.TicketsKept != 1 || res.Request.Status != StatusCompleted || res.Request.Mode == nil || *res.Request.Mode != ModeRedact {
			t.Fatalf("result = %+v", res)
		}
		// users
		if n := e.count(`SELECT COUNT(*) FROM users WHERE id = ? AND name = ? AND email = ? AND status = 'disabled' AND password_hash IS NULL`, w.cust, res.Name, ErasedEmail(w.cust)); n != 1 {
			t.Fatal("user not anonymized")
		}
		for _, q := range []string{
			`SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`,
			`SELECT COUNT(*) FROM api_tokens WHERE user_id = ?`, `SELECT COUNT(*) FROM attachments WHERE uploader_id = ?`,
			`SELECT COUNT(*) FROM ticket_messages WHERE author_id = ? AND body_md <> '[redacted]'`,
			`SELECT COUNT(*) FROM user_consents WHERE user_id = ? AND ip <> ''`,
		} {
			if n := e.count(q, w.cust); n != 0 {
				t.Errorf("%s = %d", q, n)
			}
		}
		// No PII left anywhere we control.
		for _, pii := range []string{zeldaName, zeldaEmail, "zeldasecret", "zeldaccmessage", "ORDZELDA77", "zeldacsatcomment", "zelda-sub"} {
			for _, q := range []string{
				`SELECT COUNT(*) FROM users WHERE name LIKE ? OR email LIKE ?`,
				`SELECT COUNT(*) FROM ticket_messages WHERE body_md LIKE ? OR body_md LIKE ?`,
				`SELECT COUNT(*) FROM ticket_field_values WHERE value_text LIKE ? OR value_text LIKE ?`,
				`SELECT COUNT(*) FROM tickets WHERE csat_comment LIKE ? OR csat_comment LIKE ?`,
				`SELECT COUNT(*) FROM user_identities WHERE subject LIKE ? OR email_at_link LIKE ?`,
				`SELECT COUNT(*) FROM inbound_messages WHERE from_addr LIKE ? OR from_addr LIKE ?`,
				`SELECT COUNT(*) FROM audit_log WHERE CAST(data AS TEXT) LIKE ? OR target_id LIKE ?`,
			} {
				if n := e.count(q, "%"+pii+"%", "%"+pii+"%"); n != 0 {
					t.Errorf("%q still in: %s", pii, q)
				}
			}
		}
		if search("zeldasecret") != 0 || search("ORDZELDA77") != 0 || search("zeldaccmessage") != 0 {
			t.Error("search still finds redacted text")
		}
		if search("agentreplyword") != 1 {
			t.Error("staff text of the kept ticket must stay searchable")
		}
		// Tickets and the conversation shape stay (business records).
		if n := e.count(`SELECT COUNT(*) FROM tickets`); n != ticketsBefore {
			t.Errorf("tickets %d → %d", ticketsBefore, n)
		}
		if n := e.count(`SELECT COUNT(*) FROM ticket_messages`); n != messagesBefore {
			t.Errorf("messages %d → %d", messagesBefore, n)
		}
		if e.str(`SELECT requester_id FROM tickets WHERE id = ?`, w.t1) != w.cust {
			t.Error("ticket lost its requester")
		}
		if e.count(`SELECT COUNT(*) FROM ticket_messages WHERE id = ? AND body_md LIKE '%internalonlygossip%'`, w.note) != 1 {
			t.Error("other authors' messages must be untouched")
		}
		// Files: hers and the raw mail are gone, others' stay.
		for _, k := range []string{w.attKey, w.rawKey, exportKey} {
			if _, err := e.blob.Stat(ctx, k); !errors.Is(err, blob.ErrNotFound) {
				t.Errorf("blob %s still there: %v", k, err)
			}
		}
		if _, err := e.blob.Stat(ctx, w.noteAttKey); err != nil {
			t.Errorf("staff attachment deleted: %v", err)
		}
		// The audit records the erasure, without PII.
		if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'privacy.user_erased' AND target_id = ? AND actor_id = ?`, w.cust, w.admin); n != 1 {
			t.Fatalf("erasure audit rows = %d", n)
		}
		// Re-running in the same mode is harmless and keeps the number.
		var again ErasureResult
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.cust+"/erase", "admin", map[string]string{"mode": ModeRedact}), 200).decode(t, &again)
		if again.Name != res.Name || again.MessagesRedacted != 0 {
			t.Fatalf("second run = %+v", again)
		}
	})
}

// TestErase_AnonymizeKeepsContent: the anonymize mode removes identity but
// keeps message bodies and files.
func TestErase_AnonymizeKeepsContent(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		var res ErasureResult
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+e.w.cust+"/erase", "admin", map[string]string{"mode": ModeAnonymize}), 200).decode(t, &res)
		if res.MessagesRedacted != 0 || res.AttachmentsDeleted != 0 {
			t.Fatalf("result = %+v", res)
		}
		if e.count(`SELECT COUNT(*) FROM users WHERE name = ? OR email = ?`, zeldaName, zeldaEmail) != 0 {
			t.Fatal("identity kept")
		}
		if e.count(`SELECT COUNT(*) FROM ticket_messages WHERE id = ? AND body_md LIKE '%zeldasecret%'`, e.w.custMsg) != 1 {
			t.Fatal("anonymize must keep message bodies")
		}
		if _, err := e.blob.Stat(context.Background(), e.w.attKey); err != nil {
			t.Fatalf("anonymize must keep files: %v", err)
		}
		if _, err := e.blob.Stat(context.Background(), e.w.rawKey); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("raw mail (carries the address) must go in both modes")
		}
	})
}

// TestErasure_RequestFlow: request → reject → request → approve.
func TestErasure_RequestFlow(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		var r Request
		e.want(e.do("POST", "/api/v1/me/privacy/erasure", "cust", map[string]string{"reason": "moving away"}), 202).decode(t, &r)
		if r.Status != StatusPending || r.Type != TypeErasure || r.ExpiresAt != nil {
			t.Fatalf("request = %+v", r)
		}
		e.want(e.do("POST", "/api/v1/me/privacy/erasure", "cust", nil), 409)
		var mp myPrivacy
		e.want(e.do("GET", "/api/v1/me/privacy", "cust", nil), 200).decode(t, &mp)
		if mp.CanRequestErasure || len(mp.Consents) != 1 || mp.ExportDays != 7 {
			t.Fatalf("my privacy = %+v", mp)
		}
		var page requestPage
		e.want(e.do("GET", "/api/v1/admin/privacy/requests?type=erasure&status=pending", "admin", nil), 200).decode(t, &page)
		if len(page.Items) != 1 || page.Items[0].ID != r.ID {
			t.Fatalf("pending = %+v", page.Items)
		}
		e.want(e.do("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/reject", "admin", map[string]string{"reason": " "}), 422)
		var rej Request
		e.want(e.do("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/reject", "admin", map[string]string{"reason": "open invoices"}), 200).decode(t, &rej)
		if rej.Status != StatusRejected || rej.Reason != "open invoices" || rej.DecidedBy == nil {
			t.Fatalf("rejected = %+v", rej)
		}
		e.want(e.do("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/approve", "admin", map[string]string{"mode": ModeAnonymize}), 409)
		e.want(e.do("POST", "/api/v1/me/privacy/erasure", "cust", nil), 202).decode(t, &r)
		e.want(e.invalid("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/approve", "admin", map[string]string{}), 422)
		var res ErasureResult
		e.want(e.do("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/approve", "admin", map[string]string{"mode": ModeAnonymize}), 200).decode(t, &res)
		if res.Request.ID != r.ID || res.Request.Status != StatusCompleted {
			t.Fatalf("approved = %+v", res.Request)
		}
		e.want(e.do("POST", "/api/v1/admin/privacy/requests/"+r.ID+"/approve", "admin", map[string]string{"mode": ModeAnonymize}), 409)
		if e.count(`SELECT COUNT(*) FROM audit_log WHERE action IN ('privacy.erasure_requested', 'privacy.erasure_rejected', 'privacy.user_erased') AND target_id = ?`, e.w.cust) != 4 {
			t.Fatal("audit trail incomplete")
		}
	})
}

// TestErasure_AutoApprove: with erasure_auto_approve_days, the daily run
// approves old pending requests; the preview counts them first.
func TestErasure_AutoApprove(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.putSettings(func(s *Settings) { s.ErasureAutoApproveDays = 3; s.ErasureAutoApproveMode = ModeRedact })
		var r Request
		e.want(e.do("POST", "/api/v1/me/privacy/erasure", "cust", nil), 202).decode(t, &r)
		if r.ExpiresAt == nil {
			t.Fatal("auto-approval date missing")
		}
		var pv Preview
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &pv)
		if pv.ErasureAutoApprovals != 0 {
			t.Fatalf("too early: %+v", pv)
		}
		restore := store.SetClock(func() time.Time { return time.Now().Add(3*24*time.Hour + time.Minute) })
		defer restore()
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &pv)
		run, err := e.m.RunRetention(context.Background())
		if err != nil || pv.ErasureAutoApprovals != 1 || run.ErasureAutoApprovals != 1 {
			t.Fatalf("preview %+v run %+v err %v", pv, run, err)
		}
		if e.count(`SELECT COUNT(*) FROM users WHERE id = ? AND email = ?`, e.w.cust, ErasedEmail(e.w.cust)) != 1 ||
			e.count(`SELECT COUNT(*) FROM ticket_messages WHERE author_id = ? AND body_md <> '[redacted]'`, e.w.cust) != 0 {
			t.Fatal("auto-approval did not erase in the configured mode")
		}
		if e.str(`SELECT status FROM privacy_requests WHERE id = ?`, r.ID) != StatusCompleted {
			t.Fatal("request not completed")
		}
	})
}

// TestPrivacyAuthz: customers only export themselves, only admins erase or
// read others' requests, and the safety guards hold.
func TestPrivacyAuthz(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		w := e.w
		erase := map[string]string{"mode": ModeAnonymize}
		// Admin routes: customers get 404 (I-1), agents 403.
		for _, who := range []string{"cust", "agent"} {
			code := 404
			if who == "agent" {
				code = 403
			}
			e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.other+"/erase", who, erase), code)
			e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.other+"/export", who, nil), code)
			e.want(e.do("GET", "/api/v1/admin/privacy/requests", who, nil), code)
			e.want(e.do("GET", "/api/v1/admin/privacy/settings", who, nil), code)
		}
		// Otto cannot download Zelda's export.
		var r Request
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 202).decode(t, &r)
		e.want(e.do("GET", "/api/v1/me/privacy/exports/"+r.ID, "other", nil), 404)
		e.want(e.do("GET", "/api/v1/me/privacy/exports/"+r.ID, "cust", nil), 200)
		// Staff export themselves but cannot file an erasure request.
		e.want(e.do("POST", "/api/v1/me/privacy/export", "agent", nil), 202)
		e.want(e.do("POST", "/api/v1/me/privacy/erasure", "agent", nil), 403)
		// A narrow PAT cannot use the self-service routes.
		e.want(e.do("GET", "/api/v1/me/privacy", "cust-pat", nil), 403)
		// Guards: yourself, the last active admin, unknown users, bad modes.
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.admin+"/erase", "admin", erase), 409)
		e.exec(`UPDATE users SET status = 'disabled' WHERE id = ?`, w.admin)
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.admin2+"/erase", "admin", erase), 409)
		e.exec(`UPDATE users SET status = 'active' WHERE id = ?`, w.admin)
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+store.NewID()+"/erase", "admin", erase), 404)
		e.want(e.invalid("POST", "/api/v1/admin/privacy/users/"+w.other+"/erase", "admin", map[string]string{"mode": "shred"}), 422)
		// A staff erasure unassigns nothing illegal and disables the account.
		e.want(e.do("POST", "/api/v1/admin/privacy/users/"+w.agent+"/erase", "admin", erase), 200)
		if e.count(`SELECT COUNT(*) FROM users WHERE id = ? AND status = 'disabled'`, w.agent) != 1 || e.count(`SELECT COUNT(*) FROM team_members WHERE user_id = ?`, w.agent) != 0 {
			t.Fatal("staff erasure incomplete")
		}
	})
}

// seedRetention adds tickets, inbound rows and audit rows at known ages.
func (e *env) seedRetention(now time.Time) (old []string) {
	e.t.Helper()
	w := e.w
	for i := 0; i < 3; i++ {
		id := storetest.CreateTicket(e.t, e.db, storetest.Ticket{Subject: "old closed retentionword", Requester: w.other, Status: "closed"})
		e.exec(`UPDATE tickets SET closed_at = ? WHERE id = ?`, now.AddDate(0, -14, 0), id)
		e.message(id, w.other, "public", "retentionbody text")
		e.attachment(id, "", w.other, "old.txt", "old-bytes")
		old = append(old, id)
	}
	recent := storetest.CreateTicket(e.t, e.db, storetest.Ticket{Subject: "recent closed", Requester: w.other, Status: "closed"})
	e.exec(`UPDATE tickets SET closed_at = ? WHERE id = ?`, now.AddDate(0, -2, 0), recent)
	openOld := storetest.CreateTicket(e.t, e.db, storetest.Ticket{Subject: "open old", Requester: w.other, Status: "open"})
	e.exec(`UPDATE tickets SET created_at = ? WHERE id = ?`, now.AddDate(-2, 0, 0), openOld)
	mb := e.str(`SELECT id FROM inbound_mailboxes LIMIT 1`)
	for i, c := range []struct {
		age    time.Duration
		status string
	}{{40 * 24 * time.Hour, "processed"}, {35 * 24 * time.Hour, "failed"}, {5 * 24 * time.Hour, "processed"}, {60 * 24 * time.Hour, "quarantined"}} {
		e.exec(`INSERT INTO inbound_messages (id, mailbox_id, message_id, from_addr, status, received_at, created_at) VALUES (?, ?, ?, 'x@example.test', ?, ?, ?)`,
			store.NewID(), mb, "<r"+string(rune('a'+i))+"@x>", c.status, now.Add(-c.age), now.Add(-c.age))
	}
	for _, age := range []int{13, 15, 1} {
		e.exec(`INSERT INTO audit_log (id, action, created_at) VALUES (?, 'test.old', ?)`, store.NewID(), now.AddDate(0, -age, 0))
	}
	return old
}

// TestRetention_PreviewEqualsRun: thresholds are respected and the dry-run
// counts equal what the run processes, for both ticket actions.
func TestRetention_PreviewEqualsRun(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		now := store.Now()
		old := e.seedRetention(now)
		// Defaults never touch tickets or audit rows (the audit rule is off,
		// ST_AUDIT_RETENTION_DAYS alone applies), so an upgrade deletes nothing new.
		var def Preview
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &def)
		if def.ClosedTickets != 0 || def.AuditEntries != 0 || def.InboundEmails != 0 {
			t.Fatalf("default preview = %+v", def)
		}
		e.putSettings(func(s *Settings) {
			s.Retention = Retention{ClosedTicketsMonths: 12, ClosedTicketsAction: ActionAnonymize, InboundEmailDays: 30, AuditLogMonths: 12, ExportDays: 7}
		})
		var pv Preview
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &pv)
		if pv.ClosedTickets != 3 || pv.InboundEmails != 2 || pv.AuditEntries != 2 || pv.ClosedTicketsAction != ActionAnonymize {
			t.Fatalf("preview = %+v", pv)
		}
		tickets := e.count(`SELECT COUNT(*) FROM tickets`)
		run, err := e.m.RunRetention(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if run.ClosedTickets != pv.ClosedTickets || run.InboundEmails != pv.InboundEmails || run.AuditEntries != pv.AuditEntries {
			t.Fatalf("run %+v != preview %+v", run, pv)
		}
		if e.count(`SELECT COUNT(*) FROM tickets`) != tickets {
			t.Fatal("anonymize must keep tickets")
		}
		for _, id := range old {
			if e.str(`SELECT subject FROM tickets WHERE id = ?`, id) != redacted ||
				e.count(`SELECT COUNT(*) FROM ticket_messages WHERE ticket_id = ? AND body_md <> '[redacted]'`, id) != 0 ||
				e.count(`SELECT COUNT(*) FROM attachments WHERE ticket_id = ?`, id) != 0 {
				t.Fatalf("ticket %s not anonymized", id)
			}
		}
		if e.count(`SELECT COUNT(*) FROM tickets WHERE subject IN ('recent closed', 'open old')`) != 2 {
			t.Fatal("thresholds not respected")
		}
		if e.count(`SELECT COUNT(*) FROM inbound_messages WHERE status = 'quarantined'`) != 1 || e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'test.old'`) != 1 {
			t.Fatal("inbound/audit thresholds not respected")
		}
		sub, args, _ := store.TicketMatch(db.Dialect, "retentionword", true)
		if e.count(`SELECT COUNT(*) FROM tickets t WHERE t.id IN (`+sub+`)`, args...) != 0 {
			t.Fatal("anonymized tickets still searchable")
		}
		// Nothing left to do: the preview is empty again.
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview", "admin", nil), 200).decode(t, &pv)
		if pv.ClosedTickets != 0 || pv.InboundEmails != 0 || pv.AuditEntries != 0 {
			t.Fatalf("second preview = %+v", pv)
		}
		// Purge: an unsaved override previews it; the run deletes them.
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview?closed_tickets_action=purge&closed_tickets_months=12", "admin", nil), 200).decode(t, &pv)
		if pv.ClosedTickets != 3 {
			t.Fatalf("purge preview = %+v", pv)
		}
		e.want(e.do("GET", "/api/v1/admin/privacy/retention/preview?inbound_email_days=0", "admin", nil), 422)
		e.putSettings(func(s *Settings) { s.Retention.ClosedTicketsAction = ActionPurge })
		run, err = e.m.RunRetention(context.Background())
		if err != nil || run.ClosedTickets != 3 {
			t.Fatalf("purge run = %+v, %v", run, err)
		}
		if e.count(`SELECT COUNT(*) FROM tickets`) != tickets-3 {
			t.Fatal("purge did not delete")
		}
		for _, id := range old {
			if e.count(`SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ?`, id) != 0 {
				t.Fatal("events left behind")
			}
		}
		if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'privacy.retention_run'`) != 2 {
			t.Fatal("runs not audited")
		}
	})
}

// TestConsent_RecordedWithPolicyVersion: the registration hook stores URL,
// version and IP.
func TestConsent_RecordedWithPolicyVersion(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		e.putSettings(func(s *Settings) { s.PolicyVersion = "2026-10" })
		if err := db.Tx(context.Background(), func(tx *store.Tx) error {
			return RecordConsent(context.Background(), tx, e.w.other, "https://acme.test/privacy", "192.0.2.1")
		}); err != nil {
			t.Fatal(err)
		}
		var mp myPrivacy
		e.want(e.do("GET", "/api/v1/me/privacy", "other", nil), 200).decode(t, &mp)
		if len(mp.Consents) != 1 || mp.Consents[0].PolicyVersion != "2026-10" || mp.Consents[0].IP != "192.0.2.1" || mp.Consents[0].PolicyURL != "https://acme.test/privacy" {
			t.Fatalf("consents = %+v", mp.Consents)
		}
	})
}

// TestSettings_Validation: out-of-range values are 422 with field paths.
func TestSettings_Validation(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		e := newEnv(t, db)
		var s Settings
		e.want(e.do("GET", "/api/v1/admin/privacy/settings", "admin", nil), 200).decode(t, &s)
		if s.Retention != DefaultRetention() || s.ErasureAutoApproveDays != 0 {
			t.Fatalf("defaults = %+v", s)
		}
		bad := s
		bad.Retention.InboundEmailDays = 91
		bad.Retention.ExportDays = 0
		bad.ErasureAutoApproveMode = "shred"
		r := e.want(e.invalid("PUT", "/api/v1/admin/privacy/settings", "admin", bad), 422)
		for _, f := range []string{"retention.inbound_email_days", "retention.export_days", "erasure_auto_approve_mode"} {
			if !strings.Contains(r.Body.String(), f) {
				t.Errorf("422 lacks %s: %s", f, r.Body.String())
			}
		}
		s.Retention.ExportDays = 3
		e.want(e.do("PUT", "/api/v1/admin/privacy/settings", "admin", s), 200)
		e.want(e.do("POST", "/api/v1/me/privacy/export", "cust", nil), 202)
		got := e.myLatest("cust")
		if d := got.ExpiresAt.Sub(*got.CompletedAt); d != 3*24*time.Hour {
			t.Fatalf("export_days not applied: %s", d)
		}
	})
}
