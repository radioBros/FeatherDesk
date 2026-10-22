<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import PageHeader from '@/components/PageHeader.vue'
import MarkdownView from '@/components/MarkdownView.vue'
import { useProjectBranding } from '../projectBranding'
import NewTicketForm from '../forms/NewTicketForm.vue'
import type { Category, PortalProject, CustomFieldDef, CustomerTicket, UserRef } from '../types'

/** `/tickets/new` (FE-016): loads the form's reference data, then opens the created ticket. */
const router = useRouter()
const route = useRoute()
const { enter } = useProjectBranding()
const config = useConfigStore()
const session = useSessionStore()
const categories = ref<Category[]>([])
const fields = ref<CustomFieldDef[]>([])
const members = ref<UserRef[] | null>(null)
const loading = ref(true)

const projects = ref<PortalProject[]>([])
const projectId = ref<string | null>(null)

async function loadScoped() {
  // Categories and custom fields follow the chosen project; each source is optional for the form.
  const project = projectId.value ?? undefined
  const [cats, defs] = await Promise.allSettled([
    unwrap(api.GET('/portal/categories', { params: { query: { project } } })),
    unwrap(api.GET('/portal/custom-fields', { params: { query: { project } } })),
  ])
  categories.value = cats.status === 'fulfilled' ? (cats.value ?? []) : []
  fields.value = defs.status === 'fulfilled' ? (defs.value ?? []) : []
}

onMounted(async () => {
  const wanted = typeof route.query.project === 'string' ? route.query.project : ''
  const [team, projs] = await Promise.allSettled([
    session.isOrgManager ? unwrap(api.GET('/portal/organization/members')) : Promise.resolve(null),
    unwrap(api.GET('/portal/projects')),
  ])
  if (team.status === 'fulfilled') members.value = team.value
  if (projs.status === 'fulfilled') projects.value = projs.value ?? []
  const match = projects.value.find((p) => p.key.toLowerCase() === wanted.toLowerCase() || p.id === wanted)
  projectId.value = match?.id ?? (projects.value.length === 1 ? projects.value[0]!.id : null)
  // `/p/KEY` entry: its branding stays while the ticket is written.
  if (match) void enter(match.key)
  await loadScoped()
  loading.value = false
})
watch(projectId, (_, old) => {
  if (!loading.value && old !== undefined) void loadScoped()
})

function created(t: CustomerTicket) {
  void router.push(`/tickets/${t.ref || t.id}`)
}
</script>

<template>
  <v-container class="py-6" style="max-width: 860px">
    <PageHeader :title="$t('nav.newTicket')" />
    <MarkdownView
      v-if="config.branding.new_ticket_intro_md"
      :source="config.branding.new_ticket_intro_md"
      class="mb-4"
      data-testid="new-ticket-intro"
    />
    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />
    <v-card border flat class="pa-4">
      <NewTicketForm v-model:project="projectId" :projects="projects" :categories="categories" :fields="fields" :members="members" :self-id="session.me?.id" @created="created" />
    </v-card>
  </v-container>
</template>
