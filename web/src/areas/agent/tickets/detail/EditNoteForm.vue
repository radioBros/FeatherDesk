<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Edit one of my own internal notes within 15 minutes (FE-022; the API enforces the window). */
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import { editMessage } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticketId: string; message: Pick<S['Message'], 'id' | 'body_md'> }>()
const emit = defineEmits<{ saved: [S['Message']]; cancel: [] }>()
const body = ref(props.message.body_md ?? '')
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const onSubmit = submit(async () => emit('saved', await editMessage(props.ticketId, props.message.id, body.value)))
</script>

<template>
  <v-form ref="formRef" data-testid="edit-note-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <MarkdownEditor v-model="body" required :error-messages="fieldErrors.body_md" testid="edit-note-body" @submit="onSubmit" />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="edit-note-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
