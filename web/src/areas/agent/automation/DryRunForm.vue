<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Dry-run of a saved rule against one ticket (FE-063, D-19 A): the ticket is
 * picked by number, then `POST /admin/automation/rules/{id}/dry-run` reports
 * each condition and what each action would do. Nothing is written.
 */
import { ref } from 'vue'
import { mdiCheckCircle, mdiCloseCircle } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { getByNumber } from '../tickets/detail/ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ rule?: S['AutomationRule'] | null }>()
const emit = defineEmits<{ close: [] }>()

const number = ref<string>('')
const result = ref<S['AutomationDryRun'] | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onRun = submit(async () => {
  if (!props.rule) return
  result.value = null
  const { ticket } = await getByNumber(Number(String(number.value).replace(/^#/, '')))
  result.value = await unwrap(
    api.POST('/admin/automation/rules/{id}/dry-run', { params: { path: { id: props.rule.id } }, body: { ticket_id: ticket.id } }),
  )
})
</script>

<template>
  <v-form ref="formRef" data-testid="dry-run-form" @submit.prevent="onRun">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <div class="d-flex ga-2 align-start">
      <v-text-field
        v-model="number"
        :label="$t('automation.dryRun.ticket')"
        :hint="$t('automation.dryRun.ticketHint')"
        persistent-hint
        :rules="[validations.required.any, validations.integer]"
        :error-messages="fieldErrors.ticket_id"
        inputmode="numeric"
        data-testid="dry-run-number"
      />
      <v-btn type="submit" color="primary" class="mt-2" :loading="submitting" data-testid="dry-run-submit">{{ $t('automation.dryRun.run') }}</v-btn>
    </div>
    <div v-if="result" class="mt-4" data-testid="dry-run-result">
      <v-alert :type="result.matched ? 'success' : 'info'" variant="tonal" density="compact" class="mb-3" data-testid="dry-run-verdict">
        {{ result.matched ? $t('automation.dryRun.matched') : $t('automation.dryRun.notMatched') }}
      </v-alert>
      <div v-if="result.conditions.length" class="text-subtitle-2 mb-1">{{ $t('automation.rules.conditions') }}</div>
      <v-list density="compact">
        <v-list-item
          v-for="c in result.conditions"
          :key="c.index"
          :prepend-icon="c.matched ? mdiCheckCircle : mdiCloseCircle"
          :title="`${c.index + 1}. ${$t(`automation.fields.${c.field}`)}`"
          :subtitle="c.matched ? $t('automation.dryRun.conditionMet') : $t('automation.dryRun.conditionNotMet')"
          data-testid="dry-run-condition"
        />
      </v-list>
      <template v-if="result.actions.length">
        <div class="text-subtitle-2 mb-1 mt-2">{{ $t('automation.rules.actions') }}</div>
        <v-list density="compact">
          <v-list-item
            v-for="(a, i) in result.actions"
            :key="i"
            :title="$t(`automation.actions.${a.type}`)"
            :subtitle="[$t(`automation.outcomes.${a.outcome}`), a.detail].filter(Boolean).join(' · ')"
            data-testid="dry-run-action"
          />
        </v-list>
      </template>
    </div>
    <div class="d-flex justify-end mt-2">
      <v-btn variant="text" @click="emit('close')">{{ $t('automation.dryRun.close') }}</v-btn>
    </div>
  </v-form>
</template>
