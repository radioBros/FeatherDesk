<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Create / edit a team (FE-028, FE-056): notification email + notify mode
 * (`mailbox` / `both` require the email, email config decision) and the
 * business calendar (none = 24×7).
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations, type Rule } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ team?: S['Team'] | null; calendars?: S['BusinessCalendar'][] }>()
const emit = defineEmits<{ saved: [S['Team']]; cancel: [] }>()
const { t } = useI18n()

const MODES: S['NotifyMode'][] = ['agents', 'mailbox', 'both']
/** D-19 A (FE-061): how unassigned tickets of the team get an owner. */
const STRATEGIES: S['AssignmentStrategy'][] = ['manual', 'round_robin', 'least_busy']
const model = reactive({
  name: props.team?.name ?? '',
  description: props.team?.description ?? '',
  notification_email: props.team?.notification_email ?? '',
  notify_mode: (props.team?.notify_mode ?? 'agents') as S['NotifyMode'],
  calendar_id: props.team?.calendar_id ?? null,
  assignment_strategy: (props.team?.assignment_strategy ?? 'manual') as S['AssignmentStrategy'],
})
const emailRules = computed<Rule[]>(() => (model.notify_mode === 'agents' ? [validations.email] : [validations.required.email]))
const calendarItems = computed(() => [
  { value: null, title: t('agent.teams.calendar247') },
  ...(props.calendars ?? []).map((c) => ({ value: c.id, title: `${c.name} (${c.timezone})` })),
])
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const body: S['TeamCreate'] = {
    name: model.name.trim(),
    description: model.description,
    notification_email: model.notification_email.trim() || null,
    notify_mode: model.notify_mode,
    calendar_id: model.calendar_id,
    // Always sent: the PATCH keeps the stored value when it is absent, the form owns it.
    assignment_strategy: model.assignment_strategy,
  }
  const saved = props.team
    ? await unwrap(api.PATCH('/admin/teams/{id}', { params: { path: { id: props.team.id } }, body }))
    : await unwrap(api.POST('/admin/teams', { body }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="team-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(100)]"
      :error-messages="fieldErrors.name"
      data-testid="team-name"
    />
    <v-textarea
      v-model="model.description"
      :label="$t('agent.common.description')"
      :rules="[validations.maxLength(1000)]"
      :error-messages="fieldErrors.description"
      rows="2"
      auto-grow
    />
    <v-select
      v-model="model.notify_mode"
      :items="MODES.map((m) => ({ value: m, title: $t(`agent.teams.modes.${m}`) }))"
      :label="$t('agent.teams.notifyMode')"
      :hint="$t(`agent.teams.modeHints.${model.notify_mode}`)"
      persistent-hint
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.notify_mode"
      data-testid="team-mode"
    />
    <v-text-field
      v-model="model.notification_email"
      :label="$t('agent.teams.notificationEmail')"
      type="email"
      :rules="emailRules"
      :error-messages="fieldErrors.notification_email"
      class="mt-2"
      data-testid="team-email"
    />
    <v-select
      v-model="model.calendar_id"
      :items="calendarItems"
      :label="$t('agent.teams.calendar')"
      :hint="$t('agent.teams.calendarHint')"
      persistent-hint
      :error-messages="fieldErrors.calendar_id"
      data-testid="team-calendar"
    />
    <v-select
      v-model="model.assignment_strategy"
      :items="STRATEGIES.map((s) => ({ value: s, title: $t(`automation.strategy.${s}`) }))"
      :label="$t('automation.strategy.label')"
      :hint="$t(`automation.strategy.hints.${model.assignment_strategy}`)"
      persistent-hint
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.assignment_strategy"
      class="mt-2"
      data-testid="team-strategy"
    />
    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="team-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
