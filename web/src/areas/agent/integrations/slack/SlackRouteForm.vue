<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Create / edit a Slack rule (D-21, INT-014): channel picker, what to post
 * (presets + grouped chips), filters, options, and a live preview. Save and
 * Test go through `useForm().submit`, so `validate()` runs first. Channels and
 * projects come from the page (nothing is fetched on mount).
 */
import { computed, reactive, ref, watch } from 'vue'
import { mdiLockOutline, mdiPound, mdiRefresh } from '@mdi/js'
import { useForm } from '@/composables/useForm'
import { validations } from '@/constants/validations'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import AutocompleteSearch from '@/components/AutocompleteSearch.vue'
import ProjectMultiSelect from '@/components/ProjectMultiSelect.vue'
import type { ProjectOption } from '@/components/ProjectSelect.vue'
import SlackPreview from './SlackPreview.vue'
import { PRESETS, PRIORITIES, TRIGGER_GROUPS, presetEvents, type Trigger } from './triggers'

type S = components['schemas']
const props = defineProps<{
  route?: S['SlackRoute'] | null
  channels?: S['SlackChannel'][]
  channelsLoading?: boolean
  channelsFailed?: boolean
  projects?: ProjectOption[]
  appName?: string
  preset?: string
}>()
const emit = defineEmits<{ saved: [S['SlackRoute']]; cancel: []; refresh: [] }>()

const model = reactive({
  channel_id: props.route?.channel_id ?? '',
  events: [...(props.route?.events ?? (props.preset ? presetEvents(props.preset, false) : []))] as Trigger[],
  project_ids: [...(props.route?.project_ids ?? [])] as string[],
  priorities: [...(props.route?.priorities ?? [])] as string[],
  threads: props.route?.threads ?? true,
  include_body: props.route?.include_body ?? true,
  include_internal: props.route?.include_internal ?? false,
  active: props.route?.active ?? true,
})
const testResult = ref<S['SlackTestResult'] | null>(null)
const { formRef, submit, fieldErrors, formError, submitting } = useForm()

/** The list, plus the saved channel when Slack did not return it (load failed, bot removed). */
const items = computed(() => {
  const list = [...(props.channels ?? [])]
  if (props.route && !list.some((c) => c.id === props.route!.channel_id)) {
    list.unshift({ id: props.route.channel_id, name: props.route.channel_name, is_private: props.route.channel_private, is_member: true, num_members: 0 })
  }
  return list
})
const channel = computed(() => items.value.find((c) => c.id === model.channel_id))

watch(
  () => model.include_internal,
  (on) => {
    if (!on) model.events = model.events.filter((e) => e !== 'note.created')
  },
)

function applyPreset(key: string) {
  model.events = presetEvents(key, model.include_internal)
}
const activePreset = computed(() => {
  const cur = [...model.events].sort().join()
  return PRESETS.find((p) => presetEvents(p.key, model.include_internal).sort().join() === cur)?.key
})

/** v-model of one chip group: its slice of model.events. */
function groupModel(values: Trigger[]) {
  return computed({
    get: () => model.events.filter((e) => values.includes(e)),
    set: (v: Trigger[]) => (model.events = [...model.events.filter((e) => !values.includes(e)), ...v]),
  })
}
const groups = TRIGGER_GROUPS.map((g) => ({ ...g, model: groupModel(g.items.map((i) => i.value)) }))

const body = (): S['SlackRouteInput'] => ({
  channel_id: model.channel_id,
  channel_name: channel.value?.name ?? props.route?.channel_name ?? '',
  channel_private: channel.value?.is_private ?? false,
  events: model.events,
  priorities: model.priorities as S['Priority'][],
  threads: model.threads,
  include_body: model.include_body,
  include_internal: model.include_internal,
  active: model.active,
  ...(props.projects && props.projects.length > 1 ? { project_ids: model.project_ids } : {}),
})

const onSave = submit(async () => {
  if (props.route) {
    emit('saved', await unwrap(api.PATCH('/admin/integrations/slack/routes/{id}', { params: { path: { id: props.route.id } }, body: body() })))
  } else {
    emit('saved', await unwrap(api.POST('/admin/integrations/slack/routes', { body: body() })))
  }
})

const onTest = submit(async () => {
  if (!props.route) return
  testResult.value = null
  testResult.value = await unwrap(api.POST('/admin/integrations/slack/routes/{id}/test', { params: { path: { id: props.route.id } } }))
})
</script>

<template>
  <v-form ref="formRef" data-testid="slack-route-form" @submit.prevent="onSave">
    <v-alert v-if="formError" type="error" variant="tonal" density="compact" class="mb-3">{{ formError }}</v-alert>
    <v-row>
      <v-col cols="12" md="7">
        <AutocompleteSearch
          v-model="model.channel_id"
          :items="items"
          item-title="name"
          item-value="id"
          :label="$t('agent.integrations.slack.form.channel')"
          :hint="$t('agent.integrations.slack.form.channelHint')"
          persistent-hint
          :loading="channelsLoading"
          :rules="[validations.required.any]"
          :error-messages="fieldErrors.channel_id"
          :prepend-inner-icon="channel?.is_private ? mdiLockOutline : mdiPound"
          :append-icon="mdiRefresh"
          data-testid="slack-route-channel"
          @click:append="emit('refresh')"
        >
          <template #item="{ props: itemProps, item }">
            <v-list-item v-bind="itemProps" :prepend-icon="item.is_private ? mdiLockOutline : mdiPound" :data-testid="`slack-channel-${item.name}`">
              <template #subtitle>
                <span v-if="item.num_members">{{ $t('agent.integrations.slack.form.members', { n: item.num_members }) }}</span>
              </template>
              <template #append>
                <v-chip v-if="item.is_private && !item.is_member" size="x-small" label color="warning">{{ $t('agent.integrations.slack.form.inviteBot') }}</v-chip>
              </template>
            </v-list-item>
          </template>
        </AutocompleteSearch>
        <v-alert v-if="channelsFailed" type="warning" variant="tonal" density="compact" class="mt-2">{{ $t('agent.integrations.slack.form.channelsFailed') }}</v-alert>

        <div class="text-subtitle-2 mt-5 mb-1">{{ $t('agent.integrations.slack.form.events') }}</div>
        <div class="d-flex flex-wrap ga-2 mb-2" data-testid="slack-presets">
          <v-chip
            v-for="p in PRESETS"
            :key="p.key"
            size="small"
            :variant="activePreset === p.key ? 'flat' : 'outlined'"
            :color="activePreset === p.key ? 'primary' : undefined"
            :data-testid="`slack-preset-${p.key}`"
            @click="applyPreset(p.key)"
          >
            {{ $t(`agent.integrations.slack.form.presets.${p.key}`) }}
          </v-chip>
        </div>
        <v-input :model-value="model.events" :rules="[validations.required.array]" :error-messages="fieldErrors.events" hide-details="auto" data-testid="slack-route-events">
          <div class="w-100">
            <div v-for="g in groups" :key="g.key" class="mb-1">
              <div class="text-caption text-medium-emphasis">{{ $t(`agent.integrations.slack.form.groups.${g.key}`) }}</div>
              <v-chip-group v-model="g.model.value" multiple column selected-class="text-primary">
                <v-chip
                  v-for="i in g.items"
                  :key="i.value"
                  :value="i.value"
                  :prepend-icon="i.icon"
                  filter
                  variant="outlined"
                  size="small"
                  :disabled="i.value === 'note.created' && !model.include_internal"
                  :data-testid="`slack-trigger-${i.key}`"
                >
                  {{ $t(`agent.integrations.slack.triggers.${i.key}`) }}
                </v-chip>
              </v-chip-group>
            </div>
            <div v-if="!model.include_internal" class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.form.noteNeedsInternal') }}</div>
          </div>
        </v-input>

        <div class="text-subtitle-2 mt-5 mb-1">{{ $t('agent.integrations.slack.form.filters') }}</div>
        <div class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.form.priorities') }}</div>
        <v-chip-group v-model="model.priorities" multiple column selected-class="text-primary" data-testid="slack-route-priorities">
          <v-chip v-for="p in PRIORITIES" :key="p" :value="p" filter variant="outlined" size="small">{{ $t(`priority.${p}`) }}</v-chip>
        </v-chip-group>
        <div class="text-caption text-medium-emphasis mb-3">{{ $t('agent.integrations.slack.form.prioritiesHint') }}</div>
        <ProjectMultiSelect
          v-if="projects && projects.length > 1"
          v-model="model.project_ids"
          :projects="projects"
          :label="$t('projects.config.projects')"
          :hint="$t('agent.integrations.slack.form.projectsHint')"
          :error-messages="fieldErrors.project_ids"
          testid="slack-route-projects"
        />

        <div class="text-subtitle-2 mt-4">{{ $t('agent.integrations.slack.form.options') }}</div>
        <v-switch v-model="model.threads" :label="$t('agent.integrations.slack.form.threads')" :hint="$t('agent.integrations.slack.form.threadsHint')" persistent-hint color="primary" inset class="mb-2" data-testid="slack-route-threads" />
        <v-switch v-model="model.include_body" :label="$t('agent.integrations.slack.form.includeBody')" :hint="$t('agent.integrations.slack.form.includeBodyHint')" persistent-hint color="primary" inset class="mb-2" />
        <v-switch v-model="model.include_internal" :label="$t('agent.integrations.slack.form.includeInternal')" color="warning" inset hide-details data-testid="slack-route-internal" />
        <v-alert v-if="model.include_internal" type="warning" variant="tonal" density="compact" class="my-2">{{ $t('agent.integrations.slack.form.includeInternalWarning') }}</v-alert>
        <v-switch v-model="model.active" :label="$t('agent.common.active')" color="primary" inset hide-details />
      </v-col>
      <v-col cols="12" md="5">
        <div class="preview-sticky">
          <div class="text-subtitle-2 mb-2">{{ $t('agent.integrations.slack.form.preview') }}</div>
          <SlackPreview
            :events="model.events"
            :channel="channel?.name ?? ''"
            :threads="model.threads"
            :include-body="model.include_body"
            :priority="model.priorities.includes('urgent') || !model.priorities.length ? 'urgent' : model.priorities[0]!"
            :app-name="appName ?? 'FeatherDesk'"
          />
        </div>
      </v-col>
    </v-row>

    <v-alert v-if="testResult" :type="testResult.ok ? 'success' : 'error'" variant="tonal" density="compact" class="my-2" data-testid="slack-test-result">
      {{
        testResult.ok
          ? $t('agent.integrations.slack.rules.testOk', { channel: channel?.name ?? '' })
          : $t('agent.integrations.slack.rules.testFailed', { reason: testResult.error ?? '' })
      }}
    </v-alert>
    <div class="d-flex flex-wrap justify-end ga-2 mt-3">
      <v-btn variant="text" @click="emit('cancel')">{{ $t('app.cancel') }}</v-btn>
      <v-btn v-if="route" variant="tonal" :loading="submitting" data-testid="slack-route-test" @click="onTest()">{{ $t('agent.integrations.slack.rules.test') }}</v-btn>
      <v-btn type="submit" color="primary" :loading="submitting" data-testid="slack-route-save">{{ $t('agent.common.save') }}</v-btn>
    </div>
  </v-form>
</template>

<style scoped>
.preview-sticky {
  position: sticky;
  top: 16px;
}
</style>
