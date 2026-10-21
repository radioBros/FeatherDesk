// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Portal-side aliases of the generated OpenAPI types plus tiny helpers shared
 * by the portal views. Nothing here imports from the agent area (FE-031).
 */
import type { components } from '@/api/schema'
import type { Message } from '@/types'

type S = components['schemas']

export type CustomerTicket = S['CustomerTicket']
export type CustomerTicketSummary = S['CustomerTicketSummary']
export type CustomerTicketPage = S['CustomerTicketPage']
export type CustomerMessage = S['CustomerMessage']
export type CustomerStatus = S['CustomerStatus']
export type Category = S['Category']
export type CustomFieldDef = S['CustomFieldDef']
export type CustomFieldValue = S['CustomFieldValue']
export type CustomFieldValues = S['CustomFieldValues']
export type LocalizedText = S['LocalizedText']
export type Priority = S['Priority']
export type ApiToken = S['ApiToken']
export type ApiTokenCreated = S['ApiTokenCreated']
export type Session = S['Session']
export type User = S['User']
export type UserRef = S['UserRef']
export type CustomerRole = S['CustomerRole']
export type KbArticleSummary = S['KbArticleSummary']
export type Scope = S['Scope']

export const CUSTOMER_STATUSES: readonly CustomerStatus[] = ['open', 'awaiting_reply', 'resolved', 'closed']

/** Scopes a customer may put on a personal token (permissions §4): never directory:* or admin. */
export const CUSTOMER_SCOPES: readonly Scope[] = ['tickets:read', 'tickets:write']

/** Template keys a customer can opt out of (events spec §2, `notification_opt_out`). */
export const CUSTOMER_NOTIFICATION_KEYS = ['customer.ticket_received', 'customer.reply', 'customer.resolved'] as const

export { localized } from '@/utils/localized'

/** Adapts a customer message to the shared MessageThread shape. */
export function toThreadMessage(m: CustomerMessage): Message {
  return {
    id: m.id,
    kind: m.kind,
    body_md: m.body_md,
    author: m.author ? { id: m.author.id, name: m.author.name, kind: m.author.kind } : null,
    created_at: m.created_at,
    attachments: m.attachments,
  }
}

/** Extracts a follow-up ticket id from a 409 on a closed ticket (API: id in `detail`). */
export function followUpIdFrom(body: Record<string, unknown> | undefined): string | null {
  if (!body) return null
  for (const k of ['followup_id', 'followup_ticket_id', 'ticket_id']) {
    const v = body[k]
    if (typeof v === 'string' && v) return v
  }
  const detail = typeof body.detail === 'string' ? body.detail : ''
  const m = detail.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)
  return m ? m[0] : null
}
