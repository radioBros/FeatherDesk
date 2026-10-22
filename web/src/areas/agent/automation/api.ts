// SPDX-License-Identifier: AGPL-3.0-only

/** Automation API helpers that need response headers (FE-065, D-19 A). */
import { api, unwrap } from '@/api/client'
import { ApiProblem, type ProblemBody } from '@/api/problem'
import type { components } from '@/api/schema'

type S = components['schemas']

/**
 * `POST /agent/tickets/{id}/macros/{macro_id}/apply`: one Idempotency-Key per
 * click; If-Match only with an ETag from `GET /agent/tickets/{id}` (TKT-030).
 * Returns the result and the new ETag (when the response carries one).
 */
export async function applyMacro(
  ticketId: string,
  macroId: string,
  etag: string | null,
  idempotencyKey: string,
): Promise<{ result: S['TicketUpdateResult']; etag: string | null }> {
  const header: Record<string, string> = { 'Idempotency-Key': idempotencyKey }
  if (etag) header['If-Match'] = etag
  let res
  try {
    res = await api.POST('/agent/tickets/{id}/macros/{macro_id}/apply', { params: { path: { id: ticketId, macro_id: macroId }, header } })
  } catch (e) {
    throw new ApiProblem(0, { title: e instanceof Error ? e.message : 'network error' })
  }
  if (res.response.ok && res.data) return { result: res.data, etag: res.response.headers.get('ETag') }
  const body: ProblemBody = res.error && typeof res.error === 'object' ? (res.error as ProblemBody) : { title: String(res.error ?? '') }
  throw new ApiProblem(res.response.status, body)
}

/** `GET`/`PATCH /me/availability` (FE-060). */
export const getAvailability = (): Promise<S['Availability']> => unwrap(api.GET('/me/availability'))
export const setAvailability = (away: boolean): Promise<S['Availability']> => unwrap(api.PATCH('/me/availability', { body: { away } }))
