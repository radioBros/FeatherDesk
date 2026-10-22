<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Privacy requests (exports and deletion requests) as a table (FE-080): used by
 * the admin Privacy page, the per-user privacy actions and both "My data" pages.
 * A ready export shows its download link; the row actions come from the slot.
 * Rows keep the order they are given (no header sorting: the parents may hold
 * only one page of a cursor list).
 */
import { mdiDownload } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import RelativeTime from '@/components/RelativeTime.vue'
import { formatBytes } from '@/constants/validations'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'

type PrivacyRequest = components['schemas']['PrivacyRequest']
const props = withDefaults(defineProps<{ requests: PrivacyRequest[]; showUser?: boolean; testid?: string }>(), { showUser: false, testid: 'privacy-requests' })

const { t } = useI18n()

const COLORS: Record<PrivacyRequest['status'], string> = {
  pending: 'warning',
  processing: 'info',
  ready: 'success',
  failed: 'error',
  expired: 'grey',
  completed: 'success',
  rejected: 'error',
}

const cols = useTableColumns('privacy-requests', () => [
  { key: 'type', label: t('privacy.col.type'), locked: true },
  ...(props.showUser ? [{ key: 'person', label: t('privacy.col.person'), lowPriority: true }] : []),
  { key: 'status', label: t('privacy.col.status') },
  { key: 'requested', label: t('privacy.col.requested'), lowPriority: true },
  { key: 'expires', label: t('privacy.col.expires'), lowPriority: true },
])
const rowProps = ({ item }: { item: PrivacyRequest }) => ({ 'data-testid': 'privacy-request-row', 'data-status': item.status, 'data-type': item.type })
</script>

<template>
  <div>
  <ColumnsMenu :cols="cols" />
  <DataTable :cols="cols" :items="requests" server-sort actions density="comfortable" :row-props="rowProps" :data-testid="testid">
    <template #[`item.type`]="{ item }">
      <div>{{ $t(`privacy.type.${item.type}`) }}</div>
      <div v-if="item.mode" class="text-caption text-medium-emphasis">{{ $t(`privacy.mode.${item.mode}`) }}</div>
      <div v-if="item.reason" class="text-caption text-medium-emphasis" data-testid="privacy-request-reason">{{ item.reason }}</div>
    </template>
    <template #[`item.person`]="{ item }">
      <template v-if="item.user">
        <div>{{ item.user.name }}</div>
        <div class="text-caption text-medium-emphasis">{{ item.user.email }}</div>
      </template>
      <span v-else>—</span>
    </template>
    <template #[`item.status`]="{ item }">
      <v-chip size="small" label :color="COLORS[item.status as PrivacyRequest['status']]" data-testid="privacy-request-status">{{ $t(`privacy.status.${item.status}`) }}</v-chip>
    </template>
    <template #[`item.requested`]="{ item }"><RelativeTime :value="item.created_at" /></template>
    <template #[`item.expires`]="{ item }">
      <template v-if="item.expires_at && item.status === 'ready'"><RelativeTime :value="item.expires_at" /></template>
      <span v-else>—</span>
    </template>
    <template #[`item.actions`]="{ item }">
      <span class="text-no-wrap">
        <v-btn
          v-if="item.status === 'ready' && item.download_url"
          :href="item.download_url"
          download
          variant="tonal"
          size="small"
          :prepend-icon="mdiDownload"
          data-testid="privacy-download"
        >
          {{ $t('privacy.action.download') }}<span v-if="item.file_size" class="ml-1 text-caption">({{ formatBytes(item.file_size) }})</span>
        </v-btn>
        <slot name="actions" :request="item" />
      </span>
    </template>
  </DataTable>
  </div>
</template>
