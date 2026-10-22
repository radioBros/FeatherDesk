// SPDX-License-Identifier: AGPL-3.0-only

import AgentLayout from './AgentLayout.vue'
import { mountAgent, fx, flush } from './testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const COUNTS = { mine: 3, unassigned: 5, triage: 2, tier: { 1: 4, 2: 1 } }
const api = { 'GET /agent/tickets/counts': COUNTS, 'GET /agent/views': [] }

describe('AgentLayout (FE-019)', () => {
  it('has no Admin group for an agent', async () => {
    const h = await mountAgent(AgentLayout, { me: fx.me('agent', 1), api })
    expect(h.wrapper.find('[data-testid="nav-admin"]').exists()).toBe(false)
  })

  it('shows the Admin group with every admin page for an admin, support pages for a supervisor', async () => {
    const admin = await mountAgent(AgentLayout, { me: fx.me('admin'), api })
    expect(admin.wrapper.find('[data-testid="nav-admin"]').exists()).toBe(true)
    admin.wrapper.unmount()
    const sup = await mountAgent(AgentLayout, { me: fx.me('supervisor'), api })
    expect(sup.wrapper.find('[data-testid="nav-admin"]').exists()).toBe(true)
    await sup.wrapper.find('[data-testid="nav-admin"] .v-list-group__header').trigger('click')
    await flush()
    const titles = sup.wrapper.findAll('[data-testid="nav-admin-item"]').map((w) => w.text())
    expect(titles).toContain('Teams')
    expect(titles).not.toContain('Branding') // admin-only per the route guard
  })

  it('renders counts from REST, then updates from a WS counts frame without another request', async () => {
    const h = await mountAgent(AgentLayout, { me: fx.me('agent', 2), api })
    await h.flush()
    expect(h.wrapper.find('[data-testid="queue-mine"]').text()).toContain('3')
    expect(h.wrapper.find('[data-testid="queue-unassigned"]').text()).toContain('5')
    expect(h.socket().frames('subscribe').flatMap((f) => f.topics as string[])).toContain('counts')
    h.socket().push({ type: 'counts', data: { mine: 7, unassigned: 1, triage: 0, tier: { 1: 9, 2: 0 } } })
    await h.flush()
    expect(h.wrapper.find('[data-testid="queue-mine"]').text()).toContain('7')
    expect(h.wrapper.find('[data-testid="queue-tier-1"]').text()).toContain('9')
    expect(h.calls('GET /agent/tickets/counts')).toHaveLength(1)
  })

  it('global search: #1042 + Enter opens the ticket, text searches the list', async () => {
    const h = await mountAgent(AgentLayout, { me: fx.me('agent', 1), api })
    const input = h.wrapper.find('[data-testid="global-search"] input')
    await input.setValue('#1042')
    await input.trigger('keydown', { key: 'Enter' })
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/1042')
    await input.setValue('printer')
    await input.trigger('keydown', { key: 'Enter' })
    await h.flush()
    expect(h.router.currentRoute.value.fullPath).toBe('/agent/tickets?q=printer')
  })

  it('lists saved views in the drawer and opens the shortcut help with ?', async () => {
    const h = await mountAgent(AgentLayout, {
      me: fx.me('agent', 1),
      api: { ...api, 'GET /agent/views': [{ id: 'v-1', name: 'Hot printers', shared: true, filter: { q: 'printer' } }] },
    })
    await h.flush()
    const view = h.wrapper.find('[data-testid="nav-view"]')
    expect(view.text()).toContain('Hot printers')
    expect(view.attributes('href')).toBe('/agent/tickets?view_id=v-1')
    // The help dialog is a lazy chunk (FE-031): load the module first so it renders on the next ticks.
    await import('./shared/ShortcutsHelp.vue')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }))
    await h.flush()
    expect(document.body.querySelector('[data-testid="shortcuts-help"]')).not.toBeNull()
  })
})
