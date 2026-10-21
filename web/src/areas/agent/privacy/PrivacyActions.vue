<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Per-user privacy actions (FE-087, D-19 §C) for the customer and staff detail
 * views: export the person's data (optionally with internal notes), erase it,
 * and the person's recent requests with their downloads. Admins only — for
 * anyone else it renders nothing and requests nothing.
 */
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDatabaseExportOutline, mdiAccountRemoveOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useAction } from '../shared/useAction'
import PrivacyRequestList from '@/components/privacy/PrivacyRequestList.vue'
import EraseForm from './EraseForm.vue'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ user: { id: string; name: string } }>()
const emit = defineEmits<{ erased: [S['ErasureResult']] }>()
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const { busy, run } = useAction()
const requests = ref<S['PrivacyRequest'][]>([])
const includeInternal = ref(false)
const eraseOpen = ref(false)

async function load() {
  if (!session.isAdmin) return
  try {
    const page = await unwrap(api.GET('/admin/privacy/requests', { params: { query: { user_id: props.user.id, limit: 10 } } }))
    requests.value = page.items
  } catch {
    requests.value = []
  }
}
onMounted(load)
watch(() => props.user.id, load)

async function exportData() {
  const ok = await run(async () => {
    await unwrap(api.POST('/admin/privacy/users/{id}/export', { params: { path: { id: props.user.id } }, body: { include_internal: includeInternal.value } }))
    return true
  }, t('privacy.export.queued'))
  if (ok) await load()
}
async function onErased(r: S['ErasureResult']) {
  eraseOpen.value = false
  ui.notify(t('privacy.erase.done', { messages: r.messages_redacted, attachments: r.attachments_deleted }), 'success')
  emit('erased', r)
  await load()
}
</script>

<template>
  <v-card v-if="session.isAdmin" border flat :title="$t('privacy.actions.title')" class="mt-4" data-testid="privacy-actions">
    <v-card-text>
      <v-switch v-model="includeInternal" :label="$t('privacy.export.includeInternal')" color="primary" inset hide-details density="compact" data-testid="privacy-include-internal" />
      <div class="d-flex flex-wrap ga-2 mt-2">
        <v-btn variant="tonal" :prepend-icon="mdiDatabaseExportOutline" :loading="busy" data-testid="privacy-export" @click="exportData">{{ $t('privacy.action.export') }}</v-btn>
        <v-btn variant="tonal" color="error" :prepend-icon="mdiAccountRemoveOutline" data-testid="privacy-erase" @click="eraseOpen = true">{{ $t('privacy.action.erase') }}</v-btn>
      </div>
      <h3 class="text-subtitle-2 mt-4">{{ $t('privacy.actions.recent') }}</h3>
      <p v-if="!requests.length" class="text-body-2 text-medium-emphasis" data-testid="privacy-actions-empty">{{ $t('privacy.actions.none') }}</p>
      <PrivacyRequestList v-else :requests="requests" testid="privacy-user-requests" />
    </v-card-text>
    <v-dialog v-model="eraseOpen" max-width="560">
      <v-card :title="$t('privacy.erase.title')" :subtitle="user.name">
        <v-card-text>
          <EraseForm v-if="eraseOpen" :user-id="user.id" @erased="onErased" @cancel="eraseOpen = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-card>
</template>
