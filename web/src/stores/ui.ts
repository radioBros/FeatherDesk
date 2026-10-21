// SPDX-License-Identifier: AGPL-3.0-only

import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface Toast {
  id: number
  text: string
  color?: 'success' | 'error' | 'info' | 'warning'
  timeout?: number
}

/** App-wide snackbar queue. */
export const useUiStore = defineStore('ui', () => {
  const toasts = ref<Toast[]>([])
  let seq = 0
  function notify(text: string, color: Toast['color'] = 'info', timeout = 4000) {
    toasts.value.push({ id: ++seq, text, color, timeout })
  }
  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }
  return { toasts, notify, dismiss }
})
