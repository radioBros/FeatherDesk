// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The single HTTP client (FE-006): openapi-fetch typed against the generated
 * `paths`, same-origin cookies, CSRF header on mutations, problem+json → ApiProblem,
 * and a 401 hook that sends the user to the right login page.
 */
import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from './schema'
import { ApiProblem, type ProblemBody } from './problem'

export const API_BASE = '/api/v1'

let csrfToken: string | null = null
export function setCsrfToken(token: string | null) {
  csrfToken = token
}
export function getCsrfToken() {
  return csrfToken
}

type UnauthorizedHandler = (path: string) => void
let onUnauthorized: UnauthorizedHandler | null = null
export function setUnauthorizedHandler(fn: UnauthorizedHandler | null) {
  onUnauthorized = fn
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])
/** Endpoints where 401 is an expected answer, not an expired session. */
const SILENT_401 = [/\/auth\//, /\/me$/, /\/setup\//]

export const csrfMiddleware: Middleware = {
  onRequest({ request }) {
    if (MUTATING.has(request.method) && csrfToken) request.headers.set('X-CSRF-Token', csrfToken)
    return request
  },
}

export const unauthorizedMiddleware: Middleware = {
  onResponse({ request, response }) {
    if (response.status === 401 && onUnauthorized) {
      const path = new URL(request.url, 'http://x').pathname
      if (!SILENT_401.some((re) => re.test(path))) onUnauthorized(path)
    }
    return response
  },
}

// fetch is resolved per call (not captured at creation) so tests and polyfills can replace it.
export const api = createClient<paths>({
  // Absolute so `new Request()` also works outside browsers (tests, SSR-free tooling).
  baseUrl: (typeof location !== 'undefined' ? location.origin : 'http://localhost') + API_BASE,
  credentials: 'same-origin',
  fetch: (req: Request) => globalThis.fetch(req),
})
api.use(csrfMiddleware, unauthorizedMiddleware)

/** A fresh key for `Idempotency-Key` on ticket/message POSTs (FE-045, PLT-020). */
export function newIdempotencyKey(): string {
  return typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

/**
 * Returns `data` or throws an ApiProblem. Usage:
 *   const ticket = await unwrap(api.GET('/agent/tickets/{id}', { params: { path: { id } } }))
 */
export async function unwrap<T>(p: Promise<FetchResult<T>>): Promise<T> {
  let res: FetchResult<T>
  try {
    res = await p
  } catch (e) {
    throw new ApiProblem(0, { title: e instanceof Error ? e.message : 'network error' })
  }
  if (res.response.ok) return res.data as T
  const body: ProblemBody =
    res.error && typeof res.error === 'object' ? (res.error as ProblemBody) : { title: String(res.error ?? '') }
  throw new ApiProblem(res.response.status, body)
}

/** Plain JSON fetch for endpoints outside the typed client (e.g. before codegen). */
export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const method = (init.method ?? 'GET').toUpperCase()
  if (MUTATING.has(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type'))
    headers.set('Content-Type', 'application/json')
  let response: Response
  try {
    response = await fetch(API_BASE + path, { ...init, method, headers, credentials: 'same-origin' })
  } catch (e) {
    throw new ApiProblem(0, { title: e instanceof Error ? e.message : 'network error' })
  }
  if (response.status === 401 && onUnauthorized && !SILENT_401.some((re) => re.test(path))) onUnauthorized(path)
  const text = await response.text()
  const data = text ? safeJSON(text) : undefined
  if (!response.ok) throw new ApiProblem(response.status, (data as ProblemBody) ?? {})
  return data as T
}

function safeJSON(s: string): unknown {
  try {
    return JSON.parse(s)
  } catch {
    return { title: s }
  }
}
