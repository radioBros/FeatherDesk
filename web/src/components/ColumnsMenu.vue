<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Table toolbar button: choose which columns a table shows (see useTableColumns). */
import { mdiTableColumn } from '@mdi/js'
import type { TableColumns } from '@/composables/useTableColumns'

defineProps<{ cols: TableColumns; bar?: boolean }>()
</script>

<template>
  <div :class="bar === false ? '' : 'd-flex justify-end px-2 pt-1'" data-testid="columns-bar">
    <v-menu :close-on-content-click="false" location="bottom end">
      <template #activator="{ props: p }">
        <v-btn v-bind="p" variant="text" size="small" :prepend-icon="mdiTableColumn" :aria-label="$t('app.columns.button')" data-testid="columns-menu">
          {{ $t('app.columns.button') }}
        </v-btn>
      </template>
      <v-card min-width="240" max-width="320">
        <v-card-title class="text-subtitle-2 pb-0">{{ $t('app.columns.title') }}</v-card-title>
        <v-list density="compact" class="py-0" data-testid="columns-list">
          <v-list-item v-for="c in cols.all.value" :key="c.key" class="py-0">
            <v-checkbox
              :model-value="cols.show(c.key)"
              :label="c.label"
              :disabled="c.locked"
              :title="c.locked ? $t('app.columns.locked') : undefined"
              :data-testid="`column-toggle-${c.key}`"
              @update:model-value="(on: boolean | null) => cols.set(c.key, !!on)"
            />
          </v-list-item>
        </v-list>
        <v-card-actions>
          <v-btn variant="text" size="small" :disabled="cols.isDefault.value" data-testid="columns-reset" @click="cols.reset()">{{ $t('app.columns.reset') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-menu>
  </div>
</template>
