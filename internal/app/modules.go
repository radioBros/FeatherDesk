// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"sync"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/kb"
	"github.com/radioBros/SimpleTickets/internal/mailin"
	"github.com/radioBros/SimpleTickets/internal/mcp"
	"github.com/radioBros/SimpleTickets/internal/notify"
	"github.com/radioBros/SimpleTickets/internal/tickets"
	"github.com/radioBros/SimpleTickets/internal/webhooks"
)

// Registry lists the feature modules mounted by Build, in order. Phase 2
// modules append their factory here, e.g.:
//
//	authn.New,      // provides the Authenticator (must come first)
//	directory.New,
//	tickets.New,
//	notify.New,
//	webhooks.New,
//	mailin.New,
//	kb.New,
//
// Keep authn first: it plugs in authentication used by every later module.
var Registry = []Factory{
	authnModule, // must stay first: provides the Authenticator
	ticketsModule,
	staffAdminModule,
	kbModule,
	notifyModule,
	webhooksModule,
	privacyModule, // D-19 §C (privacy_module.go); after tickets: wires the search reindex
	mcpModule,     // POST /mcp (MCP-001): tools run in-process through the REST router
	// mailinModule is appended last by init (see "Notifications wiring").
}

// mcpModule adapts Deps for the built-in MCP server (internal/mcp). It needs
// no other module: every tool is a REST call through d.Server's router.
func mcpModule(d *Deps) (Module, error) {
	return mcp.NewModule(mcp.Deps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Settings: d.Settings, Server: d.Server, Version: d.Version})
}

// kbModule adapts Deps for the knowledge base (kb cannot import app).
func kbModule(d *Deps) (Module, error) {
	return kb.New(kb.Deps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Blob: d.Blob, Settings: d.Settings, Server: d.Server})
}

// authnModule adapts Deps for authn (authn cannot import app). The OIDC
// role/tier/team sync re-validates M-4 through directory.MembershipChanged
// (authn cannot import directory).
func authnModule(d *Deps) (Module, error) {
	authn.SetMembershipChanged(directory.MembershipChanged)
	return authn.New(authn.Deps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Queue: d.Queue, Hub: d.Hub, Settings: d.Settings,
		Secrets: d.Secrets, Server: d.Server, Getenv: d.Getenv, Version: d.Version})
}

// ticketsModule adapts Deps for the tickets module, which also serves the
// directory routes (tickets cannot import app). The wrapper adds
// EmailSink() so mailinModule hands inbound mail to the ticket service.
func ticketsModule(d *Deps) (Module, error) {
	m, err := tickets.New(tickets.Deps{DB: d.DB, Log: d.Log, Queue: d.Queue, Hub: d.Hub, Blob: d.Blob, Settings: d.Settings,
		Secrets: d.Secrets, Server: d.Server, MaxUploadMB: d.Cfg.MaxUploadMB, PublicURL: d.Cfg.PublicURL})
	if err != nil {
		return nil, err
	}
	dir := m.Directory()
	// AUTH-017: disabling a user from the customer directory goes through
	// authn (status, sessions, PATs, socket close 4401, audit).
	dir.SetDisabler(authn.DisableUser)
	// AUTH-002: authn keeps a ≤ 30 s team cache per user; drop it on change.
	dir.OnTeamsChanged(func(ids []string) {
		for _, id := range ids {
			authn.InvalidateTeams(id)
		}
	})
	return ticketsWithEmail{m}, nil
}

type ticketsWithEmail struct{ *tickets.Module }

func (t ticketsWithEmail) EmailSink() mailin.TicketSink { return ticketsEmailSink{t.Service()} }

// ticketsEmailSink converts mailin's types to the tickets entry points (TKT-041).
type ticketsEmailSink struct{ s *tickets.Service }

func emailAttachments(in []mailin.EmailAttachment) []tickets.EmailAttachment {
	out := make([]tickets.EmailAttachment, len(in))
	for i, a := range in {
		out[i] = tickets.EmailAttachment{Filename: a.Filename, ContentType: a.ContentType, ContentID: a.ContentID, Inline: a.Inline, Data: a.Data}
	}
	return out
}

func (k ticketsEmailSink) CreateFromEmail(ctx context.Context, in mailin.EmailTicket) (mailin.EmailResult, error) {
	tid, mid, err := k.s.CreateFromEmailMessage(ctx, tickets.EmailTicket{InboundMessageID: in.InboundMessageID, MailboxID: in.MailboxID,
		TeamID: in.TeamID, RequesterID: in.RequesterID, CCUserIDs: in.CCUserIDs, Subject: in.Subject, BodyMD: in.BodyMD,
		Attachments: emailAttachments(in.Attachments)})
	return mailin.EmailResult{TicketID: tid, MessageID: mid}, err
}

func (k ticketsEmailSink) ReplyFromEmail(ctx context.Context, in mailin.EmailReply) (mailin.EmailResult, error) {
	tid, mid, err := k.s.ReplyFromEmailMessage(ctx, tickets.EmailReply{InboundMessageID: in.InboundMessageID, TicketID: in.TicketID,
		AuthorID: in.AuthorID, Internal: in.Internal, BodyMD: in.BodyMD, Attachments: emailAttachments(in.Attachments)})
	return mailin.EmailResult{TicketID: tid, MessageID: mid}, err
}

// staffAdminModule serves /admin/staff (AUTH-018).
func staffAdminModule(d *Deps) (Module, error) {
	return directory.NewStaffAdmin(directory.StaffAdminDeps{Cfg: d.Cfg, DB: d.DB, Log: d.Log, Queue: d.Queue, Settings: d.Settings})
}

// ---- Notifications wiring (docs/dev/internals.md "Notifications wiring") ----

// notifyModule adapts Deps for notify (SMTP, templates, notify consumer).
func notifyModule(d *Deps) (Module, error) {
	return notify.New(notify.Deps{DB: d.DB, Log: d.Log, Queue: d.Queue, Blob: d.Blob, Settings: d.Settings,
		Secrets: d.Secrets, Server: d.Server, Cfg: d.Cfg, Getenv: d.Getenv})
}

// webhooksModule adapts Deps for outbound webhooks.
func webhooksModule(d *Deps) (Module, error) {
	return webhooks.New(webhooks.Deps{DB: d.DB, Log: d.Log, Queue: d.Queue, Blob: d.Blob, Settings: d.Settings,
		Secrets: d.Secrets, Server: d.Server, Cfg: d.Cfg, Getenv: d.Getenv})
}

// builtModules records, per Build (keyed by its *Deps), the modules built by
// the Registry entries before mailin, so mailinModule can find the TicketSink
// without app.Deps changes. mailinModule deletes the entry.
var builtModules sync.Map // *Deps → []Module

func init() {
	for i, f := range Registry {
		Registry[i] = recordBuilt(f)
	}
	Registry = append(Registry, mailinModule) // last: sees every module above
}

func recordBuilt(f Factory) Factory {
	return func(d *Deps) (Module, error) {
		m, err := f(d)
		if err == nil && m != nil {
			prev, _ := builtModules.Load(d)
			list, _ := prev.([]Module)
			builtModules.Store(d, append(list, m))
		}
		return m, err
	}
}

// mailinModule builds inbound email and wires:
//   - the TicketSink: the first earlier module that implements
//     mailin.TicketSink, or exposes it via EmailSink() mailin.TicketSink;
//   - staff-reply authentication through authn.ResolveEmailSender (AUTH-034).
func mailinModule(d *Deps) (Module, error) {
	m, err := mailin.New(mailin.Deps{DB: d.DB, Log: d.Log, Queue: d.Queue, Blob: d.Blob, Settings: d.Settings,
		Secrets: d.Secrets, Server: d.Server, Cfg: d.Cfg, Getenv: d.Getenv})
	if err != nil {
		return nil, err
	}
	prev, _ := builtModules.LoadAndDelete(d)
	list, _ := prev.([]Module)
	for _, other := range list {
		if s, ok := other.(mailin.TicketSink); ok {
			m.SetTicketSink(s)
			break
		}
		if p, ok := other.(interface{ EmailSink() mailin.TicketSink }); ok && p.EmailSink() != nil {
			m.SetTicketSink(p.EmailSink())
			break
		}
	}
	db := d.DB
	m.SetStaffAuthCheck(func(ctx context.Context, from, authResults, trustedID string) (bool, error) {
		_, kind, verdict, err := authn.ResolveEmailSender(ctx, db, from, authResults, trustedID)
		return err == nil && kind == domain.KindStaff && verdict == authn.SenderOK, err
	})
	return m, nil
}
