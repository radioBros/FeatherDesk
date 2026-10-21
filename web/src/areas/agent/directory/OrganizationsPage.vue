<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/organizations (FE-026): search, cursor list, create dialog. */
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { mdiDomain, mdiMagnify, mdiPlus } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import OrganizationForm from './OrganizationForm.vue'
import { api, unwrap } from '@/api/client'
import { useCursorList } from '../shared/useCursorList'
import type { components } from '@/api/schema'
import SectionIcon from '@/components/SectionIcon.vue'

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
      <v-table v-else density="comfortable" data-testid="org-table">
        <thead>
          <tr>
            <th>{{ $t('agent.common.name') }}</th>
            <th>{{ $t('agent.organizations.domains') }}</th>
            <th>{{ $t('agent.organizations.members') }}</th>
            <th>{{ $t('agent.common.status') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="o in list.items.value" :key="o.id" class="st-row" data-testid="org-row" @click="router.push(`/agent/organizations/${o.id}`)">
            <td>
              <div class="d-flex align-center ga-2">
                <SectionIcon section="customers" :icon="mdiDomain" :size="28" />
                <router-link :to="`/agent/organizations/${o.id}`" class="text-decoration-none text-high-emphasis" @click.stop>{{ o.name }}</router-link>
              </div>
            </td>
            <td>{{ (o.email_domains ?? []).join(', ') || '—' }}</td>
            <td>{{ o.member_count ?? '—' }}</td>
            <td>
              <v-chip size="small" label :color="o.status === 'active' ? 'success' : undefined">
                {{ o.status === 'active' ? $t('agent.common.active') : $t('agent.organizations.archived') }}
              </v-chip>
            </td>
          </tr>
        </tbody>
      </v-table>
      <div v-if="list.next.value" class="pa-2 text-center">
        <v-btn variant="text" :loading="list.loading.value" @click="list.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
      </div>
    </v-card>
    <v-dialog v-model="dialog" max-width="560">
      <v-card :title="$t('agent.organizations.new')">
        <v-card-text>
          <OrganizationForm v-if="dialog" @saved="onSaved" @cancel="dialog = false" />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.st-row { cursor: pointer; }
</style>
