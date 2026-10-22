<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** /agent/admin/email (FE-048, admin): SMTP, senders, template toggles, triage mailbox, test email, template preview. */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAlertCircle, mdiCheckCircle } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmailSettingsForm from './EmailSettingsForm.vue'
import EmailTestForm from './EmailTestForm.vue'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'
import { previewUrl } from './types'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t, locale } = useI18n()
const config = useConfigStore()
const ui = useUiStore()
const settings = ref<S['EmailSettings'] | null>(null)
const loading = ref(true)
const failed = ref(false)
const formKey = ref(0)
const testConfig = ref<S['EmailSettings'] | null>(null)
const testOpen = ref(false)
const previewKey = ref<string | null>(null)
const previewLocale = ref(locale.value)

async function load() {
  loading.value = true
  failed.value = false
  try {
    settings.value = await unwrap(api.GET('/admin/email'))
    formKey.value++
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function onSaved(s: S['EmailSettings']) {
  settings.value = s
  formKey.value++
  ui.notify(t('agent.common.saved'), 'success')
  // email_enabled is public config (login pages, registration).
  await config.load()
}
function onTest(cfg: S['EmailSettings']) {
  testConfig.value = cfg
  testOpen.value = true
}
function closeTest() {
  testOpen.value = false
  testConfig.value = null
}
const status = computed(() => settings.value?.status)
const previewSrc = computed(() => (previewKey.value ? previewUrl(previewKey.value, previewLocale.value) : ''))
const LOCALES = ['en', 'it', 'de', 'fr', 'es']
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 1100px">
    <PageHeader :title="$t('agent.admin.nav.email')" :subtitle="$t('agent.email.subtitle')" />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">
      {{ $t('agent.common.loadFailed') }}
      <template #append><v-btn variant="text" @click="load">{{ $t('app.retry') }}</v-btn></template>
    </v-alert>
    <v-progress-linear v-if="loading" indeterminate />
    <template v-if="settings && !loading">
      <v-alert
        :type="status?.enabled ? 'success' : 'warning'"
        :icon="status?.enabled ? mdiCheckCircle : mdiAlertCircle"
        variant="tonal"
        density="compact"
        class="mb-4"
        data-testid="email-status"
      >
        {{ status?.enabled ? $t('agent.email.statusOn') : $t('agent.email.statusOff') }}
        <div v-if="status?.last_error" class="text-caption mt-1">{{ $t('agent.email.lastError') }}: {{ status.last_error }}</div>
      </v-alert>
      <v-card border flat class="pa-4">
        <EmailSettingsForm :key="formKey" :settings="settings" @saved="onSaved" @test="onTest" @preview="(k) => (previewKey = k)" />
      </v-card>
    </template>

    <v-dialog v-model="testOpen" max-width="480" @after-leave="testConfig = null">
      <v-card :title="$t('agent.email.sendTest')">
        <v-card-text>
          <EmailTestForm v-if="testOpen" :config="testConfig" @close="closeTest" />
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="!!previewKey" max-width="760" @update:model-value="(v: boolean) => !v && (previewKey = null)">
      <v-card>
        <v-card-title class="d-flex align-center ga-2">
          <code class="flex-grow-1">{{ previewKey }}</code>
          <AutocompleteSearch v-model="previewLocale" :items="LOCALES" :label="$t('agent.email.previewLocale')" density="compact" hide-details style="max-width: 120px" />
        </v-card-title>
        <v-card-text>
          <iframe v-if="previewKey" :src="previewSrc" sandbox="" referrerpolicy="no-referrer" class="fd-iframe" :title="$t('agent.common.preview')" data-testid="template-preview-iframe" />
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="previewKey = null">{{ $t('app.close') }}</v-btn></v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.fd-iframe { width: 100%; height: 520px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); border-radius: 4px; background: #fff; }
</style>
