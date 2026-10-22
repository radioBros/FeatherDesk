// SPDX-License-Identifier: AGPL-3.0-only

/** Test helpers for the notification centre (Vitest only; never imported by app code). */
import { i18n } from '@/i18n'
import enNotifications from '@/locales/notifications/en.json'
import type { Notification } from './types'

let loaded = false
/** Merges the `notifications.*` pack into `en` synchronously (the app loads it lazily per area). */
export function loadNotificationMessagesForTests() {
  if (loaded) return
  i18n.global.mergeLocaleMessage('en', enNotifications as never)
  loaded = true
}

/** A stand-in for the browser's Notification API: records every pop-up. */
export class MockNotification {
  static permission: NotificationPermission = 'granted'
  static requestPermission = vi.fn(async (): Promise<NotificationPermission> => MockNotification.permission)
  static instances: MockNotification[] = []
  onclick: ((ev: Event) => void) | null = null
  close = vi.fn()
  constructor(
    public title: string,
    public options: NotificationOptions = {},
  ) {
    MockNotification.instances.push(this)
  }
  static reset(permission: NotificationPermission = 'granted') {
    MockNotification.permission = permission
    MockNotification.instances = []
    MockNotification.requestPermission = vi.fn(async (): Promise<NotificationPermission> => MockNotification.permission)
  }
}

export function installNotification(permission: NotificationPermission = 'granted') {
  MockNotification.reset(permission)
  vi.stubGlobal('Notification', MockNotification)
  return MockNotification
}

/** Fakes `document.visibilityState` (and fires visibilitychange). */
export function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  document.dispatchEvent(new Event('visibilitychange'))
}

const ISO = '2026-10-21T09:00:00Z'

/** Project chips (D-20) for fixtures: the Default project and a second one. */
export const HD = { id: 'p-hd', key: 'HD', name: 'Help desk', color: '' }
export const WEB = { id: 'p-web', key: 'WEB', name: 'Web App', color: '#1E88E5' }

export function notification(extra: Partial<Notification> = {}): Notification {
  return {
    id: 'n-1',
    type: 'staff.assigned',
    ticket_id: 't-1',
    ticket_number: 1042,
    ticket_subject: 'Printer on fire',
    ticket_ref: 'HD-1042',
    project: HD,
    actor_name: 'Sam Supervisor',
    params: {},
    read_at: null,
    created_at: ISO,
    ...extra,
  }
}
