// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/notify/thread"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Job kinds owned by this module (deferred_jobs.kind).
const (
	JobSend      = "notify.send"      // one rendered mail whose first attempt failed
	JobCoalesced = "notify.coalesced" // a pending coalesced mail (NOT-017)
)

// consumerTimeout bounds one event's fan-out; items not attempted before
// deadlineMargin of it are deferred instead of risking a whole-event spill
// (which would re-mail recipients already served).
const (
	consumerTimeout = 2 * time.Minute
	deadlineMargin  = 20 * time.Second
	retryDelay      = 30 * time.Second
)

// coalescible lists the templates merged per (recipient, ticket) within
// FD_NOTIFY_COALESCE_SECONDS. Only chatty staff notifications; never auth.*.
var coalescible = map[string]bool{TplStaffCustomerReplied: true}

// handleEvent is the durable "notify" consumer: resolve recipients from the
// snapshot, render, and make one in-memory delivery attempt per mail. It
// never fails the event because of a single recipient: failed mails are
// persisted once as JobSend rows (attempts=1) and retried by the queue.
func (m *Module) handleEvent(ctx context.Context, e events.Event) error {
	all, users, err := m.resolve(ctx, e)
	if err != nil {
		return err // DB trouble: let the queue spill and replay the event
	}
	// In-app first (one tx per event, idempotent on replay; independent of
	// SMTP), then email from the same resolution (NOT-036).
	if err := m.writeInApp(ctx, e, all, users); err != nil {
		return err
	}
	targets := emailTargets(all, users)
	if len(targets) == 0 {
		return nil
	}
	em, err := m.emailSettings(ctx)
	if err != nil {
		return err
	}
	ec := &evCtx{m: m, ctx: ctx, e: e}
	for _, t := range targets {
		if slices.Contains(em.DisabledTemplates, t.Key) && !strings.HasPrefix(t.Key, "auth.") {
			continue
		}
		if coalescible[t.Key] && m.coalesce > 0 {
			if cfg, _ := m.smtpConfig(ctx); cfg.Configured() {
				if err := m.coalesceAdd(ctx, t, e); err != nil {
					return err
				}
				continue
			}
		}
		ml, err := ec.compose(t, em)
		if err != nil {
			m.log.Error("notify: render failed", "event", e.ID, "template", t.Key, "err", err)
			continue
		}
		if err := m.deliver(ctx, t.Key, ml); err != nil {
			return err
		}
	}
	return nil
}

// deliver makes the first attempt and persists failures once. It returns an
// error only when even the deferral could not be written.
func (m *Module) deliver(ctx context.Context, key string, ml *Mail) error {
	cfg, err := m.smtpConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		// NOT-022: email-disabled mode. Subject and recipient only: never the
		// body, links or tokens.
		m.log.Info("email disabled: mail not sent", "template", key, "to", ml.To.Address, "subject", ml.Subject)
		return nil
	}
	if ml.MessageID != "" && m.sent.has(ml.MessageID) {
		return nil // replayed event: already delivered by this process
	}
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < deadlineMargin {
		_, err := m.deferMail(ctx, key, ml, 0, "retrying", "deferred: fan-out deadline", m.now())
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, cfg.Timeout+5*time.Second)
	err = m.sender.Send(sctx, cfg, ml)
	cancel()
	if err == nil {
		m.markSent(ml)
		return nil
	}
	m.recordFailure(err)
	status := "retrying"
	if IsPermanent(err) {
		status = "dead"
	}
	_, derr := m.deferMail(ctx, key, ml, 1, status, err.Error(), m.now().Add(jitter(retryDelay)))
	return derr
}

func (m *Module) markSent(ml *Mail) {
	if ml.MessageID != "" {
		m.sent.add(ml.MessageID)
	}
	m.setLastErr("")
}

func (m *Module) recordFailure(err error) {
	var f *SendFailure
	if errors.As(err, &f) {
		m.setLastErr(f.Code + ": " + f.Err.Error())
		return
	}
	m.setLastErr(err.Error())
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// sendJob is the JobSend payload.
type sendJob struct {
	Template string `json:"template"`
	Mail     Mail   `json:"mail"`
}

// deferMail writes one deferred_jobs row (the only DB write of the mail path).
func (m *Module) deferMail(ctx context.Context, key string, ml *Mail, attempts int, status, lastErr string, next time.Time) (string, error) {
	b, err := json.Marshal(sendJob{Template: key, Mail: *ml})
	if err != nil {
		return "", err
	}
	return m.insertJob(ctx, JobSend, b, attempts, status, lastErr, next)
}

func (m *Module) insertJob(ctx context.Context, kind string, payload []byte, attempts int, status, lastErr string, next time.Time) (string, error) {
	id := store.NewID()
	if len(lastErr) > 2000 {
		lastErr = lastErr[:2000]
	}
	err := m.db.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		now := store.Now()
		_, err := tx.ExecContext(ctx, `INSERT INTO deferred_jobs (id, kind, payload, attempts, max_attempts, next_run_at, status, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, 8, ?, ?, ?, ?, ?)`,
			id, kind, string(payload), attempts, next.UTC().Truncate(time.Microsecond), status, lastErr, now, now)
		return err
	})
	return id, err
}

// handleSendJob retries one persisted mail. A permanent rejection on a
// retry records a dead row (visible in /admin/jobs) and ends the retries.
func (m *Module) handleSendJob(ctx context.Context, payload json.RawMessage) error {
	var j sendJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return fmt.Errorf("notify.send: bad payload: %w", err)
	}
	cfg, err := m.smtpConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		m.log.Info("email disabled: mail not sent", "template", j.Template, "to", j.Mail.To.Address, "subject", j.Mail.Subject)
		return nil
	}
	if err := m.sender.Send(ctx, cfg, &j.Mail); err != nil {
		m.recordFailure(err)
		if IsPermanent(err) {
			_, derr := m.insertJob(ctx, JobSend, payload, 1, "dead", err.Error(), m.now())
			return derr
		}
		return err
	}
	m.markSent(&j.Mail)
	return nil
}

func (m *Module) emailSettings(ctx context.Context) (settings.EmailView, error) {
	if m.set == nil {
		return settings.EmailView{}, nil
	}
	return m.set.Email(ctx)
}

// ---- composing ----

type msgRow struct {
	ID, AuthorID, Kind, Body string
}

// evCtx caches per-event lookups while composing several mails.
type evCtx struct {
	m   *Module
	ctx context.Context
	e   events.Event

	loaded   bool
	brand    Brand
	support  settings.Support
	teamName string
	reqName  string
	actor    *userRow
	msg      *msgRow
	author   *userRow
}

func (c *evCtx) load() error {
	if c.loaded {
		return nil
	}
	c.loaded = true
	m, ctx, e := c.m, c.ctx, c.e
	var err error
	if c.brand, err = m.brand(ctx); err != nil {
		return err
	}
	if c.support, err = m.support(ctx); err != nil {
		return err
	}
	if t, err := m.team(ctx, e.Snapshot.TeamID); err == nil {
		c.teamName = t.Name
	}
	if u, err := m.user(ctx, e.Snapshot.RequesterID); err == nil {
		c.reqName = displayName(u)
	}
	if u, err := m.user(ctx, e.ActorID); err == nil {
		c.actor = u
	}
	var ref events.MessageRef
	_ = e.Decode(&ref)
	if ref.MessageID != "" {
		c.msg, _ = m.message(ctx, ref.MessageID)
	} else if e.Type == events.TicketCreated {
		c.msg, _ = m.firstPublicMessage(ctx, e.TicketID)
	}
	if c.msg != nil && c.msg.AuthorID != "" {
		c.author, _ = m.user(ctx, c.msg.AuthorID)
	}
	return nil
}

func displayName(u *userRow) string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.Name) != "" {
		return u.Name
	}
	return u.Email
}

func (m *Module) brand(ctx context.Context) (Brand, error) {
	if m.set == nil {
		return BrandFrom(settings.DefaultBranding(), m.publicURL), nil
	}
	b, err := m.set.Branding(ctx)
	if err != nil {
		return Brand{}, err
	}
	return BrandFrom(b, m.publicURL), nil
}

func (m *Module) message(ctx context.Context, id string) (*msgRow, error) {
	var r msgRow
	var author sql.NullString
	err := m.db.Read().QueryRowContext(ctx, `SELECT id, author_id, kind, body_md FROM ticket_messages WHERE id = ?`, id).Scan(&r.ID, &author, &r.Kind, &r.Body)
	if err != nil {
		return nil, err
	}
	r.AuthorID = author.String
	return &r, nil
}

func (m *Module) firstPublicMessage(ctx context.Context, ticketID string) (*msgRow, error) {
	var r msgRow
	var author sql.NullString
	err := m.db.Read().QueryRowContext(ctx, `SELECT id, author_id, kind, body_md FROM ticket_messages WHERE ticket_id = ? AND kind = 'public' ORDER BY created_at, id LIMIT 1`, ticketID).Scan(&r.ID, &author, &r.Kind, &r.Body)
	if err != nil {
		return nil, err
	}
	r.AuthorID = author.String
	return &r, nil
}

func (m *Module) tierLabel(sup settings.Support, tier int) string {
	if l := sup.TierLabels[fmt.Sprint(tier)]; l != "" {
		return l
	}
	return fmt.Sprintf("L%d", tier)
}

func (c *evCtx) actorName() string {
	if c.actor != nil {
		return displayName(c.actor)
	}
	return c.brand.ProductName
}

func (c *evCtx) staffView() StaffTicketView {
	s := c.e.Snapshot
	team := c.teamName
	if team == "" {
		team = "—"
	}
	return StaffTicketView{ID: c.e.TicketID, Number: s.Number, Ref: viewRef(s.Ref, s.Number), Subject: s.Subject, Priority: s.Priority, Status: s.Status,
		TierLabel: c.m.tierLabel(c.support, s.Tier), TeamName: team, RequesterName: c.reqName, URL: c.m.agentURL + "/tickets/" + c.e.TicketID}
}

// data builds the template data for one target. Customer keys only ever
// get CustomerData built from public fields (I-7).
func (c *evCtx) data(t Target) (branded, error) {
	if err := c.load(); err != nil {
		return nil, err
	}
	e, s := c.e, c.e.Snapshot
	name := t.R.Name
	if strings.HasPrefix(t.Key, "customer.") {
		d := &CustomerData{RecipientName: name, Ticket: CustomerTicketView{Number: s.Number, Ref: viewRef(s.Ref, s.Number), Subject: s.Subject, URL: c.m.portalURL + "/tickets/" + e.TicketID}}
		switch t.Key {
		case TplCustomerReply:
			if c.msg == nil || c.msg.Kind != "public" {
				return nil, errors.New("customer.reply without a public message")
			}
			d.Message = CustomerMessageView{AuthorName: displayName(c.author), Body: c.msg.Body}
			if c.author != nil && c.author.Kind == "staff" {
				d.Signature = c.author.Signature
			}
		case TplCustomerResolved:
			d.CSATURL = d.Ticket.URL + "?rate=1"
		}
		return d, nil
	}
	d := &StaffData{RecipientName: name, Ticket: c.staffView(), ActorName: c.actorName()}
	if c.msg != nil && (t.Key == TplStaffTicketNew || t.Key == TplStaffCustomerReplied || t.Key == TplStaffMentioned) {
		d.Messages = []StaffMessageView{{AuthorName: displayName(c.author), Body: c.msg.Body}}
	}
	switch t.Key {
	case TplStaffEscalated:
		var tc events.TierChange
		_ = e.Decode(&tc)
		d.Reason = tc.Reason
		d.FromTier, d.ToTier = c.m.tierLabel(c.support, tc.FromTier), c.m.tierLabel(c.support, tc.ToTier)
	case TplStaffSLA:
		var sl events.SLA
		_ = e.Decode(&sl)
		d.SLAWhich, d.SLABreached = sl.Which, e.Type == events.SLABreached
		if !sl.DueAt.IsZero() {
			d.SLADue = sl.DueAt.UTC().Format("2006-01-02 15:04")
		}
	}
	return d, nil
}

func (c *evCtx) compose(t Target, em settings.EmailView) (*Mail, error) {
	d, err := c.data(t)
	if err != nil {
		return nil, err
	}
	locale := c.m.recipientLocale(t.R, c.support)
	r, err := c.m.tpl.Render(t.Key, locale, c.brand, d)
	if err != nil {
		return nil, err
	}
	return c.m.envelope(t, r, c.e.ID, c.e.TicketID, c.brand, em), nil
}

func (m *Module) recipientLocale(r Recipient, sup settings.Support) string {
	if r.Locale != "" && !r.Mailbox {
		return m.tpl.Locale(r.Locale, sup.DefaultLocale)
	}
	return m.tpl.Locale(sup.DefaultLocale)
}

// envelope sets From/Reply-To by audience (D-12), the deterministic
// Message-ID and the ticket thread headers.
func (m *Module) envelope(t Target, r Rendered, eventID, ticketID string, brand Brand, em settings.EmailView) *Mail {
	id := em.StaffFrom
	if t.R.Audience == "customer" {
		id = em.CustomerFrom
	}
	from := mail.Address{Name: id.Name, Address: strings.TrimSpace(id.Address)}
	if from.Name == "" {
		from.Name = brand.ProductName
	}
	if from.Address == "" {
		from.Address = "noreply@" + m.host
	}
	ml := &Mail{From: from, To: mail.Address{Name: t.R.Name, Address: t.R.Addr}, Subject: r.Subject, Text: r.Text, HTML: r.HTML,
		ReplyTo: strings.TrimSpace(id.ReplyTo)}
	if eventID != "" {
		ml.MessageID = thread.MessageID(eventID, t.R.Addr, m.host)
	} else {
		ml.MessageID = thread.MessageID(store.NewID(), t.R.Addr, m.host)
	}
	if ticketID != "" {
		ml.References = thread.TicketThreadID(ticketID, m.host)
		ml.InReplyTo = ml.References
		if em.PlusAddressing && m.box != nil {
			base := ml.ReplyTo
			if base == "" {
				base = from.Address
			}
			if tok, err := thread.PlusToken(m.box, ticketID, t.R.Addr); err == nil {
				ml.ReplyTo = thread.PlusAddress(base, tok)
			}
		}
	}
	return ml
}

// ---- coalescing (NOT-017) ----

type pendingCoalesce struct {
	rowID   string
	payload coalescedPayload
}

type coalescedPayload struct {
	RowID     string         `json:"row_id"`
	Template  string         `json:"template"`
	TicketID  string         `json:"ticket_id"`
	Recipient Recipient      `json:"recipient"`
	Events    []events.Event `json:"events"`
}

const maxCoalesced = 50

// coalesceAdd merges e into the pending mail for (recipient, ticket,
// template), held as one deferred_jobs row due at the end of the window. The
// row is updated in place while it is safely in the future; otherwise a new
// pending row starts (the old one is about to be sent).
func (m *Module) coalesceAdd(ctx context.Context, t Target, e events.Event) error {
	key := t.Key + "|" + e.TicketID + "|" + strings.ToLower(t.R.Addr)
	m.coMu.Lock()
	defer m.coMu.Unlock()
	if p := m.pending[key]; p != nil && len(p.payload.Events) < maxCoalesced {
		next := p.payload
		next.Events = append(slices.Clone(next.Events), e)
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		var n int64
		err = m.db.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE deferred_jobs SET payload = ?, updated_at = ? WHERE id = ? AND status = 'retrying' AND attempts = 0 AND next_run_at > ?`,
				string(b), store.Now(), p.rowID, store.Now().Add(2*time.Second))
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			return nil
		})
		if err != nil {
			return err
		}
		if n == 1 {
			p.payload = next
			return nil
		}
	}
	p := &pendingCoalesce{rowID: store.NewID()}
	p.payload = coalescedPayload{RowID: p.rowID, Template: t.Key, TicketID: e.TicketID, Recipient: t.R, Events: []events.Event{e}}
	b, err := json.Marshal(p.payload)
	if err != nil {
		return err
	}
	err = m.db.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		now := store.Now()
		_, err := tx.ExecContext(ctx, `INSERT INTO deferred_jobs (id, kind, payload, attempts, max_attempts, next_run_at, status, last_error, created_at, updated_at) VALUES (?, ?, ?, 0, 8, ?, 'retrying', '', ?, ?)`,
			p.rowID, JobCoalesced, string(b), now.Add(m.coalesce), now, now)
		return err
	})
	if err != nil {
		return err
	}
	m.pending[key] = p
	return nil
}

// handleCoalescedJob sends the aggregated mail when the window ends.
func (m *Module) handleCoalescedJob(ctx context.Context, payload json.RawMessage) error {
	var p coalescedPayload
	if err := json.Unmarshal(payload, &p); err != nil || len(p.Events) == 0 {
		return fmt.Errorf("notify.coalesced: bad payload: %v", err)
	}
	m.coMu.Lock()
	for k, v := range m.pending {
		if v.rowID == p.RowID {
			delete(m.pending, k)
		}
	}
	m.coMu.Unlock()
	last := p.Events[len(p.Events)-1]
	ec := &evCtx{m: m, ctx: ctx, e: last}
	if err := ec.load(); err != nil {
		return err
	}
	d := &StaffData{RecipientName: p.Recipient.Name, Ticket: ec.staffView(), ActorName: ec.actorName()}
	for _, e := range p.Events {
		var ref events.MessageRef
		_ = e.Decode(&ref)
		if ref.MessageID == "" {
			continue
		}
		msg, err := m.message(ctx, ref.MessageID)
		if err != nil {
			continue
		}
		var author *userRow
		if msg.AuthorID != "" {
			author, _ = m.user(ctx, msg.AuthorID)
		}
		d.Messages = append(d.Messages, StaffMessageView{AuthorName: displayName(author), Body: msg.Body})
	}
	em, err := m.emailSettings(ctx)
	if err != nil {
		return err
	}
	t := Target{Key: p.Template, R: p.Recipient}
	r, err := m.tpl.Render(p.Template, m.recipientLocale(p.Recipient, ec.support), ec.brand, d)
	if err != nil {
		return err
	}
	ml := m.envelope(t, r, last.ID, p.TicketID, ec.brand, em)
	cfg, err := m.smtpConfig(ctx)
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		m.log.Info("email disabled: mail not sent", "template", p.Template, "to", ml.To.Address, "subject", ml.Subject)
		return nil
	}
	if err := m.sender.Send(ctx, cfg, ml); err != nil {
		m.recordFailure(err)
		if IsPermanent(err) {
			_, derr := m.deferMail(ctx, p.Template, ml, 1, "dead", err.Error(), m.now())
			return derr
		}
		return err
	}
	m.markSent(ml)
	return nil
}
