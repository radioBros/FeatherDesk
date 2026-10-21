<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Members of a project with their role (D-20). `PUT` replaces the whole list;
 * staff who leave lose the project's tickets at once. Read-only for project
 * admins (`editable` false). No form element: nothing here is typed free text.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAccountMinus } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { useAction } from '../shared/useAction'

type S = components['schemas']
const props = defineProps<{ projectId: string; members: S['ProjectMember'][]; staff?: S['User'][]; editable?: boolean }>()
const emit = defineEmits<{ saved: [S['ProjectMember'][]] }>()
const { t } = useI18n()
const { run, busy } = useAction()

interface Entry { user_id: string; name: string; email: string; role: S['ProjectRole'] }
const list = ref<Entry[]>(props.members.map((m) => ({ user_id: m.user_id, name: m.name, email: m.email, role: m.role })))
const adding = ref<string | null>(null)
const ROLES: S['ProjectRole'][] = ['member', 'project_admin']

const candidates = computed(() =>
  (props.staff ?? [])
    .filter((u) => u.status === 'active' && !list.value.some((m) => m.user_id === u.id))
    .map((u) => ({ value: u.id, title: `${u.name} (${u.email})` })),
)
function add(id: string | null) {
  const u = props.staff?.find((x) => x.id === id)
  if (u) list.value.push({ user_id: u.id, name: u.name, email: u.email, role: 'member' })
  adding.value = null
}
function remove(id: string) {
  list.value = list.value.filter((m) => m.user_id !== id)
}
async function save() {
  const saved = await run(
    () =>
      unwrap(
        api.PUT('/admin/projects/{id}/members', {
          params: { path: { id: props.projectId } },
          body: { members: list.value.map((m) => ({ user_id: m.user_id, role: m.role })) },
        }),
      ),
    t('agent.common.saved'),
  )
  if (saved) emit('saved', saved)
}
</script>

<template>
  <div data-testid="project-members">
    <v-alert v-if="!editable" type="info" variant="tonal" density="compact" class="mb-3">{{ $t('projects.admin.members.readOnly') }}</v-alert>
    <v-autocomplete
      v-if="editable"
      :model-value="adding"
      :items="candidates"
      :label="$t('projects.admin.members.add')"
      density="compact"
      clearable
      data-testid="project-member-add"
      @update:model-value="add"
    />
    <div v-if="!list.length" class="text-body-2 text-medium-emphasis pa-2" data-testid="project-members-empty">{{ $t('projects.admin.members.empty') }}</div>
    <v-table v-else density="compact">
      <thead>
        <tr>
          <th>{{ $t('agent.common.name') }}</th>
          <th>{{ $t('projects.admin.members.role') }}</th>
          <th v-if="editable" class="text-right">{{ $t('agent.common.actions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="m in list" :key="m.user_id" data-testid="project-member-row">
          <td>
            {{ m.name }}
            <div class="text-caption text-medium-emphasis">{{ m.email }}</div>
          </td>
          <td style="min-width: 180px">
            <v-select
              v-model="m.role"
              :items="ROLES.map((r) => ({ value: r, title: $t(`projects.admin.members.roles.${r}`) }))"
              :aria-label="$t('projects.admin.members.role')"
              :disabled="!editable"
              density="compact"
              hide-details
              data-testid="project-member-role"
            />
          </td>
          <td v-if="editable" class="text-right">
            <v-btn :icon="mdiAccountMinus" variant="text" size="small" :aria-label="$t('agent.common.remove')" data-testid="project-member-remove" @click="remove(m.user_id)" />
          </td>
        </tr>
      </tbody>
    </v-table>
    <div v-if="editable" class="d-flex justify-end mt-3">
      <v-btn color="primary" :loading="busy" data-testid="project-members-save" @click="save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </div>
</template>
