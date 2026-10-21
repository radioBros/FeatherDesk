// SPDX-License-Identifier: AGPL-3.0-only

import { onBeforeUnmount, onMounted } from 'vue'

/**
 * Single-key shortcuts for the component's lifetime (FE-038): `j`/`k` in the
 * list, `r`/`n`/`e` on a ticket, `?` for help. Disabled while focus is in an
 * input, textarea, select or contenteditable, with a modifier key held, or
 * while a dialog/menu overlay is open.
 */
export function isTypingTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false
  if (el.isContentEditable) return true
  return !!el.closest('input, textarea, select, [contenteditable="true"], [role="combobox"]')
}

export function useShortcuts(map: Record<string, (e: KeyboardEvent) => void>) {
  function onKey(e: KeyboardEvent) {
    if (e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey) return
    if (isTypingTarget(e.target) || isTypingTarget(document.activeElement)) return
    if (document.querySelector('.v-overlay--active.v-dialog, .v-overlay--active.v-menu')) return
    const fn = map[e.key]
    if (!fn) return
    e.preventDefault()
    fn(e)
  }
  onMounted(() => window.addEventListener('keydown', onKey))
  onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
}
