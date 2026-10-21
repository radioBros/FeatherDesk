<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { localized, type CustomFieldDef, type CustomFieldValues } from '../types'

/** Read-only list of the customer-visible custom fields on a ticket (FE-054). */
const props = defineProps<{ defs: CustomFieldDef[]; values?: CustomFieldValues | null }>()
const { locale, t } = useI18n()

const rows = computed(() =>
  props.defs
    .filter((d) => d.customer_visible && props.values && props.values[d.key] !== undefined && props.values[d.key] !== null && props.values[d.key] !== '')
    .map((d) => {
      const v = props.values![d.key]
      const opt = (val: string) => localized((d.options ?? []).find((o) => o.value === val)?.label, locale.value) || val
      let text: string
      if (d.type === 'checkbox') text = v ? t('portal.fields.yes') : t('portal.fields.no')
      else if (Array.isArray(v)) text = v.map((x) => opt(String(x))).join(', ')
      else if (d.type === 'select') text = opt(String(v))
      else text = String(v)
      return { key: d.key, label: localized(d.label, locale.value) || d.key, text }
    }),
)
</script>

<template>
  <v-list v-if="rows.length" density="compact" class="py-0" data-testid="cf-values">
    <v-list-item v-for="r in rows" :key="r.key" :title="r.text" :subtitle="r.label" :data-testid="`cf-value-${r.key}`" />
  </v-list>
</template>
