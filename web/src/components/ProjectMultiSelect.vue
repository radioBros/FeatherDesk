<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Multi project picker (teams, organizations, webhooks, custom fields). An
 * empty selection has a meaning the caller states in `hint` (usually "every
 * project"). Presentational: `projects` come from the page.
 */
import { optionOf } from './projectOption'
import type { ProjectOption } from './ProjectSelect.vue'

defineProps<{
  modelValue: string[]
  projects: ProjectOption[]
  label: string
  hint?: string
  disabled?: boolean
  rules?: ((v: unknown) => true | string)[]
  errorMessages?: string[]
  testid?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [string[]] }>()
</script>

<template>
  <AutocompleteSearch
    :model-value="modelValue"
    :items="projects"
    item-title="name"
    item-value="id"
    :label="label"
    :hint="hint"
    :persistent-hint="!!hint"
    :disabled="disabled"
    :rules="rules"
    :error-messages="errorMessages"
    multiple
    chips
    closable-chips
    :data-testid="testid ?? 'project-multiselect'"
    @update:model-value="(v: string[]) => emit('update:modelValue', v)"
  >
    <template #chip="{ item, props: cp }">
      <v-chip v-bind="cp" size="small" label :title="optionOf(item).name">{{ optionOf(item).key }}</v-chip>
    </template>
  </AutocompleteSearch>
</template>
