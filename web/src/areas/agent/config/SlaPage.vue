<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/sla (FE-041, FE-056; supervisor+): SLA targets per priority. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PageHeader from '@/components/PageHeader.vue'
import SlaForm from './SlaForm.vue'
import { api, unwrap } from '@/api/client'
import { useUiStore } from '@/stores/ui'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()
const ui = useUiStore()
const policies = ref<S['SlaPolicy'][]>([])
const loading = ref(true)
const failed = ref(false)
const version = ref(0)

async function load() {
  loading.value = true
  failed.value = false
  try {
    policies.value = (await unwrap(api.GET('/admin/sla-policies'))) ?? []
    version.value++
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

function onSaved(saved: S['SlaPolicy'][]) {
  policies.value = saved
  ui.notify(t('agent.common.saved'), 'success')
}
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.sla')" :subtitle="$t('agent.sla.subtitle')" />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-alert type="info" variant="tonal" density="compact" class="mb-4">
      {{ $t('agent.sla.calendarHint') }}
      <router-link to="/agent/admin/calendars">{{ $t('agent.admin.nav.calendars') }}</router-link>
    </v-alert>
    <v-card border flat class="pa-2">
      <v-progress-linear v-if="loading" indeterminate />
      <SlaForm v-else :key="version" :policies="policies" @saved="onSaved" />
    </v-card>
  </v-container>
</template>
