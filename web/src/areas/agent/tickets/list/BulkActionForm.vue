<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Bulk actions on selected tickets (FE-038): assign, status, priority, team.
 * Runs one PATCH per ticket, **sequentially**, and keeps a per-row result so a
 * partial failure (403, 409 invalid-transition…) is shown next to its ticket.
 */
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAlertCircleOutline, mdiCheckCircleOutline } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import type { components } from '@/api/schema'
import type { BulkResult } from './bulk'

type S = components['schemas']
type Action = 'assign' | 'status' | 'priority' | 'team'

const props = defineProps<{
  tickets?: Pick<S['TicketSummary'], 'id' | 'number'>[]
  staff?: S['StaffRef'][]
  teams?: S['Team'][]
  meId?: string | null
}>()
const emit = defineEmits<{ done: [BulkResult[]] }>()
const { t } = useI18n()

const STATUSES: S['TicketStatus'][] = ['new', 'open', 'pending', 'on_hold', 'resolved', 'closed']
const PRIORITIES: S['Priority'][] = ['low', 'normal', 'high', 'urgent']
const model = reactive<{ action: Action | null; value: string | null }>({ action: null, value: null })
const results = ref<BulkResult[]>([])
const progress = ref(0)
watch(
  () => model.action,
  () => (model.value = null),
)

const actions = computed(() =>
  (['assign', 'status', 'priority', 'team'] as Action[]).map((a) => ({ value: a, title: t(`agent.bulk.actions.${a}`) })),
)
const values = computed(() => {
  switch (model.action) {
    case 'assign':
      return [
        ...(props.meId ? [{ value: props.meId, title: t('agent.bulk.me') }] : []),
        { value: 'none', title: t('agent.bulk.unassign') },
        ...(props.staff ?? []).filter((s) => s.id !== props.meId).map((s) => ({ value: s.id, title: s.name })),
      ]
    case 'status':
      return STATUSES.map((s) => ({ value: s, title: t(`status.${s}`) }))
    case 'priority':
      return PRIORITIES.map((p) => ({ value: p, title: t(`priority.${p}`) }))
    case 'team':
      return (props.teams ?? []).map((tm) => ({ value: tm.id, title: tm.name }))
    default:
      return []
  }
})

function patchFor(action: Action, value: string): S['TicketPatch'] {
  switch (action) {
    case 'assign':
      return { assignee_id: value === 'none' ? null : value }
    case 'status':
      return { status: value as S['TicketStatus'] }
    case 'priority':
      return { priority: value as S['Priority'] }
    case 'team':
      return { team_id: value }
  }
}

const { formRef, submit, submitting } = useForm()
const onApply = submit(async () => {
  const body = patchFor(model.action!, model.value!)
  const out: BulkResult[] = []
  progress.value = 0
  for (const tk of props.tickets ?? []) {
    try {
      await unwrap(api.PATCH('/agent/tickets/{id}', { params: { path: { id: tk.id } }, body }))
      out.push({ id: tk.id, number: tk.number, ok: true })
    } catch (e) {
      out.push({ id: tk.id, number: tk.number, ok: false, error: e instanceof ApiProblem ? e.userMessage : t('errors.generic') })
    }
    progress.value = out.length
  }
  results.value = out
  emit('done', out)
})
const failed = computed(() => results.value.filter((r) => !r.ok))
</script>

<template>
  <v-form ref="formRef" class="d-flex flex-wrap align-center ga-2" data-testid="bulk-form" @submit.prevent="onApply">
    <span class="text-body-2 font-weight-medium" data-testid="bulk-count">{{ $t('agent.bulk.selected', { n: tickets?.length ?? 0 }) }}</span>
    <v-select
      v-model="model.action"
      :items="actions"
      :label="$t('agent.bulk.action')"
      :rules="[validations.required.select]"
      density="compact"
      style="max-width: 200px"
      data-testid="bulk-action"
    />
    <v-select
      v-model="model.value"
      :items="values"
      :label="$t('agent.bulk.value')"
      :rules="[validations.required.select]"
      :disabled="!model.action"
      density="compact"
      style="max-width: 240px"
      data-testid="bulk-value"
    />
    <v-btn type="submit" color="primary" :loading="submitting" :disabled="!tickets?.length" data-testid="bulk-apply">
      {{ $t('agent.bulk.apply') }}
    </v-btn>
    <v-progress-linear v-if="submitting" :model-value="(progress / Math.max(1, tickets?.length ?? 1)) * 100" class="w-100" />
    <div v-if="results.length" class="w-100" data-testid="bulk-results">
      <div class="text-body-2 mb-1">{{ $t('agent.bulk.done', { ok: results.length - failed.length, failed: failed.length }) }}</div>
      <div v-for="r in results" :key="r.id" class="d-flex align-center ga-2 text-body-2" :data-testid="r.ok ? 'bulk-ok' : 'bulk-error'">
        <v-icon :icon="r.ok ? mdiCheckCircleOutline : mdiAlertCircleOutline" :color="r.ok ? 'success' : 'error'" size="small" />
        <span class="font-weight-medium">#{{ r.number }}</span>
        <span>{{ r.ok ? $t('agent.bulk.ok') : r.error }}</span>
      </div>
    </div>
  </v-form>
</template>
