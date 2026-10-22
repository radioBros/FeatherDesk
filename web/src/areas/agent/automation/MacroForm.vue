<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Create / edit a macro (FE-064, D-19 A): a one-click bundle of a reply or
 * note, status, priority, tags and assignee. `shared` saves through
 * `/admin/macros` (supervisor+), otherwise through the caller's own
 * `/agent/macros`. Empty fields mean "leave unchanged".
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import { PRIORITIES, STATUSES } from './model'
import type { components } from '@/api/schema'
import ProjectSelect, { type ProjectOption } from '@/components/ProjectSelect.vue'

type S = components['schemas']
const props = defineProps<{ macro?: S['Macro'] | null; shared?: boolean; staff?: S['StaffRef'][]; tags?: string[]; projects?: ProjectOption[]; canGlobal?: boolean }>()
const emit = defineEmits<{ saved: [S['Macro']]; cancel: [] }>()
const { t } = useI18n()

/** '' = unchanged (Vuetify selects cannot hold a null item value). */
const UNCHANGED = ''
const model = reactive({
  name: props.macro?.name ?? '',
  reply_kind: (props.macro?.reply_kind ?? 'public') as 'public' | 'internal',
  reply_md: props.macro?.reply_md ?? '',
  status: (props.macro?.status ?? UNCHANGED) as string,
  priority: (props.macro?.priority ?? UNCHANGED) as string,
  add_tags: [...(props.macro?.add_tags ?? [])],
  remove_tags: [...(props.macro?.remove_tags ?? [])],
  assignee: props.macro?.assignee ?? UNCHANGED,
  project_id: (props.macro?.project_id ?? null) as string | null,
})
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const unchanged = computed(() => ({ value: UNCHANGED, title: t('automation.macros.unchanged') }))
const statusItems = computed(() => [unchanged.value, ...STATUSES.map((s) => ({ value: s, title: t(`status.${s}`) }))])
const priorityItems = computed(() => [unchanged.value, ...PRIORITIES.map((p) => ({ value: p, title: t(`priority.${p}`) }))])
const assigneeItems = computed(() => [
  unchanged.value,
  { value: 'me', title: t('automation.macros.assignMe') },
  { value: 'none', title: t('automation.macros.unassign') },
  ...(props.staff ?? []).map((s) => ({ value: s.id, title: s.name })),
])
const kindItems = computed(() => [
  { value: 'public', title: t('automation.macros.kinds.public') },
  { value: 'internal', title: t('automation.macros.kinds.internal') },
])
const norm = (list: string[]) => [...new Set(list.map((x) => x.trim().toLowerCase()).filter(Boolean))]

const onSave = submit(async () => {
  const body: S['MacroCreate'] = {
    name: model.name.trim(),
    reply_kind: model.reply_kind,
    reply_md: model.reply_md,
    status: (model.status || null) as S['TicketStatus'] | null,
    priority: (model.priority || null) as S['Priority'] | null,
    add_tags: norm(model.add_tags),
    remove_tags: norm(model.remove_tags),
    assignee: model.assignee || null,
    ...(props.projects?.length ? { project_id: model.project_id } : {}),
  }
  let saved: S['Macro']
  if (props.shared) {
    saved = props.macro
      ? await unwrap(api.PATCH('/admin/macros/{id}', { params: { path: { id: props.macro.id } }, body }))
      : await unwrap(api.POST('/admin/macros', { body }))
  } else {
    saved = props.macro
      ? await unwrap(api.PATCH('/agent/macros/{id}', { params: { path: { id: props.macro.id } }, body }))
      : await unwrap(api.POST('/agent/macros', { body }))
  }
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="macro-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('automation.macros.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name"
      data-testid="macro-name"
    />
    <ProjectSelect
      v-if="projects?.length"
      v-model="model.project_id"
      :projects="projects"
      :nullable="canGlobal !== false"
      :null-label="$t('projects.config.global')"
      :label="$t('projects.config.project')"
      :hint="$t('projects.config.scopeHint')"
      :rules="canGlobal === false ? [validations.required.any] : []"
      :error-messages="fieldErrors.project_id"
      testid="macro-project"
    />
    <AutocompleteSearch
      v-model="model.reply_kind"
      :items="kindItems"
      :label="$t('automation.macros.replyKind')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.reply_kind"
      data-testid="macro-kind"
    />
    <MarkdownEditor v-model="model.reply_md" :label="$t('automation.macros.reply')" :error-messages="fieldErrors.reply_md" testid="macro-reply" />
    <div class="text-caption text-medium-emphasis mb-3">
      {{ $t('automation.macros.variables') }} <code v-pre>{{customer.name}}</code>, <code v-pre>{{ticket.number}}</code>, <code v-pre>{{agent.name}}</code>
    </div>
    <v-row density="compact">
      <v-col cols="12" md="4">
        <AutocompleteSearch v-model="model.status" :items="statusItems" :label="$t('automation.macros.status')" :error-messages="fieldErrors.status" data-testid="macro-status" />
      </v-col>
      <v-col cols="12" md="4">
        <AutocompleteSearch v-model="model.priority" :items="priorityItems" :label="$t('automation.macros.priority')" :error-messages="fieldErrors.priority" data-testid="macro-priority" />
      </v-col>
      <v-col cols="12" md="4">
        <AutocompleteSearch v-model="model.assignee" :items="assigneeItems" :label="$t('automation.macros.assignee')" :error-messages="fieldErrors.assignee" data-testid="macro-assignee" />
      </v-col>
    </v-row>
    <v-combobox
      :model-value="model.add_tags"
      @update:model-value="(v: string[]) => (model.add_tags = norm(v))"
      :items="tags ?? []"
      :label="$t('automation.macros.addTags')"
      :rules="[validations.maxItems(20), validations.listOf(validations.tagName)]"
      :error-messages="fieldErrors.add_tags"
      multiple
      chips
      closable-chips
      data-testid="macro-add-tags"
    />
    <v-combobox
      :model-value="model.remove_tags"
      @update:model-value="(v: string[]) => (model.remove_tags = norm(v))"
      :items="tags ?? []"
      :label="$t('automation.macros.removeTags')"
      :rules="[validations.maxItems(20), validations.listOf(validations.tagName)]"
      :error-messages="fieldErrors.remove_tags"
      multiple
      chips
      closable-chips
      data-testid="macro-remove-tags"
    />
    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="macro-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
