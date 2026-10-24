// SPDX-License-Identifier: AGPL-3.0-only

import TicketListPage from '../TicketListPage.vue'
import { mountAgent, fx, json } from '../../testing'
import { setViewportWidth } from '@/test/mount'

// The keyboard shortcuts drive the desktop table; phones get the card list.
beforeEach(() => setViewportWidth(1280))

afterEach(() => {
  setViewportWidth(1024)
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const api = {
  'GET /agent/tickets': () => json(fx.page([fx.summary(), fx.summary({ id: 't-2', number: 1043 })])),
  'GET /agent/teams': [],
  'GET /agent/categories': [],
  'GET /agent/staff': [],
  'GET /agent/custom-fields': [],
  'GET /agent/views': [],
}
const key = (k: string, target: EventTarget = window) => target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }))
const focusedIndex = () => [...document.querySelectorAll('[data-testid="ticket-row"]')].findIndex((r) => r.classList.contains('fd-row--focused'))

describe('list keyboard shortcuts (FE-038)', () => {
  it('j/k move the focus, x selects, Enter opens', async () => {
    const h = await mountAgent(TicketListPage, { path: '/agent/tickets', route: '/agent/tickets', api })
    key('j')
    await h.flush()
    expect(focusedIndex()).toBe(0)
    key('j')
    await h.flush()
    expect(focusedIndex()).toBe(1)
    key('k')
    await h.flush()
    expect(focusedIndex()).toBe(0)
    key('x')
    await h.flush()
    expect(h.wrapper.find('[data-testid="bulk-bar"]').exists()).toBe(true)
    key('Enter')
    await h.flush()
    expect(h.router.currentRoute.value.path).toBe('/agent/tickets/WEB-1042')
  })

  it('j/k are ignored while an input has focus', async () => {
    const h = await mountAgent(TicketListPage, { path: '/agent/tickets', route: '/agent/tickets', api })
    const input = h.wrapper.find('[data-testid="filter-q"] input').element as HTMLInputElement
    input.focus()
    key('j', input)
    key('j', input)
    await h.flush()
    expect(focusedIndex()).toBe(-1)
    input.blur()
    key('j')
    await h.flush()
    expect(focusedIndex()).toBe(0)
  })
})
