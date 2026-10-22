// SPDX-License-Identifier: AGPL-3.0-only

/**
 * The only sanctioned way to submit a form (CONVENTIONS §12, FE-005).
 *
 *   const { formRef, submit, fieldErrors, submitting } = useForm()
 *   const onSave = submit(async () => { await unwrap(api.PUT(...)) })
 *   <v-form ref="formRef" @submit.prevent="onSave"> … <v-text-field :rules="[validations.required.any]" :error-messages="fieldErrors.name" />
 *
 * `submit(fn)` always calls the form's `validate()` first and stops when it is
 * invalid, so the field errors show and no request is sent. Server 422 errors
 * are mapped back onto the fields.
 */
import { reactive, ref } from 'vue'
import { ApiProblem, applyServerErrors } from '@/api/problem'

export interface ValidatableForm {
  validate: () => Promise<{ valid: boolean }>
  resetValidation?: () => void
  reset?: () => void
}

export function useForm() {
  const formRef = ref<ValidatableForm | null>(null)
  const fieldErrors = reactive<Record<string, string[]>>({})
  const formError = ref<string | null>(null)
  const submitting = ref(false)

  function clearErrors() {
    for (const k of Object.keys(fieldErrors)) delete fieldErrors[k]
    formError.value = null
  }

  /** Wraps an action so it runs only on a valid form. Resolves true on success. */
  function submit<T>(action: () => Promise<T> | T) {
    return async (event?: Event): Promise<boolean> => {
      event?.preventDefault?.()
      if (submitting.value) return false
      clearErrors()
      const form = formRef.value
      if (!form) return false
      const { valid } = await form.validate()
      if (!valid) return false
      submitting.value = true
      try {
        await action()
        return true
      } catch (e) {
        if (e instanceof ApiProblem) {
          const hasFields = e.status === 422 && applyServerErrors(fieldErrors, e)
          formError.value = hasFields ? null : e.userMessage
          return false
        }
        throw e
      } finally {
        submitting.value = false
      }
    }
  }

  return { formRef, fieldErrors, formError, submitting, submit, clearErrors }
}
