<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/**
 * Escalate / de-escalate (FE-024): target tier is ±1 and read-only, an
 * optional target team, and a required reason. Emits the `TicketUpdateResult`
 * so the page can navigate away when `visible` is false.
 */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import TierChip from '@/components/TierChip.vue'
import { escalate } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: Pick<S['Ticket'], 'id' | 'tier' | 'team_id'>; direction: 'escalate' | 'deescalate'; teams?: S['Team'][] }>()
const emit = defineEmits<{ done: [S['TicketUpdateResult']]; cancel: [] }>()
const model = reactive({ reason: '', target_team_id: null as string | null })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const target = computed(() => Math.min(3, Math.max(1, props.ticket.tier + (props.direction === 'escalate' ? 1 : -1))))

const onSubmit = submit(async () => {
  const body: S['EscalateRequest'] = { reason: model.reason.trim(), ...(model.target_team_id ? { target_team_id: model.target_team_id } : {}) }
  emit('done', await escalate(props.ticket.id, body, props.direction))
})
</script>

<template>
  <v-form ref="formRef" data-testid="escalate-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <div class="d-flex align-center ga-2 mb-4">
      <span class="text-medium-emphasis">{{ $t('agent.ticket.escalate.targetTier') }}</span>
      <TierChip :tier="ticket.tier" />
      <span aria-hidden="true">→</span>
      <TierChip :tier="target" data-testid="escalate-target" />
    </div>
    <AutocompleteSearch
      v-model="model.target_team_id"
      :items="teams ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.ticket.escalate.targetTeam')"
      :hint="$t('agent.ticket.escalate.targetTeamHint')"
      persistent-hint
      clearable
      class="mb-2"
    />
    <v-textarea
      v-model="model.reason"
      :label="$t('agent.ticket.escalate.reason')"
      :rules="[validations.required.any, validations.maxLength(1000)]"
      :error-messages="fieldErrors.reason"
      rows="3"
      auto-grow
      data-testid="escalate-reason"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="escalate-submit">
        {{ direction === 'escalate' ? $t('agent.ticket.escalate.escalate') : $t('agent.ticket.escalate.deescalate') }}
      </v-btn>
    </div>
  </v-form>
</template>
