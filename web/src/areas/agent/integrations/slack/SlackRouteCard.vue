<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** One Slack rule (D-21, INT-014): channel, what it posts, filters, state, actions. */
import { computed } from 'vue'
import { mdiDelete, mdiDotsVertical, mdiLockOutline, mdiPencil, mdiPound, mdiSend } from '@mdi/js'
import type { components } from '@/api/schema'
import RelativeTime from '@/components/RelativeTime.vue'
import { triggerDef } from './triggers'

type S = components['schemas']
const props = defineProps<{ route: S['SlackRoute']; projectName: (id: string) => string; botName: string; busy?: boolean }>()
const emit = defineEmits<{ edit: []; test: []; remove: []; toggle: [boolean] }>()

const shown = computed(() => props.route.events.slice(0, 4).map((e) => triggerDef(e)).filter((d) => !!d))
const more = computed(() => Math.max(0, props.route.events.length - 4))
const errorKey = computed(() => {
  const e = props.route.last_error
  if (!e) return null
  return ['not_in_channel', 'channel_not_found', 'is_archived'].includes(e) ? e : 'other'
})
</script>

<template>
  <v-card border flat class="h-100 d-flex flex-column" :class="{ 'opacity-60': !route.active }" data-testid="slack-route-card">
    <v-card-item>
      <template #prepend>
        <v-icon :icon="route.channel_private ? mdiLockOutline : mdiPound" />
      </template>
      <v-card-title class="text-subtitle-1 font-weight-medium" data-testid="slack-route-channel-name">{{ route.channel_name || route.channel_id }}</v-card-title>
      <v-card-subtitle>
        <span v-if="route.last_posted_at">{{ $t('agent.integrations.slack.rules.lastPost', { when: '' }) }}<RelativeTime :value="route.last_posted_at" /></span>
        <span v-else>{{ $t('agent.integrations.slack.rules.noPostYet') }}</span>
      </v-card-subtitle>
      <template #append>
        <v-switch
          :model-value="route.active"
          :disabled="busy"
          color="primary"
          inset
          hide-details
          density="compact"
          :aria-label="$t('agent.common.active')"
          data-testid="slack-route-active"
          @update:model-value="(v: boolean | null) => emit('toggle', !!v)"
        />
        <v-menu>
          <template #activator="{ props: menuProps }">
            <v-btn v-bind="menuProps" :icon="mdiDotsVertical" variant="text" size="small" :aria-label="$t('agent.common.actions')" data-testid="slack-route-menu" />
          </template>
          <v-list density="compact">
            <v-list-item :prepend-icon="mdiPencil" :title="$t('agent.common.edit')" data-testid="slack-route-edit" @click="emit('edit')" />
            <v-list-item :prepend-icon="mdiSend" :title="$t('agent.integrations.slack.rules.test')" data-testid="slack-route-test-menu" @click="emit('test')" />
            <v-list-item :prepend-icon="mdiDelete" :title="$t('agent.common.delete')" base-color="error" data-testid="slack-route-delete" @click="emit('remove')" />
          </v-list>
        </v-menu>
      </template>
    </v-card-item>
    <v-card-text class="pt-0 flex-grow-1">
      <div class="d-flex flex-wrap ga-1 mb-2">
        <v-chip v-for="d in shown" :key="d!.value" size="small" label :prepend-icon="d!.icon">{{ $t(`agent.integrations.slack.triggers.${d!.key}`) }}</v-chip>
        <v-chip v-if="more" size="small" label variant="text">+{{ more }}</v-chip>
      </div>
      <div class="d-flex flex-wrap ga-1 text-caption">
        <v-chip size="x-small" variant="outlined">
          {{ route.project_ids.length ? route.project_ids.map(projectName).join(', ') : $t('agent.integrations.slack.rules.allProjects') }}
        </v-chip>
        <v-chip size="x-small" variant="outlined">
          {{ route.priorities.length ? route.priorities.map((p) => $t(`priority.${p}`)).join(', ') : $t('agent.integrations.slack.rules.allPriorities') }}
        </v-chip>
        <v-chip v-if="route.threads" size="x-small" variant="outlined">{{ $t('agent.integrations.slack.rules.threaded') }}</v-chip>
        <v-chip v-if="route.include_internal" size="x-small" color="warning" variant="tonal">{{ $t('agent.integrations.slack.rules.internal') }}</v-chip>
        <v-chip v-if="!route.active" size="x-small" variant="tonal">{{ $t('agent.integrations.slack.rules.paused') }}</v-chip>
      </div>
      <v-alert v-if="errorKey" type="warning" variant="tonal" density="compact" class="mt-3 text-body-2" data-testid="slack-route-error">
        {{ $t(`agent.integrations.slack.channelErrors.${errorKey}`, { bot: botName, code: route.last_error }) }}
      </v-alert>
    </v-card-text>
  </v-card>
</template>
