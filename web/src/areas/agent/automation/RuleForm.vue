<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import SelectionChips from '@/components/SelectionChips.vue'
/**
 * Rule builder (FE-063, D-19 A): name, event, ALL/ANY, condition rows
 * (operators filtered per field; hours-since fields on time rules only) and
 * 1…10 action rows. Reference data (teams, categories, staff, canned
 * responses, custom fields, organizations) comes in as props.
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiDelete, mdiPlus } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import { localized } from '@/utils/localized'
import {
  ACTIONS,
  CHANNELS,
  EVENTS,
  MAX_ACTIONS,
  MAX_CONDITIONS,
  PRIORITIES,
  STATUSES,
  TIERS,
  TIME_FIELDS,
  actionFromApi,
  actionParam,
  actionToApi,
  conditionFromApi,
  conditionToApi,
  fieldsFor,
  newAction,
  newCondition,
  opsFor,
  resetAction,
  resetCondition,
  valueKind,
  type ActionRow,
  type ConditionRow,
  type Field,
} from './model'
import type { components } from '@/api/schema'
import ProjectSelect, { type ProjectOption } from '@/components/ProjectSelect.vue'

type S = components['schemas']
const props = defineProps<{
  rule?: S['AutomationRule'] | null
  teams?: S['Team'][]
  categories?: S['Category'][]
  staff?: S['StaffRef'][]
  canned?: S['CannedResponse'][]
  customFields?: S['CustomFieldDef'][]
  organizations?: S['Organization'][]
  projects?: ProjectOption[]
  canGlobal?: boolean
}>()
const emit = defineEmits<{ saved: [S['AutomationRule']]; cancel: [] }>()
const { t, locale } = useI18n()
const config = useConfigStore()

const model = reactive({
  name: props.rule?.name ?? '',
  event: (props.rule?.event ?? 'ticket.created') as S['AutomationEvent'],
  match: (props.rule?.match ?? 'all') as S['AutomationMatch'],
  enabled: props.rule?.enabled ?? true,
  project_id: (props.rule?.project_id ?? null) as string | null,
  stop_processing: props.rule?.stop_processing ?? false,
  conditions: (props.rule?.conditions ?? []).map(conditionFromApi) as ConditionRow[],
  actions: (props.rule?.actions?.length ? props.rule.actions.map(actionFromApi) : [newAction()]) as ActionRow[],
})
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const opt = <T extends string | number>(values: readonly T[], key: (v: T) => string) => values.map((v) => ({ value: v, title: t(key(v)) }))
const eventItems = computed(() => opt(EVENTS, (e) => `automation.events.${e.replace('.', '_')}`))
const fieldItems = computed(() => opt(fieldsFor(model.event), (f) => `automation.fields.${f}`))
const actionItems = computed(() => opt(ACTIONS, (a) => `automation.actions.${a}`))
const opItems = (f: Field) => opt(opsFor(f), (o) => `automation.ops.${o}`)
const tierItems = computed(() => TIERS.map((n) => ({ value: n, title: config.tierLabel(n) })))
const boolItems = computed(() => [
  { value: true, title: t('automation.rules.yes') },
  { value: false, title: t('automation.rules.no') },
])
const assignModes = computed(() => opt(['round_robin', 'least_busy', 'agent'] as const, (m) => `automation.assignModes.${m}`))
const staffItems = computed(() => (props.staff ?? []).map((s) => ({ value: s.id, title: s.name })))
const cannedItems = computed(() => (props.canned ?? []).map((c) => ({ value: c.id, title: c.title })))
const cfItems = computed(() =>
  (props.customFields ?? []).map((f) => ({ value: f.key, title: localized(f.label as Record<string, string>, locale.value) || f.key })),
)

function multiItems(f: Field) {
  switch (f) {
    case 'channel':
      return CHANNELS.map((c) => ({ value: c, title: t(`automation.channels.${c}`) }))
    case 'priority':
      return PRIORITIES.map((p) => ({ value: p, title: t(`priority.${p}`) }))
    case 'status':
      return STATUSES.map((s) => ({ value: s, title: t(`status.${s}`) }))
    case 'category_id':
      return (props.categories ?? []).map((c) => ({ value: c.id, title: c.name }))
    case 'project':
      return (props.projects ?? []).map((p) => ({ value: p.id, title: `${p.key} · ${p.name}` }))
    case 'team_id':
      return (props.teams ?? []).map((tm) => ({ value: tm.id, title: tm.name }))
    default:
      return (props.organizations ?? []).map((o) => ({ value: o.id, title: o.name }))
  }
}

function onEvent(e: S['AutomationEvent']) {
  model.event = e
  // Hours-since conditions only exist on time rules.
  if (e !== 'time') for (const c of model.conditions) if (TIME_FIELDS.includes(c.field)) Object.assign(c, newCondition())
}
function onField(row: ConditionRow, f: Field) {
  row.field = f
  resetCondition(row)
}
function onActionType(row: ActionRow, type: S['AutomationActionType']) {
  row.type = type
  resetAction(row)
}
const addCondition = () => model.conditions.push(newCondition(model.event === 'time' ? 'hours_since_update' : 'subject'))
const addAction = () => model.actions.push(newAction())
const needsHours = computed(() => model.event === 'time' && !model.conditions.some((c) => TIME_FIELDS.includes(c.field)))

const onSave = submit(async () => {
  const body: S['AutomationRuleCreate'] = {
    name: model.name.trim(),
    event: model.event,
    match: model.match,
    enabled: model.enabled,
    ...(props.projects?.length ? { project_id: model.project_id } : {}),
    stop_processing: model.stop_processing,
    conditions: model.conditions.map(conditionToApi),
    actions: model.actions.map(actionToApi),
  }
  const saved = props.rule
    ? await unwrap(api.PATCH('/admin/automation/rules/{id}', { params: { path: { id: props.rule.id } }, body }))
    : await unwrap(api.POST('/admin/automation/rules', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="rule-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-alert v-if="fieldErrors.conditions || fieldErrors.actions" type="error" variant="tonal" density="compact" class="mb-3" data-testid="rule-list-errors">
      {{ [...(fieldErrors.conditions ?? []), ...(fieldErrors.actions ?? [])].join(' ') }}
    </v-alert>
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
      testid="rule-project"
    />
    <v-row density="compact">
      <v-col cols="12" md="6">
        <v-text-field
          v-model="model.name"
          :label="$t('automation.rules.name')"
          :rules="[validations.required.any, validations.maxLength(120)]"
          :error-messages="fieldErrors.name"
          data-testid="rule-name"
        />
      </v-col>
      <v-col cols="12" md="3">
        <v-select
          :model-value="model.event"
          :items="eventItems"
          :label="$t('automation.rules.event')"
          :rules="[validations.required.select]"
          :error-messages="fieldErrors.event"
          data-testid="rule-event"
          @update:model-value="onEvent"
        />
      </v-col>
      <v-col cols="12" md="3">
        <v-select
          v-model="model.match"
          :items="[{ value: 'all', title: $t('automation.rules.matchAll') }, { value: 'any', title: $t('automation.rules.matchAny') }]"
          :label="$t('automation.rules.match')"
          :rules="[validations.required.select]"
          data-testid="rule-match"
        />
      </v-col>
    </v-row>

    <div class="text-subtitle-2 mt-2 mb-1">{{ $t('automation.rules.conditions') }}</div>
    <div class="text-caption text-medium-emphasis mb-2">{{ $t('automation.rules.conditionsHint') }}</div>
    <v-alert v-if="needsHours" type="info" variant="tonal" density="compact" class="mb-2" data-testid="rule-time-hint">{{ $t('automation.rules.timeHint') }}</v-alert>
    <v-row v-for="(c, i) in model.conditions" :key="`c${i}`" dense class="align-start" data-testid="rule-condition">
      <v-col cols="12" md="3">
        <v-select
          :model-value="c.field"
          :items="fieldItems"
          :label="$t('automation.rules.field')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="condition-field"
          @update:model-value="(f: Field) => onField(c, f)"
        />
      </v-col>
      <v-col cols="12" md="2">
        <v-select v-model="c.op" :items="opItems(c.field)" :label="$t('automation.rules.operator')" :rules="[validations.required.select]" density="compact" data-testid="condition-op" />
      </v-col>
      <v-col v-if="c.field === 'custom_field'" cols="12" md="2">
        <v-select v-model="c.key" :items="cfItems" :label="$t('automation.rules.customField')" :rules="[validations.required.select]" density="compact" data-testid="condition-key" />
      </v-col>
      <v-col cols="12" :md="c.field === 'custom_field' ? 4 : 6">
        <v-text-field
          v-if="valueKind(c.field) === 'text'"
          v-model="c.value"
          :label="$t('automation.rules.value')"
          :rules="c.op === 'regex' ? [validations.required.any, validations.re2Pattern] : [validations.required.any, validations.maxLength(500)]"
          density="compact"
          data-testid="condition-value"
        />
        <v-select
          v-else-if="valueKind(c.field) === 'multi'"
          :model-value="c.value as string[]"
          @update:model-value="(v: string[]) => (c.value = v)"
          :items="multiItems(c.field)"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.array]"
          multiple
          density="compact"
          data-testid="condition-value">
          <template #selection="{ item, index }"><SelectionChips :item="item" :index="index" :total="((c.value as string[]) ?? []).length" /></template>
        </v-select>
        <v-select
          v-else-if="valueKind(c.field) === 'tier'"
          v-model="c.value"
          :items="tierItems"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="condition-value"
        />
        <v-combobox
          v-else-if="valueKind(c.field) === 'domains'"
          :model-value="c.value as string[]"
          @update:model-value="(v: string[]) => (c.value = v)"
          :label="$t('automation.rules.domains')"
          :rules="[validations.required.array, validations.listOf(validations.domain)]"
          multiple
          chips
          closable-chips
          density="compact"
          data-testid="condition-value"
        />
        <v-text-field
          v-else-if="valueKind(c.field) === 'tag'"
          v-model="c.value"
          :label="$t('automation.rules.tag')"
          :rules="[validations.required.any, validations.tagName]"
          density="compact"
          data-testid="condition-value"
        />
        <v-select
          v-else-if="valueKind(c.field) === 'bool'"
          v-model="c.value"
          :items="boolItems"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="condition-value"
        />
        <v-text-field
          v-else
          v-model.number="c.value"
          type="number"
          :label="$t('automation.rules.hours')"
          :rules="[validations.required.any, validations.integer, validations.range(1, 8760)]"
          density="compact"
          data-testid="condition-value"
        />
      </v-col>
      <v-col cols="12" md="1" class="text-right">
        <v-btn :icon="mdiDelete" variant="text" size="small" :aria-label="$t('automation.rules.removeCondition')" data-testid="condition-remove" @click="model.conditions.splice(i, 1)" />
      </v-col>
    </v-row>
    <v-btn variant="text" size="small" :prepend-icon="mdiPlus" :disabled="model.conditions.length >= MAX_CONDITIONS" class="mb-3" data-testid="condition-add" @click="addCondition">
      {{ $t('automation.rules.addCondition') }}
    </v-btn>

    <div class="text-subtitle-2 mt-2 mb-2">{{ $t('automation.rules.actions') }}</div>
    <v-row v-for="(a, i) in model.actions" :key="`a${i}`" dense class="align-start" data-testid="rule-action">
      <v-col cols="12" md="3">
        <v-select
          :model-value="a.type"
          :items="actionItems"
          :label="$t('automation.rules.action')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-type"
          @update:model-value="(v: S['AutomationActionType']) => onActionType(a, v)"
        />
      </v-col>
      <v-col cols="12" md="8">
        <v-select
          v-if="actionParam(a.type) === 'priority'"
          v-model="a.value"
          :items="PRIORITIES.map((p) => ({ value: p, title: $t(`priority.${p}`) }))"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-value"
        />
        <v-select
          v-else-if="actionParam(a.type) === 'status'"
          v-model="a.value"
          :items="STATUSES.map((s) => ({ value: s, title: $t(`status.${s}`) }))"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-value"
        />
        <v-select
          v-else-if="actionParam(a.type) === 'team'"
          v-model="a.value"
          :items="(teams ?? []).map((tm) => ({ value: tm.id, title: tm.name }))"
          :label="$t('automation.rules.team')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-value"
        />
        <v-select
          v-else-if="actionParam(a.type) === 'category'"
          v-model="a.value"
          :items="(categories ?? []).map((ct) => ({ value: ct.id, title: ct.name }))"
          :label="$t('automation.rules.category')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-value"
        />
        <v-select
          v-else-if="actionParam(a.type) === 'tier'"
          v-model="a.value"
          :items="tierItems"
          :label="$t('automation.rules.value')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-value"
        />
        <v-text-field
          v-else-if="actionParam(a.type) === 'tag'"
          v-model="a.value"
          :label="$t('automation.rules.tag')"
          :rules="[validations.required.any, validations.tagName]"
          density="compact"
          data-testid="action-value"
        />
        <div v-else-if="actionParam(a.type) === 'assign'" class="d-flex ga-2">
          <v-select v-model="a.assignMode" :items="assignModes" :label="$t('automation.rules.assignMode')" :rules="[validations.required.select]" density="compact" data-testid="action-assign-mode" />
          <v-autocomplete
            v-if="a.assignMode === 'agent'"
            v-model="a.user_id"
            :items="staffItems"
            :label="$t('automation.rules.agent')"
            :rules="[validations.required.select]"
            density="compact"
            data-testid="action-user"
          />
        </div>
        <v-autocomplete
          v-else-if="actionParam(a.type) === 'staff'"
          v-model="a.user_id"
          :items="staffItems"
          :label="$t('automation.rules.agent')"
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-user"
        />
        <v-textarea
          v-else-if="actionParam(a.type) === 'note'"
          v-model="a.body_md"
          :label="$t('automation.rules.note')"
          :hint="$t('automation.rules.noteHint')"
          persistent-hint
          :rules="[validations.required.any, validations.maxLength(10000)]"
          rows="2"
          auto-grow
          density="compact"
          data-testid="action-note"
        />
        <v-select
          v-else-if="actionParam(a.type) === 'canned'"
          v-model="a.canned_response_id"
          :items="cannedItems"
          :label="$t('automation.rules.canned')"
          :hint="$t('automation.rules.cannedHint')"
          persistent-hint
          :rules="[validations.required.select]"
          density="compact"
          data-testid="action-canned"
        />
        <div v-else class="text-caption text-medium-emphasis pt-2">{{ $t('automation.rules.noParams') }}</div>
      </v-col>
      <v-col cols="12" md="1" class="text-right">
        <v-btn
          :icon="mdiDelete"
          variant="text"
          size="small"
          :disabled="model.actions.length <= 1"
          :aria-label="$t('automation.rules.removeAction')"
          data-testid="action-remove"
          @click="model.actions.splice(i, 1)"
        />
      </v-col>
    </v-row>
    <v-btn variant="text" size="small" :prepend-icon="mdiPlus" :disabled="model.actions.length >= MAX_ACTIONS" class="mb-3" data-testid="action-add" @click="addAction">
      {{ $t('automation.rules.addAction') }}
    </v-btn>

    <div class="d-flex flex-wrap ga-4">
      <v-switch v-model="model.enabled" :label="$t('automation.rules.enabled')" color="primary" density="compact" hide-details data-testid="rule-enabled" />
      <v-switch
        v-model="model.stop_processing"
        :label="$t('automation.rules.stopProcessing')"
        :hint="$t('automation.rules.stopHint')"
        color="primary"
        density="compact"
        persistent-hint
        data-testid="rule-stop"
      />
    </div>
    <div class="d-flex justify-end ga-2 mt-3">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="rule-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
