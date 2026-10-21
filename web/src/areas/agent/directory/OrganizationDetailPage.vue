<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/organizations/:id (FE-026): details, members (customers of the org), tickets, edit + add member. */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { mdiAccountPlus, mdiPencil } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import OrganizationForm from './OrganizationForm.vue'
import ProjectCell from '../shared/ProjectCell.vue'
import { useConfigProjects } from '../shared/useConfigProjects'
import CustomerForm from './CustomerForm.vue'
import TicketMiniList from './TicketMiniList.vue'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useCursorList } from '../shared/useCursorList'
import type { components } from '@/api/schema'

type S = components['schemas']
const route = useRoute()
const id = computed(() => String(route.params.id))
const org = ref<S['Organization'] | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const cp = useConfigProjects()
const portalProjects = computed(() => cp.all.value.filter((p) => p.portal_visible && p.active))
const editDialog = ref(false)
const memberDialog = ref(false)
const members = useCursorList((cursor) =>
  unwrap(api.GET('/agent/customers', { params: { query: { organization_id: id.value, cursor: cursor ?? undefined, limit: 50 } } })),
)

async function load() {
  loading.value = true
  error.value = null
  try {
    org.value = await unwrap(api.GET('/agent/organizations/{id}', { params: { path: { id: id.value } } }))
    void members.reload()
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : String(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())
watch(id, () => void load())
function onSaved(o: S['Organization']) {
  org.value = o
  editDialog.value = false
}
function onMemberSaved() {
  memberDialog.value = false
  void members.reload()
}
</script>

<template>
  <v-container fluid class="pa-4">
    <v-progress-linear v-if="loading" indeterminate />
    <v-alert v-if="error" type="error" variant="tonal" data-testid="load-error">{{ error }}</v-alert>
    <template v-if="org">
      <PageHeader :title="org.name" :subtitle="(org.email_domains ?? []).join(', ')">
        <template #actions>
          <v-btn variant="tonal" :prepend-icon="mdiPencil" data-testid="org-edit" @click="editDialog = true">{{ $t('agent.common.edit') }}</v-btn>
        </template>
      </PageHeader>
      <div class="mb-3 d-flex align-center ga-2" data-testid="org-projects-line">
        <span class="text-caption text-medium-emphasis">{{ $t('projects.config.projects') }}</span>
        <ProjectCell :ids="org.project_ids ?? []" />
      </div>
      <v-alert v-if="org.notes" variant="tonal" density="compact" class="mb-4" style="white-space: pre-line">{{ org.notes }}</v-alert>
      <v-row>
        <v-col cols="12" md="5">
          <v-card border flat :title="$t('agent.organizations.members')">
            <template #append>
              <v-btn size="small" variant="text" :prepend-icon="mdiAccountPlus" data-testid="org-add-member" @click="memberDialog = true">
                {{ $t('agent.common.add') }}
              </v-btn>
            </template>
            <v-progress-linear v-if="members.loading.value" indeterminate />
            <EmptyState v-if="!members.loading.value && !members.items.value.length" :title="$t('agent.organizations.noMembers')" body="" />
            <!-- Links to the members: they keep their link role (FE-032). -->
            <v-list v-else density="compact" role="none" data-testid="org-members">
              <v-list-item
                v-for="m in members.items.value"
                :key="m.id"
                :to="`/agent/customers/${m.id}`"
                :title="m.name"
                :subtitle="m.email"
              >
                <template #append>
                  <v-chip size="x-small" label>{{ $t(`agent.customers.roles.${m.role === 'org_manager' ? 'org_manager' : 'requester'}`) }}</v-chip>
                </template>
              </v-list-item>
            </v-list>
            <div v-if="members.next.value" class="pa-2 text-center">
              <v-btn variant="text" @click="members.loadMore()">{{ $t('agent.common.loadMore') }}</v-btn>
            </div>
          </v-card>
        </v-col>
        <v-col cols="12" md="7">
          <v-card border flat :title="$t('agent.organizations.tickets')">
            <TicketMiniList :organization-id="org.id" />
          </v-card>
        </v-col>
      </v-row>
      <v-dialog v-model="editDialog" max-width="560">
        <v-card :title="$t('agent.organizations.edit')">
          <v-card-text>
            <OrganizationForm v-if="editDialog" :organization="org" :projects="portalProjects" @saved="onSaved" @cancel="editDialog = false" />
          </v-card-text>
        </v-card>
      </v-dialog>
      <v-dialog v-model="memberDialog" max-width="560">
        <v-card :title="$t('agent.customers.new')">
          <v-card-text>
            <CustomerForm v-if="memberDialog" :organizations="[org]" :organization-id="org.id" @saved="onMemberSaved" @cancel="memberDialog = false" />
          </v-card-text>
        </v-card>
      </v-dialog>
    </template>
  </v-container>
</template>
