<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/admin/system (FE-029, admin): version, database, disk, OIDC, SMTP, realtime status. */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiCheckCircle, mdiAlertCircle, mdiMinusCircleOutline, mdiRefresh } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import { api, unwrap } from '@/api/client'
import { formatBytes } from '@/constants/validations'
import type { components } from '@/api/schema'

type Status = components['schemas']['SystemStatus']
const { t, locale } = useI18n()
const sys = ref<Status | null>(null)
const loading = ref(true)
const failed = ref(false)

async function load() {
  loading.value = true
  failed.value = false
  try {
    sys.value = await unwrap(api.GET('/admin/system'))
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

const bytes = (n?: number | null) => (n === null || n === undefined ? '—' : formatBytes(n))
function uptime(sec?: number) {
  if (sec === undefined) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const nf = new Intl.NumberFormat(locale.value)
  return t('agent.system.uptimeValue', { d: nf.format(d), h: nf.format(h), m: nf.format(m) })
}

type Light = { icon: string; color: string; text: string }
function oidc(a?: { configured?: boolean; discovery_ok?: boolean }): Light {
  if (!a?.configured) return { icon: mdiMinusCircleOutline, color: 'grey', text: t('agent.system.notConfigured') }
  return a.discovery_ok
    ? { icon: mdiCheckCircle, color: 'success', text: t('agent.system.ok') }
    : { icon: mdiAlertCircle, color: 'error', text: t('agent.system.discoveryFailed') }
}
const smtp = computed<Light>(() => {
  const s = sys.value?.smtp
  if (!s?.configured) return { icon: mdiMinusCircleOutline, color: 'grey', text: t('agent.system.notConfigured') }
  return s.ok === false
    ? { icon: mdiAlertCircle, color: 'error', text: t('agent.system.failing') }
    : { icon: mdiCheckCircle, color: 'success', text: t('agent.system.ok') }
})
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 1100px">
    <PageHeader :title="$t('agent.admin.nav.system')" :subtitle="$t('agent.system.subtitle')">
      <template #actions>
        <v-btn :prepend-icon="mdiRefresh" variant="tonal" :loading="loading" data-testid="system-refresh" @click="load">{{ $t('agent.common.refresh') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-progress-linear v-if="loading && !sys" indeterminate />
    <v-row v-if="sys" data-testid="system-status">
      <v-col cols="12" md="6">
        <v-card border flat :title="$t('agent.system.application')">
          <v-list density="compact">
            <v-list-item :title="$t('agent.system.version')" :subtitle="sys.version" data-testid="sys-version" />
            <v-list-item :title="$t('agent.system.commit')" :subtitle="sys.commit || '—'" />
            <v-list-item :title="$t('agent.system.uptime')" :subtitle="uptime(sys.uptime_seconds)" />
            <v-list-item :title="$t('agent.system.wsConnections')" :subtitle="String(sys.ws?.connections ?? '—')" />
          </v-list>
        </v-card>
      </v-col>
      <v-col cols="12" md="6">
        <v-card border flat :title="$t('agent.system.database')">
          <v-list density="compact">
            <v-list-item :title="$t('agent.system.engine')" :subtitle="sys.db.engine === 'postgres' ? 'PostgreSQL' : 'SQLite'" data-testid="sys-engine" />
            <v-list-item :title="$t('agent.system.schemaVersion')" :subtitle="String(sys.db.schema_version ?? '—')" />
            <v-list-item :title="$t('agent.system.dbSize')" :subtitle="bytes(sys.db.size_bytes)" />
            <v-list-item :title="$t('agent.system.diskUsed')" :subtitle="bytes(sys.disk?.used_bytes)" />
            <v-list-item :title="$t('agent.system.diskFree')" :subtitle="bytes(sys.disk?.free_bytes)" />
          </v-list>
        </v-card>
      </v-col>
      <v-col cols="12" md="6">
        <v-card border flat :title="$t('agent.system.sso')">
          <v-list density="compact">
            <v-list-item :title="$t('agent.system.staffOidc')" data-testid="sys-oidc-staff">
              <template #append><v-chip :color="oidc(sys.oidc?.staff).color" :prepend-icon="oidc(sys.oidc?.staff).icon" size="small" label>{{ oidc(sys.oidc?.staff).text }}</v-chip></template>
            </v-list-item>
            <v-list-item :title="$t('agent.system.customerOidc')">
              <template #append><v-chip :color="oidc(sys.oidc?.customer).color" :prepend-icon="oidc(sys.oidc?.customer).icon" size="small" label>{{ oidc(sys.oidc?.customer).text }}</v-chip></template>
            </v-list-item>
          </v-list>
        </v-card>
      </v-col>
      <v-col cols="12" md="6">
        <v-card border flat :title="$t('agent.system.smtp')">
          <v-list density="compact">
            <v-list-item :title="$t('agent.common.status')" data-testid="sys-smtp">
              <template #append><v-chip :color="smtp.color" :prepend-icon="smtp.icon" size="small" label>{{ smtp.text }}</v-chip></template>
            </v-list-item>
            <v-list-item v-if="sys.smtp?.last_error" :title="$t('agent.system.lastError')" :subtitle="sys.smtp.last_error" />
          </v-list>
          <v-card-actions>
            <v-btn to="/agent/admin/email" variant="text" color="primary" data-testid="sys-email-link">{{ $t('agent.system.openEmail') }}</v-btn>
          </v-card-actions>
        </v-card>
      </v-col>
      <v-col v-if="sys.queue" cols="12">
        <v-card border flat :title="$t('agent.admin.nav.queue')">
          <v-card-text>
            {{ $t('agent.system.queueSummary', { retrying: sys.queue.deferred.retrying, dead: sys.queue.deferred.dead }) }}
          </v-card-text>
          <v-card-actions><v-btn to="/agent/admin/queue" variant="text" color="primary">{{ $t('agent.system.openQueue') }}</v-btn></v-card-actions>
        </v-card>
      </v-col>
    </v-row>
  </v-container>
</template>
