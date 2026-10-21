<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Optional custom-field columns, chosen per user (FE-054). */
import { mdiTableColumnPlusAfter } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import { localized } from '@/utils/localized'
import type { components } from '@/api/schema'

defineProps<{ defs: components['schemas']['CustomFieldDef'][]; modelValue: string[] }>()
const emit = defineEmits<{ 'update:modelValue': [string[]] }>()
const { locale } = useI18n()
</script>

<template>
  <v-menu :close-on-content-click="false">
    <template #activator="{ props: p }">
      <v-btn v-bind="p" variant="text" :prepend-icon="mdiTableColumnPlusAfter" :disabled="!defs.length" data-testid="column-chooser">
        {{ $t('agent.list.customColumns') }}
      </v-btn>
    </template>
    <v-list density="compact" min-width="220">
      <v-list-item v-for="d in defs" :key="d.key">
        <v-checkbox
          :model-value="modelValue.includes(d.key)"
          :label="localized(d.label, locale) || d.key"
          hide-details
          density="compact"
          :data-testid="`column-${d.key}`"
          @update:model-value="(on: boolean | null) => emit('update:modelValue', on ? [...modelValue, d.key] : modelValue.filter((k) => k !== d.key))"
        />
      </v-list-item>
    </v-list>
  </v-menu>
</template>
