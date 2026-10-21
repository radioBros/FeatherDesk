<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Report range form (FE-043): from/to dates (to ≥ from), optional team and
 * SLA calendar mode. Emits the validated filter; the page fetches.
 */
import { computed, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import type { components } from '@/api/schema'
import type { ReportFilter } from './format'

type S = components['schemas']
const props = withDefaults(defineProps<{ initial?: Partial<ReportFilter>; teams?: S['Team'][]; showTeam?: boolean; projects?: { id: string; key: string; name: string }[] }>(), {
  initial: () => ({}),
  teams: () => [],
  showTeam: false,
  projects: () => [],
})
const emit = defineEmits<{ apply: [ReportFilter] }>()
const { t } = useI18n()
const model = reactive<ReportFilter>({
  from: props.initial.from ?? '',
  to: props.initial.to ?? '',
  team_id: props.initial.team_id ?? null,
  project: props.initial.project ?? null,
  calendar_mode: props.initial.calendar_mode ?? null,
})
watch(() => props.initial.project, (p) => (model.project = p ?? null))
const projectItems = computed(() => props.projects.map((p) => ({ value: p.id, title: `${p.key} · ${p.name}` })))
const toRules = [validations.required.any, validations.date, validations.dateRangeOrder(() => model.from)]
const modeItems = computed(() => (['24x7', 'team'] as const).map((m) => ({ value: m, title: t(`agent.reports.mode.${m === '24x7' ? 'always' : 'team'}`) })))

const { formRef, submit, submitting } = useForm()
const onApply = submit(() => emit('apply', { ...model }))
</script>

<template>
  <v-form ref="formRef" class="d-flex flex-wrap align-start ga-3" data-testid="reports-form" @submit.prevent="onApply">
    <v-text-field v-model="model.from" type="date" :label="$t('agent.reports.from')" :rules="[validations.required.any, validations.date]" density="compact" style="max-width: 190px" data-testid="reports-from" />
    <v-text-field v-model="model.to" type="date" :label="$t('agent.reports.to')" :rules="toRules" density="compact" style="max-width: 190px" data-testid="reports-to" />
    <v-select
      v-if="showTeam"
      v-model="model.team_id"
      :items="teams"
      item-title="name"
      item-value="id"
      :label="$t('agent.common.team')"
      density="compact"
      clearable
      style="max-width: 220px"
    />
    <v-select
      v-if="projects.length > 1"
      v-model="model.project"
      :items="projectItems"
      :label="$t('projects.reports.project')"
      :placeholder="$t('projects.switcher.all')"
      persistent-placeholder
      density="compact"
      clearable
      style="max-width: 240px"
      data-testid="reports-project"
    />
    <v-select v-model="model.calendar_mode" :items="modeItems" :label="$t('agent.reports.calendarMode')" density="compact" clearable style="max-width: 220px" />
    <v-btn type="submit" color="primary" :loading="submitting" data-testid="reports-apply">{{ $t('agent.reports.apply') }}</v-btn>
  </v-form>
</template>
