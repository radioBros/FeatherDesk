<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/tickets/new — create a ticket on behalf of a customer (FE-025). */
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageHeader from '@/components/PageHeader.vue'
import TicketCreateForm from './detail/TicketCreateForm.vue'
import { useLookupsStore } from '../shared/lookups'
import { api, unwrap } from '@/api/client'
import { useProjectsStore } from '@/stores/projects'
import type { components } from '@/api/schema'

type S = components['schemas']
const router = useRouter()
const route = useRoute()
const lookups = useLookupsStore()
const projects = useProjectsStore()
const requester = ref<S['User'] | null>(null)
const ready = ref(false)

onMounted(async () => {
  const id = typeof route.query.requester_id === 'string' ? route.query.requester_id : null
  await Promise.all([
    projects.load(),
    lookups.ensure('categories', 'teams', 'customFields'),
    id ? unwrap(api.GET('/agent/customers/{id}', { params: { path: { id } } })).then((u) => (requester.value = u)).catch(() => null) : null,
  ])
  ready.value = true
})

function onCreated(t: S['Ticket']) {
  void router.push(`/agent/tickets/${t.ref ?? t.number}`)
}
</script>

<template>
  <v-container class="pa-4" style="max-width: 960px">
    <PageHeader :title="$t('nav.newTicket')" :subtitle="$t('agent.newTicket.subtitle')" />
    <v-card border flat class="pa-4">
      <v-progress-linear v-if="!ready" indeterminate />
      <TicketCreateForm
        v-else
        :categories="lookups.categories"
        :teams="lookups.teams"
        :custom-fields="lookups.customFields"
        :requester="requester"
        :projects="projects.active"
        :default-project-id="projects.currentId"
        @created="onCreated"
      />
    </v-card>
  </v-container>
</template>
