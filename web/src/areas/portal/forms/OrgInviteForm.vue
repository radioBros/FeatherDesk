<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { CustomerRole, User } from '../types'

/** Invite a colleague into the organization (FE-035, org_manager only). */
const emit = defineEmits<{ invited: [user: User] }>()
const { t } = useI18n()
const model = reactive({ email: '', name: '', role: 'requester' as CustomerRole })
const roles = computed(() =>
  (['requester', 'org_manager'] as CustomerRole[]).map((r) => ({ value: r, title: t(`portal.members.role.${r}`) })),
)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSubmit = submit(async () => {
  const user = await unwrap(
    api.POST('/portal/organization/members', { body: { email: model.email.trim(), name: model.name.trim(), role: model.role } }),
  )
  model.email = model.name = ''
  model.role = 'requester'
  formRef.value?.resetValidation?.()
  emit('invited', user)
})
</script>

<template>
  <v-form ref="formRef" data-testid="org-invite-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3" data-testid="form-error">{{ formError }}</v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('portal.account.name')"
      :rules="[validations.required.any, validations.maxLength(120)]"
      :error-messages="fieldErrors.name"
      data-testid="invite-member-name"
    />
    <v-text-field
      v-model="model.email"
      :label="$t('auth.email')"
      :rules="[validations.required.email]"
      :error-messages="fieldErrors.email"
      type="email"
      data-testid="invite-member-email"
    />
    <v-select
      v-model="model.role"
      :items="roles"
      :label="$t('portal.members.roleLabel')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.role"
      data-testid="invite-member-role"
    />
    <v-btn type="submit" color="primary" :loading="submitting" data-testid="invite-member-submit">{{ $t('portal.members.invite') }}</v-btn>
  </v-form>
</template>
