<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Create a ticket on behalf of a customer (FE-025): pick an existing
 * customer, or "new customer" (name + email → `requester_email` +
 * `requester_name`, find-or-create on the server). One Idempotency-Key per
 * draft (FE-045). Reference lists come in as props; customer search runs only
 * while typing.
 */
import { computed, reactive, ref, watch } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap, newIdempotencyKey } from '@/api/client'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import AttachmentUploader from '@/components/AttachmentUploader.vue'
import CustomFieldInput from '@/components/CustomFieldInput.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import { dropFieldValues, emptyFieldValue, fieldApplies } from '@/utils/customFields'
import type { AgentProject } from '@/stores/projects'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = withDefaults(
  defineProps<{ categories?: S['Category'][]; teams?: S['Team'][]; customFields?: S['CustomFieldDef'][]; requester?: S['User'] | null; projects?: AgentProject[]; defaultProjectId?: string | null }>(),
  { categories: () => [], teams: () => [], customFields: () => [], requester: null, projects: () => [], defaultProjectId: null },
)
const emit = defineEmits<{ created: [S['Ticket']] }>()
const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']

const newCustomer = ref(false)
const model = reactive({
  requester: props.requester as S['User'] | null,
  requester_name: '',
  requester_email: '',
  project_id: (props.defaultProjectId ?? (props.projects.length === 1 ? props.projects[0]!.id : null)) as string | null,
  subject: '',
  category_id: null as string | null,
  priority: 'normal' as S['Priority'],
  team_id: null as string | null,
  body: '',
  attachments: [] as string[],
  custom: {} as S['CustomFieldValues'],
})
const idempotencyKey = newIdempotencyKey()
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

// Customer search while typing (never on mount).
async function searchCustomers(q: string): Promise<S['User'][]> {
  if (q.length < 2) return []
  return (await unwrap(api.GET('/agent/customers', { params: { query: { q, limit: 10 } } }))).items ?? []
}
/** The picker shows only when there is a choice; with one project the server's default applies. */
const projectItems = computed(() => props.projects.filter((p) => p.active))
const showProject = computed(() => projectItems.value.length > 1)
/** Categories and teams of the chosen project (the lists carry `project_id` / `project_ids`). */
const categoryItems = computed(() =>
  props.categories.filter((c) => c.active && (!model.project_id || !c.project_id || c.project_id === model.project_id)),
)
const teamItems = computed(() => props.teams.filter((tm) => !model.project_id || !tm.project_ids?.length || tm.project_ids.includes(model.project_id)))
watch(
  () => model.project_id,
  () => {
    if (model.category_id && !categoryItems.value.some((c) => c.id === model.category_id)) model.category_id = null
    if (model.team_id && !teamItems.value.some((tm) => tm.id === model.team_id)) model.team_id = null
  },
)

/**
 * Fields that apply to the chosen category and project (no scope = every
 * one): only these are shown, required and sent. A category-scoped field does
 * not apply while no category is chosen.
 */
const defs = computed(() =>
  props.customFields
    .filter((d) => d.active && fieldApplies(d, model.category_id, model.project_id))
    .sort((a, b) => a.sort - b.sort),
)
// Values of fields that stop applying (category or project change) are dropped.
watch(defs, (list) => dropFieldValues(model.custom, list.map((d) => d.key)))
watch(
  () => model.category_id,
  (id) => {
    const cat = props.categories.find((c) => c.id === id)
    if (cat?.default_team_id && !model.team_id) model.team_id = cat.default_team_id
    if (cat?.default_priority) model.priority = cat.default_priority
  },
)

const onSubmit = submit(async () => {
  const custom: S['CustomFieldValues'] = {}
  for (const d of defs.value) if (!emptyFieldValue(model.custom[d.key])) custom[d.key] = model.custom[d.key]!
  const body: S['AgentTicketCreate'] = {
    subject: model.subject.trim(),
    body_md: model.body,
    priority: model.priority,
    ...(model.project_id ? { project_id: model.project_id } : {}),
    category_id: model.category_id,
    team_id: model.team_id,
    ...(newCustomer.value
      ? { requester_email: model.requester_email.trim(), requester_name: model.requester_name.trim() }
      : { requester_id: model.requester!.id }),
    ...(model.attachments.length ? { attachment_ids: model.attachments } : {}),
    ...(Object.keys(custom).length ? { custom_fields: custom } : {}),
  }
  const created = await unwrap(api.POST('/agent/tickets', { params: { header: { 'Idempotency-Key': idempotencyKey } }, body }))
  emit('created', created)
})
</script>

<template>
  <v-form ref="formRef" data-testid="ticket-create-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-switch v-model="newCustomer" :label="$t('agent.newTicket.newCustomer')" color="primary" inset hide-details class="mb-2" data-testid="new-customer-toggle" />
    <template v-if="newCustomer">
      <v-row dense>
        <v-col cols="12" sm="6">
          <v-text-field
            v-model="model.requester_name"
            :label="$t('agent.common.name')"
            :rules="[validations.required.any, validations.maxLength(200)]"
            :error-messages="fieldErrors.requester_name"
            data-testid="requester-name"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field
            v-model="model.requester_email"
            :label="$t('agent.common.email')"
            type="email"
            :rules="[validations.required.email]"
            :error-messages="fieldErrors.requester_email"
            data-testid="requester-email"
          />
        </v-col>
      </v-row>
    </template>
    <AutocompleteSearch
      v-else
      v-model="model.requester"
      :fetch-items="searchCustomers"
      :initial-items="model.requester ? [model.requester] : []"
      :item-title="(u: S['User']) => `${u.name} <${u.email}>`"
      item-value="id"
      return-object
      :label="$t('agent.newTicket.requester')"
      :hint="$t('agent.newTicket.requesterHint')"
      persistent-hint
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.requester_id"
      class="mb-2"
      data-testid="requester-search"
    />
    <AutocompleteSearch
      v-if="showProject"
      v-model="model.project_id"
      :items="projectItems"
      item-title="name"
      item-value="id"
      :label="$t('projects.ticket.project')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.project_id"
      data-testid="ticket-project"
    >
      <template #item="{ item, props: p }">
        <v-list-item v-bind="p" :title="item.name"><template #prepend><ProjectChip :project="item" size="small" class="mr-2" /></template></v-list-item>
      </template>
    </AutocompleteSearch>
    <v-text-field
      v-model="model.subject"
      :label="$t('agent.newTicket.subject')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.subject"
      data-testid="ticket-subject-input"
    />
    <v-row dense>
      <v-col cols="12" sm="4">
        <AutocompleteSearch v-model="model.category_id" :items="categoryItems" item-title="name" item-value="id" :label="$t('agent.common.category')" clearable :error-messages="fieldErrors.category_id" data-testid="ticket-category" />
      </v-col>
      <v-col cols="12" sm="4">
        <AutocompleteSearch
          v-model="model.priority"
          :items="PRIORITIES.map((p) => ({ value: p, title: $t(`priority.${p}`) }))"
          :label="$t('agent.ticket.props.priority')"
          :rules="[validations.required.select]"
          data-testid="ticket-priority"
        />
      </v-col>
      <v-col cols="12" sm="4">
        <AutocompleteSearch v-model="model.team_id" :items="teamItems" item-title="name" item-value="id" :label="$t('agent.common.team')" clearable :error-messages="fieldErrors.team_id" data-testid="ticket-team" />
      </v-col>
    </v-row>
    <CustomFieldInput v-for="d in defs" :key="d.id" v-model="model.custom[d.key]" :def="d" :error-messages="fieldErrors[`cf.${d.key}`]" />
    <MarkdownEditor v-model="model.body" required :label="$t('agent.newTicket.body')" :error-messages="fieldErrors.body_md" testid="ticket-body" />
    <div class="d-flex flex-wrap align-center ga-2">
      <AttachmentUploader v-model="model.attachments" area="agent" />
      <v-spacer />
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="ticket-create">{{ $t('agent.newTicket.create') }}</v-btn>
    </div>
  </v-form>
</template>
