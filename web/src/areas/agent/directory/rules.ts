// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Small combinators over `validations.*` for this group (CONVENTIONS §12): they
 * only compose existing rules, never define new checks.
 */
import type { Rule } from '@/constants/validations'

/** Applies `rule` to every item of an array value (chips inputs); the first failure wins. */
export function eachItem(rule: Rule): Rule {
  return (v) => {
    if (!Array.isArray(v)) return rule(v)
    for (const item of v) {
      const r = rule(item)
      if (r !== true) return r
    }
    return true
  }
}
