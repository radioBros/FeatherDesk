<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Create / edit a KB category (FE-052): parent (never itself or a
 * descendant), slug, per-locale titles, visibility, sort.
 */
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import LocalizedTextFields from '../shared/LocalizedTextFields.vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { localized } from '@/utils/localized'
import ProjectSelect, { type ProjectOption } from '@/components/ProjectSelect.vue'
import { descendantsOf, flattenTree, VISIBILITIES, type KbCategory, type KbVisibility } from './kb'

const props = withDefaults(defineProps<{ category?: KbCategory | null; categories?: KbCategory[]; projects?: ProjectOption[]; canGlobal?: boolean }>(), {
  category: null,
  categories: () => [],
  projects: () => [],
  canGlobal: true,
})
const emit = defineEmits<{ saved: [KbCategory]; cancel: [] }>()
const { t, locale } = useI18n()

const model = reactive({
  parent_id: props.category?.parent_id ?? (null as string | null),
  slug: props.category?.slug ?? '',
  title: { ...(props.category?.title ?? {}) } as Record<string, string>,
  visibility: (props.category?.visibility ?? null) as KbVisibility | null,
  project_id: (props.category?.project_id ?? null) as string | null,
  sort: props.category?.sort !== undefined && props.category?.sort !== null ? String(props.category.sort) : '',
})

const parentItems = computed(() => {
  const excluded = props.category ? descendantsOf(props.categories, props.category.id) : new Set<string>()
  return flattenTree(props.categories, (c) => localized(c.title, locale.value) || c.slug)
    .filter(({ item }) => !excluded.has(item.id))
    .map(({ item, depth }) => ({ value: item.id, title: `${'— '.repeat(depth)}${localized(item.title, locale.value) || item.slug}` }))
})
// A sub-category belongs to its parent's project.
const parent = computed(() => props.categories.find((c) => c.id === model.parent_id))
watch(parent, (p) => {
  if (p) model.project_id = p.project_id ?? null
})
const visibilityItems = computed(() => VISIBILITIES.map((v) => ({ value: v, title: t(`agent.kb.vis.${v}`) })))

const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  const body = {
    parent_id: model.parent_id,
    slug: model.slug.trim(),
    title: model.title,
    visibility: model.visibility!,
    ...(props.projects.length ? { project_id: model.project_id } : {}),
    sort: model.sort === '' ? undefined : Number(model.sort),
  }
  const saved = props.category
    ? await unwrap(api.PATCH('/admin/kb/categories/{id}', { params: { path: { id: props.category.id } }, body }))
    : await unwrap(api.POST('/admin/kb/categories', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="kb-category-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <LocalizedTextFields v-model="model.title" :label="$t('agent.kbCategories.title')" :max-length="120" testid="kbcat-title" />
    <v-text-field
      v-model="model.slug"
      :label="$t('agent.kb.slug')"
      :rules="[validations.required.any, validations.kbSlug]"
      :error-messages="fieldErrors.slug"
      data-testid="kbcat-slug"
    />
    <AutocompleteSearch
      v-model="model.parent_id"
      :items="parentItems"
      :label="$t('agent.kbCategories.parent')"
      :hint="$t('agent.kbCategories.parentHint')"
      persistent-hint
      clearable
      :error-messages="fieldErrors.parent_id"
      data-testid="kbcat-parent"
    />
    <ProjectSelect
      v-if="projects.length"
      v-model="model.project_id"
      :projects="projects"
      :nullable="canGlobal"
      :null-label="$t('projects.config.global')"
      :disabled="!!parent"
      :label="$t('projects.config.project')"
      :hint="parent ? $t('projects.config.subHint') : $t('projects.config.scopeHint')"
      :rules="canGlobal ? [] : [validations.required.any]"
      :error-messages="fieldErrors.project_id"
      testid="kbcat-project"
    />
    <AutocompleteSearch
      v-model="model.visibility"
      :items="visibilityItems"
      :label="$t('agent.kb.visibility')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.visibility"
      class="mt-2"
      data-testid="kbcat-visibility"
    />
    <v-text-field
      v-model="model.sort"
      :label="$t('agent.kbCategories.sort')"
      inputmode="numeric"
      :rules="[validations.integer]"
      :error-messages="fieldErrors.sort"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="kbcat-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
