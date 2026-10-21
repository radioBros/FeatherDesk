<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Move a ticket to another project (D-20). Needs the `transfer` action; the
 * server re-checks membership, clears a category of the old project and picks
 * a team that serves the target. A 422 (`not_allowed`, `wrong_project`) shows
 * on the select. Emits the `TicketUpdateResult` (`visible: false` = handed over).
 */
import { computed, reactive } from 'vue'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { useProjectsStore } from '@/stores/projects'
import ProjectChip from '@/components/ProjectChip.vue'
import { patchTicket } from './ticketApi'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ ticket: Pick<S['Ticket'], 'id' | 'ref' | 'project_id'>; etag?: string | null }>()
const emit = defineEmits<{ moved: [S['TicketUpdateResult'], string | null]; cancel: [] }>()
const projects = useProjectsStore()
const model = reactive({ project_id: null as string | null })
const { formRef, submit, fieldErrors, formError, submitting } = useForm()
const targets = computed(() => projects.active.filter((p) => p.id !== props.ticket.project_id))

const onSubmit = submit(async () => {
  const { result, etag } = await patchTicket(props.ticket.id, { project_id: model.project_id! }, props.etag ?? null)
  emit('moved', result, etag)
})
</script>

<template>
  <v-form ref="formRef" data-testid="move-project-form" @submit.prevent="onSubmit">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-select
      v-model="model.project_id"
      :items="targets"
      item-title="name"
      item-value="id"
      :label="$t('projects.ticket.move.target')"
      :hint="$t('projects.ticket.move.hint', { ref: ticket.ref })"
      persistent-hint
      :rules="[validations.required.any]"
      :error-messages="fieldErrors.project_id"
      data-testid="move-target"
    >
      <template #item="{ item, props: p }">
        <v-list-item v-bind="p" :title="item.name"><template #prepend><ProjectChip :project="item" size="small" class="mr-2" /></template></v-list-item>
      </template>
    </v-select>
    <div class="d-flex justify-end ga-2 mt-2">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="move-submit">{{ $t('projects.ticket.move.confirm') }}</v-btn>
    </div>
  </v-form>
</template>
