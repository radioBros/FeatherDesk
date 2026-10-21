<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Add a ticket link (FE-055): target ticket + link type, both required. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import TicketSearchField from './TicketSearchField.vue'
import { addLink } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticketId: string }>()
const emit = defineEmits<{ saved: [S['TicketLink']]; cancel: [] }>()
const TYPES: S['LinkType'][] = ['parent_of', 'related_to', 'duplicate_of']
const model = reactive({ target: null as S['TicketSummary'] | null, type: null as S['LinkType'] | null })
const { formRef, submit, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  emit('saved', await addLink(props.ticketId, { to_ticket_id: model.target!.id, type: model.type! }))
})
</script>

<template>
  <v-form ref="formRef" data-testid="link-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <TicketSearchField
      v-model="model.target"
      :label="$t('agent.ticket.links.target')"
      :rules="[validations.required.any]"
      :exclude-id="ticketId"
      testid="link-target"
    />
    <v-select
      v-model="model.type"
      :items="TYPES.map((t) => ({ value: t, title: $t(`agent.ticket.links.type.${t}`) }))"
      :label="$t('agent.ticket.links.typeLabel')"
      :rules="[validations.required.select]"
      class="mt-2"
      data-testid="link-type"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="link-submit">{{ $t('agent.ticket.links.add') }}</v-btn>
    </div>
  </v-form>
</template>
