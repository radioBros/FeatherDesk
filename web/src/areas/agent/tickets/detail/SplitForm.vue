<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Split the selected messages into a new child ticket (FE-055). */
import { computed, ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { split } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = withDefaults(defineProps<{ ticketId: string; selected?: string[] }>(), { selected: () => [] })
const emit = defineEmits<{ split: [S['Ticket']]; cancel: [] }>()
const subject = ref('')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const selectedList = computed(() => props.selected)

const onSubmit = submit(async () => {
  emit('split', await split(props.ticketId, { subject: subject.value.trim(), message_ids: [...props.selected] }))
})
</script>

<template>
  <v-form ref="formRef" data-testid="split-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <!-- At least one message must be ticked in the thread. -->
    <v-input :model-value="selectedList" :rules="[validations.required.array]" data-testid="split-selected">
      <span class="text-body-2">{{ $t('agent.ticket.split.selected', { n: selected.length }, selected.length) }}</span>
    </v-input>
    <v-text-field
      v-model="subject"
      :label="$t('agent.ticket.split.subject')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.subject"
      data-testid="split-subject"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="split-submit">{{ $t('agent.ticket.split.confirm') }}</v-btn>
    </div>
  </v-form>
</template>
