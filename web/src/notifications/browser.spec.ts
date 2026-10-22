// SPDX-License-Identifier: AGPL-3.0-only

import * as browser from './browser'
import { detailOf, linkFor, refOf, titleOf } from './types'
import { installNotification, loadNotificationMessagesForTests, notification } from './testing'
import { t } from '@/i18n'

beforeAll(loadNotificationMessagesForTests)
afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('browser notifications (FE-094)', () => {
  it('shows a tagged pop-up only with the permission; a click focuses the tab and runs the action', () => {
    const N = installNotification('default')
    expect(browser.show({ tag: 'n-1', title: 'T', body: 'B', onClick: () => {} })).toBeNull()
    N.permission = 'granted'
    const onClick = vi.fn()
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})
    browser.show({ tag: 'n-1', title: 'T', body: 'B', onClick })
    expect(N.instances).toHaveLength(1)
    expect(N.instances[0]!.options).toMatchObject({ tag: 'n-1', body: 'B' })
    N.instances[0]!.onclick!(new Event('click'))
    expect(focus).toHaveBeenCalled()
    expect(onClick).toHaveBeenCalled()
    expect(N.instances[0]!.close).toHaveBeenCalled()
  })

  it('plays the optional sound with WebAudio only when enabled', () => {
    installNotification('granted')
    const start = vi.fn()
    class Ctx {
      currentTime = 0
      destination = {}
      createOscillator() {
        return { frequency: { value: 0 }, connect: vi.fn(), start, stop: vi.fn(), onended: null }
      }
      createGain() {
        return { gain: { value: 0 }, connect: vi.fn() }
      }
      close = vi.fn()
    }
    vi.stubGlobal('AudioContext', Ctx)
    browser.show({ tag: 'a', title: 'T', body: '', onClick: () => {} })
    expect(start).not.toHaveBeenCalled()
    browser.setSoundEnabled(true)
    expect(browser.soundEnabled()).toBe(true)
    browser.show({ tag: 'b', title: 'T', body: '', onClick: () => {} })
    expect(start).toHaveBeenCalledOnce()
    browser.setSoundEnabled(false)
    expect(browser.soundEnabled()).toBe(false)
  })

  it('reports unsupported browsers', async () => {
    const saved = (window as unknown as { Notification?: unknown }).Notification
    delete (window as unknown as { Notification?: unknown }).Notification
    try {
      expect(browser.permission()).toBe('unsupported')
      expect(await browser.requestPermission()).toBe('unsupported')
    } finally {
      if (saved) (window as unknown as { Notification?: unknown }).Notification = saved
    }
  })
})

describe('notification texts and links (FE-091)', () => {
  const tier = (n: number) => `L${n}`
  it('builds titles per type, with fallbacks', () => {
    expect(titleOf(notification({ type: 'staff.ticket_new' }), t, tier)).toBe('New ticket HD-1042')
    expect(titleOf(notification({ type: 'staff.customer_replied', actor_name: null }), t, tier)).toBe('Someone replied on HD-1042')
    expect(titleOf(notification({ type: 'staff.escalated', params: { from_tier: 1, to_tier: 3 } }), t, tier)).toBe('HD-1042 was escalated to L3')
    expect(titleOf(notification({ type: 'staff.sla', params: { sla: 'first_response' } }), t, tier)).toBe('SLA warning on HD-1042')
    expect(titleOf(notification({ type: 'customer.resolved', ticket_ref: 'WEB-7' }), t, tier)).toBe('Your request WEB-7 was resolved')
    expect(detailOf(notification({ type: 'staff.sla', params: { sla: 'first_response' } }), t)).toBe('Printer on fire · First response due')
  })
  it('shows the ref, else #number (a live frame of an event written before projects)', () => {
    expect(refOf(notification())).toBe('HD-1042')
    expect(refOf(notification({ ticket_ref: null }))).toBe('#1042')
    expect(refOf(notification({ ticket_ref: null, ticket_number: null }))).toBe('')
    expect(titleOf(notification({ type: 'staff.ticket_new', ticket_ref: null }), t, tier)).toBe('New ticket #1042')
  })
  it('links to the ticket in the right area', () => {
    expect(linkFor(notification(), 'agent')).toBe('/agent/tickets/1042')
    expect(linkFor(notification(), 'portal')).toBe('/tickets/t-1')
    expect(linkFor(notification({ ticket_id: null, ticket_number: null }), 'portal')).toBe('/notifications')
  })
})
