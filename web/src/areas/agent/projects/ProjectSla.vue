<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Per-project SLA overrides (D-20): each priority either inherits the global
 * default (shown greyed out) or carries its own targets. Saving sends only the
 * overridden priorities; none overridden (`[]`) clears them all.
 */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { PRIORITIES } from '../config/constants'

type S = components['schemas']
const props = defineProps<{ projectId: string; policies?: S['ProjectSlaPolicy'][]; readonly?: boolean }>()
const emit = defineEmits<{ saved: [S['ProjectSlaPolicy'][]] }>()

interface Row {
  priority: S['Priority']
  override: boolean
  first_response_minutes: string
  resolution_minutes: string
  calendar_mode: S['CalendarMode']
}
const rows = reactive<Row[]>(
  PRIORITIES.map((pr) => {
    const f = props.policies?.find((x) => x.priority === pr)
    return {
      priority: pr,
      override: !!f && !f.inherited,
      first_response_minutes: f ? String(f.first_response_minutes) : '',
      resolution_minutes: f ? String(f.resolution_minutes) : '',
      calendar_mode: f?.calendar_mode ?? '24x7',
    }
  }),
)
const minuteRules = [validations.required.any, validations.integer, validations.range(1, 525600)]
const noRules: never[] = []
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

function clearAll() {
  for (const r of rows) r.override = false
}

const onSave = submit(async () => {
  const policies: S['SlaPolicy'][] = rows
    .filter((r) => r.override)
    .map((r) => ({
      priority: r.priority,
      first_response_minutes: Number(r.first_response_minutes),
      resolution_minutes: Number(r.resolution_minutes),
      calendar_mode: r.calendar_mode,
    }))
  const saved = await unwrap(api.PUT('/admin/projects/{id}/sla-policies', { params: { path: { id: props.projectId } }, body: { policies } }))
  emit('saved', saved ?? [])
})
</script>

<template>
  <v-form ref="formRef" data-testid="project-sla-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <div class="text-body-2 text-medium-emphasis mb-2">{{ $t('projects.admin.sla.hint') }}</div>
    <v-table density="compact">
      <thead>
        <tr>
          <th>{{ $t('agent.sla.priority') }}</th>
          <th>{{ $t('projects.admin.sla.override') }}</th>
          <th>{{ $t('agent.sla.firstResponse') }}</th>
          <th>{{ $t('agent.sla.resolution') }}</th>
          <th>{{ $t('agent.sla.calendarMode') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rows" :key="r.priority" :data-testid="`project-sla-row-${r.priority}`">
          <td>
            {{ $t(`priority.${r.priority}`) }}
            <div v-if="!r.override" class="text-caption text-medium-emphasis">{{ $t('projects.admin.sla.inherited') }}</div>
          </td>
          <td>
            <v-checkbox v-model="r.override" :disabled="readonly" density="compact" hide-details :aria-label="$t('projects.admin.sla.override')" :data-testid="`project-sla-override-${r.priority}`" />
          </td>
          <td class="py-1">
            <v-text-field
              v-model="r.first_response_minutes"
              :label="$t('agent.sla.minutes')"
              inputmode="numeric"
              density="compact"
              :disabled="!r.override || readonly"
              :rules="r.override ? minuteRules : noRules"
              :error-messages="fieldErrors[`policies[${i}].first_response_minutes`]"
              :data-testid="`project-sla-first-${r.priority}`"
            />
          </td>
          <td class="py-1">
            <v-text-field
              v-model="r.resolution_minutes"
              :label="$t('agent.sla.minutes')"
              inputmode="numeric"
              density="compact"
              :disabled="!r.override || readonly"
              :rules="r.override ? minuteRules : noRules"
              :error-messages="fieldErrors[`policies[${i}].resolution_minutes`]"
              :data-testid="`project-sla-resolution-${r.priority}`"
            />
          </td>
          <td class="py-1">
            <v-select
              v-model="r.calendar_mode"
              :items="[
                { value: '24x7', title: $t('agent.sla.mode24x7') },
                { value: 'team', title: $t('agent.sla.modeTeam') },
              ]"
              :label="$t('agent.sla.calendarMode')"
              density="compact"
              :disabled="!r.override || readonly"
              :rules="r.override ? [validations.required.select] : noRules"
              :data-testid="`project-sla-mode-${r.priority}`"
            />
          </td>
        </tr>
      </tbody>
    </v-table>
    <div v-if="!readonly" class="d-flex justify-end ga-2 mt-3">
      <v-btn variant="text" data-testid="project-sla-clear" @click="clearAll">{{ $t('projects.admin.sla.clear') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="project-sla-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
