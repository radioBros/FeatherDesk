// SPDX-License-Identifier: AGPL-3.0-only

/** Saved ticket views (FE-038): personal + shared, shown in the drawer and applied by the list. */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

export type SavedView = components['schemas']['SavedView']

export const useViewsStore = defineStore('agent-views', () => {
  const views = ref<SavedView[]>([])
  const loaded = ref(false)
  async function load() {
    try {
      views.value = (await unwrap(api.GET('/agent/views'))) ?? []
    } catch {
      /* the drawer simply shows no views */
    } finally {
      loaded.value = true
    }
  }
  function add(v: SavedView) {
    views.value = [...views.value.filter((x) => x.id !== v.id), v]
  }
  async function remove(id: string) {
    await unwrap(api.DELETE('/agent/views/{id}', { params: { path: { id } } }))
    views.value = views.value.filter((v) => v.id !== id)
  }
  return { views, loaded, load, add, remove }
})
