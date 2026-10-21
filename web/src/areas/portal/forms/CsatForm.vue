<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { CustomerTicket } from '../types'

/** Satisfaction rating 1–5 + optional comment (FE-036). Works with a session or an emailed `csat_token`. */
const props = defineProps<{ ticketId: string; score?: number | null; csatToken?: string | null }>()
const emit = defineEmits<{ rated: [ticket: CustomerTicket | null] }>()
const score = ref<number | null>(props.score ?? null)
const comment = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const SCORES = [1, 2, 3, 4, 5] as const

const onSubmit = submit(async () => {
  const res = await unwrap(
    api.POST('/portal/tickets/{id}/csat', {
      params: { path: { id: props.ticketId } },
      body: {
        score: score.value!,
        ...(comment.value.trim() ? { comment: comment.value.trim() } : {}),
        ...(props.csatToken ? { csat_token: props.csatToken } : {}),
      },
    }),
  )
  emit('rated', (res as CustomerTicket | undefined) ?? null)
})
</script>

<template>
  <v-form ref="formRef" data-testid="csat-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-radio-group
      v-model="score"
      :label="$t('portal.csat.question')"
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.score"
      inline
      data-testid="csat-score"
    >
      <v-radio v-for="s in SCORES" :key="s" :value="s" :label="$t(`portal.csat.score${s}`)" :data-testid="`csat-score-${s}`" />
    </v-radio-group>
    <v-textarea
      v-model="comment"
      :label="$t('portal.csat.comment')"
      :rules="[validations.maxLength(2000)]"
      :error-messages="fieldErrors.comment"
      rows="2"
      auto-grow
      counter="2000"
      data-testid="csat-comment"
    />
    <v-btn type="submit" color="primary" :loading="submitting" data-testid="csat-submit">{{ $t('portal.csat.submit') }}</v-btn>
  </v-form>
</template>
