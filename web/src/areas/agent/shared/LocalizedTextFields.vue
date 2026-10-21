<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * One input per available locale for a `LocalizedText` value
 * (`{"en": "Billing", "it": "Fatturazione"}`). The default locale is required.
 */
import { computed } from 'vue'
import { validations, type Rule } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'
import { SUPPORTED_LOCALES } from '@/i18n'

const props = withDefaults(
  defineProps<{ modelValue: Record<string, string>; label: string; maxLength?: number; testid?: string }>(),
  { maxLength: 200 },
)
const emit = defineEmits<{ 'update:modelValue': [Record<string, string>] }>()
const config = useConfigStore()
const defaultLocale = computed(() => config.config.branding.default_locale ?? 'en')
const locales = computed(() => {
  const list = config.config.branding.locales?.length ? config.config.branding.locales : [...SUPPORTED_LOCALES]
  return [defaultLocale.value, ...list.filter((l) => l !== defaultLocale.value)]
})
const requiredRules: Rule[] = [validations.required.any]
const lengthRules = computed<Rule[]>(() => [validations.maxLength(props.maxLength)])
const fieldRules = (l: string) => (l === defaultLocale.value ? [...requiredRules, ...lengthRules.value] : lengthRules.value)

function set(locale: string, v: string) {
  const next = { ...props.modelValue }
  if (v) next[locale] = v
  else delete next[locale]
  emit('update:modelValue', next)
}
</script>

<template>
  <div :data-testid="testid ?? 'localized-text'">
    <v-text-field
      v-for="l in locales"
      :key="l"
      :model-value="modelValue[l] ?? ''"
      :label="`${label} (${l.toUpperCase()})`"
      :rules="fieldRules(l)"
      density="compact"
      :data-testid="`${testid ?? 'localized-text'}-${l}`"
      @update:model-value="(v: string) => set(l, v)"
    />
  </div>
</template>
