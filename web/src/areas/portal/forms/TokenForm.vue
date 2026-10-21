<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import SelectionChips from '@/components/SelectionChips.vue'
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { CUSTOMER_SCOPES, type ApiTokenCreated, type Scope } from '../types'

/**
 * New personal API token (FE-037). Customers only ever see `tickets:read` and
 * `tickets:write` (permissions §4) — never `directory:*` or `admin`.
 */
const emit = defineEmits<{ created: [token: ApiTokenCreated] }>()
const { t } = useI18n()
const model = reactive({ name: '', scopes: ['tickets:read'] as Scope[], expires: '' })
const scopeItems = computed(() => CUSTOMER_SCOPES.map((s) => ({ value: s, title: s, subtitle: t(`portal.tokens.scope.${s.replace(':', '_')}`) })))
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const created = await unwrap(
    api.POST('/me/tokens', {
      body: {
        name: model.name.trim(),
        scopes: model.scopes.filter((s) => CUSTOMER_SCOPES.includes(s)),
        expires_at: model.expires ? new Date(model.expires + 'T23:59:59Z').toISOString() : null,
      },
    }),
  )
  model.name = ''
  model.expires = ''
  formRef.value?.resetValidation?.()
  emit('created', created)
})
</script>

<template>
  <v-form ref="formRef" data-testid="token-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('portal.tokens.name')"
      :rules="[validations.required.any, validations.maxLength(80)]"
      :error-messages="fieldErrors.name"
      data-testid="token-name"
    />
    <AutocompleteSearch
      v-model="model.scopes"
      :items="scopeItems"
      item-props
      :label="$t('portal.tokens.scopes')"
      :rules="[validations.required.array]"
      :error-messages="fieldErrors.scopes"
      multiple
      data-testid="token-scopes">
      <template #selection="{ item, index }"><SelectionChips :item="item" :index="index" :total="(model.scopes ?? []).length" /></template>
    </AutocompleteSearch>
    <v-text-field
      v-model="model.expires"
      :label="$t('portal.tokens.expires')"
      :hint="$t('portal.tokens.expiresHint')"
      persistent-hint
      :rules="[validations.date]"
      :error-messages="fieldErrors.expires_at"
      type="date"
      class="mb-2"
      data-testid="token-expires"
    />
    <v-btn type="submit" color="primary" :loading="submitting" data-testid="token-submit">{{ $t('portal.tokens.create') }}</v-btn>
  </v-form>
</template>
