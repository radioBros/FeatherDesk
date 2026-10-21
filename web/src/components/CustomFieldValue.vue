<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Read-only rendering of a custom field value (FE-054): option labels, dates and booleans localised. */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { localized } from '@/utils/localized'
import type { components } from '@/api/schema'

type Def = components['schemas']['CustomFieldDef']
type Value = components['schemas']['CustomFieldValue']
const props = defineProps<{ def: Def; value?: Value }>()
const { locale, t } = useI18n()

const text = computed(() => {
  const v = props.value
  if (v === null || v === undefined || v === '' || (Array.isArray(v) && !v.length)) return '—'
  const label = (x: string) => {
    const o = props.def.options?.find((op) => op.value === x)
    return o ? localized(o.label, locale.value) || x : x
  }
  switch (props.def.type) {
    case 'checkbox':
      return v === true ? t('app.yes') : t('app.no')
    case 'select':
      return label(String(v))
    case 'multiselect':
      return (Array.isArray(v) ? v : [String(v)]).map(label).join(', ')
    case 'number':
      return typeof v === 'number' ? new Intl.NumberFormat(locale.value).format(v) : String(v)
    case 'date': {
      const d = new Date(String(v) + 'T00:00:00Z')
      return Number.isNaN(d.getTime()) ? String(v) : new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeZone: 'UTC' }).format(d)
    }
    default:
      return String(v)
  }
})
</script>

<template>
  <span :data-testid="`cfv-${def.key}`">{{ text }}</span>
</template>
