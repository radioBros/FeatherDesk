<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Create / edit a canned response (FE-041): title, Markdown body, optional team scope (none = global). */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import type { components } from '@/api/schema'
import ProjectSelect, { type ProjectOption } from '@/components/ProjectSelect.vue'

type S = components['schemas']
const props = defineProps<{ canned?: S['CannedResponse'] | null; teams?: S['Team'][]; projects?: ProjectOption[]; canGlobal?: boolean }>()
const emit = defineEmits<{ saved: [S['CannedResponse']]; cancel: [] }>()

const model = reactive({
  title: props.canned?.title ?? '',
  body_md: props.canned?.body_md ?? '',
  team_id: props.canned?.team_id ?? null,
  project_id: (props.canned?.project_id ?? null) as string | null,
})
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body: S['CannedCreate'] = { title: model.title.trim(), body_md: model.body_md, team_id: model.team_id, ...(props.projects?.length ? { project_id: model.project_id } : {}) }
  const saved = props.canned
    ? await unwrap(api.PATCH('/admin/canned-responses/{id}', { params: { path: { id: props.canned.id } }, body }))
    : await unwrap(api.POST('/admin/canned-responses', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="canned-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.title"
      :label="$t('agent.canned.title')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.title"
      data-testid="canned-title"
    />
    <v-select
      v-model="model.team_id"
      :items="teams ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.canned.scope')"
      :hint="$t('agent.canned.scopeHint')"
      persistent-hint
      clearable
      class="mb-2"
      data-testid="canned-team"
    />
    <ProjectSelect
      v-if="projects?.length"
      v-model="model.project_id"
      :projects="projects"
      :nullable="canGlobal !== false"
      :null-label="$t('projects.config.global')"
      :label="$t('projects.config.project')"
      :hint="$t('projects.config.scopeHint')"
      :rules="canGlobal === false ? [validations.required.any] : []"
      :error-messages="fieldErrors.project_id"
      testid="canned-project"
    />
    <MarkdownEditor v-model="model.body_md" :label="$t('agent.canned.body')" required :error-messages="fieldErrors.body_md" />
    <div class="text-caption text-medium-emphasis mb-3" data-testid="canned-variables">
      {{ $t('agent.canned.variables') }} <code v-pre>{{requester.name}}</code>, <code v-pre>{{ticket.number}}</code>, <code v-pre>{{agent.name}}</code>
    </div>
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="canned-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
