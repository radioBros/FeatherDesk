<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** "Save current view" (FE-038): name + shared toggle; the filter is the list's current URL query. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { SavedView } from './views'

const props = defineProps<{ filter?: Record<string, unknown> }>()
const emit = defineEmits<{ saved: [SavedView]; cancel: [] }>()
const model = reactive({ name: '', shared: false })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const view = await unwrap(
    api.POST('/agent/views', { body: { name: model.name.trim(), shared: model.shared, filter: { ...(props.filter ?? {}) } } }),
  )
  emit('saved', view)
})
</script>

<template>
  <v-form ref="formRef" data-testid="save-view-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.views.name')"
      :rules="[validations.required.any, validations.maxLength(100)]"
      :error-messages="fieldErrors.name"
      autofocus
      data-testid="view-name"
    />
    <v-switch v-model="model.shared" :label="$t('agent.views.shared')" color="primary" inset hide-details data-testid="view-shared" />
    <div class="d-flex justify-end ga-2 mt-4">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="view-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
