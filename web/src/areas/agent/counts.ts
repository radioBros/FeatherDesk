// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { apiFetch } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'

export interface Counts {
  mine: number
  unassigned: number
  triage: number
  breached?: number
  tier: Record<string, number>
}

type Raw = Counts & { by_project?: Record<string, Counts> }

/**
 * Sidebar badges: initial REST load, then pushed by the `counts` WS topic (FE-019).
 * The payload carries the totals and `by_project` (D-20); `counts` is what the
 * badges show: the switcher's project when one is selected, else the totals.
 */
export const useCountsStore = defineStore('agent-counts', () => {
  const raw = ref<Raw>({ mine: 0, unassigned: 0, triage: 0, tier: {} })
  const projects = useProjectsStore()
  const counts = computed<Counts>(() => {
    const id = projects.currentId
    const one = id ? raw.value.by_project?.[id] : undefined
    return one ? { ...raw.value, ...one, tier: one.tier ?? {} } : raw.value
  })
  const byProject = computed(() => raw.value.by_project ?? {})
  async function load() {
    try {
      raw.value = { ...raw.value, ...(await apiFetch<Partial<Raw>>('/agent/tickets/counts')) }
    } catch {
      /* the badges simply stay at their last value */
    }
  }
  function apply(data: Record<string, unknown>) {
    raw.value = { ...raw.value, ...(data as Partial<Raw>) }
  }
  return { counts, byProject, load, apply }
})
