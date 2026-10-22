<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
/** Members of one team (FE-028): add from the staff directory, remove; IdP-synced members are read-only. */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAccountPlus, mdiClose } from '@mdi/js'
import TierChip from '@/components/TierChip.vue'
import EmptyState from '@/components/EmptyState.vue'
import { api, unwrap } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../../shared/useAction'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ team: S['Team']; staff: S['User'][] }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const { confirm } = useConfirm()
const { run, busy } = useAction()
const members = ref<S['TeamMember'][]>([])
const loading = ref(true)
const failed = ref(false)
const pick = ref<string | null>(null)

const candidates = computed(() => {
  const ids = new Set(members.value.map((m) => m.user_id))
  return props.staff.filter((s) => !ids.has(s.id) && s.status === 'active').map((s) => ({ value: s.id, title: `${s.name} · ${s.email}` }))
})

async function load() {
  loading.value = true
  failed.value = false
  try {
    members.value = (await unwrap(api.GET('/admin/teams/{id}/members', { params: { path: { id: props.team.id } } }))) ?? []
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())
watch(() => props.team.id, () => void load())

async function add() {
  const user_id = pick.value
  if (!user_id) return
  const res = await run(() => unwrap(api.POST('/admin/teams/{id}/members', { params: { path: { id: props.team.id } }, body: { user_id } })), t('agent.teams.memberAdded'))
  if (res) {
    pick.value = null
    await load()
    emit('changed')
  }
}
async function remove(m: S['TeamMember']) {
  if (!(await confirm({ body: t('agent.teams.removeMember', { name: m.name, team: props.team.name }) }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/teams/{id}/members/{user_id}', { params: { path: { id: props.team.id, user_id: m.user_id } } }))
    return true
  })
  if (ok) {
    await load()
    emit('changed')
  }
}
</script>

<template>
  <div data-testid="team-members">
    <div class="d-flex ga-2 align-center mb-2">
      <AutocompleteSearch
        v-model="pick"
        :items="candidates"
        :label="$t('agent.teams.addMember')"
        density="compact"
        hide-details
        clearable
        data-testid="member-pick"
      />
      <v-btn :icon="mdiAccountPlus" color="primary" variant="tonal" :disabled="!pick" :loading="busy" :aria-label="$t('agent.teams.addMember')" data-testid="member-add" @click="add" />
    </div>
    <v-progress-linear v-if="loading" indeterminate />
    <v-alert v-if="failed" type="error" variant="tonal" density="compact">{{ $t('agent.common.loadFailed') }}</v-alert>
    <EmptyState v-else-if="!loading && !members.length" :title="$t('agent.teams.noMembers')" body="" />
    <v-list v-else density="compact">
      <v-list-item v-for="m in members" :key="m.user_id" :title="m.name" :subtitle="$t(`agent.staff.roles.${m.role}`)" data-testid="member-row">
        <template #append>
          <TierChip :tier="m.tier" class="mr-2" />
          <v-chip v-if="m.source === 'idp'" size="x-small" label data-testid="member-idp">{{ $t('agent.teams.fromIdp') }}</v-chip>
          <v-btn
            v-else
            :icon="mdiClose"
            size="small"
            variant="text"
            :aria-label="$t('agent.common.remove')"
            data-testid="member-remove"
            @click="remove(m)"
          />
        </template>
      </v-list-item>
    </v-list>
  </div>
</template>
