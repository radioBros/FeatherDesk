// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// Trusted integrations (I-12): a token holding tickets:act_as_customer posts
// customer messages on a customer's behalf. The reply runs through the
// portal reply path (AddCustomerReply) with the customer as principal, so
// status, reopen window, follow-ups, SLA, notifications and events are those
// of a portal reply; only the message's via_token_id, the draft owner and an
// audit row differ.

// onBehalf is the server-side context of a write made with
// tickets:act_as_customer.
type onBehalf struct {
	OwnerID string // the token owner (staff): audit actor and draft uploader
	TokenID string
	IP      string
}

func (o *onBehalf) tokenID() string {
	if o == nil {
		return ""
	}
	return o.TokenID
}

// uploader is whose drafts may be attached: the token owner on behalf,
// otherwise the principal.
func (o *onBehalf) uploader(principalID string) string {
	if o == nil {
		return principalID
	}
	return o.OwnerID
}

// AuditOnBehalf is the audit_log action of a message posted on behalf.
const AuditOnBehalf = "ticket.message.on_behalf"

// auditOnBehalf writes the audit row (actor = the token owner) inside tx.
func auditOnBehalf(ctx context.Context, tx *store.Tx, o *onBehalf, customerID, ticketID, msgID string) error {
	if o == nil {
		return nil
	}
	data := map[string]any{"on_behalf_of": customerID, "ticket_id": ticketID, "message_id": msgID, "token_id": o.TokenID}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, 'ticket', ?, ?, ?, ?)`,
		store.NewID(), o.OwnerID, AuditOnBehalf, ticketID, store.MustJSON(data), o.IP, store.Now())
	return err
}

// OnBehalfInput is CustomerMessageOnBehalfCreate.
type OnBehalfInput struct {
	OnBehalfOf    string   `json:"on_behalf_of"`
	BodyMD        string   `json:"body_md"`
	BodyHTML      string   `json:"body_html"`
	AttachmentIDs []string `json:"attachment_ids"`
	SetStatus     string   `json:"set_status"`
	Kind          *string  `json:"kind"` // not part of the schema: anything but public is a 422
}

type followupRefDTO struct {
	ID     string `json:"id"`
	Ref    string `json:"ref"`
	Number int64  `json:"number"`
}

// onBehalfResultDTO is CustomerMessageOnBehalfResult: deliberately minimal,
// so the scope never discloses staff data (status is the customer label).
type onBehalfResultDTO struct {
	Message        *messageDTO     `json:"message"`
	TicketID       string          `json:"ticket_id"`
	Ref            string          `json:"ref"`
	Status         string          `json:"status"`
	FollowupTicket *followupRefDTO `json:"followup_ticket,omitempty"`
}

// onBehalfTicketDTO is CustomerTicketOnBehalfResult (createAgentTicket under
// tickets:act_as_customer).
type onBehalfTicketDTO struct {
	TicketID string      `json:"ticket_id"`
	Ref      string      `json:"ref"`
	Number   int64       `json:"number"`
	Status   string      `json:"status"`
	Message  *messageDTO `json:"message"`
}

// onBehalfCustomer resolves the target customer as a principal: unknown 404,
// staff 403 on-behalf-not-customer, disabled 409 on-behalf-disabled.
func onBehalfCustomer(ctx context.Context, q store.Querier, id string) (*domain.Principal, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	var kind domain.UserKind
	var role domain.Role
	var status domain.UserStatus
	var org sql.NullString
	err := q.QueryRowContext(ctx, `SELECT kind, role, status, organization_id FROM users WHERE id = ?`, id).Scan(&kind, &role, &status, &org)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, problems.NotFound()
	}
	if err != nil {
		return nil, err
	}
	if kind != domain.KindCustomer {
		return nil, problems.New(problems.SlugOnBehalfNotCustomer, "on_behalf_of must be a customer")
	}
	if status == domain.UserDisabled {
		return nil, problems.New(problems.SlugOnBehalfDisabled, "the customer account is disabled")
	}
	return &domain.Principal{UserID: id, Kind: domain.KindCustomer, Role: role, OrganizationID: org.String}, nil
}

// readTicket loads a ticket with its watchers outside a transaction.
func (s *Service) readTicket(ctx context.Context, id string) (*ticketRow, error) {
	if !validID(id) {
		return nil, problems.NotFound()
	}
	q := s.db.Read()
	t, err := scanTicket(q.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE t.id = ?`, id))
	if err != nil {
		return nil, store.NotFound(err)
	}
	if err := loadWatchers(ctx, q, t); err != nil {
		return nil, err
	}
	s.fillKey(ctx, t)
	return t, nil
}

// ReplyOnBehalf posts a customer reply on behalf of in.OnBehalfOf (I-12).
// owner is the token's principal; it must hold tickets:act_as_customer.
func (s *Service) ReplyOnBehalf(ctx context.Context, owner *domain.Principal, ticketID string, in OnBehalfInput, ip string) (*onBehalfResultDTO, error) {
	if !owner.ActsAsCustomer() {
		return nil, problems.Forbidden("token lacks " + domain.ScopeActAsCustomer)
	}
	var fields []problems.FieldError
	if in.Kind != nil && *in.Kind != string(domain.MessagePublic) {
		fields = append(fields, problems.Field("kind", "enum", "only public messages are posted on behalf of a customer"))
	}
	if in.SetStatus != "" && in.SetStatus != string(domain.StatusResolved) {
		fields = append(fields, problems.Field("set_status", "enum", "only resolved"))
	}
	if in.OnBehalfOf == "" {
		fields = append(fields, problems.Field("on_behalf_of", "required", "the customer's user id"))
	}
	if len(fields) > 0 {
		return nil, problems.Validation(fields...)
	}
	body, err := resolveBody(in.BodyMD, in.BodyHTML)
	if err != nil {
		return nil, err
	}
	in.BodyMD = body
	t, err := s.readTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	cust, err := onBehalfCustomer(ctx, s.db.Read(), in.OnBehalfOf)
	if err != nil {
		return nil, err
	}
	// I-3 for the target: the integration acts within what the customer may see.
	if !authz.CanSeeTicket(cust, t.view(s.Policy(ctx), s.clock())) {
		return nil, problems.New(problems.SlugOnBehalfNotVisible, "the customer cannot see this ticket")
	}
	res, err := s.AddCustomerReply(ctx, cust, t.ID, MessageInput{Kind: string(domain.MessagePublic), BodyMD: in.BodyMD, AttachmentIDs: in.AttachmentIDs,
		SetStatus: in.SetStatus, channel: domain.ChannelAPI, onBehalf: &onBehalf{OwnerID: owner.UserID, TokenID: owner.TokenID, IP: ip}})
	if err != nil {
		return nil, err
	}
	msg, err := s.staffMessageNoAuth(ctx, res.TicketID, res.MessageID)
	if err != nil {
		return nil, err
	}
	out := &onBehalfResultDTO{Message: msg, TicketID: t.ID, Ref: t.Ref(), Status: CustomerStatus(res.Status)}
	if res.FollowupID != "" {
		f, err := s.readTicket(ctx, res.FollowupID)
		if err != nil {
			return nil, err
		}
		out.FollowupTicket = &followupRefDTO{ID: f.ID, Ref: f.Ref(), Number: f.Number}
	}
	return out, nil
}

// onBehalfCreated builds the minimal create response under the scope.
func (s *Service) onBehalfCreated(ctx context.Context, id string) (*onBehalfTicketDTO, error) {
	t, err := s.readTicket(ctx, id)
	if err != nil {
		return nil, err
	}
	var msgID string
	if err := s.db.Read().QueryRowContext(ctx, `SELECT id FROM ticket_messages WHERE ticket_id = ? ORDER BY created_at, id LIMIT 1`, id).Scan(&msgID); err != nil {
		return nil, store.NotFound(err)
	}
	msg, err := s.staffMessageNoAuth(ctx, id, msgID)
	if err != nil {
		return nil, err
	}
	return &onBehalfTicketDTO{TicketID: t.ID, Ref: t.Ref(), Number: t.Number, Status: CustomerStatus(t.Status), Message: msg}, nil
}

// On-behalf writes are limited per token: 60 per minute, bursts of 20.
const (
	onBehalfPerSecond = 1.0
	onBehalfBurst     = 20
)

// onBehalfLimit limits tickets:act_as_customer callers per token. It runs
// before the Idempotency-Key middleware, so a 429 is never stored as the
// key's answer and a retry after Retry-After with the same key goes through.
func (m *Module) onBehalfLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := principal(r); m.d.Server != nil && p.ActsAsCustomer() {
			if err := m.d.Server.AllowKey("on_behalf", "t:"+p.TokenID, onBehalfPerSecond, onBehalfBurst); err != nil {
				problems.Write(w, r, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// agentCustomerMessage is POST /agent/tickets/{id}/customer-messages.
func (m *Module) agentCustomerMessage(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if !p.ActsAsCustomer() {
		return problems.Forbidden("token lacks " + domain.ScopeActAsCustomer)
	}
	var in OnBehalfInput
	if err := decodeJSON(r, &in); err != nil {
		return err
	}
	out, err := m.svc.ReplyOnBehalf(r.Context(), p, urlParam(r, "id"), in, server.ClientIP(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}
