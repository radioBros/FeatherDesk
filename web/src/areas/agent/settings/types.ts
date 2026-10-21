// SPDX-License-Identifier: AGPL-3.0-only

/** Shapes shared between the settings pages and their form components. */
export interface AuditFilter {
  action: string
  actor_id: string | null
  since: string
}

/** Template keys known to the notifier (NOT-005/NOT-020); the server list adds any others. */
export const KNOWN_TEMPLATES = [
  'customer.ticket_received',
  'customer.reply',
  'customer.resolved',
  'staff.ticket_new',
  'staff.customer_replied',
  'staff.assigned',
  'staff.escalated',
  'staff.mentioned',
  'staff.sla',
  'staff.children_resolved',
  'staff.webhook_disabled',
  'auth.verify_email',
  'auth.reset_password',
  'auth.magic_link',
  'auth.invite',
  'privacy.export_ready',
] as const

/** The email preview a branding change is checked against. */
export const PREVIEW_TEMPLATE = 'customer.reply'

export function previewUrl(key: string, locale: string, version = 0): string {
  return `/api/v1/admin/email/templates/${encodeURIComponent(key)}/preview?locale=${encodeURIComponent(locale)}&v=${version}`
}
