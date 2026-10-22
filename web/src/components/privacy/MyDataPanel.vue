<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "My data" (FE-082/FE-083, D-19 §C): request an export of one's own data,
 * download it while it is ready, file an account-deletion request (customers
 * only, `allowErasure`), and see the consent records. Shared by the portal
 * `/profile/data` and the agent `/agent/profile/data` pages.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDatabaseExportOutline, mdiRefresh } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useUiStore } from '@/stores/ui'
import RelativeTime from '@/components/RelativeTime.vue'
import PrivacyRequestList from './PrivacyRequestList.vue'
import ErasureRequestForm from './ErasureRequestForm.vue'
import type { components } from '@/api/schema'

const props = withDefaults(defineProps<{ allowErasure?: boolean }>(), { allowErasure: false })
const { t } = useI18n()
const ui = useUiStore()
const data = ref<components['schemas']['MyPrivacy'] | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const exporting = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    data.value = await unwrap(api.GET('/me/privacy'))
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}
onMounted(load)

const requests = computed(() => data.value?.requests ?? [])
const exportPending = computed(() => requests.value.some((r) => r.type === 'export' && (r.status === 'pending' || r.status === 'processing')))
const erasurePending = computed(() => requests.value.some((r) => r.type === 'erasure' && r.status === 'pending'))
const showErasure = computed(() => props.allowErasure && !!data.value?.can_request_erasure)

async function requestExport() {
  exporting.value = true
  try {
    await unwrap(api.POST('/me/privacy/export'))
    ui.notify(t('privacy.export.queued'), 'success')
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
  } finally {
    exporting.value = false
  }
  await load()
}
async function onErasureRequested() {
  ui.notify(t('privacy.my.deletionRequested'), 'success')
  await load()
}
</script>

<template>
  <div data-testid="my-data">
    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-2" />

    <v-card border flat class="pa-4 mb-4" data-testid="my-data-export">
      <h2 class="text-h6 mb-2">{{ $t('privacy.my.exportTitle') }}</h2>
      <p class="text-body-2 mb-3">{{ $t('privacy.my.exportBody', { days: data?.export_days ?? 7 }) }}</p>
      <v-alert v-if="exportPending" type="info" variant="tonal" density="compact" class="mb-3" data-testid="export-pending">{{ $t('privacy.my.exportPending') }}</v-alert>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          color="primary"
          :prepend-icon="mdiDatabaseExportOutline"
          :loading="exporting"
          :disabled="exportPending || loading"
          data-testid="export-request"
          @click="requestExport"
        >
          {{ $t('privacy.my.exportButton') }}
        </v-btn>
        <v-btn variant="text" :prepend-icon="mdiRefresh" :loading="loading" data-testid="my-data-refresh" @click="load">{{ $t('privacy.action.refresh') }}</v-btn>
      </div>
    </v-card>

    <v-card border flat class="mb-4">
      <v-card-title class="text-h6">{{ $t('privacy.my.requestsTitle') }}</v-card-title>
      <p v-if="!loading && !requests.length" class="text-body-2 text-medium-emphasis px-4 pb-4" data-testid="my-data-empty">{{ $t('privacy.my.empty') }}</p>
      <PrivacyRequestList v-else-if="requests.length" :requests="requests" testid="my-requests" />
    </v-card>

    <v-card v-if="allowErasure" border flat class="pa-4 mb-4" data-testid="my-data-erasure">
      <h2 class="text-h6 mb-2">{{ $t('privacy.my.deletionTitle') }}</h2>
      <p class="text-body-2 mb-3">{{ $t('privacy.my.deletionBody') }}</p>
      <v-alert v-if="erasurePending" type="warning" variant="tonal" density="compact" data-testid="erasure-pending">{{ $t('privacy.my.deletionPending') }}</v-alert>
      <ErasureRequestForm v-else-if="showErasure" @requested="onErasureRequested" />
    </v-card>

    <v-card border flat class="pa-4" data-testid="my-data-consents">
      <h2 class="text-h6 mb-2">{{ $t('privacy.my.consentsTitle') }}</h2>
      <p v-if="!data?.consents.length" class="text-body-2 text-medium-emphasis">{{ $t('privacy.my.noConsents') }}</p>
      <ul v-else class="text-body-2 pl-4">
        <li v-for="c in data.consents" :key="c.id" data-testid="consent-row">
          {{ $t('privacy.my.consentLine') }}<span v-if="c.policy_version"> ({{ c.policy_version }})</span>:
          <a :href="c.policy_url" target="_blank" rel="noopener noreferrer">{{ c.policy_url }}</a>
          · <RelativeTime :value="c.accepted_at" />
        </li>
      </ul>
    </v-card>
  </div>
</template>
