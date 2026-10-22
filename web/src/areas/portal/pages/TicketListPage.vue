<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useDisplay } from 'vuetify'
import { useI18n } from 'vue-i18n'
import { mdiMagnify, mdiPlus, mdiTicketOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useRealtime } from '@/composables/useRealtime'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import type { ServerEvent } from '@/realtime/client'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import StatusChip from '@/components/StatusChip.vue'
import PriorityChip from '@/components/PriorityChip.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import TicketNumber from '@/components/TicketNumber.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import MarkdownView from '@/components/MarkdownView.vue'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import { CUSTOMER_STATUSES, type CustomerStatus, type CustomerTicketSummary, type PortalProject } from '../types'

/**
 * "My tickets" (FE-015): status filter in customer labels, search, cursor
 * "Load more", Mine/Organization toggle for org managers, live rows over the
 * `tickets` topic (FE-044) — no polling.
 */
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const { mdAndUp } = useDisplay()
const session = useSessionStore()
// The project column and filter only exist when the customer can see more than one project.
const items = ref<CustomerTicketSummary[]>([])
const projects = ref<PortalProject[]>([])
const seenProjects = computed(() => new Set(items.value.map((i) => i.project_id)))
const showProject = computed(() => projects.value.length > 1 || seenProjects.value.size > 1)
const cols = useTableColumns('portal-tickets', () => [
  { key: 'number', label: t('portal.projects.ref'), locked: true },
  ...(showProject.value ? [{ key: 'project', label: t('portal.projects.project'), lowPriority: true }] : []),
  { key: 'subject', label: t('portal.tickets.subject'), locked: true },
  ...(session.isOrgManager ? [{ key: 'requester', label: t('portal.tickets.requester'), lowPriority: true }] : []),
  { key: 'status', label: t('portal.tickets.status') },
  { key: 'priority', label: t('portal.tickets.priority'), lowPriority: true },
  { key: 'updated', label: t('portal.tickets.updated'), lowPriority: true },
])
const config = useConfigStore()

const q = (v: unknown) => (typeof v === 'string' ? v : '')
const project = ref<string | null>(typeof route.query.project === 'string' ? route.query.project : null)
const status = ref<CustomerStatus | null>((CUSTOMER_STATUSES as readonly string[]).includes(q(route.query.status)) ? (q(route.query.status) as CustomerStatus) : null)
const search = ref(q(route.query.q))
const scope = ref<'mine' | 'org'>(route.query.scope === 'mine' ? 'mine' : 'org')
const nextCursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref<string | null>(null)

const statusItems = computed(() => [
  { value: null, title: t('portal.tickets.allStatuses') },
  ...CUSTOMER_STATUSES.map((s) => ({ value: s, title: t(`statusCustomer.${s}`) })),
])
const projectItems = computed(() => [
  { value: null, title: t('portal.projects.allProjects') },
  ...projects.value.map((p) => ({ value: p.id, title: `${p.key} · ${p.name}` })),
])
const filtered = computed(() => !!status.value || !!project.value || !!search.value.trim())

function query(cursor?: string | null) {
  return {
    status: status.value ?? undefined,
    project: project.value ?? undefined,
    q: search.value.trim() || undefined,
    mine_only: session.isOrgManager ? scope.value === 'mine' : undefined,
    cursor: cursor ?? undefined,
    limit: 25,
  }
}

let seq = 0
async function load() {
  const my = ++seq
  loading.value = true
  error.value = null
  try {
    const page = await unwrap(api.GET('/portal/tickets', { params: { query: query() } }))
    if (my !== seq) return
    items.value = page.items
    nextCursor.value = page.next_cursor
  } catch (e) {
    if (my === seq) error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    if (my === seq) loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value) return
  loadingMore.value = true
  try {
    const page = await unwrap(api.GET('/portal/tickets', { params: { query: query(nextCursor.value) } }))
    const seen = new Set(items.value.map((i) => i.id))
    items.value.push(...page.items.filter((i) => !seen.has(i.id)))
    nextCursor.value = page.next_cursor
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loadingMore.value = false
  }
}

// Filters live in the URL so a reload or a shared link keeps them.
function syncUrl() {
  const query: Record<string, string> = {}
  if (status.value) query.status = status.value
  if (project.value) query.project = project.value
  if (search.value.trim()) query.q = search.value.trim()
  if (session.isOrgManager && scope.value === 'mine') query.scope = 'mine'
  void router.replace({ query })
}

let debounce: ReturnType<typeof setTimeout> | null = null
watch(search, () => {
  if (debounce) clearTimeout(debounce)
  debounce = setTimeout(() => {
    syncUrl()
    void load()
  }, 300)
})
watch([status, scope, project], () => {
  syncUrl()
  void load()
})
onBeforeUnmount(() => debounce && clearTimeout(debounce))

/** Patches or inserts a row from a realtime summary instead of refetching. */
function onEvent(ev: ServerEvent) {
  const tk = ev.data?.ticket as CustomerTicketSummary | undefined
  if (!tk?.id) return
  const i = items.value.findIndex((x) => x.id === tk.id)
  const matches = !status.value || tk.status === status.value
  if (i >= 0) {
    if (matches) items.value[i] = { ...items.value[i]!, ...tk }
    else items.value.splice(i, 1)
  } else if (matches && !search.value.trim() && ev.event === 'ticket.created') {
    items.value.unshift(tk)
  }
}
useRealtime(['tickets'], { onEvent, onResync: () => void load() })

async function loadProjects() {
  try {
    const list = await unwrap(api.GET('/portal/projects'))
    projects.value = Array.isArray(list) ? list : []
  } catch {
    projects.value = []
  }
}
void load()
void loadProjects()
</script>

<template>
  <v-container class="py-6" style="max-width: 1100px">
    <MarkdownView
      v-if="config.branding.portal_welcome_md"
      :source="config.branding.portal_welcome_md"
      class="mb-6"
      data-testid="portal-welcome"
    />
    <PageHeader :title="$t('nav.myTickets')">
      <template #actions>
        <v-btn to="/tickets/new" color="primary" :prepend-icon="mdiPlus" data-testid="list-new-ticket">{{ $t('nav.newTicket') }}</v-btn>
      </template>
    </PageHeader>

    <div class="d-flex flex-wrap ga-3 mb-4 align-center">
      <v-text-field
        v-model="search"
        :label="$t('app.search')"
        :prepend-inner-icon="mdiMagnify"
        density="compact"
        hide-details
        clearable
        style="min-width: 220px; max-width: 360px"
        data-testid="ticket-search"
        @click:clear="search = ''"
      />
      <AutocompleteSearch
        v-model="status"
        :items="statusItems"
        :label="$t('portal.tickets.status')"
        density="compact"
        hide-details
        style="min-width: 200px; max-width: 260px"
        data-testid="status-filter"
      />
      <AutocompleteSearch
        v-if="projects.length > 1"
        v-model="project"
        :items="projectItems"
        :label="$t('portal.projects.project')"
        density="compact"
        hide-details
        style="min-width: 200px; max-width: 260px"
        data-testid="project-filter"
      />
      <v-btn-toggle
        v-if="session.isOrgManager"
        v-model="scope"
        mandatory
        density="compact"
        variant="outlined"
        divided
        data-testid="scope-toggle"
      >
        <v-btn value="mine">{{ $t('portal.tickets.mine') }}</v-btn>
        <v-btn value="org">{{ $t('portal.tickets.organization') }}</v-btn>
      </v-btn-toggle>
    </div>

    <v-alert v-if="error" type="error" variant="tonal" class="mb-4" data-testid="list-error">
      {{ error }}
      <template #append><v-btn variant="text" @click="load">{{ $t('app.retry') }}</v-btn></template>
    </v-alert>

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-2" />

    <EmptyState
      v-if="!loading && !error && !items.length"
      :icon="mdiTicketOutline"
      :title="filtered ? $t('portal.tickets.noMatch') : $t('portal.tickets.empty')"
      :body="filtered ? '' : $t('portal.tickets.emptyHint')"
    >
      <v-btn v-if="!filtered" to="/tickets/new" color="primary" :prepend-icon="mdiPlus" data-testid="empty-new-ticket">{{ $t('nav.newTicket') }}</v-btn>
    </EmptyState>

    <template v-else-if="items.length">
      <ColumnsMenu v-if="mdAndUp" :cols="cols" />
      <v-table v-if="mdAndUp" data-testid="ticket-table" hover>
        <thead>
          <tr>
            <th v-if="cols.show('number')" scope="col">{{ $t('portal.projects.ref') }}</th>
            <th v-if="showProject && cols.show('project')" scope="col">{{ $t('portal.projects.project') }}</th>
            <th v-if="cols.show('subject')" scope="col">{{ $t('portal.tickets.subject') }}</th>
            <th v-if="session.isOrgManager && cols.show('requester')" scope="col">{{ $t('portal.tickets.requester') }}</th>
            <th v-if="cols.show('status')" scope="col">{{ $t('portal.tickets.status') }}</th>
            <th v-if="cols.show('priority')" scope="col">{{ $t('portal.tickets.priority') }}</th>
            <th v-if="cols.show('updated')" scope="col">{{ $t('portal.tickets.updated') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="tk in items" :key="tk.id" data-testid="ticket-row" class="fd-row" @click="router.push(`/tickets/${tk.ref || tk.id}`)">
            <td v-if="cols.show('number')"><TicketNumber :number="tk.number" :reftext="tk.ref" /></td>
            <td v-if="showProject && cols.show('project')"><ProjectChip :project="tk.project" /></td>
            <td v-if="cols.show('subject')"><router-link :to="`/tickets/${tk.ref || tk.id}`" class="text-high-emphasis" @click.stop>{{ tk.subject }}</router-link></td>
            <td v-if="session.isOrgManager && cols.show('requester')">{{ tk.requester.name }}</td>
            <td v-if="cols.show('status')"><StatusChip :status="tk.status" area="portal" /></td>
            <td v-if="cols.show('priority')"><PriorityChip :priority="tk.priority" /></td>
            <td v-if="cols.show('updated')"><RelativeTime :value="tk.updated_at" /></td>
          </tr>
        </tbody>
      </v-table>
      <div v-else class="d-flex flex-column ga-2" data-testid="ticket-cards">
        <v-card v-for="tk in items" :key="tk.id" :to="`/tickets/${tk.ref || tk.id}`" border flat data-testid="ticket-row">
          <v-card-item>
            <v-card-title class="text-body-1 text-wrap"><TicketNumber :number="tk.number" :reftext="tk.ref" /> {{ tk.subject }}</v-card-title>
            <v-card-subtitle><RelativeTime :value="tk.updated_at" /><span v-if="session.isOrgManager"> · {{ tk.requester.name }}</span></v-card-subtitle>
          </v-card-item>
          <v-card-text class="d-flex ga-2 pt-0">
            <ProjectChip v-if="showProject" :project="tk.project" />
            <StatusChip :status="tk.status" area="portal" />
            <PriorityChip :priority="tk.priority" />
          </v-card-text>
        </v-card>
      </div>
      <div v-if="nextCursor" class="text-center mt-4">
        <v-btn variant="outlined" :loading="loadingMore" data-testid="load-more" @click="loadMore">{{ $t('portal.tickets.loadMore') }}</v-btn>
      </div>
    </template>
  </v-container>
</template>

<style scoped>
.fd-row { cursor: pointer; }
</style>
