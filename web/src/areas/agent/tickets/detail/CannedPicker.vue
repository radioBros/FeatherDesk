<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Canned-response picker (FE-039): searches `/agent/canned-responses` when
 * opened (never on mount) and emits the body with `{{…}}` variables filled.
 */
import { ref, watch } from 'vue'
import { mdiTextBoxOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { fillVars } from './composer'
import type { components } from '@/api/schema'

type S = components['schemas']
/** `compact`: icon-only activator (the composer toolbar on a phone); the label stays as aria-label. */
const props = defineProps<{ vars: Record<string, string>; compact?: boolean }>()
const emit = defineEmits<{ insert: [string] }>()
const open = ref(false)
const q = ref('')
const items = ref<S['CannedResponse'][]>([])
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null

async function search() {
  loading.value = true
  try {
    items.value = (await unwrap(api.GET('/agent/canned-responses', { params: { query: q.value ? { q: q.value } : {} } }))) ?? []
  } catch {
    items.value = []
  } finally {
    loading.value = false
  }
}
watch(open, (v) => v && void search())
watch(q, () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(search, 300)
})

function pick(c: S['CannedResponse']) {
  emit('insert', fillVars(c.body_md, props.vars))
  open.value = false
}
</script>

<template>
  <v-menu v-model="open" :close-on-content-click="false" location="top start">
    <template #activator="{ props: p }">
      <v-btn v-if="compact" v-bind="p" variant="text" :icon="mdiTextBoxOutline" :aria-label="$t('agent.ticket.composer.canned')" data-testid="canned-open" />
      <v-btn v-else v-bind="p" variant="text" size="small" :prepend-icon="mdiTextBoxOutline" data-testid="canned-open">
        {{ $t('agent.ticket.composer.canned') }}
      </v-btn>
    </template>
    <v-card min-width="320" max-width="440">
      <v-card-text class="pb-0">
        <v-text-field v-model="q" :label="$t('agent.common.search')" density="compact" autofocus hide-details data-testid="canned-search" />
      </v-card-text>
      <v-progress-linear v-if="loading" indeterminate />
      <v-list density="compact" max-height="300" class="overflow-y-auto">
        <v-list-item
          v-for="c in items"
          :key="c.id"
          :title="c.title"
          :subtitle="c.body_md.slice(0, 80)"
          data-testid="canned-item"
          @click="pick(c)"
        />
        <v-list-item v-if="!loading && !items.length" :title="$t('agent.common.noResults')" disabled />
      </v-list>
    </v-card>
  </v-menu>
</template>
