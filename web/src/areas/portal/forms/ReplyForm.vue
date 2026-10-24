<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { ref } from 'vue'
import { useForm } from '@/composables/useForm'
import { api, newIdempotencyKey, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import MarkdownEditor from '@/components/MarkdownEditor.vue'
import AttachmentUploader from '@/components/AttachmentUploader.vue'
import { followUpIdFrom, type CustomerMessage } from '../types'

/**
 * Customer reply composer (FE-017). On a closed ticket the server opens a
 * follow-up ticket; the form reports its id so the page can navigate there.
 */
const props = defineProps<{ ticketId: string; closed?: boolean }>()
const emit = defineEmits<{ sent: [message: CustomerMessage]; followup: [ticketId: string] }>()
const body = ref('')
const attachments = ref<string[]>([])
const uploader = ref<InstanceType<typeof AttachmentUploader> | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
let idemKey = newIdempotencyKey()

function reset() {
  body.value = ''
  attachments.value = []
  uploader.value?.clear()
  formRef.value?.resetValidation?.()
  idemKey = newIdempotencyKey()
}

const onSubmit = submit(async () => {
  let msg: CustomerMessage
  try {
    msg = await unwrap(
      api.POST('/portal/tickets/{id}/messages', {
        params: { path: { id: props.ticketId }, header: { 'Idempotency-Key': idemKey } },
        body: { body_md: body.value, attachment_ids: attachments.value },
      }),
    )
  } catch (e) {
    const follow = e instanceof ApiProblem && e.status === 409 ? followUpIdFrom(e.body) : null
    if (follow) {
      reset()
      emit('followup', follow)
      return
    }
    throw e
  }
  reset()
  if (msg.ticket_id && msg.ticket_id !== props.ticketId) emit('followup', msg.ticket_id)
  else emit('sent', msg)
})
</script>

<template>
  <v-form ref="formRef" data-testid="reply-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <MarkdownEditor
      v-model="body"
      :label="$t('portal.detail.yourReply')"
      required
      :error-messages="fieldErrors.body_md"
      @files="(f: File[]) => uploader?.add(f)"
      @submit="onSubmit()"
    />
    <div class="d-flex flex-wrap align-center ga-2">
      <AttachmentUploader ref="uploader" v-model="attachments" area="portal" class="flex-grow-1" />
      <v-btn type="submit" color="primary" :loading="submitting" class="fd-reply-submit" data-testid="reply-submit">
        {{ closed ? $t('portal.detail.sendFollowUp') : $t('portal.detail.send') }}
      </v-btn>
    </div>
  </v-form>
</template>

<style scoped>
@media (max-width: 599.98px) {
  .fd-reply-submit { flex: 1 1 100%; max-width: 100%; }
}
</style>
