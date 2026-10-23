<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Slack app credentials (D-21, INT-013): Client ID and the write-only Client
 * Secret (`PATCH /admin/integrations/slack`). Save goes through
 * `useForm().submit`, so `validate()` runs first.
 */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import PasswordWriteOnly from '../../shared/PasswordWriteOnly.vue'

type S = components['schemas']
const props = defineProps<{ clientId?: string; secretSet?: boolean }>()
const emit = defineEmits<{ saved: [S['SlackStatus']]; cancel: [] }>()

const model = reactive({ client_id: props.clientId ?? '', client_secret: '' })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const secretRules = computed(() => (props.secretSet ? [validations.maxLength(200)] : [validations.required.any, validations.maxLength(200)]))

const onSave = submit(async () => {
  const body: S['SlackAppUpdate'] = { client_id: model.client_id.trim() }
  if (model.client_secret) body.client_secret = model.client_secret.trim()
  emit('saved', await unwrap(api.PATCH('/admin/integrations/slack', { body })))
  model.client_secret = ''
})
</script>

<template>
  <v-form ref="formRef" data-testid="slack-app-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-row dense>
      <v-col cols="12" md="6">
        <v-text-field
          v-model="model.client_id"
          :label="$t('agent.integrations.slack.setup.clientId')"
          :hint="$t('agent.integrations.slack.setup.clientIdHint')"
          persistent-hint
          autocomplete="off"
          :rules="[validations.required.any, validations.slackClientId]"
          :error-messages="fieldErrors.client_id"
          data-testid="slack-client-id"
        />
      </v-col>
      <v-col cols="12" md="6">
        <PasswordWriteOnly
          v-model="model.client_secret"
          :label="$t('agent.integrations.slack.setup.clientSecret')"
          :password-set="secretSet"
          :rules="secretRules"
          :error-messages="fieldErrors.client_secret"
          testid="slack-client-secret"
        />
      </v-col>
    </v-row>
    <div class="d-flex justify-end ga-2 mt-2">
      <slot name="cancel" />
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="slack-app-save">{{ $t('agent.integrations.slack.setup.saveCredentials') }}</v-btn>
    </div>
  </v-form>
</template>
