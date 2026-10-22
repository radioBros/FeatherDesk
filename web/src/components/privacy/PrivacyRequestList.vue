<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Privacy requests (exports and deletion requests) as a table (FE-080): used by
 * the admin Privacy page, the per-user privacy actions and both "My data" pages.
 * A ready export shows its download link; the row actions come from the slot.
 */
import { mdiDownload } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import RelativeTime from '@/components/RelativeTime.vue'
import { formatBytes } from '@/constants/validations'
import type { components } from '@/api/schema'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import ColumnCells from '@/components/ColumnCells.vue'

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
</script>

<template>
  <div>
  <ColumnsMenu :cols="cols" />
  <v-table density="comfortable" :data-testid="testid">
    <thead>
      <tr>
        <ColumnCells :cols="cols" head>
          <th data-col="type" v-if="cols.show('type')">{{ $t('privacy.col.type') }}</th>
          <th data-col="person" v-if="showUser && cols.show('person')">{{ $t('privacy.col.person') }}</th>
          <th data-col="status" v-if="cols.show('status')">{{ $t('privacy.col.status') }}</th>
          <th data-col="requested" v-if="cols.show('requested')">{{ $t('privacy.col.requested') }}</th>
          <th data-col="expires" v-if="cols.show('expires')">{{ $t('privacy.col.expires') }}</th>
          <th class="text-right">{{ $t('privacy.col.actions') }}</th>
        </ColumnCells>
      </tr>
    </thead>
    <tbody>
      <tr v-for="r in requests" :key="r.id" data-testid="privacy-request-row" :data-status="r.status" :data-type="r.type">
        <ColumnCells :cols="cols">
          <td data-col="type" v-if="cols.show('type')">
            <div>{{ $t(`privacy.type.${r.type}`) }}</div>
            <div v-if="r.mode" class="text-caption text-medium-emphasis">{{ $t(`privacy.mode.${r.mode}`) }}</div>
            <div v-if="r.reason" class="text-caption text-medium-emphasis" data-testid="privacy-request-reason">{{ r.reason }}</div>
          </td>
          <td data-col="person" v-if="showUser && cols.show('person')">
            <template v-if="r.user">
              <div>{{ r.user.name }}</div>
              <div class="text-caption text-medium-emphasis">{{ r.user.email }}</div>
            </template>
            <span v-else>—</span>
          </td>
          <td data-col="status" v-if="cols.show('status')">
            <v-chip size="small" label :color="COLORS[r.status]" data-testid="privacy-request-status">{{ $t(`privacy.status.${r.status}`) }}</v-chip>
          </td>
          <td data-col="requested" v-if="cols.show('requested')"><RelativeTime :value="r.created_at" /></td>
          <td data-col="expires" v-if="cols.show('expires')">
            <template v-if="r.expires_at && r.status === 'ready'"><RelativeTime :value="r.expires_at" /></template>
            <span v-else>—</span>
          </td>
          <td class="text-right text-no-wrap">
            <v-btn
              v-if="r.status === 'ready' && r.download_url"
              :href="r.download_url"
              download
              variant="tonal"
              size="small"
              :prepend-icon="mdiDownload"
              data-testid="privacy-download"
            >
              {{ $t('privacy.action.download') }}<span v-if="r.file_size" class="ml-1 text-caption">({{ formatBytes(r.file_size) }})</span>
            </v-btn>
            <slot name="actions" :request="r" />
          </td>
        </ColumnCells>
      </tr>
    </tbody>
  </v-table>
  </div>
</template>
