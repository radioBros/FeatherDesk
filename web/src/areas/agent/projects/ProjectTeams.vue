<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Team mapping of a project (D-20): which teams serve it. `PUT` replaces the
 * list; the project's default team has to stay in it (checked here, enforced
 * by the API). A checkbox list, no form element.
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { useAction } from '../shared/useAction'

type S = components['schemas']
const props = defineProps<{ projectId: string; teams: S['Team'][]; teamIds: string[]; defaultTeamId?: string | null; editable?: boolean }>()
const emit = defineEmits<{ saved: [string[]] }>()
const { t } = useI18n()
const { run, busy } = useAction()
const selected = ref<string[]>([...props.teamIds])
const error = ref<string | null>(null)

function toggle(id: string, on: boolean | null) {
  selected.value = on ? [...new Set([...selected.value, id])] : selected.value.filter((x) => x !== id)
}
async function save() {
  error.value = null
  if (props.defaultTeamId && !selected.value.includes(props.defaultTeamId)) {
    error.value = t('projects.admin.teams.defaultStays')
    return
  }
  const saved = await run(
    () => unwrap(api.PUT('/admin/projects/{id}/teams', { params: { path: { id: props.projectId } }, body: { team_ids: selected.value } })),
    t('agent.common.saved'),
  )
  if (saved) emit('saved', saved.team_ids ?? selected.value)
}
</script>

<template>
  <div data-testid="project-teams">
    <div class="text-body-2 text-medium-emphasis mb-2">{{ $t('projects.admin.teams.hint') }}</div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-2" data-testid="project-teams-error">{{ error }}</v-alert>
    <div v-if="!teams.length" class="text-body-2 text-medium-emphasis pa-2">{{ $t('agent.teams.empty') }}</div>
    <v-checkbox
      v-for="tm in teams"
      :key="tm.id"
      :model-value="selected.includes(tm.id)"
      :label="tm.id === defaultTeamId ? `${tm.name} (${$t('projects.admin.teams.default')})` : tm.name"
      :disabled="!editable"
      density="compact"
      hide-details
      :data-testid="`project-team-${tm.id}`"
      @update:model-value="(on: boolean | null) => toggle(tm.id, on)"
    />
    <div v-if="editable" class="d-flex justify-end mt-3">
      <v-btn color="primary" :loading="busy" data-testid="project-teams-save" @click="save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </div>
</template>
