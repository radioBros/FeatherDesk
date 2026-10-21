// SPDX-License-Identifier: AGPL-3.0-only

/**
 * KB editor helpers (FE-052): visibility ordering (M-10), the `allowed_actions`
 * gate for every editor control, and the category source for staff.
 */
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { SUPPORTED_LOCALES } from '@/i18n'
import type { AuthConfig } from '@/types'

type S = components['schemas']
export type KbVisibility = S['KbVisibility']
export type KbCategory = S['KbCategory']

export type KbAllowedAction = S['KbAllowedAction']
/** The editor's article: `allowed_actions` (KB-013) is always sent by the API. */
export type KbArticleWithActions = S['KbArticle']

/** Widest → narrowest. An article is never wider than its category (M-10). */
export const VISIBILITIES: KbVisibility[] = ['public', 'customers', 'internal']

export function allowedVisibilities(category?: { visibility: KbVisibility } | null): KbVisibility[] {
  if (!category) return [...VISIBILITIES]
  const min = VISIBILITIES.indexOf(category.visibility)
  return VISIBILITIES.filter((_, i) => i >= min)
}

/**
 * An editor control exists iff the server listed its action in the article's
 * `allowed_actions` (edit, publish, unpublish, archive, delete). Never derived
 * from the role (permissions §6.4).
 */
export function canKb(article: Pick<KbArticleWithActions, 'allowed_actions'> | null | undefined, action: KbAllowedAction): boolean {
  return !!article?.allowed_actions?.includes(action)
}

/** Staff read every category through the public endpoint (I-10); the admin list is the fallback. */
export async function loadKbCategories(): Promise<KbCategory[]> {
  try {
    return (await unwrap(api.GET('/kb/categories', { params: { query: {} } }))) ?? []
  } catch {
    return (await unwrap(api.GET('/admin/kb/categories'))) ?? []
  }
}

/** Editor locales: the instance's available locales, default first. */
export function editorLocales(config: AuthConfig): string[] {
  const def = config.branding.default_locale ?? 'en'
  const list = config.branding.locales?.length ? config.branding.locales : [...SUPPORTED_LOCALES]
  return [def, ...list.filter((l) => l !== def)]
}

/** Categories ordered as a tree (parent before children), with depth. */
export function flattenTree<T extends { id: string; parent_id?: string | null; sort?: number }>(
  items: T[],
  label: (c: T) => string,
): { item: T; depth: number }[] {
  const byParent = new Map<string | null, T[]>()
  const ids = new Set(items.map((c) => c.id))
  for (const c of items) {
    const p = c.parent_id && ids.has(c.parent_id) ? c.parent_id : null
    const list = byParent.get(p) ?? []
    list.push(c)
    byParent.set(p, list)
  }
  for (const list of byParent.values()) list.sort((a, b) => (a.sort ?? 0) - (b.sort ?? 0) || label(a).localeCompare(label(b)))
  const out: { item: T; depth: number }[] = []
  const walk = (parent: string | null, depth: number) => {
    for (const c of byParent.get(parent) ?? []) {
      out.push({ item: c, depth })
      if (depth < 20) walk(c.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

/** Ids of `id` and every category below it. */
export function descendantsOf<T extends { id: string; parent_id?: string | null }>(items: T[], id: string): Set<string> {
  const out = new Set([id])
  let grew = true
  while (grew) {
    grew = false
    for (const c of items) {
      if (c.parent_id && out.has(c.parent_id) && !out.has(c.id)) {
        out.add(c.id)
        grew = true
      }
    }
  }
  return out
}
