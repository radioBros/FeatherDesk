<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import PasswordField from '@/components/PasswordField.vue'
/**
 * Create local staff / edit staff (FE-027). For an `idp_managed` user the role
 * and tier come from the identity provider: the selects are disabled and the
 * fields are left out of the PATCH body. Disabling a user asks for
 * confirmation because it revokes their sessions.
 */
import { computed, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import { useForm } from '@/composables/useForm'
import { useConfirm } from '@/composables/useConfirm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import { useConfigStore } from '@/stores/config'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ staff?: S['User'] | null; teams?: S['Team'][] }>()
const emit = defineEmits<{ saved: [S['User']]; cancel: [] }>()
const { t } = useI18n()
const config = useConfigStore()
const { confirm } = useConfirm()

const editing = computed(() => !!props.staff)
const managed = computed(() => !!props.staff?.idp_managed)
const ROLES: S['StaffRole'][] = ['agent', 'supervisor', 'admin']
const model = reactive({
  name: props.staff?.name ?? '',
  email: props.staff?.email ?? '',
  role: ((props.staff?.role as S['StaffRole'] | undefined) ?? 'agent') as S['StaffRole'],
  tier: props.staff?.tier ?? 1,
  team_ids: [...(props.staff?.team_ids ?? [])] as string[],
  status: (props.staff?.status ?? 'active') as S['UserStatus'],
  break_glass: props.staff?.break_glass ?? false,
  password: '',
  send_invite: false,
})
const tierItems = computed(() => [1, 2, 3].map((n) => ({ value: n, title: config.tierLabel(n) })))
const roleItems = computed(() => ROLES.map((r) => ({ value: r, title: t(`agent.staff.roles.${r}`) })))
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

const onSave = submit(async () => {
  let saved: S['User']
  if (props.staff) {
    if (model.status === 'disabled' && props.staff.status !== 'disabled') {
      const ok = await confirm({ title: t('agent.staff.disableTitle'), body: t('agent.staff.disableBody', { name: props.staff.name }), color: 'error' })
      if (!ok) return
    }
    const body: S['StaffPatch'] = {
      name: model.name.trim(),
      team_ids: model.team_ids,
      status: model.status,
      break_glass: model.break_glass,
    }
    if (!managed.value) {
      body.role = model.role
      body.tier = model.tier
    }
    saved = await unwrap(api.PATCH('/admin/staff/{id}', { params: { path: { id: props.staff.id } }, body }))
  } else {
    const body: S['StaffCreate'] = {
      name: model.name.trim(),
      email: model.email.trim(),
      role: model.role,
      tier: model.tier,
      team_ids: model.team_ids,
      break_glass: model.break_glass,
      send_invite: model.send_invite,
    }
    if (model.password) body.password = model.password
    saved = await unwrap(api.POST('/admin/staff', { body }))
  }
  emit('saved', saved)
})
</script>

<template>
  <v-form ref="formRef" data-testid="staff-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-alert v-if="managed" type="info" variant="tonal" density="compact" class="mb-3" data-testid="staff-idp-hint">
      {{ $t('agent.staff.idpManaged') }}
    </v-alert>
    <v-text-field
      v-model="model.name"
      :label="$t('agent.common.name')"
      :rules="[validations.required.any, validations.maxLength(200)]"
      :error-messages="fieldErrors.name"
      data-testid="staff-name"
    />
    <v-text-field
      v-model="model.email"
      :label="$t('agent.common.email')"
      type="email"
      :rules="editing ? [] : [validations.required.email]"
      :disabled="editing"
      :error-messages="fieldErrors.email"
      data-testid="staff-email"
    />
    <v-row density="compact">
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.role"
          :items="roleItems"
          :label="$t('agent.staff.role')"
          :rules="[validations.required.select]"
          :disabled="managed"
          :hint="managed ? $t('agent.staff.managedHint') : undefined"
          :persistent-hint="managed"
          :error-messages="fieldErrors.role"
          data-testid="staff-role"
        />
      </v-col>
      <v-col cols="12" sm="6">
        <v-select
          v-model="model.tier"
          :items="tierItems"
          :label="$t('tier.label')"
          :rules="[validations.required.select]"
          :disabled="managed"
          :hint="managed ? $t('agent.staff.managedHint') : $t('agent.staff.tierHint')"
          persistent-hint
          :error-messages="fieldErrors.tier"
          data-testid="staff-tier"
        />
      </v-col>
    </v-row>
    <v-select
      v-model="model.team_ids"
      :items="teams ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.staff.teams')"
      :error-messages="fieldErrors.team_ids"
      multiple
      chips
      closable-chips
      class="mt-2"
      data-testid="staff-teams"
    />
    <v-select
      v-if="editing"
      v-model="model.status"
      :items="[
        { value: 'active', title: $t('agent.common.active') },
        { value: 'disabled', title: $t('agent.common.disabled') },
      ]"
      :label="$t('agent.common.status')"
      :rules="[validations.required.select]"
      :error-messages="fieldErrors.status"
      data-testid="staff-status"
    />
    <template v-else>
      <PasswordField
        v-model="model.password"
        :label="$t('agent.staff.password')"
        :hint="$t('agent.staff.passwordHint')"
        persistent-hint
        autocomplete="new-password"
        :rules="[validations.password]"
        :error-messages="fieldErrors.password"
        data-testid="staff-password"
      />
      <v-switch v-model="model.send_invite" :label="$t('agent.staff.sendInvite')" color="primary" inset hide-details />
    </template>
    <v-switch v-model="model.break_glass" :label="$t('agent.staff.breakGlass')" :hint="$t('agent.staff.breakGlassHint')" persistent-hint color="primary" inset />
    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="staff-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>
