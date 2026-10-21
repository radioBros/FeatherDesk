// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiFetch } from '@/api/client'

export interface Counts {
  mine: number
  unassigned: number
  triage: number
  breached?: number
  tier: Record<string, number>
}

/** Sidebar badges: initial REST load, then pushed by the `counts` WS topic (FE-019). */
export const useCountsStore = defineStore('agent-counts', () => {
  const counts = ref<Counts>({ mine: 0, unassigned: 0, triage: 0, tier: {} })
  async function load() {
    try {
      counts.value = { ...counts.value, ...(await apiFetch<Partial<Counts>>('/agent/tickets/counts')) }
    } catch {
      /* the badges simply stay at their last value */
    }
  }
  function apply(data: Record<string, unknown>) {
    counts.value = { ...counts.value, ...(data as Partial<Counts>) }
  }
  return { counts, load, apply }
})
