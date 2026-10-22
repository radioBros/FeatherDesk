// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { apiFetch, setCsrfToken } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import type { Me } from '@/types'

/** The signed-in principal from GET /me (FE-007). */
export const useSessionStore = defineStore('session', () => {
  const me = ref<Me | null>(null)
  const checked = ref(false)

  async function refresh(): Promise<Me | null> {
    try {
      const data = await apiFetch<Me>('/me')
      set(data)
    } catch (e) {
      if (!(e instanceof ApiProblem) || e.status === 401 || e.status === 403) clear()
      else throw e
    } finally {
      checked.value = true
    }
    return me.value
  }

  function set(data: Me | null) {
    me.value = data
    setCsrfToken(data?.csrf_token ?? null)
  }

  function clear() {
    set(null)
  }

  async function logout(): Promise<string | null> {
    try {
      const res = await apiFetch<{ redirect_url?: string }>('/auth/logout', { method: 'POST' })
      return res?.redirect_url ?? null
    } finally {
      clear()
    }
  }

  const isStaff = computed(() => me.value?.kind === 'staff')
  const isCustomer = computed(() => me.value?.kind === 'customer')
  const isAdmin = computed(() => isStaff.value && me.value?.role === 'admin')
  const isSupervisor = computed(() => isStaff.value && (me.value?.role === 'admin' || me.value?.role === 'supervisor'))
  /** Customer who manages their organization (org view, members page). */
  const isOrgManager = computed(() => isCustomer.value && me.value?.role === 'org_manager')

  /** Tiers whose queues the nav shows (UX only; the API filters by I-4). */
  const visibleTiers = computed<number[]>(() => {
    if (!isStaff.value) return []
    const max = isSupervisor.value ? 3 : Math.min(3, Math.max(1, me.value?.tier ?? 1))
    return [1, 2, 3].filter((t) => t <= max)
  })

  /**
   * Whether the server allows an action on a ticket. Reads ONLY the
   * `allowed_actions` the API returned — never re-derives rules (permissions §6.4).
   */
  function can(action: string, ticket?: { allowed_actions?: readonly string[] } | null): boolean {
    return !!ticket?.allowed_actions?.includes(action)
  }

  return { me, checked, refresh, set, clear, logout, isStaff, isCustomer, isAdmin, isSupervisor, isOrgManager, visibleTiers, can }
})
