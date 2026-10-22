// SPDX-License-Identifier: AGPL-3.0-only

import { ref } from 'vue'
import { ApiProblem } from '@/api/problem'
import { useUiStore } from '@/stores/ui'
import { t } from '@/i18n'

/**
 * Runs a non-form action (delete, retry, toggle…) with a busy flag and a toast
 * for the outcome. Form submits go through `useForm().submit` instead.
 */
export function useAction() {
  const ui = useUiStore()
  const busy = ref(false)
  async function run<T>(fn: () => Promise<T>, success?: string): Promise<T | undefined> {
    busy.value = true
    try {
      const r = await fn()
      if (success) ui.notify(success, 'success')
      return r
    } catch (e) {
      ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
      return undefined
    } finally {
      busy.value = false
    }
  }
  return { busy, run }
}
