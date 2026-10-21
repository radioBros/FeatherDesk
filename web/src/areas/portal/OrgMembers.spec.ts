// SPDX-License-Identifier: AGPL-3.0-only

import { createMemoryHistory, createRouter } from 'vue-router'
import OrgMembersPage from './pages/OrgMembersPage.vue'
import { decide } from '@/router/guards'
import { portalRoutes } from '@/router/routes'
import { mockFetch, json } from '@/test/mount'
import { mountPage, requests, resolveFor, settle, CUSTOMER, ORG_MANAGER } from './testing'

afterEach(() => vi.unstubAllGlobals())

const MEMBERS = [
  { id: 'u-mgr', name: 'Mario', kind: 'customer', email: 'mario@acme.test', role: 'org_manager', status: 'active' },
  { id: 'u-2', name: 'Lucia', kind: 'customer', email: 'lucia@acme.test', role: 'requester', status: 'active' },
]

describe('organization members (FE-035)', () => {
  it('the route is redirected for role=requester', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: portalRoutes })
    const to = resolveFor(router, '/organization/members')
    expect(decide(to, { me: CUSTOMER, setupPending: false })).toEqual({ path: '/tickets' })
    expect(decide(to, { me: ORG_MANAGER, setupPending: false })).toBe(true)
  })

  it('lists members, disables one, and the invite form passes FE-005', async () => {
    const { fn } = mockFetch((url, init) => {
      if ((init.method ?? 'GET') === 'PATCH') return json({ ...MEMBERS[1], status: 'disabled' })
      if ((init.method ?? 'GET') === 'POST') return json({ id: 'u-3', name: 'Nina', kind: 'customer', email: 'nina@acme.test', role: 'requester', status: 'pending_verification' }, 201)
      return json(MEMBERS)
    })
    const { wrapper } = await mountPage(OrgMembersPage, { path: '/organization/members', me: ORG_MANAGER })
    await settle(8)
    const rows = wrapper.findAll('[data-testid="member-row"]')
    expect(rows).toHaveLength(2)
    // no controls on yourself
    expect(rows[0]!.find('[data-testid="member-toggle"]').exists()).toBe(false)
    await rows[1]!.find('[data-testid="member-toggle"]').trigger('click')
    await settle(8)
    expect((await requests(fn)).find((r) => r.method === 'PATCH')).toMatchObject({ path: '/portal/organization/members/u-2', body: { status: 'disabled' } })
    expect(rows[1]!.text()).toContain('Disabled')

    await wrapper.find('[data-testid="invite-open"]').trigger('click')
    await settle()
    const form = document.body.querySelector('[data-testid="org-invite-form"]') as HTMLFormElement
    form.dispatchEvent(new Event('submit'))
    await settle()
    expect((await requests(fn)).filter((r) => r.method === 'POST')).toHaveLength(0)
  })
})
