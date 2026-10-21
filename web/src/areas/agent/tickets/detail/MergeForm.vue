<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Merge this ticket into another (FE-055). Shows what will happen, and a
 * 409 `merge-cross-requester` as a translated explanation (the dialog stays open).
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { ApiProblem } from '@/api/problem'
import TicketSearchField from './TicketSearchField.vue'
import { merge } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: Pick<S['Ticket'], 'id' | 'number'> }>()
const emit = defineEmits<{ merged: [target: S['TicketSummary'], result: S['TicketUpdateResult']]; cancel: [] }>()
const { t } = useI18n()
const target = ref<S['TicketSummary'] | null>(null)
const reason = ref<string | null>(null)
const { formRef, submit, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  reason.value = null
  try {
    const res = await merge(props.ticket.id, target.value!.id)
    emit('merged', target.value!, res)
  } catch (e) {
    if (e instanceof ApiProblem && e.status === 409 && (e.slug === 'merge-cross-requester' || e.slug === 'link-cycle')) {
      reason.value = t(`agent.ticket.merge.problem.${e.slug === 'link-cycle' ? 'cycle' : 'crossRequester'}`)
      return
    }
    throw e
  }
})
</script>

<template>
  <v-form ref="formRef" data-testid="merge-form" @submit.prevent="onSubmit">
    <v-alert v-if="reason" type="error" variant="tonal" density="compact" class="mb-3" data-testid="merge-problem">{{ reason }}</v-alert>
    <v-alert v-else-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <TicketSearchField
      v-model="target"
      :label="$t('agent.ticket.merge.target')"
      :rules="[validations.required.any]"
      :exclude-id="ticket.id"
      testid="merge-target"
    />
    <v-alert v-if="target" type="warning" variant="tonal" density="compact" class="my-3" data-testid="merge-summary">
      {{ $t('agent.ticket.merge.summary', { source: ticket.number, target: target.number }) }}
    </v-alert>
    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="error" :loading="submitting" data-testid="merge-submit">{{ $t('agent.ticket.merge.confirm') }}</v-btn>
    </div>
  </v-form>
</template>
