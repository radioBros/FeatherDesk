// SPDX-License-Identifier: AGPL-3.0-only

import { onBeforeUnmount, watch, toValue, type MaybeRefOrGetter } from 'vue'
import { useRealtimeStore } from '@/stores/realtime'
import { useSessionStore } from '@/stores/session'
import { isStaffOnlyTopic, type ServerEvent, type TicketCounts } from '@/realtime/client'

export interface UseRealtimeOptions {
  /** Called for every event on the subscribed topics. */
  onEvent?: (ev: ServerEvent) => void
  /** Called on `resync` (gap too large, slow consumer, tab visible again): refetch over REST. */
  onResync?: (reason: string) => void
  onCounts?: (data: TicketCounts) => void
  /** Essential topics stay subscribed while the tab is hidden (`me`). */
  essential?: boolean
}

/**
 * Subscribes the calling component to topics for its lifetime (route-driven
 * subscriptions: lists → `tickets`/`counts`, detail → `ticket:<id>`).
 */
export function useRealtime(topics: MaybeRefOrGetter<string[]>, opts: UseRealtimeOptions = {}) {
  const store = useRealtimeStore()
  const session = useSessionStore()
  const client = store.client ?? store.start()
  // A customer's socket is closed with 4403 on a staff-only topic: never send one from the portal.
  const allowed = (list: string[]) => (session.isStaff ? list : list.filter((t) => !isStaffOnlyTopic(t)))
  let unsubs: Array<() => void> = []
  const offs: Array<() => void> = []

  const apply = (list: string[]) => {
    unsubs.forEach((u) => u())
    unsubs = allowed(list).map((t) => client.subscribe(t, { essential: opts.essential }))
  }
  const stopWatch = watch(() => [...toValue(topics)], apply, { immediate: true })

  if (opts.onEvent) {
    const handler = opts.onEvent
    offs.push(client.onEvent((ev) => {
      if (toValue(topics).includes(ev.topic)) handler(ev)
    }))
  }
  if (opts.onResync) offs.push(client.onResync(opts.onResync))
  if (opts.onCounts) offs.push(client.onCounts(opts.onCounts))

  onBeforeUnmount(() => {
    stopWatch()
    unsubs.forEach((u) => u())
    offs.forEach((o) => o())
  })
  return { connected: store.connected }
}
