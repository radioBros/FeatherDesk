<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { SUPPORTED_LOCALES, isLocale, setLocale, type Locale } from '@/i18n'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'
import type { Me } from '@/types'

/** Name and language (applied at once) (FE-018). Email notifications are in the notification matrix (FE-095). */
const session = useSessionStore()
const config = useConfigStore()
const ui = useUiStore()
const { t, locale } = useI18n()
const me = computed(() => session.me)
const model = reactive({
  name: me.value?.name ?? '',
  locale: (isLocale(me.value?.locale) ? me.value!.locale : isLocale(locale.value) ? locale.value : 'en') as Locale,
})
const NAMES: Record<Locale, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }
const localeItems = computed(() =>
  SUPPORTED_LOCALES.filter((l) => !config.branding.locales?.length || config.branding.locales.includes(l)).map((l) => ({ value: l, title: NAMES[l] })),
)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const updated = await unwrap(api.PATCH('/me', { body: { name: model.name.trim(), locale: model.locale } }))
  if (updated) session.set({ ...(session.me ?? {}), ...(updated as unknown as Me) } as Me)
  await setLocale(model.locale)
  try {
    localStorage.setItem('st.locale', model.locale)
  } catch {
    /* private mode */
  }
  ui.notify(t('portal.profile.saved'), 'success')
})
</script>

<template>
  <v-form ref="formRef" data-testid="profile-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('portal.account.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name"
      autocomplete="name"
      data-testid="profile-name"
    />
    <v-text-field :model-value="me?.email ?? ''" :label="$t('auth.email')" readonly disabled data-testid="profile-email" />
    <AutocompleteSearch
      v-model="model.locale"
      :items="localeItems"
      :label="$t('app.language')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.locale"
      data-testid="profile-locale"
    />
    <v-btn type="submit" color="primary" :loading="submitting" data-testid="profile-submit">{{ $t('app.save') }}</v-btn>
  </v-form>
</template>
