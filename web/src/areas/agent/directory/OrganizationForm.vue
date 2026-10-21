<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Create / edit an organization (FE-026); each email-domain chip must be a valid domain. */
import { reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { eachItem } from './rules'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ organization?: S['Organization'] | null }>()
const emit = defineEmits<{ saved: [S['Organization']]; cancel: [] }>()

const model = reactive({
  name: props.organization?.name ?? '',
  email_domains: [...(props.organization?.email_domains ?? [])] as string[],
  notes: props.organization?.notes ?? '',
  status: (props.organization?.status ?? 'active') as 'active' | 'archived',
})
const domainRules = [eachItem(validations.domain)]
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

function normalise(list: string[]) {
  return [...new Set(list.map((d) => d.trim().toLowerCase()).filter(Boolean))]
}

const onSave = submit(async () => {
  const base = { name: model.name.trim(), email_domains: normalise(model.email_domains), notes: model.notes }
  const saved = props.organization
    ? await unwrap(
        api.PATCH('/agent/organizations/{id}', {
          params: { path: { id: props.organization.id } },
          body: { ...base, status: model.status } satisfies S['OrganizationPatch'],
        }),
      )
    : await unwrap(api.POST('/agent/organizations', { body: base satisfies S['OrganizationCreate'] }))
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="organization-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.name"
      data-testid="org-name"
    />
    <v-combobox
      v-model="model.email_domains"
      :label="$t('agent.organizations.domains')"
      :hint="$t('agent.organizations.domainsHint')"
      persistent-hint
      multiple
      chips
      closable-chips
      :rules="domainRules"
      :error-messages="fieldErrors.email_domains"
      data-testid="org-domains"
    />
    <v-textarea
      v-model="model.notes"
      :label="$t('agent.organizations.notes')"
      :rules="[validations.maxLength(5000)]"
      :error-messages="fieldErrors.notes"
      rows="3"
      auto-grow
      class="mt-2"
    />
    <v-select
      v-if="organization"
      v-model="model.status"
      :items="[
        { value: 'active', title: $t('agent.common.active') },
        { value: 'archived', title: $t('agent.organizations.archived') },
      ]"
      :label="$t('agent.common.status')"
      :rules="[validations.required.select]"
    />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="org-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
