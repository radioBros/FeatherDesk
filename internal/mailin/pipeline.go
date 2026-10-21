// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/mailin/parse"
	"github.com/radioBros/SimpleTickets/internal/notify/thread"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Inbound message statuses and reasons. The schema has no "pending" status,
// so a recorded-but-unprocessed message is status=failed, reason=pending.
const (
	statusProcessed   = "processed"
	statusQuarantined = "quarantined"
	statusFailed      = "failed"
	statusIgnored     = "ignored"

	reasonPending  = "pending"
	reasonRetrying = "retrying: "
)

// originalEMLMax caps the original.eml attachment (NOT-026).
const originalEMLMax = 2 << 20

// Recorded is the outcome of Record.
type Recorded struct {
	ID        string
	Duplicate bool
}

// headerMessageID extracts the Message-ID from raw headers only.
func headerMessageID(raw []byte) (msgID, from, subject string) {
	tp := textproto.NewReader(bufio.NewReader(io.LimitReader(bytes.NewReader(raw), 256<<10)))
	h, _ := tp.ReadMIMEHeader()
	msgID = strings.TrimSpace(h.Get("Message-Id"))
	if ids := thread.SplitReferences(msgID); len(ids) > 0 {
		msgID = ids[0]
	}
	if a, err := mail.ParseAddress(h.Get("From")); err == nil {
		from = strings.ToLower(a.Address)
	} else {
		from = trunc(h.Get("From"), 320)
	}
	dec := new(mailWordDecoder)
	subject = trunc(dec.decode(h.Get("Subject")), 998)
	return
}

// Record stores the raw bytes in the BlobStore and records the message once
// in inbound_messages (before any processing). A duplicate Message-ID for the
// same mailbox returns Duplicate=true and stores nothing.
func (m *Module) Record(ctx context.Context, mb *mailbox, raw []byte) (Recorded, error) {
	msgID, from, subject := headerMessageID(raw)
	if msgID == "" {
		sum := sha256.Sum256(raw)
		msgID = "sha256:" + hex.EncodeToString(sum[:])
	}
	msgID = trunc(msgID, 998)
	var existing string
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM inbound_messages WHERE mailbox_id = ? AND message_id = ?`, mb.ID, msgID).Scan(&existing)
	if err == nil {
		return Recorded{ID: existing, Duplicate: true}, nil
	}
	if !errors.Is(store.NotFound(err), store.ErrNotFound) {
		return Recorded{}, err
	}
	now := store.Now()
	key := blob.NewKey("inbound", now)
	if _, err := m.d.Blob.Put(ctx, key, bytes.NewReader(raw), int64(len(raw)), "message/rfc822"); err != nil {
		return Recorded{}, fmt.Errorf("inbound: store raw: %w", err)
	}
	id := store.NewID()
	err = m.d.DB.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO inbound_messages (id, mailbox_id, message_id, from_addr, subject, status, reason, raw_blob_key, received_at, created_at) VALUES (?, ?, ?, ?, ?, 'failed', ?, ?, ?, ?)`,
			id, mb.ID, msgID, from, subject, reasonPending, key, now, now)
		return err
	})
	if err != nil {
		_ = m.d.Blob.Delete(context.WithoutCancel(ctx), key)
		if store.IsUniqueViolation(err) {
			var dup string
			_ = m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM inbound_messages WHERE mailbox_id = ? AND message_id = ?`, mb.ID, msgID).Scan(&dup)
			return Recorded{ID: dup, Duplicate: true}, nil
		}
		return Recorded{}, err
	}
	return Recorded{ID: id}, nil
}

// Receive records raw for mb and schedules processing.
func (m *Module) Receive(ctx context.Context, mb *mailbox, raw []byte) (Recorded, error) {
	rec, err := m.Record(ctx, mb, raw)
	if err != nil || rec.Duplicate {
		return rec, err
	}
	m.enqueue(ctx, rec.ID)
	return rec, nil
}

type inboundRow struct {
	ID, MailboxID, MessageID, Status, Reason, BlobKey string
	TicketID                                          sql.NullString
}

// outcome is the result of the rules for one message.
type outcome struct {
	status, reason      string
	ticketID, messageID string
}

// retryable marks a processing error that should be retried by the queue.
type retryable struct{ err error }

func (r retryable) Error() string { return r.err.Error() }
func (r retryable) Unwrap() error { return r.err }

// Process runs the pipeline for one recorded message. retry=true is an admin
// retry (bypasses the per-sender rate limit). Processed rows are left alone.
// The returned error is non-nil only for transient failures (queue retries).
func (m *Module) Process(ctx context.Context, id string, retry bool) error {
	var r inboundRow
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id, mailbox_id, message_id, status, reason, raw_blob_key, ticket_id FROM inbound_messages WHERE id = ?`, id).
		Scan(&r.ID, &r.MailboxID, &r.MessageID, &r.Status, &r.Reason, &r.BlobKey, &r.TicketID)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return nil
		}
		return err
	}
	if r.Status == statusProcessed {
		return nil
	}
	if !retry && (r.Status != statusFailed || (r.Reason != reasonPending && !strings.HasPrefix(r.Reason, reasonRetrying))) {
		return nil // already decided (queue replay)
	}
	unlock := m.locks.Lock(r.MessageID)
	defer unlock()

	out, perr := m.run(ctx, &r, retry)
	var rt retryable
	if perr != nil {
		reason := perr.Error()
		if errors.As(perr, &rt) {
			reason = reasonRetrying + reason
		}
		out = outcome{status: statusFailed, reason: trunc(reason, 500)}
	}
	if uerr := m.setOutcome(ctx, r.ID, out); uerr != nil {
		return uerr
	}
	if errors.As(perr, &rt) {
		return perr
	}
	return nil
}

func (m *Module) setOutcome(ctx context.Context, id string, o outcome) error {
	return m.d.DB.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inbound_messages SET status = ?, reason = ?, ticket_id = ?, message_row_id = ? WHERE id = ?`,
			o.status, o.reason, store.Nullable(o.ticketID), store.Nullable(o.messageID), id)
		return err
	})
}

var subjectNumber = regexp.MustCompile(`\[#(\d{1,12})\]`)

// subjectRef matches the D-20 reference tag "[KEY-123]" (any letter case:
// mail clients and people retype subjects).
var subjectRef = regexp.MustCompile(`\[([A-Za-z][A-Za-z0-9]{1,9}-[1-9]\d{0,11})\]`)

func (m *Module) run(ctx context.Context, r *inboundRow, retry bool) (outcome, error) {
	mb, err := m.getMailbox(ctx, r.MailboxID)
	if err != nil {
		return outcome{}, err
	}
	raw, err := m.readBlob(ctx, r.BlobKey)
	if err != nil {
		return outcome{}, retryable{fmt.Errorf("read raw: %w", err)}
	}
	msg, err := parse.Parse(raw, parse.Options{MaxBytes: m.maxBytes})
	if err != nil || msg == nil {
		return outcome{status: statusFailed, reason: "parse_error"}, nil
	}
	if msg.From == nil || msg.From.Address == "" {
		return outcome{status: statusIgnored, reason: "no_sender"}, nil
	}
	from := strings.ToLower(strings.TrimSpace(msg.From.Address))

	// 1. Loop and noise protection (NOT-029).
	if reason, auto := parse.AutoReply(msg.Header); auto {
		if reason != "list_id" || !mb.IMAP.AllowListMail {
			return outcome{status: statusIgnored, reason: reason}, nil
		}
	}
	if m.isOwn(ctx, mb, from, msg.MessageID) {
		return outcome{status: statusIgnored, reason: "own_message"}, nil
	}
	// 2. Blocklist.
	em, err := m.d.Settings.Email(ctx)
	if err != nil {
		return outcome{}, retryable{err}
	}
	if blocked(em.InboundBlocklist, from) {
		return outcome{status: statusIgnored, reason: "blocked"}, nil
	}
	// 3. Cross-mailbox duplicate (same Message-ID via IMAP and webhook).
	var other string
	err = m.d.DB.Read().QueryRowContext(ctx, `SELECT id FROM inbound_messages WHERE message_id = ? AND id <> ? AND status = 'processed' LIMIT 1`, r.MessageID, r.ID).Scan(&other)
	if err == nil {
		return outcome{status: statusIgnored, reason: "duplicate"}, nil
	}
	// 4. Per-sender rate limit.
	if !retry && !m.limiter.Allow(from, m.ratePerHour, m.now()) {
		return outcome{status: statusQuarantined, reason: "rate_limited"}, nil
	}
	// 5. Sender.
	res := m.senderResolver()
	snd, err := res.ResolveSender(ctx, from, "", false)
	if err != nil {
		return outcome{}, retryable{err}
	}
	if snd == nil {
		sup, err := m.d.Settings.Support(ctx)
		if err != nil {
			return outcome{}, retryable{err}
		}
		if !sup.SelfRegistration {
			return outcome{status: statusQuarantined, reason: "unknown_sender"}, nil
		}
		if snd, err = res.ResolveSender(ctx, from, msg.From.Name, true); err != nil {
			return outcome{}, retryable{err}
		}
		if snd == nil {
			return outcome{status: statusQuarantined, reason: "unknown_sender"}, nil
		}
	}
	if snd.Status == "disabled" {
		return outcome{status: statusQuarantined, reason: "disabled_sender"}, nil
	}
	// 6. Threading (NOT-027).
	ticketID, err := m.findTicket(ctx, msg, snd, from)
	if err != nil {
		return outcome{}, retryable{err}
	}
	body := msg.Text
	if stripped, ok := parse.StripQuotes(body); ok && strings.TrimSpace(stripped) != "" {
		body = stripped
	}
	body = strings.TrimSpace(body)
	atts := m.attachments(msg, raw, r.ID)

	sink := m.ticketSink()
	if snd.Kind == "staff" {
		if ticketID == "" {
			return outcome{status: statusQuarantined, reason: "staff_new_ticket"}, nil
		}
		trusted := append([]string(nil), m.trustedAuthserv...)
		if t := strings.ToLower(strings.TrimSpace(mb.IMAP.TrustedAuthservID)); t != "" {
			trusted = append(trusted, t)
		}
		_, fromDomain, _ := strings.Cut(from, "@")
		ok, err := m.staffAuthenticated(ctx, msg.Header, trusted, from, fromDomain)
		if err != nil {
			return outcome{}, retryable{err}
		}
		if !ok {
			return outcome{status: statusQuarantined, reason: "staff_spoof"}, nil
		}
		internal := false
		if first, rest, _ := strings.Cut(body, "\n"); strings.EqualFold(strings.TrimSpace(first), "#internal") {
			internal, body = true, strings.TrimSpace(rest)
		}
		if sink == nil {
			return outcome{status: statusFailed, reason: "no_sink"}, nil
		}
		er, err := sink.ReplyFromEmail(ctx, EmailReply{InboundMessageID: r.ID, TicketID: ticketID, AuthorID: snd.UserID, Internal: internal, BodyMD: body, Attachments: atts})
		if err != nil {
			return outcome{}, retryable{fmt.Errorf("sink: %w", err)}
		}
		return processed(er, ticketID), nil
	}
	if sink == nil {
		return outcome{status: statusFailed, reason: "no_sink"}, nil
	}
	if ticketID != "" {
		er, err := sink.ReplyFromEmail(ctx, EmailReply{InboundMessageID: r.ID, TicketID: ticketID, AuthorID: snd.UserID, BodyMD: body, Attachments: atts})
		if err != nil {
			return outcome{}, retryable{fmt.Errorf("sink: %w", err)}
		}
		return processed(er, ticketID), nil
	}
	subject := strings.TrimSpace(msg.Subject)
	if subject == "" {
		subject = "(no subject)"
	}
	if len([]rune(subject)) > 200 {
		subject = string([]rune(subject)[:200])
	}
	er, err := sink.CreateFromEmail(ctx, EmailTicket{
		InboundMessageID: r.ID, MailboxID: mb.ID, TeamID: m.effectiveTeam(ctx, mb), ProjectID: mb.ProjectID, RequesterID: snd.UserID,
		CCUserIDs: m.ccUsers(ctx, msg, snd), Subject: subject, BodyMD: body, Attachments: atts,
	})
	if err != nil {
		return outcome{}, retryable{fmt.Errorf("sink: %w", err)}
	}
	return processed(er, ""), nil
}

func processed(er EmailResult, fallbackTicket string) outcome {
	t := er.TicketID
	if t == "" {
		t = fallbackTicket
	}
	return outcome{status: statusProcessed, ticketID: t, messageID: er.MessageID}
}

func (m *Module) readBlob(ctx context.Context, key string) ([]byte, error) {
	rc, _, err := m.d.Blob.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, m.maxBytes+1))
}

// isOwn: mail from our own sender identities / mailbox addresses, or carrying
// a Message-ID on our own outgoing domain, never creates tickets.
func (m *Module) isOwn(ctx context.Context, mb *mailbox, from, msgID string) bool {
	if ids := thread.SplitReferences(msgID); len(ids) > 0 {
		id := strings.Trim(ids[0], "<>")
		if _, d, ok := strings.Cut(id, "@"); ok && strings.EqualFold(d, m.host) {
			return true
		}
	}
	own := map[string]bool{strings.ToLower(mb.Address): true}
	if em, err := m.d.Settings.Email(ctx); err == nil {
		for _, a := range []string{em.CustomerFrom.Address, em.CustomerFrom.ReplyTo, em.StaffFrom.Address, em.StaffFrom.ReplyTo, em.TriageMailbox} {
			if a != "" {
				own[strings.ToLower(a)] = true
			}
		}
	}
	if own[from] {
		return true
	}
	// The plus-address variant of an own address (support+t-…@acme.com).
	if local, domain, ok := strings.Cut(from, "@"); ok {
		if i := strings.IndexByte(local, '+'); i > 0 && own[local[:i]+"@"+domain] {
			return true
		}
	}
	return false
}

type ticketRef struct {
	ID, RequesterID, OrgID string
}

func (m *Module) loadTicket(ctx context.Context, where string, args ...any) (*ticketRef, error) {
	var t ticketRef
	var org sql.NullString
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id, requester_id, organization_id FROM tickets WHERE `+where, args...).Scan(&t.ID, &t.RequesterID, &org)
	if err != nil {
		if errors.Is(store.NotFound(err), store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	t.OrgID = org.String
	return &t, nil
}

// participant: a customer may reply to a ticket only as its requester or a
// CC watcher; staff are checked separately (Authentication-Results).
func (m *Module) participant(ctx context.Context, t *ticketRef, s *Sender) (bool, error) {
	if s.Kind == "staff" {
		return true, nil
	}
	if t.RequesterID == s.UserID {
		return true, nil
	}
	var one int
	err := m.d.DB.Read().QueryRowContext(ctx, `SELECT 1 FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?`, t.ID, s.UserID).Scan(&one)
	if err == nil {
		return true, nil
	}
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// findTicket applies D-16 threading in order: own References/In-Reply-To →
// plus-address token → "[#n]" with a known participant → "" (new ticket).
func (m *Module) findTicket(ctx context.Context, msg *parse.Message, s *Sender, from string) (string, error) {
	try := func(t *ticketRef) (string, error) {
		if t == nil {
			return "", nil
		}
		ok, err := m.participant(ctx, t, s)
		if err != nil || !ok {
			return "", err
		}
		return t.ID, nil
	}
	// 1. References / In-Reply-To matching our own ids.
	refs := append(append([]string(nil), msg.InReplyTo...), msg.References...)
	for i := len(refs) - 1; i >= 0; i-- { // newest reference last
		ref, ok := thread.ParseOwnID(refs[i], m.host)
		if !ok {
			continue
		}
		var t *ticketRef
		var err error
		if ref.TicketID != "" {
			t, err = m.loadTicket(ctx, `id = ?`, ref.TicketID)
		} else {
			var tid string
			e := m.d.DB.Read().QueryRowContext(ctx, `SELECT ticket_id FROM ticket_events WHERE id = ?`, ref.EventID).Scan(&tid)
			if e == nil {
				t, err = m.loadTicket(ctx, `id = ?`, tid)
			} else if !errors.Is(store.NotFound(e), store.ErrNotFound) {
				err = e
			}
		}
		if err != nil {
			return "", err
		}
		if id, err := try(t); err != nil || id != "" {
			return id, err
		}
	}
	// 2. Plus-address token in To/Cc/Delivered-To/X-Original-To.
	if m.d.Secrets != nil {
		for _, a := range recipientAddrs(msg) {
			tok, ok := thread.TokenFromAddress(a)
			if !ok {
				continue
			}
			tid, _, ok := thread.VerifyPlusToken(m.d.Secrets, tok)
			if !ok {
				continue // tampered: fall through, never to another ticket
			}
			t, err := m.loadTicket(ctx, `id = ?`, tid)
			if err != nil {
				return "", err
			}
			if id, err := try(t); err != nil || id != "" {
				return id, err
			}
		}
	}
	// 3. "[KEY-123]" (D-20) or "[#n]" in the subject, sender must be
	// requester or watcher.
	for _, mm := range subjectRef.FindAllStringSubmatch(msg.Subject, 3) {
		key, seq, ok := domain.ParseTicketRef(mm[1])
		if !ok {
			continue
		}
		t, err := m.loadTicket(ctx, `project_id = (SELECT id FROM projects WHERE key = ?) AND project_seq = ?`, key, seq)
		if err != nil {
			return "", err
		}
		if id, err := try(t); err != nil || id != "" {
			return id, err
		}
	}
	if mm := subjectNumber.FindStringSubmatch(msg.Subject); mm != nil {
		n, _ := strconv.ParseInt(mm[1], 10, 64)
		t, err := m.loadTicket(ctx, `number = ?`, n)
		if err != nil {
			return "", err
		}
		if id, err := try(t); err != nil || id != "" {
			return id, err
		}
	}
	_ = from
	return "", nil
}

func recipientAddrs(msg *parse.Message) []string {
	var out []string
	for _, a := range msg.To {
		out = append(out, a.Address)
	}
	for _, a := range msg.Cc {
		out = append(out, a.Address)
	}
	for _, k := range []string{"Delivered-To", "X-Original-To", "Envelope-To"} {
		for _, v := range msg.Header[k] {
			if as, err := mail.ParseAddressList(v); err == nil {
				for _, a := range as {
					out = append(out, a.Address)
				}
			} else {
				out = append(out, strings.Trim(strings.TrimSpace(v), "<>"))
			}
		}
	}
	return out
}

// ccUsers returns known customers in Cc who belong to the requester's
// organization (watchers must share the ticket organization).
func (m *Module) ccUsers(ctx context.Context, msg *parse.Message, s *Sender) []string {
	if s.OrganizationID == "" {
		return nil
	}
	var out []string
	for _, a := range msg.Cc {
		var id, org string
		err := m.d.DB.Read().QueryRowContext(ctx, `SELECT id, COALESCE(organization_id, '') FROM users WHERE lower(email) = ? AND kind = 'customer' AND status <> 'disabled'`, strings.ToLower(a.Address)).Scan(&id, &org)
		if err == nil && org == s.OrganizationID && id != s.UserID {
			out = append(out, id)
		}
	}
	return out
}

func (m *Module) attachments(msg *parse.Message, raw []byte, inboundID string) []EmailAttachment {
	out := make([]EmailAttachment, 0, len(msg.Attachments)+1)
	for _, a := range msg.Attachments {
		out = append(out, EmailAttachment{Filename: a.Filename, ContentType: a.ContentType, ContentID: a.ContentID, Inline: a.Inline, Data: a.Data})
	}
	if len(raw) <= originalEMLMax {
		out = append(out, EmailAttachment{Filename: "original.eml", ContentType: "message/rfc822", Data: raw})
	} else {
		note := fmt.Sprintf("The original message (%d bytes) exceeds the 2 MB original.eml limit. It is kept as inbound message %s.\n", len(raw), inboundID)
		out = append(out, EmailAttachment{Filename: "original.eml.txt", ContentType: "text/plain", Data: []byte(note)})
	}
	return out
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// keep valid UTF-8
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
