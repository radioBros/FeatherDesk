<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Project picker for configuration forms (D-20). Presentational: the page loads
 * `useProjectsStore` and passes `projects`. With `nullable` the empty choice is
 * "all projects" (global scope); without it the field is required by the
 * caller's `rules`. Project admins only get their own projects in `projects`.
 */
import { computed } from 'vue'
import { optionOf } from './projectOption'
import ProjectChip from './ProjectChip.vue'

export interface ProjectOption { id: string; key: string; name: string; color?: string; active?: boolean }

const props = defineProps<{
  modelValue: string | null | undefined
  projects: ProjectOption[]
  label: string
  nullable?: boolean
  nullLabel?: string
  disabled?: boolean
  hint?: string
  rules?: ((v: unknown) => true | string)[]
  errorMessages?: string[]
  testid?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [string | null] }>()

const NONE = '__global__'
const items = computed(() => [
  ...(props.nullable ? [{ id: NONE, key: '', name: props.nullLabel ?? '' }] : []),
  ...props.projects.map((p) => ({ ...p })),
])
const value = computed(() => props.modelValue ?? (props.nullable ? NONE : null))
function onUpdate(v: string | null) {
  emit('update:modelValue', v === NONE ? null : v)
}
</script>

<template>
  <v-select
    :model-value="value"
    :items="items"
    item-title="name"
    item-value="id"
    :label="label"
    :disabled="disabled"
    :hint="hint"
    :persistent-hint="!!hint"
    :rules="rules"
    :error-messages="errorMessages"
    :data-testid="testid ?? 'project-select'"
    @update:model-value="onUpdate"
  >
    <template #item="{ props: ip, item }">
      <v-list-item v-bind="ip" :title="optionOf(item).name">
        <template v-if="optionOf(item).id !== NONE" #prepend><ProjectChip :project="optionOf(item)" size="small" class="mr-2" /></template>
      </v-list-item>
    </template>
    <template #selection="{ item }">
      <ProjectChip v-if="optionOf(item).id !== NONE" :project="optionOf(item)" size="small" class="mr-2" />
      <span>{{ optionOf(item).name }}</span>
    </template>
  </v-select>
</template>
