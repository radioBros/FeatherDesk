// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Table layout for resized columns (useTableColumns widths). Until the user
 * resizes a column the table keeps its automatic layout. The first resize
 * measures every header cell, so the table keeps the look it had, and switches
 * the table to `table-layout: fixed` with an explicit width: a column can then
 * be made narrower than its content, and the table scrolls when it is wider
 * than the page.
 */

/** Width given to a column shown after the others were sized. */
export const DEFAULT_COLUMN_WIDTH = 160

/**
 * Widths of every cell of a header row: declared columns by the key of their
 * resizer, the others (selection, actions) as `:<n>` by their position.
 */
export function measureHeaderRow(row: Element): Record<string, number> {
  const out: Record<string, number> = {}
  let n = 0
  for (const cell of Array.from(row.children)) {
    const key = cell.querySelector<HTMLElement>('[data-resize-col]')?.dataset.resizeCol ?? `:${n++}`
    out[key] = Math.round(cell.getBoundingClientRect().width)
  }
  return out
}

/** Fixed layout at `total` px, or back to the automatic layout when total is 0. */
export function syncTableLayout(table: HTMLTableElement | null | undefined, total: number) {
  if (!table) return
  if (total > 0) {
    table.style.tableLayout = 'fixed'
    table.style.width = `${total}px`
    table.classList.add('fd-cols-sized')
  } else {
    table.style.removeProperty('table-layout')
    table.style.removeProperty('width')
    table.classList.remove('fd-cols-sized')
  }
}
