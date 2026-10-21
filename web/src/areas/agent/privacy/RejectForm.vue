<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Reject a pending deletion request with a reason (FE-084, D-19 §C), admin only. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

const props = defineProps<{ requestId: string }>()
const emit = defineEmits<{ rejected: [components['schemas']['PrivacyRequest']]; cancel: [] }>()
const model = reactive({ reason: '' })
const reasonRules = [validations.required.any, validations.maxLength(1000)]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const req = await unwrap(
    api.POST('/admin/privacy/requests/{id}/reject', { params: { path: { id: props.requestId } }, body: { reason: model.reason.trim() } }),
  )
  emit('rejected', req)
})
</script>

<template>
  <v-form ref="formRef" data-testid="reject-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-textarea
      v-model="model.reason"
      :label="$t('privacy.reject.reason')"
      :rules="reasonRules"
      :error-messages="fieldErrors.reason"
      rows="3"
      auto-grow
      counter="1000"
      data-testid="reject-reason"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('privacy.action.cancel') }}</v-btn>
      <v-btn type="submit" color="error" :loading="submitting" data-testid="reject-submit">{{ $t('privacy.action.reject') }}</v-btn>
    </div>
  </v-form>
</template>
