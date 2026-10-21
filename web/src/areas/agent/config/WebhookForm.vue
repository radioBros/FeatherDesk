<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import SelectionChips from '@/components/SelectionChips.vue'
/**
 * Create / edit a webhook (FE-040). Save and Test both go through
 * `useForm().submit`, so `validate()` runs first and an invalid form sends
 * nothing. Test is available once the webhook exists (the API tests a saved
 * webhook); the result is shown inline.
 */
import { reactive, ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import ProjectMultiSelect from '@/components/ProjectMultiSelect.vue'
import type { ProjectOption } from '@/components/ProjectSelect.vue'
import { EVENT_TYPES } from './constants'

type S = components['schemas']
const props = defineProps<{ webhook?: S['Webhook'] | null; projects?: ProjectOption[] }>()
const emit = defineEmits<{ saved: [S['Webhook'], string | null]; cancel: [] }>()

const model = reactive({
  url: props.webhook?.url ?? '',
  events: [...(props.webhook?.events ?? [])] as S['EventType'][],
  include_internal: props.webhook?.include_internal ?? false,
  active: props.webhook?.active ?? true,
  project_ids: [...(props.webhook?.project_ids ?? [])] as string[],
})
const testResult = ref<S['WebhookTestResult'] | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body = { url: model.url.trim(), events: model.events, include_internal: model.include_internal, active: model.active, ...(props.projects?.length ? { project_ids: model.project_ids } : {}) }
  if (props.webhook) {
    const saved = await unwrap(api.PATCH('/admin/webhooks/{id}', { params: { path: { id: props.webhook.id } }, body }))
    emit('saved', saved, null)
  } else {
    const created = await unwrap(api.POST('/admin/webhooks', { body }))
    emit('saved', created, created.secret ?? null)
  }
})

const onTest = submit(async () => {
  if (!props.webhook) return
  testResult.value = null
  testResult.value = await unwrap(api.POST('/admin/webhooks/{id}/test', { params: { path: { id: props.webhook.id } } }))
})
</script>

<template>
  <v-form ref="formRef" data-testid="webhook-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.url"
      :label="$t('agent.webhooks.url')"
      :hint="$t('agent.webhooks.urlHint')"
      persistent-hint
      :rules="[validations.required.any, validations.httpsUrl]"
      :error-messages="fieldErrors.url"
      class="mb-2"
      data-testid="webhook-url"
    />
    <AutocompleteSearch
      v-model="model.events"
      :items="EVENT_TYPES"
      :label="$t('agent.webhooks.events')"
      :rules="[validations.required.array]"
      :error-messages="fieldErrors.events"
      multiple
      data-testid="webhook-events">
      <template #selection="{ item, index }"><SelectionChips :item="item" :index="index" :total="(model.events ?? []).length" /></template>
    </AutocompleteSearch>
    <ProjectMultiSelect
      v-if="projects?.length"
      v-model="model.project_ids"
      :projects="projects"
      :label="$t('projects.config.projects')"
      :hint="$t('projects.config.webhookHint')"
      :error-messages="fieldErrors.project_ids"
      class="mb-2"
      testid="webhook-projects"
    />
    <v-switch v-model="model.include_internal" :label="$t('agent.webhooks.includeInternal')" color="warning" inset hide-details data-testid="webhook-internal" />
    <v-alert v-if="model.include_internal" type="warning" variant="tonal" density="compact" class="my-2" data-testid="webhook-internal-warning">
      {{ $t('agent.webhooks.includeInternalWarning') }}
    </v-alert>
    <v-switch v-model="model.active" :label="$t('agent.common.active')" color="primary" inset hide-details />

    <v-alert
      v-if="testResult"
      :type="testResult.error ? 'error' : 'success'"
      variant="tonal"
      density="compact"
      class="my-2"
      data-testid="webhook-test-result"
    >
      <div>{{ $t('agent.webhooks.testStatus', { code: testResult.status_code ?? '—', ms: testResult.response_ms ?? '—' }) }}</div>
      <div v-if="testResult.error">{{ testResult.error }}</div>
    </v-alert>

    <div class="d-flex flex-wrap justify-end ga-2 mt-3">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn v-if="webhook" variant="tonal" :loading="submitting" data-testid="webhook-test" @click="onTest()">{{ $t('agent.common.test') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="webhook-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
