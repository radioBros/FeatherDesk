// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Notification centre shapes and pure helpers (D-19 §D, FE-090). The types
 * come from the OpenAPI components; nothing here touches the DOM or the API.
 */
import type { components } from '@/api/schema'

type S = components['schemas']
export type Notification = S['Notification']
export type NotificationType = S['NotificationType']
export type Channels = S['NotificationChannels']
export type Prefs = Record<string, Channels>
export type Area = 'agent' | 'portal'

/** NotificationType per audience, in the order of the preferences matrix. */
export const STAFF_TYPES: readonly NotificationType[] = [
  'staff.ticket_new',
  'staff.customer_replied',
  'staff.assigned',
  'staff.escalated',
  'staff.mentioned',
  'staff.sla',
  'staff.children_resolved',
]
export const CUSTOMER_TYPES: readonly NotificationType[] = ['customer.ticket_received', 'customer.reply', 'customer.resolved']

export const typesFor = (area: Area) => (area === 'agent' ? STAFF_TYPES : CUSTOMER_TYPES)

/** `customer.reply` email cannot be turned off (the server answers `not_optional`). */
export const EMAIL_FORCED: readonly string[] = ['customer.reply']

/** i18n segment of a type: `staff.ticket_new` → `staff_ticket_new`. */
export const typeKey = (t: string) => t.replace('.', '_')

/** Frame payloads on the `me` topic (WsServerFrame event data). */
export interface CreatedData {
  notification: Notification
  unread_count: number
  browser?: boolean
}
export interface ReadData {
  ids?: string[]
  all?: boolean
  unread_count: number
}

/** Where a notification leads: the ticket in the caller's area, else the centre. */
export function linkFor(n: Pick<Notification, 'ticket_id' | 'ticket_number'>, area: Area): string {
  if (area === 'agent') return n.ticket_number != null ? `/agent/tickets/${n.ticket_number}` : '/agent/notifications'
  return n.ticket_id ? `/tickets/${n.ticket_id}` : '/notifications'
}

export const centreFor = (area: Area) => (area === 'agent' ? '/agent/notifications' : '/notifications')
export const settingsFor = (area: Area) => (area === 'agent' ? '/agent/profile' : '/profile')

type T = (key: string, named?: Record<string, unknown>) => string

/** The ticket as people know it: the D-20 ref (`WEB-12`), else `#1042` (a live frame of an event written before projects). */
export function refOf(n: Pick<Notification, 'ticket_ref' | 'ticket_number'>): string {
  if (n.ticket_ref) return n.ticket_ref
  return n.ticket_number != null ? `#${n.ticket_number}` : ''
}

/** The one-line text of a notification (bell, page and browser pop-up). */
export function titleOf(n: Notification, t: T, tierLabel: (tier: number) => string): string {
  const k = `notifications.types.${typeKey(n.type)}`
  const named = {
    ref: refOf(n),
    number: n.ticket_number ?? '',
    actor: n.actor_name || t('notifications.someone'),
    tier: n.params.to_tier ? tierLabel(n.params.to_tier) : '',
  }
  if (n.type === 'staff.sla' && n.params.breached) return t(`${k}.breached`, named)
  return t(`${k}.title`, named)
}

/** The second line: the ticket subject, plus the SLA clock for SLA notices. */
export function detailOf(n: Notification, t: T): string {
  const parts = [n.ticket_subject ?? '']
  if (n.type === 'staff.sla' && n.params.sla) parts.push(t(`notifications.sla.${n.params.sla}`))
  return parts.filter(Boolean).join(' · ')
}
