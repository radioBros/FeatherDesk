<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/settings (FE-029, admin): support settings only; branding and email have their own pages. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PageHeader from '@/components/PageHeader.vue'
import SupportSettingsForm from './SupportSettingsForm.vue'
import SecurityPolicyCard from './SecurityPolicyCard.vue'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'
import type { components } from '@/api/schema'

const { t } = useI18n()
const config = useConfigStore()
const ui = useUiStore()
const settings = ref<components['schemas']['SupportSettings'] | null>(null)
const loading = ref(true)
const failed = ref(false)
const formKey = ref(0)

async function load() {
  loading.value = true
  failed.value = false
  try {
    settings.value = await unwrap(api.GET('/admin/settings'))
    formKey.value++
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function onSaved(s: components['schemas']['SupportSettings']) {
  settings.value = s
  ui.notify(t('agent.common.saved'), 'success')
  // Tier labels and policies are public config: refresh them for this tab.
  await config.load()
}
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 960px">
    <PageHeader :title="$t('agent.admin.nav.settings')" :subtitle="$t('agent.settings.subtitle')" />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">
      {{ $t('agent.common.loadFailed') }}
      <template #append><v-btn variant="text" @click="load">{{ $t('app.retry') }}</v-btn></template>
    </v-alert>
    <v-progress-linear v-if="loading" indeterminate />
    <v-card v-else-if="settings" border flat class="pa-4">
      <SupportSettingsForm :key="formKey" :settings="settings" @saved="onSaved" />
    </v-card>
    <SecurityPolicyCard />
  </v-container>
</template>
