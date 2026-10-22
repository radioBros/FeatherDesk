<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Custom fields on the ticket (FE-054): every definition that applies to the
 * ticket's category, editable only when `edit_fields` is allowed. Rules come
 * from `validations.forField` inside `CustomFieldInput`.
 */
import { reactive, watch } from 'vue'
import { useForm } from '@/composables/useForm'
import CustomFieldInput from '@/components/CustomFieldInput.vue'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{
  defs: S['CustomFieldDef'][]
  values?: S['CustomFieldValues']
  editable?: boolean
  save: (values: S['CustomFieldValues']) => Promise<unknown>
}>()
const model = reactive<S['CustomFieldValues']>({})
watch(
  () => [props.values, props.defs] as const,
  () => {
    for (const k of Object.keys(model)) delete model[k]
    for (const d of props.defs) model[d.key] = props.values?.[d.key] ?? (d.type === 'multiselect' ? [] : d.type === 'checkbox' ? false : null)
  },
  { immediate: true, deep: true },
)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSave = submit(async () => {
  await props.save({ ...model })
})
</script>

<template>
  <v-form ref="formRef" data-testid="custom-fields-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-2">{{ formError }}</v-alert>
    <CustomFieldInput
      v-for="d in defs"
      :key="d.id"
      v-model="model[d.key]"
      :def="d"
      :disabled="!editable"
      density="compact"
      :error-messages="fieldErrors[`cf.${d.key}`]"
    />
    <div v-if="editable" class="d-flex justify-end">
      <v-btn type="submit" size="small" color="primary" variant="tonal" :loading="submitting" data-testid="custom-fields-save">
        {{ $t('agent.common.save') }}
      </v-btn>
    </div>
  </v-form>
</template>
