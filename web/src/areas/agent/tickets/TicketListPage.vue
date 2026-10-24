<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * /agent/tickets (FE-021, FE-038, FE-054). The URL query is the only filter
 * state: changing a filter replaces the query, the query drives the API call,
 * so a reload restores the view. Results are never filtered client-side.
 * Live over the `tickets` topic: known rows are patched in place, anything
 * else triggers one debounced refetch.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useDisplay } from 'vuetify'
import { useI18n } from 'vue-i18n'
import { mdiBookmarkPlusOutline, mdiDeleteOutline, mdiFilterRemoveOutline, mdiLinkVariant, mdiPlus, mdiRefresh, mdiShareVariantOutline, mdiTuneVariant } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import TierChip from '@/components/TierChip.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import CustomFieldInput from '@/components/CustomFieldInput.vue'
import CustomFieldValue from '@/components/CustomFieldValue.vue'
import { apiFetch, api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import { useConfigStore } from '@/stores/config'
import { useRealtime } from '@/composables/useRealtime'
import { useConfirm } from '@/composables/useConfirm'
import { localized } from '@/utils/localized'
import { useLookupsStore } from '../shared/lookups'
import { useShortcuts } from '../shared/useShortcuts'
import { useAction } from '../shared/useAction'
import { useViewsStore } from './list/views'
import SaveViewForm from './list/SaveViewForm.vue'
import BulkActionForm from './list/BulkActionForm.vue'
import type { BulkResult } from './list/bulk'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { COLUMNS_PREFIX, useTableColumns, type ColumnDef } from '@/composables/useTableColumns'
import DataTable from '@/components/DataTable.vue'
import { DEFAULT_SORT, SORTS, cfFromString, cfToString, filtersFromQuery, toApiParams, toRouteQuery, type ListFilters } from './list/query'
import type { components } from '@/api/schema'
import type { ServerEvent } from '@/realtime/client'
import UserAvatar from '@/components/UserAvatar.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import { useProjectsStore } from '@/stores/projects'

type S = components['schemas']
type Row = S['TicketSummary'] & { links?: S['TicketLink'][] }

const route = useRoute()
const router = useRouter()
const { t, locale } = useI18n()
const { xs } = useDisplay()
const session = useSessionStore()
const config = useConfigStore()
const lookups = useLookupsStore()
const viewsStore = useViewsStore()
const projects = useProjectsStore()
const { confirm } = useConfirm()
const { run } = useAction()

const items = ref<Row[]>([])
const next = ref<string | null>(null)
const loading = ref(false)
const failed = ref(false)
const selected = ref<string[]>([])
const focused = ref(-1)
const saveDialog = ref(false)
const showCustom = ref(false)
const filtersExpanded = ref(false)
const qInput = ref('')
const loadSentinel = ref<HTMLElement | null>(null)
const canInfiniteScroll = typeof IntersectionObserver !== 'undefined'
let loadObserver: IntersectionObserver | null = null
let seq = 0

const filters = computed<ListFilters>(() => filtersFromQuery(route.query))
/** What the API gets: an explicit `?project=` wins over the switcher's saved choice. */
const apiFilters = computed<ListFilters>(() => ({ ...filters.value, project: filters.value.project ?? projects.filter }))
const activeView = computed(() => viewsStore.views.find((v) => v.id === filters.value.view_id) ?? null)

const activeDefs = computed(() => lookups.customFields.filter((d) => d.active))
const defByKey = (k: string) => lookups.customFields.find((d) => d.key === k)

// Column visibility, order and widths per user (useTableColumns). Custom-field columns are optional
// (hidden by default). Header sorting is the API's `sort` (only the columns it can order by).
const colDefs = computed<ColumnDef[]>(() => [
  { key: 'number', label: t('projects.ticket.ref'), locked: true, width: 110, sort: { asc: 'number', desc: '-number' } },
  { key: 'project', label: t('projects.ticket.project'), lowPriority: true },
  { key: 'subject', label: t('agent.list.columns.subject'), locked: true },
  { key: 'requester', label: t('agent.list.columns.requester') },
  { key: 'status', label: t('agent.list.columns.status') },
  { key: 'priority', label: t('agent.list.columns.priority'), lowPriority: true, sort: { asc: 'priority', desc: '-priority' } },
  { key: 'tier', label: t('agent.list.columns.tier'), lowPriority: true },
  { key: 'team', label: t('agent.list.columns.team'), lowPriority: true },
  { key: 'assignee', label: t('agent.list.columns.assignee'), lowPriority: true },
  ...activeDefs.value.map((d) => ({ key: `cf.${d.key}`, label: localized(d.label, locale.value) || d.key, defaultHidden: true })),
  { key: 'updated_at', label: t('agent.list.columns.updated'), lowPriority: true, sort: { asc: 'updated_at', desc: '-updated_at' } },
  { key: 'due', label: t('agent.list.columns.due'), lowPriority: true, sort: { asc: 'due', desc: '-due' } },
])
// The old custom-field column picker stored its keys under one global key: carry them over once.
try {
  const old = localStorage.getItem('fd.v1.agent.columns')
  if (old) {
    const target = `${COLUMNS_PREFIX}${session.me?.id ?? 'anon'}.agent-tickets`
    const keys = JSON.parse(old)
    if (Array.isArray(keys) && !localStorage.getItem(target)) {
      const o: Record<string, boolean> = {}
      for (const k of keys) if (typeof k === 'string') o[`cf.${k}`] = true
      localStorage.setItem(target, JSON.stringify(o))
    }
    localStorage.removeItem('fd.v1.agent.columns')
  }
} catch {
  /* ignore */
}
const cols = useTableColumns('agent-tickets', colDefs)
const cfColumns = computed(() => activeDefs.value.map((d) => d.key).filter((k) => cols.show(`cf.${k}`)))

const table = ref<{ scrollToIndex: (index: number) => void }>()

const title = computed(() => {
  if (activeView.value) return activeView.value.name
  const v = filters.value.view
  if (v === 'tier' && filters.value.tier) return config.tierLabel(filters.value.tier)
  return v ? t(`agent.layout.view.${v}`) : t('agent.list.title')
})

// ---- loading -------------------------------------------------------------

async function fetchPage(cursor: string | null, limit?: number) {
  const mine = ++seq
  loading.value = true
  failed.value = false
  try {
    const page = await apiFetch<S['TicketPage']>('/agent/tickets?' + toApiParams(apiFilters.value, cursor, limit).toString())
    if (mine !== seq) return
    items.value = cursor ? [...items.value, ...(page.items ?? [])] : page.items ?? []
    next.value = page.next_cursor ?? null
    if (!cursor) {
      selected.value = selected.value.filter((id) => items.value.some((r) => r.id === id))
      focused.value = Math.min(focused.value, items.value.length - 1)
    }
  } catch {
    if (mine === seq) failed.value = true
  } finally {
    if (mine === seq) loading.value = false
  }
}
const reload = () => fetchPage(null)
/** A live refetch keeps the rows already scrolled in (up to the API's 200), so the list does not jump back to its first page. */
const refresh = () => fetchPage(null, Math.min(200, Math.max(50, items.value.length)))
const loadMore = () => (next.value ? fetchPage(next.value) : undefined)

// A saved view in the URL (`?view_id=` alone) is expanded into its filter once.
let appliedView: string | null = null
async function onQuery() {
  const f = filters.value
  qInput.value = f.q ?? ''
  const onlyView = f.view_id && Object.keys(route.query).every((k) => k === 'view_id')
  if (f.view_id && onlyView && appliedView !== f.view_id) {
    appliedView = f.view_id
    try {
      const view = await unwrap(api.GET('/agent/views/{id}', { params: { path: { id: f.view_id } } }))
      const filter = Object.fromEntries(
        Object.entries(view.filter ?? {}).filter(([, v]) => typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean').map(([k, v]) => [k, String(v)]),
      )
      if (Object.keys(filter).length) {
        await router.replace({ query: { ...filter, view_id: f.view_id } })
        return
      }
    } catch {
      /* unknown view: show the plain list */
    }
  }
  await reload()
}
watch(() => route.fullPath, () => void onQuery())
let projectsReady = false
watch(() => projects.currentId, () => projectsReady && void reload())

function bindLoadObserver() {
  loadObserver?.disconnect()
  loadObserver = null
  if (!xs.value || !canInfiniteScroll || !loadSentinel.value) return
  loadObserver = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting) && next.value && !loading.value) loadMore()
    },
    { rootMargin: '160px' },
  )
  loadObserver.observe(loadSentinel.value)
}

onMounted(() => {
  void lookups.ensure('teams', 'categories', 'staff', 'customFields')
  if (!viewsStore.loaded) void viewsStore.load()
  // The saved switcher project must be known before the first query (one request, already filtered).
  void projects.load().then(() => {
    projectsReady = true
    return onQuery()
  })
  bindLoadObserver()
})
watch([loadSentinel, next, xs], () => bindLoadObserver())

function setFilters(patch: Partial<ListFilters>) {
  const f = { ...filters.value, ...patch }
  // Editing the filters of a saved view keeps its id only while nothing changed.
  const q = toRouteQuery({ ...f, view_id: undefined })
  void router.replace({ query: q })
}
function setCf(key: string, v: unknown) {
  const cf = { ...filters.value.cf }
  const s = cfToString(v)
  if (s) cf[key] = s
  else delete cf[key]
  setFilters({ cf })
}
function clearFilters() {
  void router.replace({ query: filters.value.view ? { view: filters.value.view, ...(filters.value.tier ? { tier: filters.value.tier } : {}) } : {} })
}
const hasFilters = computed(() => {
  const f = filters.value
  return !!(f.status.length || f.priority.length || f.team_id || f.assignee || f.category_id || f.project || f.tag || f.q || f.breached || Object.keys(f.cf).length)
})
/** Active list filters on phone (excludes search and sort). */
const activeFilterCount = computed(() => {
  const f = filters.value
  let n = f.status.length + f.priority.length
  if (f.tier) n++
  if (f.team_id) n++
  if (f.assignee) n++
  if (f.category_id) n++
  if (f.tag) n++
  if (f.breached) n++
  n += Object.keys(f.cf).length
  return n
})
const showProjectColumn = computed(() => cols.show('project'))

// ---- realtime --------------------------------------------------------------

let refetchTimer: ReturnType<typeof setTimeout> | null = null
function scheduleRefetch() {
  if (refetchTimer) return
  refetchTimer = setTimeout(() => {
    refetchTimer = null
    void refresh()
  }, 1000)
}
onBeforeUnmount(() => {
  if (refetchTimer) clearTimeout(refetchTimer)
  loadObserver?.disconnect()
})
function onEvent(ev: ServerEvent) {
  const tk = (ev.data?.ticket ?? null) as Row | null
  const pid = apiFilters.value.project
  // With a project selected by id, other projects' events never touch the list (keys are matched by the API refetch).
  if (pid && tk?.project_id && /^[0-9a-f-]{36}$/i.test(pid) && tk.project_id !== pid) return
  if (tk?.id) {
    const i = items.value.findIndex((r) => r.id === tk.id)
    if (i >= 0) {
      items.value.splice(i, 1, { ...items.value[i]!, ...tk })
      return
    }
  }
  if (tk?.id || ev.event === 'ticket.created') scheduleRefetch()
}
// One project selected (switcher or ?project=<id|key>): the server-filtered `project:<id>` topic
// (PRJ-023) instead of every ticket event; all projects: `tickets`.
const liveTopics = computed(() => {
  const f = apiFilters.value.project
  const p = f ? projects.projects.find((x) => x.id === f || x.key === f) : undefined
  return p ? [`project:${p.id}`] : ['tickets']
})
useRealtime(liveTopics, { onEvent, onResync: () => void refresh() })

// ---- rows, selection, shortcuts ---------------------------------------------

function open(row: Row) {
  void router.push(`/agent/tickets/${row.ref ?? row.number}`)
}
function toggleSelect(row: Row) {
  selected.value = selected.value.includes(row.id) ? selected.value.filter((id) => id !== row.id) : [...selected.value, row.id]
}
function move(d: number) {
  if (!items.value.length) return
  focused.value = Math.max(0, Math.min(items.value.length - 1, focused.value + d))
  // Rows out of view are not rendered: bring the focused one in.
  table.value?.scrollToIndex(focused.value)
}
useShortcuts({
  j: () => move(1),
  k: () => move(-1),
  Enter: () => {
    const row = items.value[focused.value]
    if (row) open(row)
  },
  x: () => {
    const row = items.value[focused.value]
    if (row) toggleSelect(row)
  },
})
const rowProps = ({ index }: { index: number }) => ({
  class: index === focused.value ? 'fd-row--focused' : '',
  'data-testid': 'ticket-row',
})

const selectedTickets = computed(() => items.value.filter((r) => selected.value.includes(r.id)))
function onBulkDone(results: BulkResult[]) {
  const ok = results.filter((r) => r.ok).map((r) => r.id)
  selected.value = selected.value.filter((id) => !ok.includes(id))
  void reload()
}

function due(row: Row): string | null {
  const ds = [row.first_response_due_at, row.resolution_due_at].filter((d): d is string => !!d).sort()
  return ds[0] ?? null
}
const hasParent = (r: Row) => !!r.links?.some((l) => l.type === 'parent_of' && l.to_ticket_id === r.id)
const hasChildren = (r: Row) => !!r.links?.some((l) => l.type === 'parent_of' && l.from_ticket_id === r.id)

// ---- filter options -------------------------------------------------------------

const STATUSES: S['TicketStatus'][] = ['new', 'open', 'pending', 'on_hold', 'resolved', 'closed']
const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']
const statusItems = computed(() => STATUSES.map((s) => ({ value: s, title: t(`status.${s}`) })))
const priorityItems = computed(() => PRIORITIES.map((p) => ({ value: p, title: t(`priority.${p}`) })))
const tierItems = computed(() => [1, 2, 3].map((n) => ({ value: String(n), title: config.tierLabel(n) })))
const teamItems = computed(() => [{ value: 'none', title: t('agent.list.filters.triage') }, ...lookups.teams.map((tm) => ({ value: tm.id, title: tm.name }))])
const assigneeItems = computed(() => [
  { value: 'me', title: t('agent.list.filters.assigneeMe') },
  { value: 'none', title: t('agent.list.filters.assigneeNone') },
  ...lookups.staff.filter((s) => s.id !== session.me?.id).map((s) => ({ value: s.id, title: s.name })),
])
const categoryItems = computed(() => lookups.categories.map((c) => ({ value: c.id, title: c.name })))
const sortItems = computed(() => SORTS.map((s) => ({ value: s.value, title: t(`agent.list.sort.${s.key}`) })))

async function onViewSaved(v: S['SavedView']) {
  saveDialog.value = false
  viewsStore.add(v)
  appliedView = v.id
  await router.replace({ query: { ...route.query, view_id: v.id } })
}
async function deleteView() {
  const v = activeView.value
  if (!v || !(await confirm({ body: t('agent.common.confirmDelete', { name: v.name }), color: 'error' }))) return
  const ok = await run(async () => (await viewsStore.remove(v.id), true), t('agent.views.deleted'))
  if (ok !== undefined) await router.replace({ query: toRouteQuery({ ...filters.value, view_id: undefined }) })
}
const currentFilter = computed(() => {
  const q = toRouteQuery({ ...filters.value, view_id: undefined })
  return Object.fromEntries(Object.entries(q).map(([k, v]) => [k, String(v)]))
})
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="title">
      <template #actions>
        <v-btn v-if="activeView" variant="text" :prepend-icon="mdiDeleteOutline" data-testid="view-delete" @click="deleteView">
          {{ $t('agent.views.delete') }}
        </v-btn>
        <v-btn variant="text" :prepend-icon="mdiBookmarkPlusOutline" data-testid="view-save-open" @click="saveDialog = true">
          {{ $t('agent.views.save') }}
        </v-btn>
        <v-btn variant="text" :icon="mdiRefresh" :aria-label="$t('agent.common.refresh')" @click="reload" />
        <v-btn color="primary" :prepend-icon="mdiPlus" to="/agent/tickets/new">{{ $t('nav.newTicket') }}</v-btn>
      </template>
    </PageHeader>

    <v-card border flat class="mb-3 pa-3" data-testid="list-filters">
      <div
        v-if="xs"
        class="d-flex ga-2 align-center"
      >
        <v-text-field
          v-model="qInput"
          :label="$t('agent.list.filters.q')"
          density="compact"
          hide-details
          clearable
          class="fd-filters-xs-search"
          data-testid="filter-q"
          @keydown.enter="setFilters({ q: qInput || undefined })"
          @click:clear="setFilters({ q: undefined })"
        />
        <v-btn
          variant="outlined"
          :prepend-icon="mdiTuneVariant"
          data-testid="filter-toggle"
          @click="filtersExpanded = !filtersExpanded"
        >
          {{ $t('agent.list.filters.toggle') }}
          <v-badge
            v-if="activeFilterCount"
            :content="activeFilterCount"
            inline
            color="primary"
          />
        </v-btn>
      </div>
      <v-expand-transition>
        <div v-show="!xs || filtersExpanded">
          <div class="d-flex flex-wrap ga-2 align-center fd-filters" :class="{ 'mt-2': xs }">
            <v-text-field
              v-if="!xs"
              v-model="qInput"
              :label="$t('agent.list.filters.q')"
              density="compact"
              hide-details
              clearable
              style="min-width: 200px"
              data-testid="filter-q"
              @keydown.enter="setFilters({ q: qInput || undefined })"
              @click:clear="setFilters({ q: undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.status"
              :items="statusItems"
              :label="$t('agent.list.filters.status')"
              multiple
              compact-multiple
              density="compact"
              hide-details
              data-testid="filter-status"
              @update:model-value="(v: string[]) => setFilters({ status: v })"
            />
            <AutocompleteSearch
              :model-value="filters.priority"
              :items="priorityItems"
              :label="$t('agent.list.filters.priority')"
              multiple
              compact-multiple
              hide-search
              density="compact"
              hide-details
              data-testid="filter-priority"
              @update:model-value="(v: string[]) => setFilters({ priority: v })"
            />
            <AutocompleteSearch
              :model-value="filters.tier ?? null"
              :items="tierItems"
              :label="$t('agent.list.filters.tier')"
              clearable
              hide-search
              density="compact"
              hide-details
              data-testid="filter-tier"
              @update:model-value="(v: string | null) => setFilters({ tier: v ?? undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.team_id ?? null"
              :items="teamItems"
              :label="$t('agent.list.filters.team')"
              clearable
              density="compact"
              hide-details
              data-testid="filter-team"
              @update:model-value="(v: string | null) => setFilters({ team_id: v ?? undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.assignee ?? null"
              :items="assigneeItems"
              :label="$t('agent.list.filters.assignee')"
              clearable
              density="compact"
              hide-details
              data-testid="filter-assignee"
              @update:model-value="(v: string | null) => setFilters({ assignee: v ?? undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.category_id ?? null"
              :items="categoryItems"
              :label="$t('agent.list.filters.category')"
              clearable
              density="compact"
              hide-details
              data-testid="filter-category"
              @update:model-value="(v: string | null) => setFilters({ category_id: v ?? undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.tag ?? null"
              :items="lookups.tags.map((tg) => tg.name)"
              :label="$t('agent.list.filters.tag')"
              clearable
              density="compact"
              hide-details
              data-testid="filter-tag"
              @focus="lookups.ensure('tags')"
              @update:model-value="(v: string | null) => setFilters({ tag: v ?? undefined })"
            />
            <v-checkbox
              :model-value="!!filters.breached"
              :label="$t('agent.list.filters.breached')"
              density="compact"
              hide-details
              data-testid="filter-breached"
              @update:model-value="(v: boolean | null) => setFilters({ breached: v ? true : undefined })"
            />
            <AutocompleteSearch
              :model-value="filters.sort"
              :items="sortItems"
              :label="$t('agent.list.filters.sort')"
              density="compact"
              hide-details
              data-testid="filter-sort"
              @update:model-value="(v: string) => setFilters({ sort: v })"
            />
          </div>
          <div class="d-flex flex-wrap ga-2 align-center mt-2">
            <v-btn v-if="activeDefs.length" variant="text" size="small" data-testid="filter-custom-toggle" @click="showCustom = !showCustom">
              {{ $t('agent.list.filters.more') }}
              <v-badge v-if="Object.keys(filters.cf).length" :content="Object.keys(filters.cf).length" inline color="primary" />
            </v-btn>
            <ColumnsMenu :cols="cols" :bar="false" />
            <v-spacer />
            <v-btn v-if="hasFilters" variant="text" size="small" :prepend-icon="mdiFilterRemoveOutline" data-testid="filter-clear" @click="clearFilters">
              {{ $t('agent.common.clearFilters') }}
            </v-btn>
          </div>
          <div v-if="showCustom || Object.keys(filters.cf).length" class="fd-cf-grid mt-2" data-testid="cf-filters">
            <CustomFieldInput
              v-for="d in activeDefs"
              :key="d.id"
              :def="{ ...d, required: false }"
              :model-value="cfFromString(d.type, filters.cf[d.key])"
              density="compact"
              no-rules
              @update:model-value="(v: unknown) => setCf(d.key, v)"
            />
          </div>
        </div>
      </v-expand-transition>
    </v-card>

    <v-card v-if="selected.length && !xs" border flat class="mb-3 pa-3" data-testid="bulk-bar">
      <BulkActionForm :tickets="selectedTickets" :staff="lookups.staff" :teams="lookups.teams" :me-id="session.me?.id ?? null" @done="onBulkDone" />
    </v-card>

    <v-alert v-if="failed" type="error" variant="tonal" class="mb-3" data-testid="list-error">{{ $t('agent.common.loadFailed') }}</v-alert>

    <v-card border flat>
      <DataTable
        v-if="!xs"
        ref="table"
        v-model="selected"
        :cols="cols"
        :items="items"
        :loading="loading"
        server-sort
        :sort="filters.sort"
        :has-more="!!next"
        selectable
        density="comfortable"
        hover
        :row-props="rowProps"
        data-testid="ticket-table"
        @update:sort="(s: string | null) => setFilters({ sort: s ?? DEFAULT_SORT })"
        @load-more="loadMore"
        @click:row="(_e: unknown, { item }: { item: Row }) => open(item)"
      >
        <template #[`item.number`]="{ item }">
          <TicketNumber :number="item.number" :reftext="item.ref" />
        </template>
        <template #[`item.project`]="{ item }"><ProjectChip :project="item.project" /></template>
        <template #[`item.subject`]="{ item }">
          <span>{{ item.subject }}</span>
          <v-chip v-if="hasParent(item)" size="x-small" class="ml-1" label :prepend-icon="mdiLinkVariant">{{ $t('agent.list.child') }}</v-chip>
          <v-chip v-if="hasChildren(item)" size="x-small" class="ml-1" label :prepend-icon="mdiLinkVariant">{{ $t('agent.list.parent') }}</v-chip>
          <v-chip v-for="tg in item.tags ?? []" :key="tg" size="x-small" class="ml-1" variant="outlined">{{ tg }}</v-chip>
        </template>
        <template #[`item.requester`]="{ item }">
          <div class="d-flex align-center ga-2"><UserAvatar :name="item.requester?.name" :size="24" /><span>{{ item.requester?.name }}</span></div>
        </template>
        <template #[`item.status`]="{ item }"><StatusChip :status="item.status" /></template>
        <template #[`item.priority`]="{ item }"><PriorityChip :priority="item.priority" /></template>
        <template #[`item.tier`]="{ item }"><TierChip :tier="item.tier" /></template>
        <template #[`item.team`]="{ item }">{{ item.team_id ? lookups.teamName(item.team_id) : $t('agent.list.filters.triage') }}</template>
        <template #[`item.assignee`]="{ item }">
          <div v-if="item.assignee" class="d-flex align-center ga-2"><UserAvatar :name="item.assignee.name" :size="24" /><span>{{ item.assignee.name }}</span></div>
          <span v-else class="text-medium-emphasis">{{ $t('agent.list.filters.assigneeNone') }}</span>
        </template>
        <template v-for="k in cfColumns" :key="k" #[`item.cf.${k}`]="{ item }">
          <CustomFieldValue v-if="defByKey(k)" :def="defByKey(k)!" :value="item.custom_fields?.[k]" />
        </template>
        <template #[`item.updated_at`]="{ item }"><RelativeTime :value="item.updated_at" /></template>
        <template #[`item.due`]="{ item }">
          <v-chip v-if="item.sla_breached" size="small" color="error" variant="tonal" label data-testid="sla-breached">{{ $t('agent.list.breached') }}</v-chip>
          <RelativeTime v-else-if="due(item)" :value="due(item)!" />
          <span v-else class="text-medium-emphasis">—</span>
        </template>
        <template #no-data>
          <EmptyState :title="$t('agent.list.empty')" body="" />
        </template>
      </DataTable>
      <div
        v-else
        class="fd-ticket-cards pa-3"
        data-testid="ticket-table"
      >
        <v-progress-linear
          v-if="loading && !items.length"
          indeterminate
          color="primary"
          class="mb-2"
        />
        <EmptyState
          v-else-if="!loading && !items.length"
          :title="$t('agent.list.empty')"
          body=""
        />
        <template v-else>
          <v-card
            v-for="item in items"
            :key="item.id"
            border
            flat
            class="fd-ticket-card"
            data-testid="ticket-row"
            @click="open(item)"
          >
            <div class="fd-ticket-card__inner">
              <div class="d-flex align-center ga-2 fd-ticket-card__meta">
                <TicketNumber :number="item.number" :reftext="item.ref" />
                <ProjectChip
                  v-if="showProjectColumn"
                  :project="item.project"
                />
                <v-spacer />
                <RelativeTime
                  :value="item.updated_at"
                  class="text-caption text-medium-emphasis"
                />
              </div>
              <div class="fd-ticket-card__subject">
                {{ item.subject }}
                <v-chip v-if="hasParent(item)" size="x-small" class="ml-1" label :prepend-icon="mdiLinkVariant">{{ $t('agent.list.child') }}</v-chip>
                <v-chip v-if="hasChildren(item)" size="x-small" class="ml-1" label :prepend-icon="mdiLinkVariant">{{ $t('agent.list.parent') }}</v-chip>
                <v-chip v-for="tg in item.tags ?? []" :key="tg" size="x-small" class="ml-1" variant="outlined">{{ tg }}</v-chip>
              </div>
              <div class="d-flex flex-wrap align-center ga-2 fd-ticket-card__foot">
                <StatusChip :status="item.status" />
                <PriorityChip :priority="item.priority" />
                <span class="text-body-2">{{ item.requester?.name }}</span>
                <div
                  v-if="item.assignee"
                  class="d-flex align-center ga-1"
                >
                  <UserAvatar :name="item.assignee.name" :size="24" />
                  <span class="text-body-2">{{ item.assignee.name }}</span>
                </div>
                <span
                  v-else
                  class="text-body-2 text-medium-emphasis"
                >{{ $t('agent.list.filters.assigneeNone') }}</span>
              </div>
            </div>
          </v-card>
          <div
            v-if="next"
            ref="loadSentinel"
            class="fd-load-sentinel"
            aria-hidden="true"
          />
          <div
            v-if="next && !canInfiniteScroll"
            class="text-center pt-2"
          >
            <v-btn
              variant="outlined"
              :loading="loading"
              data-testid="load-more"
              @click="loadMore"
            >
              {{ $t('agent.common.loadMore') }}
            </v-btn>
          </div>
          <v-progress-linear
            v-else-if="loading && items.length"
            indeterminate
            color="primary"
            class="mt-2"
          />
        </template>
      </div>
    </v-card>

    <v-dialog v-model="saveDialog" max-width="460">
      <v-card :title="$t('agent.views.save')" :prepend-icon="mdiShareVariantOutline">
        <v-card-text>
          <SaveViewForm v-if="saveDialog" :filter="currentFilter" @saved="onViewSaved" @cancel="saveDialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.fd-filters > * { flex: 1 1 160px; max-width: 240px; }
.fd-filters-xs-search { flex: 1 1 auto; min-width: 0; }
.fd-cf-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 8px; align-items: start; }
.fd-ticket-cards { display: flex; flex-direction: column; gap: 8px; }
.fd-ticket-card { cursor: pointer; }
.fd-ticket-card__inner { padding: 12px 16px; min-height: 40px; }
.fd-ticket-card__subject {
  font-weight: 600;
  margin-top: 4px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.fd-ticket-card__foot { margin-top: 8px; }
.fd-load-sentinel { height: 1px; }
:deep(.fd-row--focused) { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: -2px; }
@media (max-width: 599.98px) {
  .fd-filters > * { flex: 1 1 100%; max-width: none; }
}
</style>
