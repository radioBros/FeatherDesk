<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** Staff profile: name and language (FE-030 profile page). A new language applies immediately. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { SUPPORTED_LOCALES, isLocale, setLocale } from '@/i18n'
import { useSessionStore } from '@/stores/session'
import type { Me } from '@/types'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ name?: string; locale?: string }>()
const emit = defineEmits<{ saved: [] }>()
const session = useSessionStore()
const model = reactive({ name: props.name ?? '', locale: (isLocale(props.locale) ? props.locale : null) as S['Locale'] | null })
const NAMES: Record<string, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body: S['MePatch'] = { name: model.name.trim(), ...(model.locale ? { locale: model.locale } : {}) }
  const me = await unwrap(api.PATCH('/me', { body }))
  session.set({ ...(session.me ?? {}), ...(me as unknown as Me) })
  if (model.locale) {
    await setLocale(model.locale)
    try {
      localStorage.setItem('fd.locale', model.locale)
    } catch {
      /* private mode */
    }
  }
  emit('saved')
})
</script>

<template>
  <v-form ref="formRef" data-testid="profile-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.name"
      data-testid="profile-name"
    />
    <AutocompleteSearch
      v-model="model.locale"
      :items="SUPPORTED_LOCALES.map((l) => ({ value: l, title: NAMES[l] }))"
      :label="$t('app.language')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.locale"
      data-testid="profile-locale"
    />
    <div class="d-flex justify-end">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="profile-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
