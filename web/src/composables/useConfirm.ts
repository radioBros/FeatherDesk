// SPDX-License-Identifier: AGPL-3.0-only

import { reactive } from 'vue'

export interface ConfirmOptions {
  title?: string
  body?: string
  confirmText?: string
  cancelText?: string
  color?: string
}

const state = reactive({ open: false, options: {} as ConfirmOptions, resolve: null as ((v: boolean) => void) | null })

/** Promise-based confirm: `if (await confirm({ body })) …` (rendered by <ConfirmDialog/> in App). */
export function useConfirm() {
  function confirm(options: ConfirmOptions = {}): Promise<boolean> {
    state.resolve?.(false)
    state.options = options
    state.open = true
    return new Promise((resolve) => (state.resolve = resolve))
  }
  return { confirm }
}

export function confirmState() {
  return state
}

export function settleConfirm(value: boolean) {
  state.open = false
  const r = state.resolve
  state.resolve = null
  r?.(value)
}
