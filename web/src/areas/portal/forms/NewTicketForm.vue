<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, newIdempotencyKey, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import AttachmentUploader from '@/components/AttachmentUploader.vue'
import CustomFieldInput from '@/components/CustomFieldInput.vue'
import KbSuggestions from '../components/KbSuggestions.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import type { Category, PortalProject, CustomFieldDef, CustomFieldValue, CustomerTicket, Priority, UserRef } from '../types'

/**
 * New portal ticket (FE-016). Data comes in as props (the page loads it) so the
 * form itself never fetches on mount. Custom fields: only customer-visible AND
 * customer-editable definitions for the chosen category (FE-054, I-9).
 */
const props = withDefaults(
  defineProps<{ categories?: Category[]; fields?: CustomFieldDef[]; members?: UserRef[] | null; selfId?: string; projects?: PortalProject[]; project?: string | null }>(),
  { categories: () => [], fields: () => [], members: null, selfId: '', projects: () => [], project: null },
)
const emit = defineEmits<{ created: [ticket: CustomerTicket]; 'update:project': [id: string | null] }>()
const { t } = useI18n()
const config = useConfigStore()

const model = reactive({
  subject: '',
  project_id: props.project,
  category_id: null as string | null,
  priority: 'normal' as Priority,
  body_md: '',
  requester_id: null as string | null,
  attachment_ids: [] as string[],
  custom: {} as Record<string, CustomFieldValue>,
})
const uploader = ref<InstanceType<typeof AttachmentUploader> | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
// One key per draft: a retried submit after a network error cannot create a duplicate.
let idemKey = newIdempotencyKey()

// The picker only exists when there is a choice; a single project is sent implicitly by the server.
const showProjects = computed(() => props.projects.length > 1)
watch(() => props.project, (id) => { if (id !== model.project_id) model.project_id = id })
watch(() => model.project_id, (id) => {
  model.category_id = null
  emit('update:project', id)
})

// Vuetify hands the slot the item (or a wrapper with `raw`) depending on the version: resolve by id.
const projectOf = (item: unknown): PortalProject | undefined => {
  const o = item as { id?: string; value?: string; raw?: { id?: string } }
  const id = o?.raw?.id ?? o?.id ?? o?.value
  return props.projects.find((p) => p.id === id)
}

const categoryItems = computed(() =>
  props.categories.filter((c) => c.active).sort((a, b) => a.sort - b.sort).map((c) => ({ value: c.id, title: c.name })),
)
const priorities = computed(() =>
  (['low', 'normal', 'high', 'urgent'] as Priority[])
    .filter((p) => p !== 'urgent' || config.config.customer_urgent_allowed)
    .map((p) => ({ value: p, title: t(`priority.${p}`) })),
)
const requesterItems = computed(() =>
  (props.members ?? []).filter((m) => m.id !== props.selfId).map((m) => ({ value: m.id, title: m.name })),
)

/** Definitions shown for the selected category; staff-only fields never reach this list. */
const visibleFields = computed(() =>
  props.fields
    .filter((d) => d.active !== false && d.customer_visible && d.customer_editable)
    .filter((d) => !d.category_ids || !d.category_ids.length || (model.category_id !== null && d.category_ids.includes(model.category_id)))
    .sort((a, b) => a.sort - b.sort),
)
// Values of fields that disappear with a category change are dropped, not sent.
watch(visibleFields, (defs) => {
  const keep = new Set(defs.map((d) => d.key))
  for (const k of Object.keys(model.custom)) if (!keep.has(k)) delete model.custom[k]
})
// The category's default priority is a suggestion the customer can change.
watch(
  () => model.category_id,
  (id) => {
    const p = props.categories.find((c) => c.id === id)?.default_priority
    if (p && priorities.value.some((x) => x.value === p)) model.priority = p
  },
)

function customPayload() {
  const out: Record<string, string | number | boolean | string[] | null> = {}
  for (const d of visibleFields.value) {
    const v = model.custom[d.key]
    if (v === undefined || v === null || v === '' || (Array.isArray(v) && !v.length)) continue
    out[d.key] = d.type === 'number' ? Number(v) : (v as string | boolean | string[])
  }
  return out
}

const onSubmit = submit(async () => {
  const custom = customPayload()
  const ticket = await unwrap(
    api.POST('/portal/tickets', {
      params: { header: { 'Idempotency-Key': idemKey } },
      body: {
        subject: model.subject.trim(),
        body_md: model.body_md,
        category_id: model.category_id,
        ...(model.project_id ? { project_id: model.project_id } : {}),
        priority: model.priority,
        attachment_ids: model.attachment_ids,
        ...(model.requester_id ? { requester_id: model.requester_id } : {}),
        ...(Object.keys(custom).length ? { custom_fields: custom } : {}),
      },
    }),
  )
  idemKey = newIdempotencyKey()
  emit('created', ticket)
})
</script>

<template>
  <v-form ref="formRef" data-testid="new-ticket-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.subject"
      :label="$t('portal.tickets.subject')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.subject"
      counter="200"
      data-testid="nt-subject"
    />
    <KbSuggestions :query="model.subject" />
    <v-select
      v-if="showProjects"
      v-model="model.project_id"
      :items="projects"
      item-value="id"
      item-title="name"
      :label="$t('portal.projects.project')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.project_id"
      data-testid="nt-project"
    >
      <template #item="{ props: ip, item }">
        <v-list-item v-bind="ip">
          <template #prepend><ProjectChip :project="projectOf(item)" size="small" class="mr-2" /></template>
        </v-list-item>
      </template>
      <template #selection="{ item }"><ProjectChip :project="projectOf(item)" size="small" class="mr-2" />{{ projectOf(item)?.name }}</template>
    </v-select>
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.category_id"
          :items="categoryItems"
          :label="$t('portal.newTicket.category')"
          :error-messages="fieldErrors.category_id"
          clearable
          data-testid="nt-category"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.priority"
          :items="priorities"
          :label="$t('portal.tickets.priority')"
          :rules="[validations.required.select]"
          :error-messages="fieldErrors.priority"
          data-testid="nt-priority"
        />
      </v-col>
    </v-row>
    <v-select
      v-if="requesterItems.length"
      v-model="model.requester_id"
      :items="requesterItems"
      :label="$t('portal.newTicket.onBehalfOf')"
      :hint="$t('portal.newTicket.onBehalfOfHint')"
      persistent-hint
      clearable
      class="mb-2"
      :error-messages="fieldErrors.requester_id"
      data-testid="nt-requester"
    />
    <CustomFieldInput
      v-for="d in visibleFields"
      :key="d.key"
      v-model="model.custom[d.key]"
      :def="d"
      :error-messages="fieldErrors[`custom_fields.${d.key}`]"
    />
    <MarkdownEditor
      v-model="model.body_md"
      :label="$t('portal.newTicket.description')"
      required
      :error-messages="fieldErrors.body_md"
      @files="(f: File[]) => uploader?.add(f)"
      @submit="onSubmit()"
    />
    <AttachmentUploader ref="uploader" v-model="model.attachment_ids" area="portal" class="mb-4" />
    <div class="d-flex justify-end ga-2">
      <v-btn to="/tickets" variant="text">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="nt-submit">{{ $t('portal.newTicket.submit') }}</v-btn>
    </div>
  </v-form>
</template>
