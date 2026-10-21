// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Cached reference data for the agent panel: teams, categories, staff,
 * custom-field definitions, tags. Loaded once per session on first use and
 * invalidated by the admin pages after they change something.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
type Key = 'teams' | 'categories' | 'staff' | 'customFields' | 'tags'

export const useLookupsStore = defineStore('agent-lookups', () => {
  const teams = ref<S['Team'][]>([])
  const categories = ref<S['Category'][]>([])
  const staff = ref<S['StaffRef'][]>([])
  const customFields = ref<S['CustomFieldDef'][]>([])
  const tags = ref<S['Tag'][]>([])
  const inflight = new Map<Key, Promise<void>>()
  const loaded = new Set<Key>()

  const loaders: Record<Key, () => Promise<void>> = {
    teams: async () => {
      teams.value = (await unwrap(api.GET('/agent/teams'))) ?? []
    },
    categories: async () => {
      categories.value = (await unwrap(api.GET('/agent/categories'))) ?? []
    },
    staff: async () => {
      staff.value = (await unwrap(api.GET('/agent/staff', { params: { query: {} } }))) ?? []
    },
    customFields: async () => {
      customFields.value = (await unwrap(api.GET('/agent/custom-fields', { params: { query: {} } }))) ?? []
    },
    tags: async () => {
      tags.value = (await unwrap(api.GET('/agent/tags', { params: { query: {} } }))) ?? []
    },
  }

  /** Loads each key once; failures leave the list empty and are retried next time. */
  function ensure(...keys: Key[]): Promise<void> {
    return Promise.all(
      keys.map((k) => {
        if (loaded.has(k)) return Promise.resolve()
        let p = inflight.get(k)
        if (!p) {
          p = loaders[k]()
            .then(() => void loaded.add(k))
            .catch(() => {})
            .finally(() => inflight.delete(k))
          inflight.set(k, p)
        }
        return p
      }),
    ).then(() => {})
  }

  function invalidate(...keys: Key[]) {
    for (const k of keys) loaded.delete(k)
  }

  const teamName = (id?: string | null) => (id ? teams.value.find((t) => t.id === id)?.name ?? '' : '')
  const categoryName = (id?: string | null) => (id ? categories.value.find((c) => c.id === id)?.name ?? '' : '')

  return { teams, categories, staff, customFields, tags, ensure, invalidate, teamName, categoryName }
})
