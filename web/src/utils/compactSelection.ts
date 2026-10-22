// SPDX-License-Identifier: AGPL-3.0-only
// =========================================================================
// Compact multi-select — the rules behind AutocompleteSearch's
// `compact-multiple` mode (owner, 2026-10-02).
// =========================================================================
//
//   "show the chip when they are max 2 items, over 2 just make it say
//    'N items selected' (no chips), and when multiple items are selected, the
//    selected ones need to appear top in the options list."
//
// Pure functions, so the rules are unit-tested here (the web tests cannot
// mount SFCs); AutocompleteSearch.vue only wires them to Vuetify's slots.

export type SelectionValue = string | number;

/** Up to this many selected values are drawn as chips; more → one summary. */
export const COMPACT_CHIP_LIMIT = 2;

/**
 * What the field draws for the selection at `index` when `count` values are
 * selected: a chip, the "N selected" summary (once, at index 0), or nothing.
 */
export const compactSelectionSlot = (
  count: number,
  index: number,
  limit: number = COMPACT_CHIP_LIMIT,
): 'chip' | 'summary' | 'none' => {
  if (count <= 0 || index < 0 || index >= count) return 'none';
  if (count <= Math.max(0, limit)) return 'chip';
  return index === 0 ? 'summary' : 'none';
};

/**
 * The options with the `pinned` (selected) ones first — both groups keep their
 * original order. `pinned` is the selection captured when the menu OPENED, so
 * a click inside the open menu never makes the row jump under the pointer.
 */
export const orderSelectedFirst = <T>(
  items: readonly T[],
  pinned: readonly SelectionValue[],
  valueOf: (item: T) => unknown,
): T[] => {
  if (pinned.length === 0) return [...items];
  const keys = new Set(pinned.map((v) => String(v)));
  const first: T[] = [];
  const rest: T[] = [];
  for (const item of items) {
    const v = valueOf(item);
    (v !== null && v !== undefined && keys.has(String(v)) ? first : rest).push(item);
  }
  return [...first, ...rest];
};

/** The selection as an array (a multi-select's model is an array; anything
 *  else counts as nothing selected). */
export const selectedValues = (model: unknown): SelectionValue[] =>
  Array.isArray(model)
    ? model.filter((v): v is SelectionValue => typeof v === 'string' || typeof v === 'number')
    : [];
