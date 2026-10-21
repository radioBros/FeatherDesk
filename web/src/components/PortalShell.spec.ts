// SPDX-License-Identifier: AGPL-3.0-only

import { defineComponent, h } from 'vue'
import PortalShell from '@/components/PortalShell.vue'
import { mockFetch } from '@/test/mount'
import { mountPage, CUSTOMER } from '@/areas/portal/testing'

afterEach(() => vi.unstubAllGlobals())

const Shell = defineComponent({ render: () => h(PortalShell, null, { default: () => h('div') }) })
const links = (w: { findAll: (s: string) => { attributes: (n: string) => string | undefined }[] }) => w.findAll('a').map((a) => a.attributes('href'))

describe('PortalShell top bar', () => {
  it('portal on: visitors see Sign in, customers see My tickets and New ticket', async () => {
    mockFetch()
    const anon = await mountPage(Shell, { path: '/x', me: null })
    expect(links(anon.wrapper)).toContain('/login')
    expect(anon.wrapper.find('[data-testid="staff-sign-in"]').exists()).toBe(false)
    anon.wrapper.unmount()
    const cust = await mountPage(Shell, { path: '/x', me: CUSTOMER })
    expect(links(cust.wrapper)).toEqual(expect.arrayContaining(['/tickets', '/tickets/new']))
  })

  it('portal off: no customer entry point, a staff sign-in instead, the help centre stays', async () => {
    mockFetch()
    const { wrapper } = await mountPage(Shell, { path: '/kb', me: null, config: { portal_enabled: false, kb_enabled: true } })
    const hrefs = links(wrapper)
    for (const customerOnly of ['/login', '/register', '/tickets', '/tickets/new']) expect(hrefs).not.toContain(customerOnly)
    expect(wrapper.find('[data-testid="staff-sign-in"]').attributes('href')).toBe('/agent/login')
    expect(hrefs).toContain('/kb')
  })

  it('the actions sit in one flex container (spacing is central, in AppBar)', async () => {
    mockFetch()
    const { wrapper } = await mountPage(Shell, { path: '/x', me: null })
    expect(wrapper.find('.st-appbar').exists()).toBe(true)
    expect(wrapper.find('[data-testid="appbar-actions"]').classes()).toContain('st-appbar-actions')
  })
})
