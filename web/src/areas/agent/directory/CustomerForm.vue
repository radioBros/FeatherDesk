<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** Create / edit a customer (FE-026). Options come in as props; nothing loads on mount. */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { SUPPORTED_LOCALES } from '@/i18n'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ customer?: S['User'] | null; organizations?: S['Organization'][]; organizationId?: string | null }>()
const emit = defineEmits<{ saved: [S['User']]; cancel: [] }>()

const editing = computed(() => !!props.customer)
const model = reactive({
  name: props.customer?.name ?? '',
  email: props.customer?.email ?? '',
  organization_id: props.customer?.organization_id ?? props.organizationId ?? null,
  role: (props.customer?.role === 'org_manager' ? 'org_manager' : 'requester') as S['CustomerRole'],
  locale: (props.customer?.locale ?? null) as S['Locale'] | null,
  status: (props.customer?.status ?? 'active') as S['UserStatus'],
  send_invite: false,
})
const ROLES: S['CustomerRole'][] = ['requester', 'org_manager']
const STATUSES: S['UserStatus'][] = ['active', 'pending_verification', 'disabled']
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  let saved: S['User']
  if (props.customer) {
    const body: S['CustomerPatch'] = {
      name: model.name.trim(),
      organization_id: model.organization_id,
      role: model.role,
      status: model.status,
      ...(model.locale ? { locale: model.locale } : {}),
    }
    saved = await unwrap(api.PATCH('/agent/customers/{id}', { params: { path: { id: props.customer.id } }, body }))
  } else {
    const body: S['CustomerCreate'] = {
      name: model.name.trim(),
      email: model.email.trim(),
      organization_id: model.organization_id,
      role: model.role,
      send_invite: model.send_invite,
      ...(model.locale ? { locale: model.locale } : {}),
    }
    saved = await unwrap(api.POST('/agent/customers', { body }))
  }
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="customer-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.name"
      data-testid="customer-name"
    />
    <v-text-field
      v-model="model.email"
      :label="$t('agent.common.email')"
      type="email"
      :rules="editing ? [] : [validations.required.email]"
      :disabled="editing"
      :error-messages="fieldErrors.email"
      data-testid="customer-email"
    />
    <v-select
      v-model="model.organization_id"
      :items="organizations ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.customers.organization')"
      :error-messages="fieldErrors.organization_id"
      clearable
      data-testid="customer-org"
    />
    <v-select
      v-model="model.role"
      :items="ROLES.map((r) => ({ value: r, title: $t(`agent.customers.roles.${r}`) }))"
      :label="$t('agent.customers.role')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.role"
    />
    <v-select
      v-model="model.locale"
      :items="SUPPORTED_LOCALES.map((l) => ({ value: l, title: l.toUpperCase() }))"
      :label="$t('agent.customers.locale')"
      clearable
    />
    <v-select
      v-if="editing"
      v-model="model.status"
      :items="STATUSES.map((s) => ({ value: s, title: $t(`agent.customers.statuses.${s}`) }))"
      :label="$t('agent.common.status')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.status"
    />
    <v-switch v-else v-model="model.send_invite" :label="$t('agent.customers.sendInvite')" color="primary" inset data-testid="customer-invite" />
    <div class="d-flex justify-end ga-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="customer-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
