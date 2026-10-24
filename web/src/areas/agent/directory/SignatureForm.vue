<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Staff email signature (FE-059): Markdown, at most 2000 characters, appended to public replies by email. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import MarkdownEditor from '@/components/MarkdownEditor.vue'

const props = defineProps<{ signature?: string }>()
const emit = defineEmits<{ saved: [string] }>()
const model = reactive({ body: props.signature ?? '' })
const signatureRules = [validations.maxLength(2000)]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  const res = await unwrap(api.PUT('/me/signature', { body: { signature_md: model.body } }))
  emit('saved', res?.signature_md ?? model.body)
})
</script>

<template>
  <v-form ref="formRef" data-testid="signature-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-input :model-value="model.body" :rules="signatureRules" :error-messages="fieldErrors.signature_md" data-testid="signature-input">
      <div class="w-100">
        <MarkdownEditor v-model="model.body" :label="$t('agent.profile.signature')" />
        <div class="text-caption text-medium-emphasis text-right">{{ model.body.length }} / 2000</div>
      </div>
    </v-input>
    <div class="d-flex justify-end mt-2">
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="signature-save">{{ $t('agent.profile.saveSignature') }}</v-btn>
    </div>
  </v-form>
</template>
