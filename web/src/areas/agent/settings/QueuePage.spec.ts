// SPDX-License-Identifier: AGPL-3.0-only

import QueuePage from './QueuePage.vue'
import { mountAgent, fx, byTestId, click } from '../testing'

afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const QUEUE = {
  deferred: { retrying: 1, dead: 4 },
  dispatcher: { lag_events: 0 },
  queues: [
    { name: 'notify', depth: 9, capacity: 10, workers: 2 },
    { name: 'webhook', depth: 0, capacity: 100, workers: 1 },
  ],
}

describe('QueuePage (FE-029)', () => {
  it('renders the fixture depths and links the dead count to the jobs page', async () => {
    const h = await mountAgent(QueuePage, { me: fx.me('admin'), api: { 'GET /admin/queue': QUEUE } })
    await h.flush()
    expect(h.wrapper.findAll('[data-testid="queue-row"]')).toHaveLength(2)
    expect(h.wrapper.text()).toContain('notify')
    const dead = byTestId('queue-dead')!
    expect(dead.textContent).toContain('4')
    expect(dead.getAttribute('href')).toBe('/agent/admin/jobs?status=dead')
  })

  it('subscribes to admin:system and refetches on events and resync, without timers', async () => {
    const h = await mountAgent(QueuePage, { me: fx.me('admin'), api: { 'GET /admin/queue': QUEUE } })
    await h.flush()
    const sock = h.socket()
    expect(sock.frames('subscribe').some((f) => (f.topics as string[]).includes('admin:system'))).toBe(true)
    expect(h.calls('GET /admin/queue')).toHaveLength(1)
    sock.push({ type: 'event', at: '2026-10-20T10:00:00Z', topic: 'admin:system', event: 'queue.changed', event_id: 'e1', data: {} })
    await h.flush()
    expect(h.calls('GET /admin/queue')).toHaveLength(2)
    sock.push({ type: 'resync', reason: 'gap_too_large' })
    await h.flush()
    expect(h.calls('GET /admin/queue')).toHaveLength(3)
  })

  it('refreshes manually', async () => {
    const h = await mountAgent(QueuePage, { me: fx.me('admin'), api: { 'GET /admin/queue': QUEUE } })
    await h.flush()
    await click(byTestId('queue-refresh'))
    await h.flush()
    expect(h.calls('GET /admin/queue')).toHaveLength(2)
  })
})
