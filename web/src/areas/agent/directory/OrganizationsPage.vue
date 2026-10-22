<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/organizations (FE-026): search, cursor list, create dialog. */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { mdiDomain, mdiMagnify, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import OrganizationForm from './OrganizationForm.vue'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import { api, unwrap } from '@/api/client'
import { useCursorList } from '../shared/useCursorList'
import type { components, operations } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const router = useRouter()
const q = ref('')
type OrgsQuery = NonNullable<operations['listOrganizations']['parameters']['query']>
const sort = ref<NonNullable<OrgsQuery['sort']> | null>(null)
const dialog = ref(false)
const list = useCursorList((cursor) =>
  unwrap(api.GET('/agent/organizations', { params: { query: { q: q.value.trim() || undefined, sort: sort.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } } })),
)
watch(sort, () => void list.reload())
let timer: ReturnType<typeof setTimeout> | undefined
function onSearch() {
  clearTimeout(timer)
  timer = setTimeout(() => void list.reload(), 300)
}
onMounted(() => void list.reload())
function onSaved(o: S['Organization']) {
  dialog.value = false
  void router.push(`/agent/organizations/${o.id}`)
}
const { t } = useI18n()
const cp = useConfigProjects()
const portalProjects = computed(() => cp.all.value.filter((p) => p.portal_visible && p.active))
const cols = useTableColumns('agent-organizations', () => [
  { key: 'name', label: t('agent.common.name'), locked: true, sort: { asc: 'name', desc: '-name' } },
  { key: 'domains', label: t('agent.organizations.domains') },
  { key: 'projects', label: t('projects.config.projects'), lowPriority: true },
  { key: 'members', label: t('agent.organizations.members'), lowPriority: true },
  { key: 'status', label: t('agent.common.status'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('nav.organizations')" :subtitle="$t('agent.organizations.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="org-new" @click="dialog = true">{{ $t('agent.organizations.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-text-field
      v-model="q"
      :prepend-inner-icon="mdiMagnify"
      :label="$t('agent.organizations.search')"
      clearable
      hide-details
      density="compact"
      class="mb-4"
      style="max-width: 420px"
      data-testid="org-search"
      @update:model-value="onSearch"
    />
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.organizations.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <DataTable
        :cols="cols"
        :items="list.items.value"
        :loading="list.loading.value"
        server-sort
        :sort="sort"
        :has-more="!!list.next.value"
        density="comfortable"
        hover
        :row-props="{ 'data-testid': 'org-row' }"
        data-testid="org-table"
        @update:sort="(v: string | null) => (sort = v as typeof sort)"
        @load-more="list.loadMore()"
        @click:row="(_e: unknown, { item }: { item: S['Organization'] }) => router.push(`/agent/organizations/${item.id}`)"
      >
        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-2">
            <SectionIcon section="customers" :icon="mdiDomain" :size="28" />
            <router-link :to="`/agent/organizations/${item.id}`" class="text-decoration-none text-high-emphasis" @click.stop>{{ item.name }}</router-link>
          </div>
        </template>
        <template #[`item.domains`]="{ item }">{{ (item.email_domains ?? []).join(', ') || '—' }}</template>
        <template #[`item.projects`]="{ item }"><ProjectCell :ids="item.project_ids ?? []" /></template>
        <template #[`item.members`]="{ item }">{{ item.member_count ?? '—' }}</template>
        <template #[`item.status`]="{ item }">
          <v-chip size="small" label :color="item.status === 'active' ? 'success' : undefined">
            {{ item.status === 'active' ? $t('agent.common.active') : $t('agent.organizations.archived') }}
          </v-chip>
        </template>
      </DataTable>
      </template>
    </v-card>
    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="$t('agent.organizations.new')">
        <v-card-text>
          <OrganizationForm v-if="dialog" :projects="portalProjects" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
