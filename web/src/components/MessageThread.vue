<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed } from 'vue'
import { mdiLockOutline, mdiConnection } from '@mdi/js'
import type { Message } from '@/types'
import MarkdownView from './MarkdownView.vue'
import RelativeTime from './RelativeTime.vue'
import UserAvatar from './UserAvatar.vue'
import AttachmentList from './AttachmentList.vue'
import EmptyState from './EmptyState.vue'

const props = defineProps<{
  messages: Message[]
  area: 'portal' | 'agent'
  /** Agent only: checkboxes on public/internal messages (split, FE-055). */
  selectable?: boolean
  selected?: string[]
}>()
const emit = defineEmits<{ 'update:selected': [string[]] }>()

function toggle(id: string, on: boolean | null) {
  const cur = new Set(props.selected ?? [])
  if (on) cur.add(id)
  else cur.delete(id)
  emit('update:selected', [...cur])
}

/**
 * Second line of defence for I-2: the portal variant drops every non-public
 * message before render, even if an API bug ever returned one.
 */
const visible = computed(() =>
  props.area === 'portal' ? props.messages.filter((m) => m.kind === 'public') : props.messages,
)
</script>

<template>
  <div class="d-flex flex-column ga-3" role="log" :aria-label="$t('thread.label')" data-testid="message-thread">
    <EmptyState v-if="!visible.length" :title="$t('thread.empty')" body="" />
    <template v-for="m in visible" :key="m.id">
      <div
        v-if="m.kind === 'system'"
        class="text-center text-caption text-medium-emphasis"
        role="article"
        :aria-label="$t('thread.system')"
        data-testid="msg-system"
      >
        <MarkdownView :source="m.body_md" /> · <RelativeTime :value="m.created_at" />
      </div>
      <v-card
        v-else
        :color="m.kind === 'internal' ? 'internal-note' : 'surface-light'"
        variant="flat"
        :class="['fd-msg', m.author?.kind === 'customer' ? 'fd-msg--customer' : 'fd-msg--staff']"
        :data-testid="m.kind === 'internal' ? 'msg-internal' : 'msg-public'"
        role="article"
        :aria-label="(m.kind === 'internal' ? $t('thread.internalNote') + ' · ' : '') + (m.author?.name ?? $t('user.unknown'))"
      >
        <v-card-item>
          <template #prepend>
            <v-checkbox-btn
              v-if="selectable && area === 'agent'"
              :model-value="(selected ?? []).includes(m.id)"
              :aria-label="$t('thread.label') + ' · ' + (m.author?.name ?? '')"
              data-testid="msg-select"
              @update:model-value="(v: boolean | null) => toggle(m.id, v)"
            />
            <UserAvatar :name="m.author?.name" />
          </template>
          <template v-if="$slots.actions" #append><slot name="actions" :message="m" /></template>
          <v-card-title class="text-subtitle-2">
            {{ m.author?.name ?? $t('user.unknown') }}
            <v-chip v-if="m.kind === 'internal'" size="x-small" class="ml-2" :prepend-icon="mdiLockOutline" label>
              {{ $t('thread.internalNote') }}
            </v-chip>
            <!-- I-12: staff only; the portal never renders "via" -->
            <v-chip
              v-if="area === 'agent' && m.via_integration"
              size="x-small"
              class="ml-2"
              :prepend-icon="mdiConnection"
              label
              data-testid="msg-via"
            >
              {{ m.via_integration_name ? $t('thread.via', { name: m.via_integration_name }) : $t('thread.viaIntegration') }}
            </v-chip>
          </v-card-title>
          <v-card-subtitle><RelativeTime :value="m.created_at" /></v-card-subtitle>
        </v-card-item>
        <v-card-text>
          <MarkdownView :source="m.body_md" />
          <AttachmentList :attachments="m.attachments ?? []" :area="area" />
        </v-card-text>
      </v-card>
    </template>
  </div>
</template>

<style scoped>
.fd-msg--customer { margin-right: 10%; }
.fd-msg--staff { margin-left: 10%; }
@media (max-width: 600px) {
  .fd-msg--customer, .fd-msg--staff { margin: 0; }
}
</style>
