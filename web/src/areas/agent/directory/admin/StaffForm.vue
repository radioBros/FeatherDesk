<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import { optionOf } from '@/components/projectOption'
import SelectionChips from '@/components/SelectionChips.vue'
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
import type { ProjectOption } from '@/components/ProjectSelect.vue'
import ProjectChip from '@/components/ProjectChip.vue'
import { mdiClose, mdiPlus } from '@mdi/js'

type S = components['schemas']
const props = defineProps<{ staff?: S['User'] | null; teams?: S['Team'][]; projects?: ProjectOption[] }>()
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
  memberships: (props.staff?.projects ?? []).map((m) => ({ project_id: m.project_id as string, role: (m.role ?? 'member') as S['ProjectRole'] })),
})
const initialMemberships = JSON.stringify(model.memberships)
const PROJECT_ROLES: S['ProjectRole'][] = ['member', 'project_admin']
const roleOptions = computed(() => PROJECT_ROLES.map((r) => ({ value: r, title: t(`projects.config.${r}`) })))
/** Projects not yet used by another row (plus the row's own). */
const projectChoices = (own: string) => (props.projects ?? []).filter((p) => p.id === own || !model.memberships.some((m) => m.project_id === p.id))
function addMembership() {
  const free = (props.projects ?? []).find((p) => !model.memberships.some((m) => m.project_id === p.id))
  if (free) model.memberships.push({ project_id: free.id, role: 'member' })
}
const memberships = () => model.memberships.filter((m) => m.project_id)
const membershipsChanged = () => JSON.stringify(model.memberships) !== initialMemberships
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
    // Replaces the list: sent only when the editor changed it.
    if (membershipsChanged()) body.projects = memberships()
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
    if (memberships().length) body.projects = memberships()
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
        <AutocompleteSearch
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
        <AutocompleteSearch
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
    <AutocompleteSearch
      v-model="model.team_ids"
      :items="teams ?? []"
      item-title="name"
      item-value="id"
      :label="$t('agent.staff.teams')"
      :error-messages="fieldErrors.team_ids"
      multiple
      class="mt-2"
      data-testid="staff-teams">
      <template #selection="{ item, index }"><SelectionChips :item="item" :index="index" :total="(model.team_ids ?? []).length" /></template>
    </AutocompleteSearch>
    <fieldset v-if="projects?.length" class="st-memberships mt-2" data-testid="staff-projects">
      <legend class="text-subtitle-2">{{ $t('projects.config.memberships') }}</legend>
      <div class="text-caption text-medium-emphasis mb-2">{{ $t('projects.config.membershipsHint') }}</div>
      <div v-if="!model.memberships.length" class="text-body-2 text-medium-emphasis mb-2">{{ $t('projects.config.noMemberships') }}</div>
      <div v-for="(m, i) in model.memberships" :key="i" class="d-flex align-center ga-2" data-testid="staff-membership">
        <AutocompleteSearch
          v-model="m.project_id"
          :items="projectChoices(m.project_id)"
          item-title="name"
          item-value="id"
          :label="$t('projects.config.project')"
          density="compact"
          :rules="[validations.required.any]"
          data-testid="staff-membership-project"
        >
          <template #item="{ props: ip, item }">
            <v-list-item v-bind="ip" :title="optionOf(item).name"><template #prepend><ProjectChip :project="optionOf(item)" size="small" class="mr-2" /></template></v-list-item>
          </template>
        </AutocompleteSearch>
        <AutocompleteSearch
          v-model="m.role"
          :items="roleOptions"
          :label="$t('projects.config.role')"
          density="compact"
          :rules="[validations.required.select]"
          data-testid="staff-membership-role"
        />
        <v-btn :icon="mdiClose" variant="text" size="small" :aria-label="$t('projects.config.removeMembership')" data-testid="staff-membership-remove" @click="model.memberships.splice(i, 1)" />
      </div>
      <v-btn size="small" variant="text" :prepend-icon="mdiPlus" :disabled="model.memberships.length >= projects.length" data-testid="staff-membership-add" @click="addMembership">
        {{ $t('projects.config.addMembership') }}
      </v-btn>
      <div v-for="(msgs, key) in fieldErrors" :key="key">
        <div v-if="String(key).startsWith('projects')" class="text-caption text-error">{{ msgs.join(' ') }}</div>
      </div>
    </fieldset>
    <AutocompleteSearch
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
