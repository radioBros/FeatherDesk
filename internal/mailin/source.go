// SPDX-License-Identifier: AGPL-3.0-only

package mailin

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/radioBros/SimpleTickets/internal/store"
)

// Meta describes where a delivered message came from.
type Meta struct {
	MailboxID string
	UID       uint32
}

// DeliverFunc receives one raw RFC 5322 message. A nil error means the message
// is safely recorded; the source then applies its processed action.
type DeliverFunc func(ctx context.Context, raw []byte, meta Meta) error

// Source produces inbound messages until ctx ends (D-16). go-imap/v2 is used
// only through this interface so the adapter can be swapped (v1 fallback).
type Source interface {
	Run(ctx context.Context, deliver DeliverFunc) error
}

// Timing knobs. pollInterval is the fallback for servers without IDLE and
// must stay ≥ 60 s in production (CONVENTIONS §10); tests lower it.
var (
	pollInterval   = 60 * time.Second
	reIdleInterval = 15 * time.Minute
	// idleSettle bounds the window between the last UID SEARCH and the
	// server registering the IDLE: a message arriving in it may not be
	// announced by servers that only report changes after registration.
	// One re-check this long after each wake-up closes it.
	idleSettle  = 3 * time.Second
	backoffMin  = time.Second
	backoffMax  = 5 * time.Minute
	dialTimeout = 15 * time.Second
)

// IMAPSource is the IMAP IDLE adapter for one mailbox.
type IMAPSource struct {
	m  *Module
	mb *mailbox

	// test hooks
	wrapConn func(net.Conn) net.Conn
	noIdle   bool // pretend the server lacks IDLE

	token *oauthTokens
	polls atomic.Int64 // fallback poll rounds (tests)
}

// NewIMAPSource builds the adapter for an imap mailbox.
func (m *Module) NewIMAPSource(mb *mailbox) *IMAPSource {
	return &IMAPSource{m: m, mb: mb}
}

// Run keeps one session alive, reconnecting with exponential backoff + jitter
// (1 s → 5 min). It returns when ctx ends.
func (s *IMAPSource) Run(ctx context.Context, deliver DeliverFunc) error {
	backoff := backoffMin
	for ctx.Err() == nil {
		healthy, err := s.session(ctx, deliver)
		if ctx.Err() != nil {
			return nil
		}
		if healthy {
			backoff = backoffMin
		}
		s.m.reportError(ctx, s.mb.ID, err)
		s.m.log.Warn("inbound: imap session ended", "mailbox", s.mb.ID, "err", err, "retry_in", backoff)
		d := time.Duration(float64(backoff) * (0.8 + 0.4*rand.Float64()))
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		backoff = min(backoff*2, backoffMax)
	}
	return nil
}

// dial connects and authenticates; returns the client.
func (s *IMAPSource) dial(ctx context.Context, newMail chan<- struct{}) (*imapclient.Client, error) {
	c := s.mb.IMAP
	opts := &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: c.Host, InsecureSkipVerify: c.AllowInsecureTLS, MinVersion: tls.VersionTLS12}, //nolint:gosec // admin opt-in
	}
	if newMail != nil {
		opts.UnilateralDataHandler = &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					select {
					case newMail <- struct{}{}:
					default:
					}
				}
			},
		}
	}
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if s.wrapConn != nil {
		conn = s.wrapConn(conn)
	}
	var cl *imapclient.Client
	switch c.Security {
	case "tls":
		tc := tls.Client(conn, opts.TLSConfig)
		hctx, cancel := context.WithTimeout(ctx, dialTimeout)
		err = tc.HandshakeContext(hctx)
		cancel()
		if err != nil {
			return nil, errors.Join(err, conn.Close())
		}
		cl = imapclient.New(tc, opts)
	case "starttls":
		cl, err = imapclient.NewStartTLS(conn, opts)
		if err != nil {
			conn.Close()
			return nil, err
		}
	default:
		cl = imapclient.New(conn, opts)
	}
	// Unblock any pending command when ctx ends.
	stop := context.AfterFunc(ctx, func() { _ = cl.Close() /* unblocks pending commands; nothing to report */ })
	go func() { <-cl.Closed(); stop() }()
	if err := cl.WaitGreeting(); err != nil {
		return nil, errors.Join(err, cl.Close())
	}
	if err := s.auth(ctx, cl); err != nil {
		return nil, errors.Join(err, cl.Close())
	}
	return cl, nil
}

func (s *IMAPSource) auth(ctx context.Context, cl *imapclient.Client) error {
	c := s.mb.IMAP
	if c.Auth == "xoauth2" {
		if s.token == nil {
			s.token = s.m.oauthFor(s.mb)
		}
		tok, err := s.token.Token(ctx)
		if err != nil {
			return fmt.Errorf("xoauth2 token: %w", err)
		}
		return cl.Authenticate(&xoauth2Client{user: c.Username, token: tok})
	}
	pw, err := s.m.imapPassword(s.mb)
	if err != nil {
		return err
	}
	return cl.Login(c.Username, pw).Wait()
}

// session runs one connection: select, fetch unseen, then IDLE (or poll).
// healthy reports whether login and select succeeded.
func (s *IMAPSource) session(ctx context.Context, deliver DeliverFunc) (healthy bool, err error) {
	newMail := make(chan struct{}, 1)
	cl, err := s.dial(ctx, newMail)
	if err != nil {
		return false, err
	}
	defer func() { _ = cl.Close() }() // session already over; close error adds nothing
	c := s.mb.IMAP
	if c.ProcessedAction == "move" {
		if err := cl.Create(c.ProcessedFolder, nil).Wait(); err != nil && !alreadyExists(err) {
			return false, fmt.Errorf("create %s: %w", c.ProcessedFolder, err)
		}
	}
	if _, err := cl.Select(c.Folder, nil).Wait(); err != nil {
		return false, fmt.Errorf("select %s: %w", c.Folder, err)
	}
	hasIdle := !s.noIdle && cl.Caps().Has(imap.CapIdle)
	settled := false // false until one settle re-check ran after each wake-up
	for ctx.Err() == nil {
		if err := s.fetchNew(ctx, cl, deliver); err != nil {
			return true, err
		}
		s.m.reportOK(s.mb.ID)
		if hasIdle {
			idle, err := cl.Idle()
			if err != nil {
				return true, err
			}
			wait := reIdleInterval
			if !settled {
				wait = idleSettle
			}
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
			case <-newMail:
				settled = false
			case <-t.C:
				settled = true
			case <-cl.Closed():
			}
			t.Stop()
			if err := idle.Close(); err != nil && ctx.Err() == nil {
				return true, err
			}
			if err := idle.Wait(); err != nil && ctx.Err() == nil {
				return true, err
			}
			continue
		}
		// No IDLE: poll, never faster than pollInterval.
		t := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return true, nil
		case <-cl.Closed():
			t.Stop()
			return true, errors.New("connection closed")
		case <-t.C:
		}
		s.polls.Add(1)
		if err := cl.Noop().Wait(); err != nil {
			return true, err
		}
	}
	return true, nil
}

func alreadyExists(err error) bool {
	var ie *imap.Error
	if errors.As(err, &ie) && ie.Code == imap.ResponseCodeAlreadyExists {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "exists")
}

// fetchNew delivers every UNSEEN message, then applies the processed action.
func (s *IMAPSource) fetchNew(ctx context.Context, cl *imapclient.Client, deliver DeliverFunc) error {
	sd, err := cl.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return err
	}
	for _, uid := range sd.AllUIDs() {
		if ctx.Err() != nil {
			return nil
		}
		set := imap.UIDSetNum(uid)
		meta, err := cl.Fetch(set, &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
		if err != nil {
			return err
		}
		if len(meta) == 0 {
			continue
		}
		if meta[0].RFC822Size > s.m.maxBytes {
			s.m.log.Warn("inbound: message over size cap; flagged seen, not imported", "mailbox", s.mb.ID, "uid", uid, "size", meta[0].RFC822Size)
			if err := s.markProcessed(cl, set, "flag"); err != nil {
				return err
			}
			continue
		}
		section := &imap.FetchItemBodySection{Peek: true}
		msgs, err := cl.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
		if err != nil {
			return err
		}
		if len(msgs) == 0 || len(msgs[0].BodySection) == 0 {
			continue
		}
		raw := msgs[0].BodySection[0].Bytes
		if err := deliver(ctx, raw, Meta{MailboxID: s.mb.ID, UID: uint32(uid)}); err != nil {
			s.m.log.Warn("inbound: deliver failed; left unseen", "mailbox", s.mb.ID, "uid", uid, "err", err)
			continue
		}
		if err := s.markProcessed(cl, set, s.mb.IMAP.ProcessedAction); err != nil {
			return err
		}
	}
	return nil
}

// markProcessed moves to the processed folder (MOVE, or UID-scoped
// COPY+EXPUNGE when UIDPLUS exists) or flags \Seen. Mail is never deleted:
// without MOVE and UIDPLUS it is copied and flagged \Seen instead.
func (s *IMAPSource) markProcessed(cl *imapclient.Client, set imap.UIDSet, action string) error {
	flag := func() error {
		return cl.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close()
	}
	if action != "move" {
		return flag()
	}
	caps := cl.Caps()
	if caps.Has(imap.CapMove) || caps.Has(imap.CapIMAP4rev2) || caps.Has(imap.CapUIDPlus) {
		if err := flag(); err != nil {
			return err
		}
		_, err := cl.Move(set, s.mb.IMAP.ProcessedFolder).Wait()
		return err
	}
	if _, err := cl.Copy(set, s.mb.IMAP.ProcessedFolder).Wait(); err != nil {
		return err
	}
	return flag()
}

// ---- status reporting (PLT-023 write-behind) ----

func (m *Module) reportOK(mailboxID string) {
	if m.q != nil {
		m.q.Touch("inbound_mailboxes", "last_ok_at", mailboxID, store.Now())
	}
	m.reportError(context.Background(), mailboxID, nil)
}

var lastErrors sync.Map // mailbox id → last reported error text

// reportError writes last_error only when it changed.
func (m *Module) reportError(ctx context.Context, mailboxID string, err error) {
	msg := ""
	if err != nil {
		msg = trunc(err.Error(), 1000)
	}
	key := fmt.Sprintf("%p/%s", m, mailboxID)
	if prev, ok := lastErrors.Load(key); ok && prev.(string) == msg {
		return
	}
	lastErrors.Store(key, msg)
	_ = m.d.DB.Tx(context.WithoutCancel(ctx), func(tx *store.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE inbound_mailboxes SET last_error = ? WHERE id = ? AND last_error <> ?`, msg, mailboxID, msg)
		return e
	})
}

// ---- supervisor: one goroutine per active IMAP mailbox, hot reload ----

type supervisor struct {
	m       *Module
	mu      sync.Mutex
	ctx     context.Context
	running map[string]*runningSource
	reloadC chan struct{}
}

type runningSource struct {
	hash   string
	cancel context.CancelFunc
	done   chan struct{}
}

func newSupervisor(m *Module) *supervisor {
	return &supervisor{m: m, running: map[string]*runningSource{}, reloadC: make(chan struct{}, 1)}
}

// reload asks the supervisor to re-read the mailbox list (after CRUD).
func (s *supervisor) reload() {
	select {
	case s.reloadC <- struct{}{}:
	default:
	}
}

func configHash(mb *mailbox) string {
	b := store.MustJSON(mb.IMAP)
	h := sha256.Sum256([]byte(b + "|" + mb.Address + "|" + mb.TeamID))
	return hex.EncodeToString(h[:8])
}

func (s *supervisor) run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	s.sync(ctx)
	t := time.NewTicker(5 * time.Minute) // safety net for out-of-band changes
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stopAll()
			return nil
		case <-s.reloadC:
		case <-t.C:
		}
		s.sync(ctx)
	}
}

func (s *supervisor) sync(ctx context.Context) {
	mbs, err := s.m.listMailboxes(ctx, true)
	if err != nil {
		s.m.log.Warn("inbound: list mailboxes", "err", err)
		return
	}
	want := map[string]*mailbox{}
	for _, mb := range mbs {
		want[mb.ID] = mb
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rs := range s.running {
		mb, ok := want[id]
		if !ok || configHash(mb) != rs.hash {
			rs.cancel()
			<-rs.done
			delete(s.running, id)
		}
	}
	for id, mb := range want {
		if _, ok := s.running[id]; ok {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		rs := &runningSource{hash: configHash(mb), cancel: cancel, done: make(chan struct{})}
		s.running[id] = rs
		src := s.m.NewIMAPSource(mb)
		go func() {
			defer close(rs.done)
			_ = src.Run(cctx, s.m.deliverFor(mb))
		}()
	}
}

func (s *supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rs := range s.running {
		rs.cancel()
		<-rs.done
		delete(s.running, id)
	}
}

// deliverFor records a message of mb and schedules its processing.
func (m *Module) deliverFor(mb *mailbox) DeliverFunc {
	return func(ctx context.Context, raw []byte, _ Meta) error {
		_, err := m.Receive(ctx, mb, raw)
		return err
	}
}
