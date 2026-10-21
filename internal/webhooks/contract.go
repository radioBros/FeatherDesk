// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import "github.com/radioBros/SimpleTickets/internal/notify"

// The admin notification for an auto-disabled webhook goes through the notify
// module's "mail.admins" job (internal/notify/contract.go).
const (
	jobAdminMail            = notify.JobAdminMail
	tplStaffWebhookDisabled = notify.TplStaffWebhookDisabled
)

// adminMail is notify.AdminMail (payload of the "mail.admins" job).
type adminMail = notify.AdminMail
