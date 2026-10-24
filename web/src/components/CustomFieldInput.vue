<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * One input for any admin-defined custom field (FE-054). The widget follows
 * the definition type and the rules come only from `validations.forField(def)`
 * (CONVENTIONS §12). Shared by the agent panel and the portal.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import SelectionChips from '@/components/SelectionChips.vue'
import { validations, type Rule } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'
import { localized } from '@/utils/localized'
import type { components } from '@/api/schema'

type Def = components['schemas']['CustomFieldDef']
type Value = components['schemas']['CustomFieldValue']

const props = withDefaults(
  defineProps<{ def: Def; modelValue?: Value; disabled?: boolean; density?: 'default' | 'comfortable' | 'compact'; errorMessages?: string[]; noRules?: boolean }>(),
  { modelValue: null, density: undefined },
)
const emit = defineEmits<{ 'update:modelValue': [Value] }>()
const { locale } = useI18n()
const config = useConfigStore()

const label = computed(() => localized(props.def.label, locale.value, config.config.branding.default_locale ?? 'en') || props.def.key)
const fieldRules = computed<Rule[]>(() =>
  props.noRules ? [] : validations.forField({ key: props.def.key, type: props.def.type, required: props.def.required, options: props.def.options }),
)
const options = computed(() =>
  (props.def.options ?? []).map((o) => ({ value: o.value, title: localized(o.label, locale.value) || o.value })),
)

function setNumber(v: string) {
  if (v === '' || v === null || v === undefined) return emit('update:modelValue', null)
  const n = Number(v)
  emit('update:modelValue', Number.isFinite(n) ? n : (v as unknown as Value))
}
const testid = computed(() => `cf-${props.def.key}`)
</script>

<template>
  <v-checkbox
    v-if="def.type === 'checkbox'"
    :model-value="modelValue === true"
    :label="label"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    :data-testid="testid"
    @update:model-value="(v: boolean | null) => emit('update:modelValue', !!v)"
  />
  <AutocompleteSearch
    v-else-if="def.type === 'select' || def.type === 'multiselect'"
    :model-value="modelValue"
    :items="options"
    :label="label"
    :multiple="def.type === 'multiselect'"
    :compact-multiple="noRules"
    :clearable="!def.required"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    :data-testid="testid"
    @update:model-value="(v: Value) => emit('update:modelValue', v ?? (def.type === 'multiselect' ? [] : null))"
  >
    <template v-if="def.type === 'multiselect' && !noRules" #selection="{ item, index }">
      <SelectionChips :item="item" :index="index" :total="Array.isArray(modelValue) ? modelValue.length : 0" />
    </template>
  </AutocompleteSearch>
  <v-textarea
    v-else-if="def.type === 'textarea'"
    :model-value="(modelValue as string) ?? ''"
    :label="label"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    auto-grow
    :rows="noRules ? 1 : undefined"
    :data-testid="testid"
    @update:model-value="(v: string) => emit('update:modelValue', v)"
  />
  <v-text-field
    v-else-if="def.type === 'number'"
    :model-value="modelValue === null || modelValue === undefined ? '' : String(modelValue)"
    :label="label"
    inputmode="decimal"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    :data-testid="testid"
    @update:model-value="setNumber"
  />
  <v-text-field
    v-else-if="def.type === 'date'"
    :model-value="(modelValue as string) ?? ''"
    :label="label"
    type="date"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    :data-testid="testid"
    @update:model-value="(v: string) => emit('update:modelValue', v || null)"
  />
  <v-text-field
    v-else
    :model-value="(modelValue as string) ?? ''"
    :label="label"
    :rules="fieldRules"
    :disabled="disabled"
    :density="density"
    :hide-details="noRules ? true : undefined"
    :error-messages="errorMessages"
    :data-testid="testid"
    @update:model-value="(v: string) => emit('update:modelValue', v)"
  />
</template>
