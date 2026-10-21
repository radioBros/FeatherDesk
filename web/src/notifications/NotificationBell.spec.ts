// SPDX-License-Identifier: AGPL-3.0-only

import { mountAgent, json, byTestId, allByTestId, click, fx } from '@/areas/agent/testing'
import { useNotificationsStore } from './store'
import NotificationBell from './NotificationBell.vue'
import { HD, WEB, installNotification, loadNotificationMessagesForTests, notification, setVisibility } from './testing'

beforeAll(loadNotificationMessagesForTests)
afterEach(() => {
  setVisibility('visible')
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const count = () => byTestId('notification-count')?.textContent?.trim()
const badgeShown = () => {
  const b = document.body.querySelector<HTMLElement>('[data-testid="notification-badge"] .v-badge__badge')
  return !!b && b.style.display !== 'none'
}

function created(n = notification(), unread = 1, browser = false) {
  return { type: 'event' as const, topic: 'me', event: 'notification.created', at: n.created_at, data: { notification: n, unread_count: unread, browser } }
}

describe('NotificationBell (FE-090, FE-093, FE-094)', () => {
  it('shows the unread count, subscribes to me as essential and follows created/read frames live', async () => {
    const h = await mountAgent(NotificationBell, { props: { area: 'agent' }, api: { 'GET /me/notifications/unread-count': { unread_count: 2 } } })
    await h.flush()
    expect(count()).toBe('2')
    expect(h.socket().frames('subscribe').flatMap((f) => f.topics as string[])).toContain('me')
    h.socket().push(created(notification(), 3))
    await h.flush()
    expect(count()).toBe('3')
    expect(byTestId('notification-bell')?.getAttribute('aria-label')).toBe('Notifications, 3 unread')
    h.socket().push({ type: 'event', topic: 'me', event: 'notification.read', at: '2026-10-21T09:01:00Z', data: { all: true, unread_count: 0 } })
    await h.flush()
    expect(useNotificationsStore().unread).toBe(0)
    await vi.waitFor(() => expect(badgeShown()).toBe(false))
    expect(byTestId('notification-bell')?.getAttribute('aria-label')).toBe('Notifications')
  })

  it('refetches the count after a reconnect and when the tab becomes visible (me frames are never replayed)', async () => {
    let n = 1
    const h = await mountAgent(NotificationBell, { props: { area: 'agent' }, api: { 'GET /me/notifications/unread-count': () => json({ unread_count: n }) } })
    await h.flush()
    expect(count()).toBe('1')
    n = 4
    setVisibility('visible')
    await h.flush()
    expect(count()).toBe('4')
    n = 6
    h.socket().close(1006) // dropped; the client reconnects (timers are stubbed: open the next socket by hand)
    const before = h.calls('GET /me/notifications/unread-count').length
    const rt = (await import('@/stores/realtime')).useRealtimeStore()
    ;(rt.client as unknown as { connect: () => void }).connect()
    h.socket().open()
    await h.flush()
    expect(h.calls('GET /me/notifications/unread-count').length).toBeGreaterThan(before)
    expect(count()).toBe('6')
  })

  it('raises a browser notification only when hidden and the browser channel is on; clicking opens the ticket and marks it read', async () => {
    const N = installNotification('granted')
    const h = await mountAgent(NotificationBell, {
      props: { area: 'agent' },
      api: { 'GET /me/notifications/unread-count': { unread_count: 0 }, 'POST /me/notifications/n-1/read': { unread_count: 0 } },
    })
    h.socket().push(created(notification({ id: 'n-0' }), 1, true)) // visible tab: no pop-up
    h.socket().push(created(notification({ id: 'n-x' }), 1, false))
    await h.flush()
    setVisibility('hidden')
    h.socket().push(created(notification({ id: 'n-2' }), 2, false)) // channel off
    await h.flush()
    expect(N.instances).toHaveLength(0)
    h.socket().push(created(notification(), 3, true))
    await h.flush()
    await h.flush()
    await vi.waitFor(() => expect(N.instances).toHaveLength(1)) // the pop-up module is imported lazily (slow on a cold CI runner)
    const pop = N.instances[0]!
    expect(pop.title).toBe('Sam Supervisor assigned HD-1042 to you')
    expect(pop.options).toMatchObject({ body: 'Printer on fire', tag: 'n-1' })
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {})
    pop.onclick!(new Event('click'))
    await h.flush()
    expect(focus).toHaveBeenCalled()
    expect(h.router.currentRoute.value.fullPath).toBe('/agent/tickets/1042')
    expect(h.calls('POST /me/notifications/n-1/read')).toHaveLength(1)
    expect(pop.close).toHaveBeenCalled()
  })

  it('no pop-up without the permission', async () => {
    const N = installNotification('denied')
    const h = await mountAgent(NotificationBell, { props: { area: 'agent' }, api: { 'GET /me/notifications/unread-count': { unread_count: 0 } } })
    setVisibility('hidden')
    h.socket().push(created(notification(), 1, true))
    await h.flush()
    await h.flush()
    expect(N.instances).toHaveLength(0)
  })

  it('opens the dropdown with the latest entries; an entry opens the portal ticket for a customer', async () => {
    const h = await mountAgent(NotificationBell, {
      props: { area: 'portal' },
      me: fx.me('agent', 1, { kind: 'customer', role: 'requester' }),
      api: {
        'GET /me/notifications/unread-count': { unread_count: 1 },
        'GET /me/notifications': { items: [notification({ type: 'customer.reply', actor_name: 'Bob' })], next_cursor: null, unread_count: 1 },
        'POST /me/notifications/n-1/read': { unread_count: 0 },
      },
    })
    await click(byTestId('notification-bell'))
    await vi.waitFor(() => expect(byTestId('notification-menu')?.textContent).toContain('Bob replied to your request HD-1042'))
    await click(document.body.querySelector('[data-testid="notification-item"]'))
    await h.flush()
    expect(h.router.currentRoute.value.fullPath).toBe('/tickets/t-1')
    expect(h.calls('POST /me/notifications/n-1/read')).toHaveLength(1)
    expect(count()).toBe('0')
  })

  it('dropdown (agent, several projects): refs and project chips, also on an entry that arrives live', async () => {
    const h = await mountAgent(NotificationBell, {
      props: { area: 'agent' },
      api: {
        'GET /me/notifications/unread-count': { unread_count: 1 },
        'GET /me/notifications': { items: [notification()], next_cursor: null, unread_count: 1 },
        'GET /agent/projects': [fx.project({ id: HD.id, key: 'HD' }), fx.project()],
      },
    })
    await click(byTestId('notification-bell'))
    await vi.waitFor(() => expect(allByTestId('project-chip').map((c) => c.textContent?.trim())).toEqual(['HD']))
    expect(byTestId('notification-menu')?.textContent).toContain('assigned HD-1042 to you')
    h.socket().push(created(notification({ id: 'n-2', ticket_ref: 'WEB-7', project: WEB }), 2))
    await vi.waitFor(() => expect(allByTestId('project-chip').map((c) => c.textContent?.trim())).toEqual(['WEB', 'HD']))
    expect(allByTestId('notification-item')[0]!.textContent).toContain('assigned WEB-7 to you')
  })

  it('dropdown (portal, one project): the ref without a chip', async () => {
    const h = await mountAgent(NotificationBell, {
      props: { area: 'portal' },
      me: fx.me('agent', 1, { kind: 'customer', role: 'requester' }),
      api: {
        'GET /me/notifications/unread-count': { unread_count: 1 },
        'GET /me/notifications': { items: [notification({ type: 'customer.resolved' })], next_cursor: null, unread_count: 1 },
        'GET /portal/projects': [{ ...HD, description: '', icon: '', logo_url: null, portal_intro_md: null, portal_title: null, primary_color: null }],
      },
    })
    await click(byTestId('notification-bell'))
    await vi.waitFor(() => expect(byTestId('notification-menu')?.textContent).toContain('Your request HD-1042 was resolved'))
    await h.flush()
    expect(h.calls('GET /portal/projects')).toHaveLength(1)
    expect(allByTestId('project-chip')).toHaveLength(0)
  })
})
