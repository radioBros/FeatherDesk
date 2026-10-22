<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * SLA policy grid (FE-041): first-response and resolution minutes per
 * priority (whole numbers 1…525600, so 0 and 1.5 are rejected before sending)
 * and the calendar mode per row (FE-056: `24x7` or the team's business hours).
 */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { PRIORITIES } from './constants'

type S = components['schemas']
const props = defineProps<{ policies?: S['SlaPolicy'][] }>()
const emit = defineEmits<{ saved: [S['SlaPolicy'][]] }>()

interface Row {
  priority: S['Priority']
  first_response_minutes: string
  resolution_minutes: string
  calendar_mode: S['CalendarMode']
}
const rows = reactive<Row[]>(
  PRIORITIES.map((p) => {
    const found = props.policies?.find((x) => x.priority === p)
    return {
      priority: p,
      first_response_minutes: found ? String(found.first_response_minutes) : '',
      resolution_minutes: found ? String(found.resolution_minutes) : '',
      calendar_mode: found?.calendar_mode ?? '24x7',
    }
  }),
)
const minuteRules = [validations.required.any, validations.integer, validations.range(1, 525600)]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const policies: S['SlaPolicy'][] = rows.map((r) => ({
    priority: r.priority,
    first_response_minutes: Number(r.first_response_minutes),
    resolution_minutes: Number(r.resolution_minutes),
    calendar_mode: r.calendar_mode,
  }))
  const saved = await unwrap(api.PUT('/admin/sla-policies', { body: { policies } }))
  emit('saved', saved ?? policies)
})
</script>

<template>
  <v-form ref="formRef" data-testid="sla-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-table density="comfortable">
      <thead>
        <tr>
          <th>{{ $t('agent.sla.priority') }}</th>
          <th>{{ $t('agent.sla.firstResponse') }}</th>
          <th>{{ $t('agent.sla.resolution') }}</th>
          <th>{{ $t('agent.sla.calendarMode') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rows" :key="r.priority" :data-testid="`sla-row-${r.priority}`">
          <td>{{ $t(`priority.${r.priority}`) }}</td>
          <td class="py-2">
            <v-text-field
              v-model="r.first_response_minutes"
              :label="$t('agent.sla.minutes')"
              inputmode="numeric"
              density="compact"
              :rules="minuteRules"
              :error-messages="fieldErrors[`policies[${i}].first_response_minutes`]"
              :data-testid="`sla-first-${r.priority}`"
            />
          </td>
          <td class="py-2">
            <v-text-field
              v-model="r.resolution_minutes"
              :label="$t('agent.sla.minutes')"
              inputmode="numeric"
              density="compact"
              :rules="minuteRules"
              :error-messages="fieldErrors[`policies[${i}].resolution_minutes`]"
              :data-testid="`sla-resolution-${r.priority}`"
            />
          </td>
          <td class="py-2">
            <AutocompleteSearch
              v-model="r.calendar_mode"
              :items="[
                { value: '24x7', title: $t('agent.sla.mode24x7') },
                { value: 'team', title: $t('agent.sla.modeTeam') },
              ]"
              :label="$t('agent.sla.calendarMode')"
              density="compact"
              :rules="[validations.required.select]"
              :data-testid="`sla-mode-${r.priority}`"
            />
          </td>
        </tr>
      </tbody>
    </v-table>
    <div class="d-flex justify-end mt-3">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="sla-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
