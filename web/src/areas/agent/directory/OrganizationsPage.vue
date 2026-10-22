<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/organizations (FE-026): search, cursor list, create dialog. */
import { computed, onMounted, ref } from 'vue'
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
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const router = useRouter()
const q = ref('')
const dialog = ref(false)
const list = useCursorList((cursor) =>
  unwrap(api.GET('/agent/organizations', { params: { query: { q: q.value.trim() || undefined, cursor: cursor ?? undefined, limit: 50 } } })),
)
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
  { key: 'name', label: t('agent.common.name'), locked: true },
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
      <v-table density="comfortable" data-testid="org-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="name" v-if="cols.show('name')">{{ $t('agent.common.name') }}</th>
              <th data-col="domains" v-if="cols.show('domains')">{{ $t('agent.organizations.domains') }}</th>
              <th data-col="projects" v-if="cols.show('projects')">{{ $t('projects.config.projects') }}</th>
              <th data-col="members" v-if="cols.show('members')">{{ $t('agent.organizations.members') }}</th>
              <th data-col="status" v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in list.items.value" :key="o.id" class="fd-row" data-testid="org-row" @click="router.push(`/agent/organizations/${o.id}`)">
            <ColumnCells :cols="cols">
              <td data-col="name" v-if="cols.show('name')">
                <div class="d-flex align-center ga-2">
                  <SectionIcon section="customers" :icon="mdiDomain" :size="28" />
                  <router-link :to="`/agent/organizations/${o.id}`" class="text-decoration-none text-high-emphasis" @click.stop>{{ o.name }}</router-link>
                </div>
              </td>
              <td data-col="domains" v-if="cols.show('domains')">{{ (o.email_domains ?? []).join(', ') || '—' }}</td>
              <td data-col="projects" v-if="cols.show('projects')"><ProjectCell :ids="o.project_ids ?? []" /></td>
              <td data-col="members" v-if="cols.show('members')">{{ o.member_count ?? '—' }}</td>
              <td data-col="status" v-if="cols.show('status')">
                <v-chip size="small" label :color="o.status === 'active' ? 'success' : undefined">
                  {{ o.status === 'active' ? $t('agent.common.active') : $t('agent.organizations.archived') }}
                </v-chip>
              </td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
      <div v-if="list.next.value" class="pa-2 text-center">
        <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
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

<style scoped>
.fd-row { cursor: pointer; }
</style>
