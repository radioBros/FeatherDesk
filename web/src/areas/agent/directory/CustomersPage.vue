<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/customers (FE-026): search, cursor list, create dialog. */
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { mdiMagnify, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useI18n } from 'vue-i18n'
import ColumnsMenu from '@/components/ColumnsMenu.vue'
import { useTableColumns } from '@/composables/useTableColumns'
import CustomerForm from './CustomerForm.vue'
import { api, unwrap } from '@/api/client'
import { useCursorList } from '../shared/useCursorList'
import type { components } from '@/api/schema'
import UserAvatar from '@/components/UserAvatar.vue'
import ColumnCells from '@/components/ColumnCells.vue'

type S = components['schemas']
const router = useRouter()
const q = ref('')
const dialog = ref(false)
const orgs = ref<S['Organization'][]>([])
const list = useCursorList((cursor) =>
  unwrap(api.GET('/agent/customers', { params: { query: { q: q.value.trim() || undefined, cursor: cursor ?? undefined, limit: 50 } } })),
)
let timer: ReturnType<typeof setTimeout> | undefined
function onSearch() {
  clearTimeout(timer)
  timer = setTimeout(() => void list.reload(), 300)
}
async function loadOrgs() {
  try {
    orgs.value = (await unwrap(api.GET('/agent/organizations', { params: { query: { limit: 100 } } }))).items ?? []
  } catch {
    orgs.value = []
  }
}
const orgName = (id?: string | null) => (id ? orgs.value.find((o) => o.id === id)?.name ?? '' : '')
onMounted(() => {
  void list.reload()
  void loadOrgs()
})
function onSaved(c: S['User']) {
  dialog.value = false
  void router.push(`/agent/customers/${c.id}`)
}
const { t } = useI18n()
const cols = useTableColumns('agent-customers', () => [
  { key: 'name', label: t('agent.common.name'), locked: true },
  { key: 'email', label: t('agent.common.email') },
  { key: 'organization', label: t('agent.customers.organization'), lowPriority: true },
  { key: 'role', label: t('agent.customers.role'), lowPriority: true },
  { key: 'status', label: t('agent.common.status'), lowPriority: true },
])
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('nav.customers')" :subtitle="$t('agent.customers.subtitle')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="customer-new" @click="dialog = true">{{ $t('agent.customers.new') }}</v-btn>
      </template>
    </PageHeader>
    <v-text-field
      v-model="q"
      :prepend-inner-icon="mdiMagnify"
      :label="$t('agent.customers.search')"
      clearable
      hide-details
      density="compact"
      class="mb-4"
      style="max-width: 420px"
      data-testid="customer-search"
      @update:model-value="onSearch"
    />
    <v-alert v-if="list.error.value" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-card border flat>
      <v-progress-linear v-if="list.loading.value" indeterminate />
      <EmptyState v-if="!list.loading.value && !list.items.value.length" :title="$t('agent.customers.empty')" body="" />
      <template v-else>
      <ColumnsMenu :cols="cols" />
      <v-table density="comfortable" data-testid="customer-table">
        <thead>
          <tr>
            <ColumnCells :cols="cols" head>
              <th data-col="name" v-if="cols.show('name')">{{ $t('agent.common.name') }}</th>
              <th data-col="email" v-if="cols.show('email')">{{ $t('agent.common.email') }}</th>
              <th data-col="organization" v-if="cols.show('organization')">{{ $t('agent.customers.organization') }}</th>
              <th data-col="role" v-if="cols.show('role')">{{ $t('agent.customers.role') }}</th>
              <th data-col="status" v-if="cols.show('status')">{{ $t('agent.common.status') }}</th>
            </ColumnCells>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in list.items.value" :key="c.id" class="fd-row" data-testid="customer-row" @click="router.push(`/agent/customers/${c.id}`)">
            <ColumnCells :cols="cols">
              <td data-col="name" v-if="cols.show('name')">
                <div class="d-flex align-center ga-2">
                  <UserAvatar :name="c.name" :size="28" />
                  <router-link :to="`/agent/customers/${c.id}`" class="text-decoration-none text-high-emphasis" @click.stop>{{ c.name }}</router-link>
                </div>
              </td>
              <td data-col="email" v-if="cols.show('email')">{{ c.email }}</td>
              <td data-col="organization" v-if="cols.show('organization')">{{ orgName(c.organization_id) || '—' }}</td>
              <td data-col="role" v-if="cols.show('role')">{{ $t(`agent.customers.roles.${c.role === 'org_manager' ? 'org_manager' : 'requester'}`) }}</td>
              <td data-col="status" v-if="cols.show('status')"><v-chip size="small" label :color="c.status === 'active' ? 'success' : undefined">{{ $t(`agent.customers.statuses.${c.status}`) }}</v-chip></td>
            </ColumnCells>
          </tr>
        </tbody>
      </v-table>
      </template>
      <div v-if="list.next.value" class="pa-2 text-center">
        <v-btn variant="text" :loading="list.loading.value" data-testid="load-more" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="$t('agent.customers.new')">
        <v-card-text>
          <CustomerForm v-if="dialog" :organizations="orgs" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.fd-row { cursor: pointer; }
</style>
