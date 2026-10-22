// SPDX-License-Identifier: AGPL-3.0-only

/**
 * FE-046: presence is staff-only. The portal ticket detail, mounted on a live
 * socket and typed into, sends zero presence frames.
 */
import PortalTicketDetailPage from '@/areas/portal/pages/TicketDetailPage.vue'
import { mountAgent, fx, json } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

describe('presence (FE-046)', () => {
  it('the portal ticket detail never sends a presence frame', async () => {
    const ticket = {
      id: 't1', number: 12, subject: 'Printer offline', status: 'open', priority: 'normal',
      requester: { id: 'u-cust', name: 'Carla', kind: 'customer' }, created_at: fx.iso, updated_at: fx.iso,
      messages: [{ id: 'm1', ticket_id: 't1', kind: 'public', body_md: 'Hello', attachments: [], created_at: fx.iso }],
      allowed_actions: ['reply'],
    }
    const h = await mountAgent(PortalTicketDetailPage, {
      path: '/tickets/:id',
      route: '/tickets/t1',
      me: { kind: 'customer', role: 'requester', tier: null, teams: [] },
      api: { 'GET /portal/tickets/t1': () => json(ticket), 'GET /portal/custom-fields': [] },
    })
    await h.flush()
    const ta = document.querySelector('textarea')
    if (ta) {
      ta.value = 'typing a reply'
      ta.dispatchEvent(new Event('input', { bubbles: true }))
      ta.dispatchEvent(new Event('blur'))
    }
    await h.flush()
    h.wrapper.unmount()
    expect(h.socket().frames().length).toBeGreaterThan(0) // the socket is live (subscribe frames)
    expect(h.socket().frames('presence')).toEqual([])
  })
})
