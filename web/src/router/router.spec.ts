// SPDX-License-Identifier: AGPL-3.0-only

import { setActivePinia, createPinia } from 'pinia'
import { createAppRouter } from './index'
import { apiFetch } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { mockFetch, json } from '@/test/mount'

afterEach(() => vi.unstubAllGlobals())

async function boot(path: string, me: object | null, portalEnabled = true) {
  setActivePinia(createPinia())
  const session = useSessionStore()
  session.set(me as never)
  session.checked = true
  useConfigStore().loaded = true
  useConfigStore().config.portal_enabled = portalEnabled
  const router = createAppRouter()
  await router.push(path)
  await router.isReady()
  return router
}

describe('router + 401 handling (FE-006, FE-008)', () => {
  it('a 401 inside /agent/** sends the user to /agent/login', async () => {
    const router = await boot('/agent/tickets', { id: 's', kind: 'staff', role: 'agent', tier: 1, name: 'S', email: 's@x.io', csrf_token: 't' })
    expect(router.currentRoute.value.path).toBe('/agent/tickets')
    mockFetch(() => json({ title: 'expired' }, 401))
    await apiFetch('/agent/tickets').catch(() => null)
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/agent/login'))
    expect(router.currentRoute.value.query.redirect).toBe('/agent/tickets')
  })
  it('a 401 in the portal sends the user to /login', async () => {
    const router = await boot('/tickets', { id: 'c', kind: 'customer', role: 'requester', name: 'C', email: 'c@x.io', csrf_token: 't' })
    mockFetch(() => json({ title: 'expired' }, 401))
    await apiFetch('/portal/tickets').catch(() => null)
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/login'))
  })
  it('anonymous deep link keeps a same-origin redirect', async () => {
    const router = await boot('/tickets/123', null)
    expect(router.currentRoute.value.fullPath).toBe('/login?redirect=/tickets/123')
  })
  it('customer opening the agent panel lands on /tickets', async () => {
    const router = await boot('/agent/tickets', { id: 'c', kind: 'customer', role: 'requester', name: 'C', email: 'c@x.io', csrf_token: 't' })
    expect(router.currentRoute.value.path).toBe('/tickets')
  })
})

describe('customer portal switched off', () => {
  const staffMe = { id: 's', kind: 'staff', role: 'agent', tier: 1, name: 'S', email: 's@x.io', csrf_token: 't' }
  it.each(['/', '/login', '/register', '/forgot-password', '/tickets', '/tickets/new', '/tickets/1', '/profile', '/organization/members', '/magic-link?token=x'])(
    'anonymous %s lands on the staff sign-in',
    async (path) => {
      const router = await boot(path, null, false)
      expect(router.currentRoute.value.path).toBe('/agent/login')
    },
  )
  it('the root goes to the staff panel (a signed-in agent sees the queue)', async () => {
    const router = await boot('/', staffMe, false)
    expect(router.currentRoute.value.path).toBe('/agent/tickets')
  })
  it('a signed-in agent opening a portal URL lands in the agent panel', async () => {
    const router = await boot('/tickets', staffMe, false)
    expect(router.currentRoute.value.path).toBe('/agent/tickets')
  })
  it('the KB and the unknown-URL page keep working', async () => {
    expect((await boot('/kb', null, false)).currentRoute.value.path).toBe('/kb')
    expect((await boot('/no/such/page', null, false)).currentRoute.value.name).toBe('not-found')
  })
  it('an expired session in the KB or an unknown page sends the user to the staff sign-in', async () => {
    const router = await boot('/no/such/page', staffMe, false)
    mockFetch(() => json({ title: 'expired' }, 401))
    await apiFetch('/agent/tickets').catch(() => null)
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/agent/login'))
  })
})
