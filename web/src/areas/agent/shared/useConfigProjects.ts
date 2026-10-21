// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Projects for the configuration pages (D-20): loads `GET /agent/projects`
 * once (call it from the page, never from a form) and offers
 *  - `options`: the projects the user may configure (`can_admin`), what the
 *    selectors list: all of them for supervisors and admins, the administered
 *    ones for a project admin;
 *  - `all`: every project the user works in (chips of existing rows);
 *  - `canGlobal`: only supervisors and admins may save an unscoped (global) row.
 */
import { computed, onMounted } from 'vue'
import { useProjectsStore } from '@/stores/projects'
import { useSessionStore } from '@/stores/session'

export function useConfigProjects() {
  const store = useProjectsStore()
  const session = useSessionStore()
  onMounted(() => void store.load())
  const all = computed(() => store.projects)
  const options = computed(() => store.projects.filter((p) => p.can_admin))
  const canGlobal = computed(() => session.isSupervisor)
  const byId = (id?: string | null) => store.byId(id)
  return { store, all, options, canGlobal, byId }
}
