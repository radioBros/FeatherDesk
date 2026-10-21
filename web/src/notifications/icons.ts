// SPDX-License-Identifier: AGPL-3.0-only

import {
  mdiAccountArrowRight,
  mdiArrowUpBold,
  mdiAt,
  mdiBellOutline,
  mdiCheckCircleOutline,
  mdiLinkVariant,
  mdiReplyOutline,
  mdiTicketConfirmationOutline,
  mdiTicketOutline,
  mdiTimerAlertOutline,
} from '@mdi/js'
import type { Section } from '@/theme/palette'

/** One icon per NotificationType (bell list and page). */
const ICONS: Record<string, string> = {
  'staff.ticket_new': mdiTicketOutline,
  'staff.customer_replied': mdiReplyOutline,
  'staff.assigned': mdiAccountArrowRight,
  'staff.escalated': mdiArrowUpBold,
  'staff.mentioned': mdiAt,
  'staff.sla': mdiTimerAlertOutline,
  'staff.children_resolved': mdiLinkVariant,
  'customer.ticket_received': mdiTicketConfirmationOutline,
  'customer.reply': mdiReplyOutline,
  'customer.resolved': mdiCheckCircleOutline,
}

export const iconFor = (type: string) => ICONS[type] ?? mdiBellOutline

/** One section colour per type, so the list reads at a glance (theme/palette.ts). */
const SECTIONS: Record<string, Section> = {
  'staff.ticket_new': 'tickets',
  'staff.customer_replied': 'customers',
  'staff.assigned': 'teams',
  'staff.escalated': 'automation',
  'staff.mentioned': 'reports',
  'staff.sla': 'danger',
  'staff.children_resolved': 'kb',
  'customer.ticket_received': 'tickets',
  'customer.reply': 'customers',
  'customer.resolved': 'privacy',
}

export const sectionFor = (type: string): Section => SECTIONS[type] ?? 'notifications'
