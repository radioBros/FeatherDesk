// SPDX-License-Identifier: AGPL-3.0-only

import { mountAgent, json, byTestId, allByTestId, click } from '@/areas/agent/testing'
import NotificationsPage from './NotificationsPage.vue'
import NotificationBell from './NotificationBell.vue'
import { defineComponent, h as hFn } from 'vue'
import { loadNotificationMessagesForTests, notification } from './testing'

beforeAll(loadNotificationMessagesForTests)
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

// The page plus the bell (which binds the store to the socket), as in the layouts.
const WithBell = defineComponent({ render: () => hFn('div', [hFn(NotificationBell, { area: 'agent' }), hFn(NotificationsPage)]) })

const page1 = {
  items: [notification({ id: 'n-3' }), notification({ id: 'n-2', type: 'staff.sla', params: { sla: 'resolution', breached: true }, actor_name: null })],
  next_cursor: 'n-2',
  unread_count: 2,
}
const page2 = { items: [notification({ id: 'n-1', read_at: '2026-10-21T09:30:00Z', type: 'staff.escalated', params: { from_tier: 1, to_tier: 2 } })], next_cursor: null, unread_count: 2 }

describe('NotificationsPage (FE-092)', () => {
  it('lists newest first, renders each type, pages with the cursor and filters unread / by type', async () => {
    const h = await mountAgent(NotificationsPage, {
      path: '/agent/notifications',
      api: { 'GET /me/notifications': (url) => json(url.searchParams.get('cursor') === 'n-2' ? page2 : page1) },
    })
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(2))
    expect(allByTestId('notification-item')[1]!.textContent).toContain('SLA breached on #1042')
    expect(allByTestId('notification-item')[1]!.textContent).toContain('Resolution due')
    await click(byTestId('notifications-more'))
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(3))
    expect(allByTestId('notification-item')[2]!.textContent).toContain('#1042 was escalated to L2')
    expect(allByTestId('notification-item')[2]!.getAttribute('data-unread')).toBe('false')
    expect(byTestId('notifications-more')).toBeNull()
    await click(byTestId('notifications-filter-unread'))
    await vi.waitFor(() => expect(h.calls(/GET \/me\/notifications\?.*unread=true/)).toHaveLength(1))
    const vm = h.wrapper.vm as unknown as { $: { setupState: { type: string | null } } }
    vm.$.setupState.type = 'staff.sla'
    await vi.waitFor(() => expect(h.calls(/GET \/me\/notifications\?.*type=staff\.sla/)).toHaveLength(1))
  })

  it('marks one read, deletes one and marks all read', async () => {
    const h = await mountAgent(NotificationsPage, {
      path: '/agent/notifications',
      api: {
        'GET /me/notifications': page1,
        'GET /me/notifications/unread-count': { unread_count: 1 },
        'POST /me/notifications/n-3/read': { unread_count: 1 },
        'DELETE /me/notifications/n-2': new Response(null, { status: 204 }),
        'POST /me/notifications/read-all': { unread_count: 0 },
      },
    })
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(2))
    await click(allByTestId('notification-mark-read')[0]!)
    await vi.waitFor(() => expect(allByTestId('notification-item')[0]!.getAttribute('data-unread')).toBe('false'))
    expect(h.calls('POST /me/notifications/n-3/read')).toHaveLength(1)
    await click(allByTestId('notification-delete')[1]!)
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(1))
    expect(h.calls('DELETE /me/notifications/n-2')).toHaveLength(1)
    await click(byTestId('notifications-read-all'))
    expect(h.calls('POST /me/notifications/read-all')).toHaveLength(1)
  })

  it('prepends new notifications and applies reads from other tabs live', async () => {
    const h = await mountAgent(WithBell, {
      path: '/agent/notifications',
      api: { 'GET /me/notifications': page1, 'GET /me/notifications/unread-count': { unread_count: 2 } },
    })
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(2))
    h.socket().push({
      type: 'event',
      topic: 'me',
      event: 'notification.created',
      at: '2026-10-21T10:00:00Z',
      data: { notification: notification({ id: 'n-4', type: 'staff.mentioned' }), unread_count: 3, browser: false },
    })
    await vi.waitFor(() => expect(allByTestId('notification-item')).toHaveLength(3))
    expect(allByTestId('notification-item')[0]!.textContent).toContain('Sam Supervisor mentioned you on #1042')
    h.socket().push({ type: 'event', topic: 'me', event: 'notification.read', at: '2026-10-21T10:01:00Z', data: { ids: ['n-4', 'n-3'], unread_count: 1 } })
    await vi.waitFor(() => expect(allByTestId('notification-item').filter((e) => e.getAttribute('data-unread') === 'true')).toHaveLength(1))
  })
})
