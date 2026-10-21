<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/reports (FE-043): volume, response/resolution times, SLA, CSAT and
 * breakdowns per tier and team for a date range. Agents get their own stats;
 * the API scopes the numbers. Charts are hand-written SVG.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatCard from '@/components/StatCard.vue'
import { mdiCheckCircleOutline, mdiLockOutline, mdiReplay, mdiTicketOutline } from '@mdi/js'
import ReportsFilterForm from './ReportsFilterForm.vue'
import GroupedBarChart from './charts/GroupedBarChart.vue'
import RatioBar from './charts/RatioBar.vue'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { useLookupsStore } from '../shared/lookups'
import { formatMinutes, type ReportFilter } from './format'
import type { components } from '@/api/schema'

type Report = components['schemas']['Report']
const { t, locale } = useI18n()
const config = useConfigStore()
const session = useSessionStore()
const lookups = useLookupsStore()

const isoDay = (d: Date) => d.toISOString().slice(0, 10)
const today = new Date()
const initial: ReportFilter = {
  from: isoDay(new Date(today.getTime() - 29 * 86400000)),
  to: isoDay(today),
  team_id: null,
  calendar_mode: null,
}
const report = ref<Report | null>(null)
const loading = ref(false)
const failed = ref<string | null>(null)

async function apply(f: ReportFilter) {
  loading.value = true
  failed.value = null
  try {
    report.value = await unwrap(
      api.GET('/agent/reports/summary', {
        params: {
          query: {
            from: `${f.from}T00:00:00Z`,
            to: `${f.to}T23:59:59Z`,
            team_id: f.team_id ?? undefined,
            calendar_mode: f.calendar_mode ?? undefined,
          },
        },
      }),
    )
  } catch (e) {
    failed.value = e instanceof ApiProblem ? e.userMessage : t('agent.common.loadFailed')
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  void apply(initial)
  if (session.isSupervisor) void lookups.ensure('teams')
})

const nf = computed(() => new Intl.NumberFormat(locale.value))
const n = (v?: number | null) => (v === null || v === undefined ? '—' : nf.value.format(v))
const dur = (v?: number | null) => formatMinutes(v, t)
const avg = (v?: number | null) =>
  v === null || v === undefined ? '—' : new Intl.NumberFormat(locale.value, { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(v)

const totals = computed(() => {
  const x = report.value?.totals ?? {}
  return [
    { key: 'created', value: x.created, icon: mdiTicketOutline, section: 'tickets' as const },
    { key: 'resolved', value: x.resolved, icon: mdiCheckCircleOutline, section: 'privacy' as const },
    { key: 'closed', value: x.closed, icon: mdiLockOutline, section: 'system' as const },
    { key: 'reopened', value: x.reopened, icon: mdiReplay, section: 'notifications' as const },
  ]
})
const series = computed(() => [
  { label: t('agent.reports.created'), color: 'primary' },
  { label: t('agent.reports.resolved'), color: 'success' },
])
const tierGroups = computed(() =>
  (report.value?.by_tier ?? []).map((r) => ({ label: config.tierLabel(r.tier ?? 1), values: [r.created ?? 0, r.resolved ?? 0] })),
)
const teamGroups = computed(() =>
  (report.value?.by_team ?? []).map((r) => ({ label: r.name || t('agent.reports.triage'), values: [r.created ?? 0, r.resolved ?? 0] })),
)
const sla = computed(() => report.value?.sla)
const slaParts = computed(() => [
  { label: t('agent.reports.slaMet'), value: sla.value?.met ?? 0, color: 'success' },
  { label: t('agent.reports.slaBreached'), value: sla.value?.breached ?? 0, color: 'error' },
])
const isEmpty = computed(() => !!report.value && !Object.values(report.value.totals ?? {}).some((v) => (v ?? 0) > 0))
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('nav.reports')" :subtitle="session.isSupervisor ? $t('agent.reports.subtitleAll') : $t('agent.reports.subtitleOwn')" />
    <v-card border flat class="pa-3 mb-4">
      <ReportsFilterForm :initial="initial" :teams="lookups.teams" :show-team="session.isSupervisor" @apply="apply" />
    </v-card>

    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="reports-error">{{ failed }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate class="mb-4" />

    <template v-if="report && !failed">
      <EmptyState v-if="isEmpty" :title="$t('agent.reports.empty')" body="" />
      <v-row density="compact" data-testid="reports-totals">
        <v-col v-for="x in totals" :key="x.key" cols="6" md="3">
          <StatCard :section="x.section" :icon="x.icon" :label="$t(`agent.reports.${x.key}`)">
            <div class="text-h5" :data-testid="`reports-total-${x.key}`">{{ n(x.value) }}</div>
          </StatCard>
        </v-col>
      </v-row>

      <v-row density="compact" class="mt-2">
        <v-col cols="12" md="4">
          <v-card border flat class="pa-3 h-100">
            <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.firstResponse') }}</div>
            <div data-testid="reports-frt">{{ $t('agent.reports.median') }}: <strong>{{ dur(report.first_response_minutes?.median) }}</strong></div>
            <div>{{ $t('agent.reports.p90') }}: <strong>{{ dur(report.first_response_minutes?.p90) }}</strong></div>
          </v-card>
        </v-col>
        <v-col cols="12" md="4">
          <v-card border flat class="pa-3 h-100">
            <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.resolution') }}</div>
            <div data-testid="reports-resolution">{{ $t('agent.reports.median') }}: <strong>{{ dur(report.resolution_minutes?.median) }}</strong></div>
            <div>{{ $t('agent.reports.p90') }}: <strong>{{ dur(report.resolution_minutes?.p90) }}</strong></div>
          </v-card>
        </v-col>
        <v-col cols="12" md="4">
          <v-card border flat class="pa-3 h-100">
            <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.csat') }}</div>
            <div data-testid="reports-csat">
              <strong>{{ avg(report.csat?.average) }}</strong> / 5
              <span class="text-medium-emphasis">· {{ $t('agent.reports.ratings', { n: report.csat?.count ?? 0 }) }}</span>
            </div>
          </v-card>
        </v-col>
      </v-row>

      <v-card v-if="sla" border flat class="pa-3 mt-2">
        <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.sla') }}</div>
        <RatioBar :parts="slaParts" :label="$t('agent.reports.sla')" />
      </v-card>

      <v-row density="compact" class="mt-2">
        <v-col v-if="tierGroups.length" cols="12" md="6">
          <v-card border flat class="pa-3 h-100">
            <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.byTier') }}</div>
            <GroupedBarChart :groups="tierGroups" :series="series" :label="$t('agent.reports.byTier')" />
            <v-table density="compact" class="mt-2" data-testid="reports-tier-table">
              <thead><tr><th>{{ $t('tier.label') }}</th><th>{{ $t('agent.reports.created') }}</th><th>{{ $t('agent.reports.resolved') }}</th><th>{{ $t('agent.reports.escalatedIn') }}</th></tr></thead>
              <tbody>
                <tr v-for="r in report.by_tier ?? []" :key="r.tier">
                  <td>{{ config.tierLabel(r.tier ?? 1) }}</td><td>{{ n(r.created) }}</td><td>{{ n(r.resolved) }}</td><td>{{ n(r.escalated_in) }}</td>
                </tr>
              </tbody>
            </v-table>
          </v-card>
        </v-col>
        <v-col v-if="teamGroups.length" cols="12" md="6">
          <v-card border flat class="pa-3 h-100">
            <div class="text-subtitle-2 mb-2">{{ $t('agent.reports.byTeam') }}</div>
            <GroupedBarChart :groups="teamGroups" :series="series" :label="$t('agent.reports.byTeam')" />
            <v-table density="compact" class="mt-2" data-testid="reports-team-table">
              <thead><tr><th>{{ $t('agent.common.team') }}</th><th>{{ $t('agent.reports.created') }}</th><th>{{ $t('agent.reports.resolved') }}</th></tr></thead>
              <tbody>
                <tr v-for="r in report.by_team ?? []" :key="r.team_id ?? 'triage'">
                  <td>{{ r.name || $t('agent.reports.triage') }}</td><td>{{ n(r.created) }}</td><td>{{ n(r.resolved) }}</td>
                </tr>
              </tbody>
            </v-table>
          </v-card>
        </v-col>
      </v-row>
    </template>
  </v-container>
</template>
