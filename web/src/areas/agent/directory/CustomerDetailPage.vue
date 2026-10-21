<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/customers/:id (FE-026): profile card, edit dialog, the customer's tickets. */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { mdiPencil } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import CustomerForm from './CustomerForm.vue'
import TicketMiniList from './TicketMiniList.vue'
import PrivacyActions from '../privacy/PrivacyActions.vue'
import UserMfaCard from './admin/UserMfaCard.vue'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import type { components } from '@/api/schema'

type S = components['schemas']
const route = useRoute()
const id = computed(() => String(route.params.id))
const customer = ref<S['User'] | null>(null)
const orgs = ref<S['Organization'][]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const dialog = ref(false)

async function load() {
  loading.value = true
  error.value = null
  try {
    customer.value = await unwrap(api.GET('/agent/customers/{id}', { params: { path: { id: id.value } } }))
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : String(e)
  } finally {
    loading.value = false
  }
}
async function loadOrgs() {
  try {
    orgs.value = (await unwrap(api.GET('/agent/organizations', { params: { query: { limit: 100 } } }))).items ?? []
  } catch {
    orgs.value = []
  }
}
const org = computed(() => orgs.value.find((o) => o.id === customer.value?.organization_id))
onMounted(() => {
  void load()
  void loadOrgs()
})
watch(id, () => void load())
function onSaved(c: S['User']) {
  customer.value = c
  dialog.value = false
}
</script>

<template>
  <v-container fluid class="pa-4">
    <v-progress-linear v-if="loading" indeterminate />
    <v-alert v-if="error" type="error" variant="tonal" data-testid="load-error">{{ error }}</v-alert>
    <template v-if="customer">
      <PageHeader :title="customer.name" :subtitle="customer.email">
        <template #actions>
          <v-btn variant="tonal" :prepend-icon="mdiPencil" data-testid="customer-edit" @click="dialog = true">{{ $t('agent.common.edit') }}</v-btn>
        </template>
      </PageHeader>
      <v-row>
        <v-col cols="12" md="4">
          <v-card border flat data-testid="customer-card">
            <v-card-item>
              <template #prepend><UserAvatar :name="customer.name" /></template>
              <v-card-title>{{ customer.name }}</v-card-title>
              <v-card-subtitle>{{ customer.email }}</v-card-subtitle>
            </v-card-item>
            <v-list density="compact">
              <v-list-item :title="$t('agent.customers.organization')">
                <template #subtitle>
                  <router-link v-if="org" :to="`/agent/organizations/${org.id}`">{{ org.name }}</router-link>
                  <span v-else>—</span>
                </template>
              </v-list-item>
              <v-list-item :title="$t('agent.customers.role')" :subtitle="$t(`agent.customers.roles.${customer.role === 'org_manager' ? 'org_manager' : 'requester'}`)" />
              <v-list-item :title="$t('agent.common.status')" :subtitle="$t(`agent.customers.statuses.${customer.status}`)" />
              <v-list-item :title="$t('agent.customers.locale')" :subtitle="customer.locale.toUpperCase()" />
            </v-list>
          </v-card>
          <PrivacyActions :user="customer" @erased="load" />
          <UserMfaCard :user-id="customer.id" />
        </v-col>
        <v-col cols="12" md="8">
          <v-card border flat :title="$t('agent.customers.tickets')">
            <TicketMiniList :requester-id="customer.id" />
          </v-card>
        </v-col>
      </v-row>
      <v-dialog v-model="dialog" max-width="560">
        <v-card :title="$t('agent.customers.edit')">
          <v-card-text>
            <CustomerForm v-if="dialog" :customer="customer" :organizations="orgs" @saved="onSaved" @cancel="dialog = false" />
          </v-card-text>
        </v-card>
      </v-dialog>
    </template>
  </v-container>
</template>
