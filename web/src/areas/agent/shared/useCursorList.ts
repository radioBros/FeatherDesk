// SPDX-License-Identifier: AGPL-3.0-only

import { ref, shallowRef } from 'vue'
import { ApiProblem } from '@/api/problem'

export interface CursorPage<T> {
  items: T[]
  next_cursor: string | null
}

/**
 * Cursor pagination ("Load more") over any `{items, next_cursor}` endpoint.
 * `fetchPage(cursor)` builds the request; `reload()` starts over.
 */
export function useCursorList<T>(fetchPage: (cursor: string | null) => Promise<CursorPage<T>>) {
  const items = ref<T[]>([]) as { value: T[] }
  const next = ref<string | null>(null)
  const loading = ref(false)
  const error = shallowRef<ApiProblem | null>(null)
  let seq = 0

  async function run(cursor: string | null, append: boolean) {
    const mine = ++seq
    loading.value = true
    error.value = null
    try {
      const page = await fetchPage(cursor)
      if (mine !== seq) return
      items.value = append ? [...items.value, ...(page.items ?? [])] : page.items ?? []
      next.value = page.next_cursor ?? null
    } catch (e) {
      if (mine !== seq) return
      // The row a cursor points at can be pruned meanwhile (logs): start over.
      if (cursor && e instanceof ApiProblem && e.status === 422 && e.errors.some((f) => f.field === 'cursor')) return void run(null, false)
      error.value = e instanceof ApiProblem ? e : new ApiProblem(0, {})
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  return {
    items,
    next,
    loading,
    error,
    reload: () => run(null, false),
    loadMore: () => (next.value ? run(next.value, true) : Promise.resolve()),
  }
}
