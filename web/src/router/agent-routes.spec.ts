// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Agent route table (FE-008/FE-027…FE-059): every admin page is a lazy chunk
 * guarded by role meta (UX only; the API enforces). Supervisors reach the
 * support-config pages but are sent away from admin-only ones; agents reach none.
 */
import { setActivePinia, createPinia } from 'pinia'
import { createAppRouter } from './index'
import { agentRoutes, adminNav } from './agent-routes'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { mockFetch, json } from '@/test/mount'

afterEach(() => vi.unstubAllGlobals())

const staff = (role: string, tier = 1) => ({ id: 's', kind: 'staff', role, tier, name: 'S', email: 's@x.io', csrf_token: 't' })

async function land(path: string, me: object) {
  setActivePinia(createPinia())
  mockFetch(() => json({}))
  const session = useSessionStore()
  session.set(me as never)
  session.checked = true
  useConfigStore().loaded = true
  const router = createAppRouter()
  await router.push(path)
  await router.isReady()
  return router.currentRoute.value.path
}

const ADMIN_ONLY = ['/agent/admin/staff', '/agent/admin/settings', '/agent/admin/system', '/agent/admin/queue', '/agent/admin/branding', '/agent/admin/email', '/agent/admin/webhooks', '/agent/admin/audit', '/agent/admin/jobs', '/agent/admin/tokens', '/agent/admin/custom-fields', '/agent/admin/inbound', '/agent/admin/inbound/log', '/agent/admin/storage', '/agent/admin/privacy']
const SUPPORT = ['/agent/admin/teams', '/agent/admin/categories', '/agent/admin/sla', '/agent/admin/canned', '/agent/admin/tags', '/agent/admin/calendars', '/agent/admin/kb-categories', '/agent/admin/automation', '/agent/admin/macros']

describe('agent routes', () => {
  it('every agent page is lazy (dynamic import), so admin code is never in the initial chunk', () => {
    const children = agentRoutes.find((r) => r.path === '/agent')!.children!
    for (const r of children.filter((c) => c.component)) expect(typeof r.component).toBe('function')
    expect(adminNav.map((n) => n.to).sort()).toEqual([...ADMIN_ONLY.filter((p) => p !== '/agent/admin/inbound/log'), ...SUPPORT].sort())
  })

  it.each(ADMIN_ONLY)('%s: supervisors are redirected, admins land', async (path) => {
    expect(await land(path, staff('supervisor'))).toBe('/agent/tickets')
    expect(await land(path, staff('admin'))).toBe(path)
  })

  it.each(SUPPORT)('%s: supervisors land, agents are redirected', async (path) => {
    expect(await land(path, staff('supervisor'))).toBe(path)
    expect(await land(path, staff('agent'))).toBe('/agent/tickets')
  })

  it('/agent/kb/articles/new is its own route, not an article id', async () => {
    expect(await land('/agent/kb/articles/new', staff('agent'))).toBe('/agent/kb/articles/new')
    const r = createAppRouter().resolve('/agent/kb/articles/new')
    expect(r.name).toBe('agent-kb-article-new')
  })

  it('ticket detail matches numbers only', () => {
    setActivePinia(createPinia())
    const router = createAppRouter()
    expect(router.resolve('/agent/tickets/1042').name).toBe('agent-ticket')
    expect(router.resolve('/agent/tickets/new').name).toBe('agent-ticket-new')
  })
})
