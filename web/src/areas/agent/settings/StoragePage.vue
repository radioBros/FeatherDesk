<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/storage (FE-059, admin, read-only): blob backend, endpoint,
 * bucket, usage and last check. Credentials are never part of the response
 * and nothing here renders them.
 */
import { onMounted, ref } from 'vue'
import { mdiCheckCircle, mdiAlertCircle, mdiRefresh } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import { api, unwrap } from '@/api/client'
import { formatBytes } from '@/constants/validations'
import type { components } from '@/api/schema'

const st = ref<components['schemas']['StorageStatus'] | null>(null)
const loading = ref(true)
const failed = ref(false)

async function load() {
  loading.value = true
  failed.value = false
  try {
    st.value = await unwrap(api.GET('/admin/storage'))
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 900px">
    <PageHeader :title="$t('agent.admin.nav.storage')" :subtitle="$t('agent.storage.subtitle')">
      <template #actions>
        <v-btn :prepend-icon="mdiRefresh" variant="tonal" :loading="loading" @click="load">{{ $t('agent.common.refresh') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-progress-linear v-if="loading && !st" indeterminate />
    <v-card v-if="st" border flat data-testid="storage-status">
      <v-list density="compact">
        <v-list-item :title="$t('agent.storage.backend')" :subtitle="st.backend === 's3' ? $t('agent.storage.s3') : $t('agent.storage.local')" data-testid="storage-backend" />
        <v-list-item v-if="st.backend === 's3'" :title="$t('agent.storage.endpoint')" :subtitle="st.endpoint || '—'" data-testid="storage-endpoint" />
        <v-list-item v-if="st.backend === 's3'" :title="$t('agent.storage.bucket')" :subtitle="st.bucket || '—'" data-testid="storage-bucket" />
        <v-list-item :title="$t('agent.storage.used')" :subtitle="st.used_bytes === null || st.used_bytes === undefined ? '—' : formatBytes(st.used_bytes)" />
        <v-list-item :title="$t('agent.storage.objects')" :subtitle="st.object_count === null || st.object_count === undefined ? '—' : String(st.object_count)" />
        <v-list-item :title="$t('agent.storage.check')">
          <template #append>
            <v-chip :color="st.ok ? 'success' : 'error'" :prepend-icon="st.ok ? mdiCheckCircle : mdiAlertCircle" size="small" label data-testid="storage-ok">
              {{ st.ok ? $t('agent.storage.ok') : $t('agent.storage.failing') }}
            </v-chip>
          </template>
        </v-list-item>
        <v-list-item v-if="st.last_error" :title="$t('agent.storage.lastError')" :subtitle="st.last_error" data-testid="storage-error" />
      </v-list>
    </v-card>
    <v-card border flat class="mt-4 pa-4">
      <div class="text-subtitle-1 mb-1">{{ $t('agent.storage.migrateTitle') }}</div>
      <p class="text-body-2 mb-2">{{ $t('agent.storage.migrateBody') }}</p>
      <code class="st-code">simpletickets blob-migrate --to s3://bucket/prefix</code>
    </v-card>
  </v-container>
</template>

<style scoped>
.st-code { display: inline-block; padding: 4px 8px; border-radius: 4px; background: rgba(var(--v-theme-on-surface), 0.06); font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
</style>
