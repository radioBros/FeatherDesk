// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Agent-side projects (D-20): the projects the signed-in staff user works in
 * (`GET /agent/projects`) and the top-bar switcher's choice, saved server-side
 * in `PATCH /me current_project_id` (null = all projects). The switcher is
 * hidden while there is at most one active project.
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { useSessionStore } from './session'

export type AgentProject = components['schemas']['AgentProject']

export const useProjectsStore = defineStore('agent-projects', () => {
  const session = useSessionStore()
  const projects = ref<AgentProject[]>([])
  const loaded = ref(false)
  let inflight: Promise<void> | null = null

  async function load(force = false): Promise<void> {
    if (loaded.value && !force) return
    inflight ??= unwrap(api.GET('/agent/projects'))
      .then((d) => {
        projects.value = d ?? []
        loaded.value = true
      })
      .catch(() => {})
      .finally(() => {
        inflight = null
      })
    return inflight
  }

  const active = computed(() => projects.value.filter((p) => p.active))
  const showSwitcher = computed(() => active.value.length > 1)
  const byId = (id?: string | null) => (id ? projects.value.find((p) => p.id === id) : undefined)

  /** The saved choice, if it is still one of the user's projects; null = all. */
  const currentId = computed<string | null>(() => {
    const id = session.me?.current_project_id ?? null
    if (!id || !showSwitcher.value) return null
    return projects.value.some((p) => p.id === id) ? id : null
  })
  const current = computed(() => byId(currentId.value))
  /** The `project` query parameter for lists, counts and reports (undefined = all). */
  const filter = computed<string | undefined>(() => currentId.value ?? undefined)

  async function select(id: string | null): Promise<void> {
    const me = session.me
    if (!me) return
    const before = me.current_project_id ?? null
    if (before === id) return
    session.set({ ...me, current_project_id: id })
    try {
      await unwrap(api.PATCH('/me', { body: { current_project_id: id } }))
    } catch (e) {
      session.set({ ...me, current_project_id: before })
      throw e
    }
  }

  return { projects, loaded, active, showSwitcher, currentId, current, filter, byId, load, select }
})
