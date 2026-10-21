<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** `security.mfa_required` (FE-074, D-19 §B): who must use two-factor authentication. */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type Settings = components['schemas']['SecuritySettings']
const props = defineProps<{ settings: Settings | null }>()
const emit = defineEmits<{ saved: [s: Settings] }>()
const { t } = useI18n()
const policy = ref<Settings['mfa_required'] | null>(props.settings?.mfa_required ?? null)
const items = computed(() => (['none', 'staff', 'all'] as const).map((v) => ({ value: v, title: t(`mfa.policy.${v}`) })))
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSubmit = submit(async () => {
  emit('saved', await unwrap(api.PATCH('/admin/security', { body: { mfa_required: policy.value! } })))
})
</script>

<template>
  <v-form ref="formRef" data-testid="security-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <AutocompleteSearch
      v-model="policy"
      :items="items"
      :label="$t('mfa.policy.label')"
      :hint="$t('mfa.policy.hint')"
      persistent-hint
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.mfa_required"
      data-testid="security-mfa-required"
    />
    <div class="d-flex justify-end mt-4">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="security-save">{{ $t('app.save') }}</v-btn>
    </div>
  </v-form>
</template>
