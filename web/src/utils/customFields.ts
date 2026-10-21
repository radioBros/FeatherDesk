// SPDX-License-Identifier: AGPL-3.0-only

import type { components } from '@/api/schema'

type Scope = Pick<components['schemas']['CustomFieldDef'], 'category_ids' | 'project_ids'>

/**
 * Whether a custom field applies to a ticket of `categoryId` in `projectId`,
 * mirroring the server's `FieldDef.AppliesTo`: a null or empty scope means
 * every category (project); a category-scoped field never applies to a ticket
 * without a category. An unknown project (null) does not filter. Forms show,
 * require and send a field only while it applies.
 */
export function fieldApplies(def: Scope, categoryId: string | null | undefined, projectId?: string | null): boolean {
  if (def.project_ids?.length && projectId && !def.project_ids.includes(projectId)) return false
  if (!def.category_ids?.length) return true
  return !!categoryId && def.category_ids.includes(categoryId)
}

/** A form value that carries nothing (never sent on create). */
export function emptyFieldValue(v: unknown): boolean {
  return v === undefined || v === null || (typeof v === 'string' && v.trim() === '') || (Array.isArray(v) && v.length === 0)
}

/** Deletes the keys of `values` that are not in `keep` (fields that stopped applying). */
export function dropFieldValues(values: Record<string, unknown>, keep: Iterable<string>) {
  const set = new Set(keep)
  for (const k of Object.keys(values)) if (!set.has(k)) delete values[k]
}
