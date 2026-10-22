// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { RealtimeClient, type RealtimeOptions } from '@/realtime/client'

/** Pinia wrapper around the single per-tab RealtimeClient (FE-044). */
export const useRealtimeStore = defineStore('realtime', () => {
  const client = shallowRef<RealtimeClient | null>(null)
  const connected = ref(false)
  const everConnected = ref(false)
  let offVisibility: (() => void) | null = null

  function start(options?: RealtimeOptions) {
    if (client.value) return client.value
    const c = new RealtimeClient(options)
    c.onStatus((v) => {
      connected.value = v
      if (v) everConnected.value = true
    })
    client.value = c
    if (typeof document !== 'undefined') {
      const onVis = () => c.setHidden(document.visibilityState === 'hidden')
      document.addEventListener('visibilitychange', onVis)
      offVisibility = () => document.removeEventListener('visibilitychange', onVis)
    }
    c.start()
    return c
  }

  function stop() {
    client.value?.stop()
    client.value = null
    offVisibility?.()
    offVisibility = null
    connected.value = false
  }

  return { client, connected, everConnected, start, stop }
})
