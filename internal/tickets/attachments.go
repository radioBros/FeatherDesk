// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/queue"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Attachments (TKT-011/012): uploads stream into the blob store (never
// buffered whole), the size limit is enforced while streaming, the content
// type is sniffed, and downloads are visibility-checked through the ticket
// predicate and, for customers, the parent message kind (I-2).

var errTooLarge = errors.New("attachment too large")

// limitReader fails once more than max bytes were read.
type limitReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, errTooLarge
	}
	return n, err
}

// cleanFilename keeps the base name, strips control characters, caps length.
func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	if r := []rune(name); len(r) > 255 {
		name = string(r[len(r)-255:])
	}
	return name
}

// StoreAttachment streams r into the blob store and records a draft
// attachment owned by uploaderID (TKT-011). Used by uploads and inbound email.
func (s *Service) StoreAttachment(ctx context.Context, uploaderID, filename string, r io.Reader) (attachmentDTO, error) {
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		if errors.Is(err, errTooLarge) {
			return attachmentDTO{}, problems.PayloadTooLarge()
		}
		return attachmentDTO{}, err
	}
	head = head[:n]
	ctype := http.DetectContentType(head)
	now := s.clock()
	key := blob.NewKey("attachments", now)
	lr := &limitReader{r: io.MultiReader(bytes.NewReader(head), r), max: s.maxUpload}
	sum, err := s.blob.Put(ctx, key, lr, -1, ctype)
	if err != nil {
		_ = s.blob.Delete(ctx, key)
		var mbe *http.MaxBytesError
		if errors.Is(err, errTooLarge) || errors.As(err, &mbe) {
			return attachmentDTO{}, problems.PayloadTooLarge()
		}
		return attachmentDTO{}, err
	}
	a := attachmentDTO{ID: store.NewID(), Filename: cleanFilename(filename), ContentType: ctype, SizeBytes: lr.n, SHA256: sum, CreatedAt: now}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO attachments (id, uploader_id, filename, content_type, size_bytes, sha256, storage_key, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, uploaderID, a.Filename, a.ContentType, a.SizeBytes, a.SHA256, key, now)
		return err
	})
	if err != nil {
		_ = s.blob.Delete(context.WithoutCancel(ctx), key)
		return attachmentDTO{}, err
	}
	return a, nil
}

func (m *Module) uploadAttachment(w http.ResponseWriter, r *http.Request) error {
	if err := requireScope(r, domain.ScopeTicketsWrite); err != nil {
		return err
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return problems.Validation(problems.Field("file", "required", "multipart/form-data with a file part"))
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return problems.PayloadTooLarge()
			}
			return problems.Validation(problems.Field("file", "required", "a file part is required"))
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		a, err := m.svc.StoreAttachment(r.Context(), principal(r).UserID, part.FileName(), part)
		_ = part.Close()
		if err != nil {
			return err
		}
		area := "agent"
		if principal(r).IsCustomer() {
			area = "portal"
		}
		a.URL = "/api/v1/" + area + "/attachments/" + a.ID
		writeJSON(w, http.StatusCreated, a)
		return nil
	}
}

type attachmentFile struct {
	filename, contentType, key string
	created                    time.Time
}

// staffAttachment: drafts only to their uploader; attached files through the
// ticket visibility predicate.
func (s *Service) staffAttachment(ctx context.Context, p *domain.Principal, id string) (attachmentFile, error) {
	pred, pargs := authz.TicketPredicate(p, s.Policy(ctx), "t")
	var f attachmentFile
	err := s.db.Read().QueryRowContext(ctx, `SELECT a.filename, a.content_type, a.storage_key, a.created_at FROM attachments a
		LEFT JOIN tickets t ON t.id = a.ticket_id
		WHERE a.id = ? AND ((a.message_id IS NULL AND a.uploader_id = ?) OR (a.message_id IS NOT NULL AND `+pred+`))`,
		append([]any{id, p.UserID}, pargs...)...).Scan(&f.filename, &f.contentType, &f.key, &f.created)
	return f, store.NotFound(err)
}

// inlineTypes may render in the browser; everything else downloads.
var inlineTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func (m *Module) downloadAttachment(w http.ResponseWriter, r *http.Request) error {
	if err := readScope(r); err != nil {
		return err
	}
	p := principal(r)
	id := urlParam(r, "id")
	if !validID(id) {
		return problems.NotFound()
	}
	var f attachmentFile
	var err error
	if p.IsCustomer() {
		f, err = m.svc.customerAttachment(r.Context(), p, id)
	} else {
		f, err = m.svc.staffAttachment(r.Context(), p, id)
	}
	if err != nil {
		return err
	}
	rc, _, err := m.svc.blob.Open(r.Context(), f.key)
	if err != nil {
		return err
	}
	defer rc.Close()
	mt, _, _ := mime.ParseMediaType(f.contentType)
	disp := "attachment"
	if inlineTypes[mt] {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", f.contentType)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": f.filename}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", f.created, rc)
	return nil
}

// ---- hard delete (TKT-031) ----

const blobDeleteJob = "tickets.blob_delete"

// HardDelete removes a ticket and everything attached to it (admin only, V1).
// Blobs are deleted after commit; a failed delete is retried through the
// queue's deferred jobs. The audit_log keeps {number, subject_hash}.
func (s *Service) HardDelete(ctx context.Context, p *domain.Principal, id, ip string) ([]string, error) {
	if err := s.warm(ctx); err != nil {
		return nil, err
	}
	pol := s.Policy(ctx)
	var keys []string
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := authz.Can(p, authz.ActDelete, t.view(pol, s.clock())); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT storage_key FROM attachments WHERE ticket_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, k)
		}
		rows.Close()
		// Split copies share blobs: keep keys still referenced by another ticket.
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT INTO purge_allow (tbl, key) VALUES ('ticket_events', ?)`, []any{id}},
			{`DELETE FROM ticket_events WHERE ticket_id = ?`, []any{id}},
			{`DELETE FROM purge_allow WHERE tbl = 'ticket_events' AND key = ?`, []any{id}},
			{`UPDATE tickets SET followup_of_id = NULL WHERE followup_of_id = ?`, []any{id}},
			{`UPDATE ticket_messages SET split_to_ticket_id = NULL WHERE split_to_ticket_id = ?`, []any{id}},
			{`DELETE FROM tickets WHERE id = ?`, []any{id}},
		}
		for _, st := range stmts {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		if err := store.UnindexTicket(ctx, tx, id); err != nil {
			return err
		}
		h := sha256.Sum256([]byte(t.Subject))
		data, _ := json.Marshal(map[string]any{"number": t.Number, "subject_hash": hex.EncodeToString(h[:])})
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, 'ticket.deleted', 'ticket', ?, ?, ?, ?)`,
			store.NewID(), p.UserID, id, string(data), ip, s.clock())
		if err != nil {
			return err
		}
		var still []string
		for _, k := range keys {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE storage_key = ?`, k).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				still = append(still, k)
			}
		}
		keys = still
		return nil
	})
	return keys, err
}

func (m *Module) agentDelete(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	keys, err := m.svc.HardDelete(r.Context(), p, urlParam(r, "id"), server.ClientIP(r))
	if err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	for _, k := range keys {
		if err := m.svc.blob.Delete(ctx, k); err != nil && !errors.Is(err, blob.ErrNotFound) {
			if m.q != nil {
				_ = m.q.Enqueue(ctx, blobDeleteJob, map[string]string{"key": k})
			} else {
				m.svc.log.Warn("tickets: blob delete failed", "key", k, "err", err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (m *Module) registerAttachJobs(q *queue.Queue) {
	m.q = q
	q.Handle(blobDeleteJob, m.blobDeleteJob)
}

// blobDeleteJob retries a blob delete that failed after a hard delete.
func (m *Module) blobDeleteJob(ctx context.Context, payload json.RawMessage) error {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.Key == "" {
		return nil
	}
	if err := m.svc.blob.Delete(ctx, p.Key); err != nil && !errors.Is(err, blob.ErrNotFound) {
		return err
	}
	return nil
}
