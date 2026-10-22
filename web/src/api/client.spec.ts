// SPDX-License-Identifier: AGPL-3.0-only

import { api, apiFetch, setCsrfToken, setUnauthorizedHandler, unwrap, newIdempotencyKey } from './client'
import { ApiProblem, applyServerErrors, fieldErrorsFrom } from './problem'
import { mockFetch, json } from '@/test/mount'
import { i18n } from '@/i18n'

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
  setUnauthorizedHandler(null)
})

function headerOf(init: RequestInit, name: string) {
  return new Headers(init.headers).get(name)
}

describe('api client (FE-006)', () => {
  it('adds X-CSRF-Token on mutating requests only', async () => {
    const { calls } = mockFetch(() => json({ ok: true }))
    setCsrfToken('tok123')
    await apiFetch('/things', { method: 'POST', body: '{}' })
    await apiFetch('/things')
    expect(headerOf(calls[0]!.init, 'X-CSRF-Token')).toBe('tok123')
    expect(headerOf(calls[1]!.init, 'X-CSRF-Token')).toBeNull()
  })

  it('typed client sends CSRF via middleware and unwrap returns data', async () => {
    const seen: Request[] = []
    vi.stubGlobal('fetch', vi.fn(async (req: Request) => {
      seen.push(req)
      return json({ id: 'x' })
    }))
    setCsrfToken('abc')
    const data = await unwrap(api.POST('/anything' as never, { body: {} } as never))
    expect(data).toEqual({ id: 'x' })
    expect(seen[0]!.headers.get('X-CSRF-Token')).toBe('abc')
    expect(seen[0]!.credentials).toBe('same-origin')
  })

  it('maps problem+json into ApiProblem with field errors', async () => {
    mockFetch(() =>
      json({ type: 'https://featherdesk.dev/problems/validation', title: 'Invalid', status: 422,
        errors: [{ field: 'email', code: 'email' }, { field: 'name', code: 'max_length', params: { n: 5 } }] }, 422),
    )
    const err = (await apiFetch('/x', { method: 'POST' }).catch((e: unknown) => e)) as ApiProblem
    expect(err).toBeInstanceOf(ApiProblem)
    expect(err.status).toBe(422)
    expect(err.slug).toBe('validation')
    const fields = fieldErrorsFrom(err)
    expect(fields.email).toEqual([i18n.global.t('validation.email')])
    expect(fields.name).toEqual([i18n.global.t('validation.maxLength', { n: 5 })])
    const target: Record<string, string[]> = { stale: ['x'] }
    expect(applyServerErrors(target, err)).toBe(true)
    expect(target.stale).toBeUndefined()
  })

  it('calls the unauthorized handler on 401, except for /auth and /me', async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    mockFetch(() => json({ title: 'no' }, 401))
    await apiFetch('/agent/tickets').catch(() => null)
    expect(handler).toHaveBeenCalledWith('/agent/tickets')
    handler.mockClear()
    await apiFetch('/me').catch(() => null)
    await apiFetch('/auth/login', { method: 'POST' }).catch(() => null)
    expect(handler).not.toHaveBeenCalled()
  })

  it('network failure becomes status 0 with a user message', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch') }))
    const err = (await apiFetch('/x').catch((e: unknown) => e)) as ApiProblem
    expect(err).toBeInstanceOf(ApiProblem)
    expect(err.status).toBe(0)
    expect(err.userMessage).toBe(i18n.global.t('errors.network'))
  })

  it('idempotency keys are unique', () => {
    const keys = new Set(Array.from({ length: 50 }, newIdempotencyKey))
    expect(keys.size).toBe(50)
  })
})
