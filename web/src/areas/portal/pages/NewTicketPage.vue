<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import PageHeader from '@/components/PageHeader.vue'
import MarkdownView from '@/components/MarkdownView.vue'
import NewTicketForm from '../forms/NewTicketForm.vue'
import type { Category, CustomFieldDef, CustomerTicket, UserRef } from '../types'

/** `/tickets/new` (FE-016): loads the form's reference data, then opens the created ticket. */
const router = useRouter()
const config = useConfigStore()
const session = useSessionStore()
const categories = ref<Category[]>([])
const fields = ref<CustomFieldDef[]>([])
const members = ref<UserRef[] | null>(null)
const loading = ref(true)

onMounted(async () => {
  // Each source is optional for the form: a failure leaves that part empty instead of blocking the page.
  const [cats, defs, team] = await Promise.allSettled([
    unwrap(api.GET('/portal/categories')),
    unwrap(api.GET('/portal/custom-fields')),
    session.isOrgManager ? unwrap(api.GET('/portal/organization/members')) : Promise.resolve(null),
  ])
  if (cats.status === 'fulfilled') categories.value = cats.value ?? []
  if (defs.status === 'fulfilled') fields.value = defs.value ?? []
  if (team.status === 'fulfilled') members.value = team.value
  loading.value = false
})

function created(t: CustomerTicket) {
  void router.push(`/tickets/${t.id}`)
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
      <NewTicketForm :categories="categories" :fields="fields" :members="members" :self-id="session.me?.id" @created="created" />
    </v-card>
  </v-container>
</template>
