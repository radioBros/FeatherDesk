<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Create / edit a custom field definition (FE-053). The key and type are
 * fixed after creation (the PATCH body has neither). Select / multiselect
 * need at least one option with unique values; `customer_editable` needs
 * `customer_visible`.
 */
import { computed, reactive, watch } from 'vue'
import { mdiDelete, mdiPlus } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import LocalizedTextFields from '../shared/LocalizedTextFields.vue'
import type { components } from '@/api/schema'
import ProjectMultiSelect from '@/components/ProjectMultiSelect.vue'
import type { ProjectOption } from '@/components/ProjectSelect.vue'
import { FIELD_TYPES } from './constants'

type S = components['schemas']
const props = defineProps<{ field?: S['CustomFieldDef'] | null; categories?: S['Category'][]; projects?: ProjectOption[] }>()
const emit = defineEmits<{ saved: [S['CustomFieldDef']]; cancel: [] }>()
const { t } = useI18n()

interface OptionRow {
  value: string
  label: Record<string, string>
}
const model = reactive({
  key: props.field?.key ?? '',
  label: { ...(props.field?.label ?? {}) } as Record<string, string>,
  type: (props.field?.type ?? null) as S['FieldType'] | null,
  options: (props.field?.options ?? []).map((o) => ({ value: o.value, label: { ...o.label } })) as OptionRow[],
  required: props.field?.required ?? false,
  customer_visible: props.field?.customer_visible ?? false,
  customer_editable: props.field?.customer_editable ?? false,
  category_ids: [...(props.field?.category_ids ?? [])] as string[],
  sort: String(props.field?.sort ?? 0),
  project_ids: [...(props.field?.project_ids ?? [])] as string[],
})
const editing = computed(() => !!props.field)
const needsOptions = computed(() => model.type === 'select' || model.type === 'multiselect')
const typeItems = computed(() => FIELD_TYPES.map((v) => ({ value: v, title: t(`agent.customFields.types.${v}`) })))
const optionValues = () => model.options.map((o) => o.value)

// A customer can only edit what they can see (I-9).
watch(
  () => model.customer_visible,
  (v) => {
    if (!v) model.customer_editable = false
  },
)

function addOption() {
  model.options.push({ value: '', label: {} })
}

const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const common = {
    label: model.label,
    options: needsOptions.value ? model.options.map((o) => ({ value: o.value.trim(), label: o.label })) : undefined,
    required: model.required,
    customer_visible: model.customer_visible,
    customer_editable: model.customer_visible && model.customer_editable,
    category_ids: model.category_ids.length ? model.category_ids : null,
    ...(props.projects?.length ? { project_ids: model.project_ids.length ? model.project_ids : null } : {}),
    sort: Number(model.sort) || 0,
  }
  const saved = props.field
    ? await unwrap(api.PATCH('/admin/custom-fields/{id}', { params: { path: { id: props.field.id } }, body: common }))
    : await unwrap(api.POST('/admin/custom-fields', { body: { ...common, key: model.key.trim(), type: model.type! } }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="custom-field-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.key"
      :label="$t('agent.customFields.key')"
      :hint="editing ? $t('agent.customFields.keyLocked') : $t('agent.customFields.keyHint')"
      persistent-hint
      :readonly="editing"
      :rules="[validations.required.any, validations.customFieldKey]"
      :error-messages="fieldErrors.key"
      class="mb-2"
      data-testid="cf-key"
    />
    <LocalizedTextFields v-model="model.label" :label="$t('agent.customFields.label')" testid="cf-label" />
    <v-select
      v-model="model.type"
      :items="typeItems"
      :label="$t('agent.customFields.type')"
      :disabled="editing"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.type"
      data-testid="cf-type"
    />

    <div v-if="needsOptions" class="mb-3" data-testid="cf-options">
      <div class="text-subtitle-2 mb-2">{{ $t('agent.customFields.options') }}</div>
      <v-card v-for="(o, i) in model.options" :key="i" border flat class="pa-3 mb-2" data-testid="cf-option">
        <div class="d-flex ga-2 align-start">
          <v-text-field
            v-model="o.value"
            :label="$t('agent.customFields.optionValue')"
            :rules="[validations.required.any, validations.uniqueValues(optionValues)]"
            density="compact"
            :data-testid="`cf-option-value-${i}`"
          />
          <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('agent.common.remove')" @click="model.options.splice(i, 1)" />
        </div>
        <LocalizedTextFields v-model="o.label" :label="$t('agent.customFields.optionLabel')" :testid="`cf-option-label-${i}`" />
      </v-card>
      <v-input :model-value="model.options" :rules="[validations.required.array]" data-testid="cf-options-check">
        <v-btn variant="tonal" size="small" :prepend-icon="mdiPlus" data-testid="cf-option-add" @click="addOption">{{ $t('agent.customFields.addOption') }}</v-btn>
      </v-input>
    </div>

    <v-switch v-model="model.required" :label="$t('agent.customFields.required')" color="primary" inset hide-details data-testid="cf-required" />
    <v-switch v-model="model.customer_visible" :label="$t('agent.customFields.customerVisible')" color="primary" inset hide-details data-testid="cf-visible" />
    <v-switch
      v-model="model.customer_editable"
      :label="$t('agent.customFields.customerEditable')"
      :disabled="!model.customer_visible"
      :hint="model.customer_visible ? '' : $t('agent.customFields.editableNeedsVisible')"
      :persistent-hint="!model.customer_visible"
      color="primary"
      inset
      data-testid="cf-editable"
    />
    <v-autocomplete
      v-model="model.category_ids"
      :items="categories ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.customFields.categories')"
      :hint="$t('agent.customFields.categoriesHint')"
      persistent-hint
      multiple
      chips
      closable-chips
      class="mb-2"
      data-testid="cf-categories"
    />
    <ProjectMultiSelect
      v-if="projects?.length"
      v-model="model.project_ids"
      :projects="projects"
      :label="$t('projects.config.projects')"
      :hint="$t('projects.config.fieldHint')"
      :error-messages="fieldErrors.project_ids"
      class="mb-2"
      testid="cf-projects"
    />
    <v-text-field
      v-model="model.sort"
      :label="$t('agent.customFields.sort')"
      inputmode="numeric"
      :rules="[validations.integer]"
      :error-messages="fieldErrors.sort"
      data-testid="cf-sort"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="cf-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
