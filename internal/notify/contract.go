// SPDX-License-Identifier: AGPL-3.0-only

package notify

import "strings"

// Shared contract with other modules (documented in docs/dev/internals.md
// "Notifications wiring"). Keep these names and JSON shapes stable.

// Non-event job kinds handled by the notify module (queue.Handle).
const (
	// JobAuthMail sends one auth.* mail. Payload: AuthMail. Enqueue it with
	// queue.Enqueue(ctx, notify.JobAuthMail, notify.AuthMail{...}) after commit.
	JobAuthMail = "mail.auth"
	// JobAdminMail sends one template to every active admin. Payload: AdminMail.
	// Used by webhooks for staff.webhook_disabled.
	JobAdminMail = "mail.admins"
)

// AuthMail is the payload of JobAuthMail.
type AuthMail struct {
	Template string `json:"template"` // auth.verify_email | auth.reset_password | auth.magic_link | auth.invite | auth.already_registered
	UserID   string `json:"user_id,omitempty"`
	To       string `json:"to"`
	Locale   string `json:"locale,omitempty"` // "" → user locale → instance default → en
	// Vars: "url" (required: the full link with token), "expires_minutes",
	// "name", "inviter", … Rendered as .Vars.<key> in the template.
	Vars map[string]string `json:"vars"`
}

// AdminMail is the payload of JobAdminMail.
type AdminMail struct {
	Template string            `json:"template"` // e.g. staff.webhook_disabled
	Vars     map[string]string `json:"vars"`
}

// Template keys (events_spec §2 + auth.* + staff.webhook_disabled).
const (
	TplCustomerTicketReceived = "customer.ticket_received"
	TplCustomerReply          = "customer.reply"
	TplCustomerResolved       = "customer.resolved"
	TplStaffTicketNew         = "staff.ticket_new"
	TplStaffCustomerReplied   = "staff.customer_replied"
	TplStaffAssigned          = "staff.assigned"
	TplStaffEscalated         = "staff.escalated"
	TplStaffMentioned         = "staff.mentioned"
	TplStaffSLA               = "staff.sla"
	TplStaffChildrenResolved  = "staff.children_resolved"
	TplStaffWebhookDisabled   = "staff.webhook_disabled"
	TplAuthVerifyEmail        = "auth.verify_email"
	TplAuthResetPassword      = "auth.reset_password"
	TplAuthMagicLink          = "auth.magic_link"
	TplAuthInvite             = "auth.invite"
	TplAuthAlreadyRegistered  = "auth.already_registered"
	TplAuthMFACode            = "auth.mfa_code" // vars.code: one-time sign-in code (D-19 §B)
	// TplPrivacyExportReady is sent through JobAuthMail to whoever requested a
	// data export (vars.url, vars.expires_days; D-19 §C).
	TplPrivacyExportReady = "privacy.export_ready"
)

// Transactional reports whether a template is a transactional mail sent
// through JobAuthMail (auth.*, privacy.*): it can't be disabled by admins and
// isn't opt-out-able.
func Transactional(key string) bool {
	return strings.HasPrefix(key, "auth.") || strings.HasPrefix(key, "privacy.")
}

// TemplateKeys is the full, ordered key list (asserted by the completeness test).
var TemplateKeys = []string{
	TplCustomerTicketReceived, TplCustomerReply, TplCustomerResolved,
	TplStaffTicketNew, TplStaffCustomerReplied, TplStaffAssigned, TplStaffEscalated,
	TplStaffMentioned, TplStaffSLA, TplStaffChildrenResolved, TplStaffWebhookDisabled,
	TplAuthVerifyEmail, TplAuthResetPassword, TplAuthMagicLink, TplAuthInvite, TplAuthAlreadyRegistered,
	TplAuthMFACode,
	TplPrivacyExportReady,
}
