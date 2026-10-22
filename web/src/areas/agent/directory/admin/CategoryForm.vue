<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Create / edit a ticket category (FE-028). A form component: data comes in
 * through props and nothing loads on mount, so the FE-005 registry test can
 * submit it empty and assert that no request is sent.
 */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import ProjectSelect, { type ProjectOption } from '@/components/ProjectSelect.vue'

type S = components['schemas']
const props = defineProps<{ category?: S['Category'] | null; teams?: S['Team'][]; projects?: ProjectOption[] }>()
const emit = defineEmits<{ saved: [S['Category']]; cancel: [] }>()

const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']
const model = reactive({
  name: props.category?.name ?? '',
  project_id: (props.category?.project_id ?? props.projects?.[0]?.id ?? null) as string | null,
  default_team_id: props.category?.default_team_id ?? null,
  default_priority: props.category?.default_priority ?? null,
  active: props.category?.active ?? true,
})
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body: S['CategoryCreate'] = {
    name: model.name.trim(),
    // A category with tickets cannot move (immutable): only send the project when it changed.
    ...(model.project_id && model.project_id !== props.category?.project_id ? { project_id: model.project_id } : {}),
    default_team_id: model.default_team_id,
    default_priority: model.default_priority,
    active: model.active,
  }
  const saved = props.category
    ? await unwrap(api.PATCH('/admin/categories/{id}', { params: { path: { id: props.category.id } }, body }))
    : await unwrap(api.POST('/admin/categories', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="category-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(100)]"
      :error-messages="fieldErrors.name"
      data-testid="category-name"
    />
    <ProjectSelect
      v-if="projects?.length"
      v-model="model.project_id"
      :projects="projects"
      :label="$t('projects.config.project')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.project_id"
      testid="category-project"
    />
    <AutocompleteSearch
      v-model="model.default_team_id"
      :items="teams ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.categories.defaultTeam')"
      :error-messages="fieldErrors.default_team_id"
      clearable
    />
    <AutocompleteSearch
      v-model="model.default_priority"
      :items="PRIORITIES.map((p) => ({ value: p, title: $t(`priority.${p}`) }))"
      :label="$t('agent.categories.defaultPriority')"
      :error-messages="fieldErrors.default_priority"
      clearable
    />
    <v-switch v-model="model.active" :label="$t('agent.common.active')" color="primary" inset />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="category-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
