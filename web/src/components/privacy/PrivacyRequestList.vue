<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Privacy requests (exports and deletion requests) as a table (FE-080): used by
 * the admin Privacy page, the per-user privacy actions and both "My data" pages.
 * A ready export shows its download link; the row actions come from the slot.
 */
import { mdiDownload } from '@mdi/js'
import RelativeTime from '@/components/RelativeTime.vue'
import { formatBytes } from '@/constants/validations'
import type { components } from '@/api/schema'

type PrivacyRequest = components['schemas']['PrivacyRequest']
withDefaults(defineProps<{ requests: PrivacyRequest[]; showUser?: boolean; testid?: string }>(), { showUser: false, testid: 'privacy-requests' })

const COLORS: Record<PrivacyRequest['status'], string> = {
  pending: 'warning',
  processing: 'info',
  ready: 'success',
  failed: 'error',
  expired: 'grey',
  completed: 'success',
  rejected: 'error',
}
</script>

<template>
  <v-table density="comfortable" :data-testid="testid">
    <thead>
      <tr>
        <th>{{ $t('privacy.col.type') }}</th>
        <th v-if="showUser">{{ $t('privacy.col.person') }}</th>
        <th>{{ $t('privacy.col.status') }}</th>
        <th>{{ $t('privacy.col.requested') }}</th>
        <th>{{ $t('privacy.col.expires') }}</th>
        <th class="text-right">{{ $t('privacy.col.actions') }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="r in requests" :key="r.id" data-testid="privacy-request-row" :data-status="r.status" :data-type="r.type">
        <td>
          <div>{{ $t(`privacy.type.${r.type}`) }}</div>
          <div v-if="r.mode" class="text-caption text-medium-emphasis">{{ $t(`privacy.mode.${r.mode}`) }}</div>
          <div v-if="r.reason" class="text-caption text-medium-emphasis" data-testid="privacy-request-reason">{{ r.reason }}</div>
        </td>
        <td v-if="showUser">
          <template v-if="r.user">
            <div>{{ r.user.name }}</div>
            <div class="text-caption text-medium-emphasis">{{ r.user.email }}</div>
          </template>
          <span v-else>—</span>
        </td>
        <td>
          <v-chip size="small" label :color="COLORS[r.status]" data-testid="privacy-request-status">{{ $t(`privacy.status.${r.status}`) }}</v-chip>
        </td>
        <td><RelativeTime :value="r.created_at" /></td>
        <td>
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
      </tr>
    </tbody>
  </v-table>
</template>
