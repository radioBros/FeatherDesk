<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * `/agent/admin/privacy` (FE-084/FE-086, D-19 §C, admin): every export and
 * deletion request with approve / reject / download, and the retention and
 * privacy settings with a dry-run preview.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiCheck, mdiClose, mdiRefresh } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import PrivacyRequestList from '@/components/privacy/PrivacyRequestList.vue'
import PrivacySettingsForm from './PrivacySettingsForm.vue'
import EraseForm from './EraseForm.vue'
import RejectForm from './RejectForm.vue'
import { api, unwrap } from '@/api/client'
import { useUiStore } from '@/stores/ui'
import { useCursorList } from '../shared/useCursorList'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()
const ui = useUiStore()
const tab = ref<'requests' | 'settings'>('requests')
const type = ref<S['PrivacyRequestType'] | null>(null)
const status = ref<S['PrivacyRequestStatus'] | null>(null)
const TYPES: S['PrivacyRequestType'][] = ['erasure', 'export']
const STATUSES: S['PrivacyRequestStatus'][] = ['pending', 'processing', 'ready', 'failed', 'expired', 'completed', 'rejected']

const list = useCursorList((cursor) =>
  unwrap(
    api.GET('/admin/privacy/requests', {
      params: { query: { type: type.value ?? undefined, status: status.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } },
    }),
  ),
)

const settings = ref<S['PrivacySettings'] | null>(null)
const settingsError = ref(false)
async function loadSettings() {
  settingsError.value = false
  try {
    settings.value = await unwrap(api.GET('/admin/privacy/settings'))
  } catch {
    settingsError.value = true
  }
}
onMounted(() => {
  void list.reload()
  void loadSettings()
})

const approving = ref<S['PrivacyRequest'] | null>(null)
const rejecting = ref<S['PrivacyRequest'] | null>(null)
function onErased(r: S['ErasureResult']) {
  approving.value = null
  ui.notify(t('privacy.erase.done', { messages: r.messages_redacted, attachments: r.attachments_deleted }), 'success')
  void list.reload()
}
function onRejected() {
  rejecting.value = null
  ui.notify(t('privacy.requests.rejected'), 'success')
  void list.reload()
}
function onSaved(s: S['PrivacySettings']) {
  settings.value = s
  ui.notify(t('privacy.settings.saved'), 'success')
}
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('privacy.nav.admin')" :subtitle="$t('privacy.admin.subtitle')" />
    <v-tabs v-model="tab" class="mb-4">
      <v-tab value="requests" data-testid="privacy-tab-requests">{{ $t('privacy.admin.tabRequests') }}</v-tab>
      <v-tab value="settings" data-testid="privacy-tab-settings">{{ $t('privacy.admin.tabSettings') }}</v-tab>
    </v-tabs>

    <div v-show="tab === 'requests'">
      <div class="d-flex flex-wrap ga-3 mb-4">
        <AutocompleteSearch
          v-model="type"
          :items="TYPES.map((v) => ({ value: v, title: $t(`privacy.type.${v}`) }))"
          :label="$t('privacy.filter.type')"
          clearable
          hide-details
          density="compact"
          style="max-width: 240px"
          data-testid="privacy-filter-type"
          @update:model-value="list.reload()"
        />
        <AutocompleteSearch
          v-model="status"
          :items="STATUSES.map((v) => ({ value: v, title: $t(`privacy.status.${v}`) }))"
          :label="$t('privacy.filter.status')"
          clearable
          hide-details
          density="compact"
          style="max-width: 240px"
          data-testid="privacy-filter-status"
          @update:model-value="list.reload()"
        />
        <v-btn variant="text" :prepend-icon="mdiRefresh" :loading="list.loading.value" @click="list.reload()">{{ $t('privacy.action.refresh') }}</v-btn>
      </div>
      <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('privacy.requests.loadFailed') }}</v-alert>
      <v-card border flat>
        <v-progress-linear v-if="list.loading.value" indeterminate />
        <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('privacy.requests.empty')" body="" />
        <PrivacyRequestList v-else :requests="list.items.value" show-user testid="privacy-admin-requests">
          <template #actions="{ request }">
            <template v-if="request.type === 'erasure' && request.status === 'pending'">
              <v-btn :icon="mdiCheck" variant="text" size="small" color="error" :aria-label="$t('privacy.action.approve')" data-testid="privacy-approve" @click="approving = request" />
              <v-btn :icon="mdiClose" variant="text" size="small" :aria-label="$t('privacy.action.reject')" data-testid="privacy-reject" @click="rejecting = request" />
            </template>
          </template>
        </PrivacyRequestList>
        <div v-if="list.next.value" class="pa-2 text-center">
          <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
        </div>
      </v-card>
    </div>

    <v-card v-show="tab === 'settings'" border flat class="pa-4">
      <v-alert v-if="settingsError" type="error" variant="tonal">{{ $t('agent.common.loadFailed') }}</v-alert>
      <v-progress-linear v-else-if="!settings" indeterminate />
      <PrivacySettingsForm v-else :settings="settings" @saved="onSaved" />
    </v-card>

    <v-dialog :model-value="!!approving" max-width="560" @update:model-value="(v) => !v && (approving = null)">
      <v-card :title="$t('privacy.approve.title')" :subtitle="approving?.user?.name">
        <v-card-text>
          <EraseForm v-if="approving" :request-id="approving.id" @erased="onErased" @cancel="approving = null" />
        </v-card-text>
      </v-card>
    </v-dialog>
    <v-dialog :model-value="!!rejecting" max-width="560" @update:model-value="(v) => !v && (rejecting = null)">
      <v-card :title="$t('privacy.reject.title')" :subtitle="rejecting?.user?.name">
        <v-card-text>
          <RejectForm v-if="rejecting" :request-id="rejecting.id" @rejected="onRejected" @cancel="rejecting = null" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
