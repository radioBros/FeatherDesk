// SPDX-License-Identifier: AGPL-3.0-only

/**
 * KB area helpers (FE-051). The KB chunk imports nothing from the agent area
 * or the portal ticket views; the API filters visibility per caller (I-10).
 */
import { ref } from 'vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

export type KbCategory = components['schemas']['KbCategory']
export type KbArticle = components['schemas']['KbArticle']
export type KbArticleSummary = components['schemas']['KbArticleSummary']
export type KbLocale = components['schemas']['Locale']

export { localized } from '@/utils/localized'

const categories = ref<KbCategory[] | null>(null)
let loadedFor: string | null = null
let pending: Promise<KbCategory[]> | null = null

/** Category tree, fetched once per locale per tab and shared by every KB page. */
export function useKbCategories(locale: () => string) {
  async function load(force = false): Promise<KbCategory[]> {
    const l = locale()
    if (!force && categories.value && loadedFor === l) return categories.value
    if (pending && loadedFor === l) return pending
    loadedFor = l
    pending = unwrap(api.GET('/kb/categories', { params: { query: { locale: l as KbLocale } } }))
      .then((list) => {
        categories.value = [...(list ?? [])].sort((a, b) => (a.sort ?? 0) - (b.sort ?? 0))
        return categories.value
      })
      .finally(() => {
        pending = null
      })
    return pending
  }
  return { categories, load }
}

/** "Was this helpful?" is sent once per article per tab (kept in memory only). */
const voted = new Map<string, boolean>()
export const feedback = {
  get: (articleId: string) => voted.get(articleId),
  has: (articleId: string) => voted.has(articleId),
  async send(articleId: string, helpful: boolean): Promise<boolean> {
    if (voted.has(articleId)) return false
    voted.set(articleId, helpful)
    try {
      await unwrap(api.POST('/kb/articles/{id}/feedback', { params: { path: { id: articleId } }, body: { helpful } }))
      return true
    } catch {
      // The vote stays recorded locally; a retry would only count twice.
      return true
    }
  },
}

/** Test hook: forget cached categories and votes. */
export function resetKbState() {
  categories.value = null
  loadedFor = null
  pending = null
  voted.clear()
}
