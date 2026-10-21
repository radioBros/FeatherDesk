// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Ticket detail API helpers (FE-022…FE-024, FE-045, FE-055).
 *
 * - `ETag` from `GET /agent/tickets/{id}` (TKT-030: `W/"<updated_at nanos>"`)
 *   is sent back as `If-Match` on PATCH; without an ETag the header is
 *   omitted (last-write-wins, as the API documents). It is never built from
 *   `updated_at` on the client.
 * - `Idempotency-Key` per composer draft on message POSTs and ticket creates,
 *   so a double click or a retry never duplicates (PLT-020).
 * - Ticket mutations return `{ticket, visible}`; `visible: false` means the
 *   caller just lost access (escalated past their tier, transferred).
 */
import { api, unwrap } from '@/api/client'
import { ApiProblem, type ProblemBody } from '@/api/problem'
import type { components } from '@/api/schema'

type S = components['schemas']
export type Ticket = S['Ticket']
export type TicketUpdate = S['TicketUpdateResult']

interface Raw<T> {
  data?: T
  error?: unknown
  response: Response
}

/** Like `unwrap`, but also returns the response headers. */
async function unwrapWithResponse<T>(p: Promise<Raw<T>>): Promise<{ data: T; response: Response }> {
  let res: Raw<T>
  try {
    res = await p
  } catch (e) {
    throw new ApiProblem(0, { title: e instanceof Error ? e.message : 'network error' })
  }
  if (res.response.ok) return { data: res.data as T, response: res.response }
  const body: ProblemBody =
    res.error && typeof res.error === 'object' ? (res.error as ProblemBody) : { title: String(res.error ?? '') }
  throw new ApiProblem(res.response.status, body)
}

const etagOf = (r: Response) => r.headers.get('ETag')

export async function getByNumber(n: number): Promise<{ ticket: Ticket; etag: string | null }> {
  const { data, response } = await unwrapWithResponse(api.GET('/agent/tickets/by-number/{n}', { params: { path: { n } } }))
  return { ticket: data, etag: etagOf(response) }
}

export async function getById(id: string): Promise<{ ticket: Ticket; etag: string | null }> {
  const { data, response } = await unwrapWithResponse(api.GET('/agent/tickets/{id}', { params: { path: { id } } }))
  return { ticket: data, etag: etagOf(response) }
}

export async function patchTicket(
  id: string,
  body: S['TicketPatch'],
  etag: string | null,
): Promise<{ result: TicketUpdate; etag: string | null }> {
  const { data, response } = await unwrapWithResponse(
    api.PATCH('/agent/tickets/{id}', {
      params: { path: { id }, header: etag ? { 'If-Match': etag } : {} },
      body,
    }),
  )
  return { result: data, etag: etagOf(response) }
}

export function sendMessage(id: string, body: S['AgentMessageCreate'], idempotencyKey: string): Promise<S['Message']> {
  return unwrap(
    api.POST('/agent/tickets/{id}/messages', {
      params: { path: { id }, header: { 'Idempotency-Key': idempotencyKey } },
      body,
    }),
  )
}

export function editMessage(id: string, mid: string, body_md: string): Promise<S['Message']> {
  return unwrap(api.PATCH('/agent/tickets/{id}/messages/{mid}', { params: { path: { id, mid } }, body: { body_md } }))
}

export function escalate(id: string, body: S['EscalateRequest'], direction: 'escalate' | 'deescalate'): Promise<TicketUpdate> {
  return direction === 'escalate'
    ? unwrap(api.POST('/agent/tickets/{id}/escalate', { params: { path: { id } }, body }))
    : unwrap(api.POST('/agent/tickets/{id}/deescalate', { params: { path: { id } }, body }))
}

export function merge(id: string, into: string): Promise<TicketUpdate> {
  return unwrap(api.POST('/agent/tickets/{id}/merge', { params: { path: { id } }, body: { into_ticket_id: into } }))
}

export function split(id: string, body: S['SplitRequest']): Promise<Ticket> {
  return unwrap(api.POST('/agent/tickets/{id}/split', { params: { path: { id } }, body }))
}

export function addLink(id: string, body: S['LinkCreate']): Promise<S['TicketLink']> {
  return unwrap(api.POST('/agent/tickets/{id}/links', { params: { path: { id } }, body }))
}

export function removeLink(id: string, linkId: string): Promise<unknown> {
  return unwrap(api.DELETE('/agent/tickets/{id}/links/{link_id}', { params: { path: { id, link_id: linkId } } }))
}

export function addWatcher(id: string, userId: string): Promise<Ticket> {
  return unwrap(api.POST('/agent/tickets/{id}/watchers', { params: { path: { id } }, body: { user_id: userId } }))
}

export function removeWatcher(id: string, userId: string): Promise<unknown> {
  return unwrap(api.DELETE('/agent/tickets/{id}/watchers/{user_id}', { params: { path: { id, user_id: userId } } }))
}

export function assignable(ticketId: string): Promise<S['StaffRef'][]> {
  return unwrap(api.GET('/agent/assignable', { params: { query: { ticket_id: ticketId } } }))
}

export function events(id: string, cursor: string | null): Promise<S['TicketEventPage']> {
  return unwrap(
    api.GET('/agent/tickets/{id}/events', { params: { path: { id }, query: cursor ? { cursor, limit: 50 } : { limit: 50 } } }),
  )
}

/** Searches visible tickets by `#number` or text (link and merge dialogs). */
export async function searchTickets(q: string): Promise<S['TicketSummary'][]> {
  const text = q.trim().replace(/^#/, '')
  if (!text) return []
  if (/^\d+$/.test(text)) {
    try {
      const { ticket } = await getByNumber(Number(text))
      return [ticket]
    } catch {
      return []
    }
  }
  const page = await unwrap(api.GET('/agent/tickets', { params: { query: { q: text, limit: 10 } } }))
  return page.items ?? []
}

/** Problem slug helpers. */
export const isInvalidTransition = (e: unknown): e is ApiProblem => e instanceof ApiProblem && e.status === 409 && e.slug === 'invalid-transition'
export const isPrecondition = (e: unknown): e is ApiProblem => e instanceof ApiProblem && e.status === 412
export const allowedFrom = (e: ApiProblem): S['TicketStatus'][] => (Array.isArray(e.body.allowed) ? (e.body.allowed as S['TicketStatus'][]) : [])
