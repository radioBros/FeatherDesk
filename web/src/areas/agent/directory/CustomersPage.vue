<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/customers (FE-026): search, cursor list, create dialog. */
import { onMounted, ref, watch } from 'vue'
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
import type { components, operations } from '@/api/schema'
import UserAvatar from '@/components/UserAvatar.vue'
import DataTable from '@/components/DataTable.vue'

type S = components['schemas']
const router = useRouter()
const q = ref('')
const sort = ref<NonNullable<CustomersQuery['sort']> | null>(null)
const dialog = ref(false)
const orgs = ref<S['Organization'][]>([])
type CustomersQuery = NonNullable<operations['listCustomers']['parameters']['query']>
const list = useCursorList((cursor) =>
  unwrap(api.GET('/agent/customers', { params: { query: { q: q.value.trim() || undefined, sort: sort.value ?? undefined, cursor: cursor ?? undefined, limit: 50 } } })),
)
watch(sort, () => void list.reload())
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
  { key: 'name', label: t('agent.common.name'), locked: true, sort: { asc: 'name', desc: '-name' } },
  { key: 'email', label: t('agent.common.email'), sort: { asc: 'email', desc: '-email' } },
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
      <DataTable
        :cols="cols"
        :items="list.items.value"
        :loading="list.loading.value"
        server-sort
        :sort="sort"
        :has-more="!!list.next.value"
        density="comfortable"
        hover
        :row-props="{ 'data-testid': 'customer-row' }"
        data-testid="customer-table"
        @update:sort="(v: string | null) => (sort = v as typeof sort)"
        @load-more="list.loadMore()"
        @click:row="(_e: unknown, { item }: { item: S['User'] }) => router.push(`/agent/customers/${item.id}`)"
      >
        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-2">
            <UserAvatar :name="item.name" :size="28" />
            <router-link :to="`/agent/customers/${item.id}`" class="text-decoration-none text-high-emphasis" @click.stop>{{ item.name }}</router-link>
          </div>
        </template>
        <template #[`item.email`]="{ item }">{{ item.email }}</template>
        <template #[`item.organization`]="{ item }">{{ orgName(item.organization_id) || '—' }}</template>
        <template #[`item.role`]="{ item }">{{ $t(`agent.customers.roles.${item.role === 'org_manager' ? 'org_manager' : 'requester'}`) }}</template>
        <template #[`item.status`]="{ item }"><v-chip size="small" label :color="item.status === 'active' ? 'success' : undefined">{{ $t(`agent.customers.statuses.${item.status}`) }}</v-chip></template>
      </DataTable>
      </template>
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

