<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import PasswordField from '@/components/PasswordField.vue'
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { SUPPORTED_LOCALES, isLocale, type Locale } from '@/i18n'
import { useConfigStore } from '@/stores/config'

/** Customer self-registration (FE-014). Emits `registered` after the 202. */
const emit = defineEmits<{ registered: [email: string] }>()
const { locale } = useI18n()
const model = reactive({
  name: '',
  email: '',
  password: '',
  confirm: '',
  locale: (isLocale(locale.value) ? locale.value : 'en') as Locale,
  accept_privacy: false,
})
// D-19 §C (FE-081): with a privacy policy URL configured, registration requires accepting it.
const privacyUrl = computed(() => useConfigStore().branding.privacy_url || '')
const consentRules = [validations.required.any]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const LOCALE_NAMES: Record<Locale, string> = { en: 'English', it: 'Italiano', de: 'Deutsch', fr: 'Français', es: 'Español' }
const localeItems = SUPPORTED_LOCALES.map((l) => ({ value: l, title: LOCALE_NAMES[l] }))

const onSubmit = submit(async () => {
  const email = model.email.trim()
  await unwrap(
    api.POST('/auth/register', {
      body: {
        name: model.name.trim(),
        email,
        password: model.password,
        locale: model.locale,
        ...(privacyUrl.value ? { accept_privacy: model.accept_privacy } : {}),
      },
    }),
  )
  emit('registered', email)
})
</script>

<template>
  <v-form ref="formRef" data-testid="register-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('portal.account.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name"
      autocomplete="name"
      data-testid="register-name"
    />
    <v-text-field
      v-model="model.email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email"
      type="email"
      autocomplete="email"
      data-testid="register-email"
    />
    <PasswordField
      v-model="model.password"
      :label="$t('auth.password')"
      :rules="[validations.required.any, validations.password]"
      :error-messages="fieldErrors.password"
      :hint="$t('portal.account.passwordHint')"
      autocomplete="new-password"
      data-testid="register-password"
    />
    <PasswordField
      v-model="model.confirm"
      :label="$t('portal.account.confirmPassword')"
      :rules="[validations.required.any, validations.sameAs(() => model.password)]"
      autocomplete="new-password"
      data-testid="register-confirm"
    />
    <AutocompleteSearch
      v-model="model.locale"
      :items="localeItems"
      :label="$t('app.language')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.locale"
      data-testid="register-locale"
    />
    <template v-if="privacyUrl">
      <v-checkbox
        v-model="model.accept_privacy"
        :label="$t('privacy.consent.accept')"
        :rules="consentRules"
        :error-messages="fieldErrors.accept_privacy"
        data-testid="register-privacy"
      />
      <p class="text-body-2 mt-n2 mb-3">
        <a :href="privacyUrl" target="_blank" rel="noopener noreferrer" data-testid="register-privacy-link">{{ $t('privacy.consent.read') }}</a>
      </p>
    </template>
    <v-btn type="submit" color="primary" block :loading="submitting" data-testid="register-submit">{{ $t('auth.register') }}</v-btn>
  </v-form>
</template>
