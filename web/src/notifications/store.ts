// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Notification centre state (D-19 §D, FE-090/FE-093): the bell badge and the
 * latest entries, kept live by the `me` WebSocket topic.
 *
 * - `me` is subscribed as an essential topic, so a hidden tab still gets
 *   `notification.created` and can raise a browser pop-up.
 * - `notification.read` (sent to every socket of the user, also on delete)
 *   syncs the badge and the read state across tabs.
 * - `me` frames are never replayed, so the unread count is refetched after a
 *   reconnect, a resync and when the tab becomes visible again.
 * Kept tiny: it ships with the layouts; the browser pop-up code is lazy.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, unwrap } from '@/api/client'
import { t } from '@/i18n'
import { useConfigStore } from '@/stores/config'
import { useRealtimeStore } from '@/stores/realtime'
import type { RealtimeClient, ServerEvent } from '@/realtime/client'
import { detailOf, linkFor, titleOf, type Area, type CreatedData, type Notification, type ReadData } from './types'

export const LATEST = 10

export const useNotificationsStore = defineStore('notifications', () => {
  const unread = ref(0)
  const latest = ref<Notification[]>([])
  const latestLoaded = ref(false)
  const area = ref<Area>('portal')
  const created = new Set<(n: Notification) => void>()
  const read = new Set<(d: ReadData) => void>()
  let navigate: (path: string) => void = () => {}
  let bound: { client: RealtimeClient; user: string } | null = null
  let offs: Array<() => void> = []

  async function refreshCount() {
    try {
      unread.value = (await unwrap(api.GET('/me/notifications/unread-count'))).unread_count
    } catch {
      /* the badge keeps its last value */
    }
  }

  async function loadLatest() {
    const page = await unwrap(api.GET('/me/notifications', { params: { query: { limit: LATEST } } }))
    latest.value = page.items
    unread.value = page.unread_count
    latestLoaded.value = true
  }

  function applyRead(d: ReadData) {
    unread.value = d.unread_count
    const at = new Date().toISOString()
    for (const n of latest.value) if (!n.read_at && (d.all || d.ids?.includes(n.id))) n.read_at = at
    for (const h of read) h(d)
  }

  function onEvent(ev: ServerEvent) {
    if (ev.topic !== 'me') return
    if (ev.event === 'notification.created') {
      const d = ev.data as unknown as CreatedData
      unread.value = d.unread_count
      if (latestLoaded.value && !latest.value.some((n) => n.id === d.notification.id)) {
        latest.value = [d.notification, ...latest.value].slice(0, LATEST)
      }
      for (const h of created) h(d.notification)
      if (d.browser && typeof document !== 'undefined' && document.visibilityState === 'hidden') void popUp(d.notification)
    } else if (ev.event === 'notification.read') {
      applyRead(ev.data as unknown as ReadData)
    }
  }

  /** Raises the browser notification for n (the tab is hidden and the user enabled the channel). */
  async function popUp(n: Notification) {
    const browser = await import('./browser')
    const config = useConfigStore()
    browser.show({
      tag: n.id,
      title: titleOf(n, t, config.tierLabel),
      body: detailOf(n, t),
      onClick: () => void open(n),
    })
  }

  /**
   * Binds the store to the tab's realtime client for a signed-in user. Called
   * by the bell; rebinding (another user, a new client after sign-out) resets.
   */
  function start(opts: { area: Area; user: string; navigate: (path: string) => void }) {
    area.value = opts.area
    navigate = opts.navigate
    const rt = useRealtimeStore()
    const client = rt.client ?? rt.start()
    if (bound && bound.client === client && bound.user === opts.user) return
    stop()
    bound = { client, user: opts.user }
    let first = !client.connected // a reconnect (not the first connection) refetches the count
    offs = [
      client.subscribe('me', { essential: true }),
      client.onEvent(onEvent),
      client.onResync(() => void refreshCount()),
      client.onStatus((up) => {
        if (up && !first) void refreshCount()
        if (up) first = false
      }),
    ]
    if (typeof document !== 'undefined') {
      const onVisible = () => {
        if (document.visibilityState === 'visible') void refreshCount()
      }
      document.addEventListener('visibilitychange', onVisible)
      offs.push(() => document.removeEventListener('visibilitychange', onVisible))
    }
    void refreshCount()
  }

  function stop() {
    offs.forEach((o) => o())
    offs = []
    bound = null
    unread.value = 0
    latest.value = []
    latestLoaded.value = false
  }

  async function markRead(id: string) {
    const r = await unwrap(api.POST('/me/notifications/{id}/read', { params: { path: { id } } }))
    applyRead({ ids: [id], unread_count: r.unread_count })
  }

  async function markAllRead() {
    const r = await unwrap(api.POST('/me/notifications/read-all'))
    applyRead({ all: true, unread_count: r.unread_count })
  }

  async function remove(id: string) {
    await unwrap(api.DELETE('/me/notifications/{id}', { params: { path: { id } } }))
    latest.value = latest.value.filter((n) => n.id !== id)
    await refreshCount()
  }

  /** Opens what a notification is about and marks it read. */
  async function open(n: Notification) {
    navigate(linkFor(n, area.value))
    if (!n.read_at) await markRead(n.id).catch(() => {})
  }

  /** Page hooks: live entries and read syncs (returns the unsubscribe). */
  function onCreated(fn: (n: Notification) => void) {
    created.add(fn)
    return () => created.delete(fn)
  }
  function onRead(fn: (d: ReadData) => void) {
    read.add(fn)
    return () => read.delete(fn)
  }

  return { unread, latest, latestLoaded, area, start, stop, refreshCount, loadLatest, markRead, markAllRead, remove, open, onCreated, onRead }
})
