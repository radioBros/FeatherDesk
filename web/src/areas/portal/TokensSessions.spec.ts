// SPDX-License-Identifier: AGPL-3.0-only

import TokensPage from './pages/TokensPage.vue'
import SessionsPage from './pages/SessionsPage.vue'
import { mockFetch, json } from '@/test/mount'
import { settleConfirm } from '@/composables/useConfirm'
import { mountPage, requests, settle } from './testing'

afterEach(() => vi.unstubAllGlobals())

describe('portal API tokens (FE-037)', () => {
  it('the customer scope list never offers directory:* or admin', async () => {
    mockFetch(() => json([]))
    const { wrapper } = await mountPage(TokensPage, { path: '/profile/tokens' })
    await settle(6)
    await wrapper.find('[data-testid="token-scopes"] .v-field').trigger('mousedown')
    await settle()
    const text = document.body.textContent ?? ''
    expect(text).toContain('tickets:write')
    expect(text).not.toMatch(/directory:|admin|kb:write/)
  })

  it('creates a token (shown once) with only customer scopes; empty name blocks the request', async () => {
    const { fn } = mockFetch((_url, init) =>
      (init.method ?? 'GET') === 'POST'
        ? json({ id: 'k1', name: 'CI', prefix: 'st_pat_ab', scopes: ['tickets:read'], created_at: '2026-10-20T10:00:00Z', token: 'st_pat_abcdef123' }, 201)
        : json([]),
    )
    const { wrapper } = await mountPage(TokensPage, { path: '/profile/tokens' })
    await settle(6)
    const before = fn.mock.calls.length
    await wrapper.find('[data-testid="token-form"]').trigger('submit')
    await settle()
    expect(fn.mock.calls.length).toBe(before)
    await wrapper.find('[data-testid="token-name"] input').setValue('CI')
    await wrapper.find('[data-testid="token-form"]').trigger('submit')
    await settle(8)
    const post = (await requests(fn)).find((r) => r.method === 'POST')!
    expect(post).toMatchObject({ path: '/me/tokens', body: { name: 'CI', scopes: ['tickets:read'], expires_at: null } })
    expect(wrapper.find('[data-testid="token-secret"]').text()).toContain('st_pat_abcdef123')
  })
})

describe('portal sessions (FE-037)', () => {
  const SESSIONS = [
    { id: 's1', audience: 'customer', created_at: '2026-10-20T10:00:00Z', expires_at: '2026-11-20T10:00:00Z', current: true, user_agent: 'Firefox' },
    { id: 's2', audience: 'customer', created_at: '2026-10-19T10:00:00Z', expires_at: '2026-11-19T10:00:00Z', current: false, user_agent: 'Phone' },
  ]

  it('revoking another session removes it from the list', async () => {
    const { fn } = mockFetch((_u, init) => ((init.method ?? 'GET') === 'DELETE' ? new Response(null, { status: 204 }) : json(SESSIONS)))
    const { wrapper } = await mountPage(SessionsPage, { path: '/profile/sessions' })
    await settle(6)
    await wrapper.findAll('[data-testid="session-revoke"]')[1]!.trigger('click')
    await settle()
    settleConfirm(true)
    await settle(8)
    expect((await requests(fn)).find((r) => r.method === 'DELETE')!.path).toBe('/me/sessions/s2')
    expect(wrapper.findAll('[data-testid="session-row"]')).toHaveLength(1)
  })

  it('revoking the current session logs out', async () => {
    mockFetch((_u, init) => ((init.method ?? 'GET') === 'DELETE' ? new Response(null, { status: 204 }) : json(SESSIONS)))
    const { wrapper, router, session } = await mountPage(SessionsPage, { path: '/profile/sessions' })
    await settle(6)
    await wrapper.findAll('[data-testid="session-revoke"]')[0]!.trigger('click')
    await settle()
    settleConfirm(true)
    await settle(8)
    expect(session.me).toBeNull()
    expect(router.currentRoute.value.path).toBe('/login')
  })
})
