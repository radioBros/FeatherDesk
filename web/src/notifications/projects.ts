// SPDX-License-Identifier: AGPL-3.0-only

/**
 * When notification rows show the project chip (D-20). Same rule as the
 * ticket lists: only when the user works with more than one project.
 * - Agents: the projects store's `showSwitcher` (more than one active
 *   project, the rule that hides the top-bar switcher).
 * - Customers: more than one project in `GET /portal/projects` (the portal
 *   ticket list's rule), fetched once per signed-in user.
 * Either way the chip also shows when the listed notifications span several
 * projects. Imported by the lazy dropdown and page only, never by the bell.
 */
import { defineStore } from 'pinia'
import { computed, ref, type ComputedRef } from 'vue'
import { api, unwrap } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import { useSessionStore } from '@/stores/session'
import type { Area, Notification } from './types'

const usePortalProjectCount = defineStore('notification-portal-projects', () => {
  const count = ref(0)
  let user: string | null = null
  function load(id: string) {
    if (user === id) return
    user = id
    count.value = 0
    unwrap(api.GET('/portal/projects'))
      .then((list) => {
        if (user === id) count.value = Array.isArray(list) ? list.length : 0
      })
      .catch(() => {
        if (user === id) user = null // try again on the next mount
      })
  }
  return { count, load }
})

export function useShowProjectChip(area: Area, items: () => readonly Notification[]): ComputedRef<boolean> {
  const spread = computed(() => new Set(items().flatMap((n) => (n.project ? [n.project.id] : []))).size > 1)
  if (area === 'agent') {
    const projects = useProjectsStore()
    void projects.load()
    return computed(() => projects.showSwitcher || spread.value)
  }
  const portal = usePortalProjectCount()
  portal.load(useSessionStore().me?.id ?? '')
  return computed(() => portal.count > 1 || spread.value)
}
