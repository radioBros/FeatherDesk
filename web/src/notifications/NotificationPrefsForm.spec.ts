// SPDX-License-Identifier: AGPL-3.0-only

import { mountAgent, json, byTestId, allByTestId, click, fx } from '@/areas/agent/testing'
import { useSessionStore } from '@/stores/session'
import NotificationPrefsForm from './NotificationPrefsForm.vue'
import { installNotification, loadNotificationMessagesForTests } from './testing'

beforeAll(loadNotificationMessagesForTests)
afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
  document.body.innerHTML = ''
})

const input = (id: string) => byTestId(id)?.querySelector('input') as HTMLInputElement
const toggle = async (id: string) => click(input(id))
const submit = async () => click(byTestId('prefs-save'))

describe('NotificationPrefsForm (FE-094, FE-095)', () => {
  it('renders the staff matrix with defaults and saves the whole matrix through PATCH /me', async () => {
    installNotification('granted')
    const h = await mountAgent(NotificationPrefsForm, {
      props: { area: 'agent', prefs: { 'staff.ticket_new': { in_app: false, email: true, browser: false } } },
      api: { 'PATCH /me': (_u, init) => json({ ...fx.me(), notification_prefs: init.body.notification_prefs }) },
    })
    expect(allByTestId('notification-prefs-form')).toHaveLength(1)
    expect(document.body.querySelectorAll('[data-testid^="prefs-row-"]')).toHaveLength(7)
    expect(input('prefs-staff.ticket_new-in_app').checked).toBe(false)
    expect(input('prefs-staff.assigned-in_app').checked).toBe(true)
    expect(input('prefs-staff.assigned-browser').checked).toBe(false)
    expect(input('prefs-staff.ticket_new-browser').disabled).toBe(true) // browser needs in-app
    expect(byTestId('prefs-permission-granted')).not.toBeNull()
    await toggle('prefs-staff.assigned-browser')
    await toggle('prefs-staff.sla-email')
    await toggle('prefs-sound')
    await submit()
    await vi.waitFor(() => expect(h.calls('PATCH /me')).toHaveLength(1))
    const body = h.calls('PATCH /me')[0]!.body as { notification_prefs: Record<string, Record<string, boolean>> }
    expect(Object.keys(body.notification_prefs)).toHaveLength(7)
    expect(body.notification_prefs['staff.assigned']).toEqual({ in_app: true, email: true, browser: true })
    expect(body.notification_prefs['staff.sla']).toEqual({ in_app: true, email: false, browser: false })
    expect(body.notification_prefs['staff.ticket_new']).toEqual({ in_app: false, email: true, browser: false })
    expect(localStorage.getItem('fd.notifications.sound')).toBe('1')
    expect((useSessionStore().me as unknown as { notification_prefs: object }).notification_prefs).toEqual(body.notification_prefs)
  })

  it('blocks the save while a browser channel is on without the permission; the prompt unlocks it (FE-005)', async () => {
    const N = installNotification('default')
    const h = await mountAgent(NotificationPrefsForm, {
      props: { area: 'agent', prefs: { 'staff.mentioned': { in_app: true, email: true, browser: true } } },
      api: { 'PATCH /me': json(fx.me()) },
    })
    await submit()
    await h.flush()
    expect(h.calls('PATCH /me')).toHaveLength(0)
    expect(document.body.textContent).toContain('Allow browser notifications first')
    N.permission = 'granted'
    await click(byTestId('prefs-enable-browser'))
    await vi.waitFor(() => expect(byTestId('prefs-permission-granted')).not.toBeNull())
    expect(N.requestPermission).toHaveBeenCalledOnce()
    await submit()
    await vi.waitFor(() => expect(h.calls('PATCH /me')).toHaveLength(1))
  })

  it('denied and unsupported browsers explain themselves', async () => {
    installNotification('denied')
    await mountAgent(NotificationPrefsForm, { props: { area: 'agent' } })
    expect(byTestId('prefs-permission-denied')).not.toBeNull()
    expect(byTestId('prefs-enable-browser')).toBeNull()
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
    const saved = (window as unknown as { Notification?: unknown }).Notification
    delete (window as unknown as { Notification?: unknown }).Notification
    try {
      await mountAgent(NotificationPrefsForm, { props: { area: 'agent' } })
      expect(byTestId('prefs-permission-unsupported')).not.toBeNull()
    } finally {
      if (saved) (window as unknown as { Notification?: unknown }).Notification = saved
    }
  })

  it('customers get their three types; customer.reply email is forced on', async () => {
    installNotification('granted')
    const h = await mountAgent(NotificationPrefsForm, {
      props: { area: 'portal' },
      me: fx.me('agent', 1, { kind: 'customer', role: 'requester' }),
      api: { 'PATCH /me': (_u, init) => json({ ...fx.me(), notification_prefs: init.body.notification_prefs }) },
    })
    expect(document.body.querySelectorAll('[data-testid^="prefs-row-"]')).toHaveLength(3)
    expect(input('prefs-customer.reply-email').checked).toBe(true)
    expect(input('prefs-customer.reply-email').disabled).toBe(true)
    expect(document.body.textContent).toContain('Replies to your requests are always emailed.')
    await toggle('prefs-customer.resolved-in_app')
    await submit()
    await vi.waitFor(() => expect(h.calls('PATCH /me')).toHaveLength(1))
    const body = h.calls('PATCH /me')[0]!.body as { notification_prefs: Record<string, Record<string, boolean>> }
    expect(Object.keys(body.notification_prefs).sort()).toEqual(['customer.reply', 'customer.resolved', 'customer.ticket_received'])
    expect(body.notification_prefs['customer.reply']!.email).toBe(true)
    expect(body.notification_prefs['customer.resolved']!.in_app).toBe(false)
  })
})
