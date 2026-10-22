<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Macro picker in the ticket header (FE-065, D-19 A). The list loads when the
 * menu first opens; applying posts once with a fresh `Idempotency-Key` and the
 * ticket's ETag as `If-Match` (only when there is one, as every other ticket
 * mutation). The page handles the `TicketUpdateResult` (incl. `visible: false`)
 * and the problems (403, 409, 412) through its usual helpers.
 */
import { ref } from 'vue'
import { mdiLightningBolt } from '@mdi/js'
import { api, newIdempotencyKey, unwrap } from '@/api/client'
import { applyMacro } from './api'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: S['Ticket']; etag?: string | null }>()
const emit = defineEmits<{ applied: [S['TicketUpdateResult'], string | null, S['Macro']]; failed: [unknown] }>()

const macros = ref<S['Macro'][]>([])
const loading = ref(false)
const loaded = ref(false)
const applying = ref<string | null>(null)

async function onOpen(open: boolean) {
  if (!open || loaded.value || loading.value) return
  loading.value = true
  try {
    macros.value = ((await unwrap(api.GET('/agent/macros'))) ?? []).sort((a, b) => a.name.localeCompare(b.name))
    loaded.value = true
  } catch (e) {
    emit('failed', e)
  } finally {
    loading.value = false
  }
}

async function apply(m: S['Macro']) {
  if (applying.value) return
  applying.value = m.id
  try {
    const { result, etag } = await applyMacro(props.ticket.id, m.id, props.etag ?? null, newIdempotencyKey())
    emit('applied', result, etag, m)
  } catch (e) {
    emit('failed', e)
  } finally {
    applying.value = null
  }
}
</script>

<template>
  <v-menu @update:model-value="onOpen">
    <template #activator="{ props: p }">
      <v-btn v-bind="p" variant="tonal" size="small" :prepend-icon="mdiLightningBolt" :loading="!!applying" data-testid="macro-picker">
        {{ $t('automation.macros.apply') }}
      </v-btn>
    </template>
    <v-list density="compact" min-width="240" data-testid="macro-picker-list">
      <v-list-item v-if="loading" :title="$t('automation.macros.loading')" />
      <v-list-item v-else-if="loaded && !macros.length" :title="$t('automation.macros.none')" :subtitle="$t('automation.macros.noneHint')" to="/agent/macros" />
      <v-list-item
        v-for="m in macros"
        :key="m.id"
        :title="m.name"
        :subtitle="m.shared ? $t('automation.macros.shared') : $t('automation.macros.personal')"
        :disabled="!!applying"
        data-testid="macro-item"
        @click="apply(m)"
      />
    </v-list>
  </v-menu>
</template>
