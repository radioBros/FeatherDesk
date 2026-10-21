<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAccountGroupOutline, mdiAccountPlusOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import UserAvatar from '@/components/UserAvatar.vue'
import OrgInviteForm from '../forms/OrgInviteForm.vue'
import type { CustomerRole, User, UserRef } from '../types'

/** `/organization/members` (FE-035): org managers list, invite, change role, disable. */
type Member = UserRef & Partial<Pick<User, 'email' | 'role' | 'status'>>
const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const members = ref<Member[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const inviteOpen = ref(false)
const busy = ref<string | null>(null)

async function load() {
  loading.value = true
  try {
    members.value = ((await unwrap(api.GET('/portal/organization/members'))) ?? []) as Member[]
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function invited(u: User) {
  inviteOpen.value = false
  members.value.push(u)
  ui.notify(t('portal.members.invited', { email: u.email }), 'success')
}

async function patch(m: Member, body: { role?: CustomerRole; status?: 'active' | 'disabled' }) {
  busy.value = m.id
  try {
    const u = await unwrap(api.PATCH('/portal/organization/members/{id}', { params: { path: { id: m.id } }, body }))
    Object.assign(m, u)
  } catch (e) {
    ui.notify(e instanceof ApiProblem ? e.userMessage : t('errors.generic'), 'error')
  } finally {
    busy.value = null
  }
}
const roleItems = () => (['requester', 'org_manager'] as CustomerRole[]).map((r) => ({ value: r, title: t(`portal.members.role.${r}`) }))
</script>

<template>
  <v-container class="py-6" style="max-width: 960px">
    <PageHeader :title="$t('portal.members.title')">
      <template #actions>
        <v-btn color="primary" :prepend-icon="mdiAccountPlusOutline" data-testid="invite-open" @click="inviteOpen = true">{{ $t('portal.members.invite') }}</v-btn>
      </template>
    </PageHeader>
    <v-alert v-if="error" type="error" variant="tonal" class="mb-4">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <EmptyState v-else-if="!members.length" :icon="mdiAccountGroupOutline" :title="$t('portal.members.empty')" body="" />
    <v-card v-else border flat>
      <v-list lines="two" data-testid="member-list">
        <v-list-item v-for="m in members" :key="m.id" data-testid="member-row">
          <template #prepend><UserAvatar :name="m.name" /></template>
          <v-list-item-title>
            {{ m.name }}
            <v-chip v-if="m.status === 'disabled'" size="x-small" class="ml-2" label>{{ $t('portal.members.disabled') }}</v-chip>
            <v-chip v-else-if="m.status === 'pending_verification'" size="x-small" class="ml-2" label>{{ $t('portal.members.pending') }}</v-chip>
          </v-list-item-title>
          <v-list-item-subtitle v-if="m.email">{{ m.email }}</v-list-item-subtitle>
          <template v-if="m.id !== session.me?.id" #append>
            <div class="d-flex align-center ga-2">
              <v-select
                v-if="m.role"
                :model-value="m.role"
                :items="roleItems()"
                :aria-label="$t('portal.members.roleLabel')"
                density="compact"
                hide-details
                style="width: 190px"
                :disabled="busy === m.id"
                @update:model-value="(r) => patch(m, { role: r as CustomerRole })"
              />
              <v-btn
                variant="text"
                :color="m.status === 'disabled' ? 'primary' : 'error'"
                :loading="busy === m.id"
                data-testid="member-toggle"
                @click="patch(m, { status: m.status === 'disabled' ? 'active' : 'disabled' })"
              >
                {{ m.status === 'disabled' ? $t('portal.members.enable') : $t('portal.members.disable') }}
              </v-btn>
            </div>
          </template>
        </v-list-item>
      </v-list>
    </v-card>
    <v-dialog v-model="inviteOpen" max-width="480">
      <v-card :title="$t('portal.members.inviteTitle')">
        <v-card-text><OrgInviteForm @invited="invited" /></v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>
