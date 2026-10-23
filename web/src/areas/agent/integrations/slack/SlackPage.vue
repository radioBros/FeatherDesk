<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/integrations/slack (D-21, INT-013/014, admin).
 *
 * Not connected: three steps — create the app from the pre-filled manifest,
 * save its credentials, connect through the OAuth popup. Connected: the
 * workspace card (reconnect, credentials, disconnect, broken-token state) and
 * the notification rules. Connecting uses the shared OAuth popup
 * (../oauth: OAuthConnectButton, never a redirect of this tab).
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  mdiAlertOutline,
  mdiArrowLeft,
  mdiCheckCircle,
  mdiKeyOutline,
  mdiLinkVariantOff,
  mdiOpenInNew,
  mdiPlus,
  mdiRefresh,
  mdiSlack,
} from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import CopyButton from '../../shared/CopyButton.vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'
import { useConfirm } from '@/composables/useConfirm'
import { useUiStore } from '@/stores/ui'
import { useAction } from '../../shared/useAction'
import { useConfigProjects } from '../../shared/useConfigProjects'
import OAuthConnectButton from '../oauth/OAuthConnectButton.vue'
import type { OAuthOutcome } from '../oauth/oauthPopup'
import { useOAuthReason } from '../oauth/reasons'
import SlackAppForm from './SlackAppForm.vue'
import SlackRouteForm from './SlackRouteForm.vue'
import SlackRouteCard from './SlackRouteCard.vue'

type S = components['schemas']
const { t } = useI18n()
const reasonText = useOAuthReason()
const ui = useUiStore()
const { confirm } = useConfirm()
const { run, busy } = useAction()
const cp = useConfigProjects()

const status = ref<S['SlackStatus'] | null>(null)
const routes = ref<S['SlackRoute'][]>([])
const failed = ref(false)
const channels = ref<S['SlackChannel'][]>([])
const channelsLoading = ref(false)
const channelsFailed = ref(false)
const credsDialog = ref(false)
const ruleDialog = ref(false)
const editing = ref<S['SlackRoute'] | null>(null)
const preset = ref<string | undefined>()

const linked = computed(() => status.value?.state === 'connected' || status.value?.state === 'error')
const step = computed(() => (!status.value ? 1 : status.value.client_id && status.value.client_secret_set ? 3 : 1))
const manifestJson = computed(() => JSON.stringify(status.value?.manifest ?? {}, null, 2))
const appName = computed(() => String((status.value?.manifest as { display_information?: { name?: string } } | undefined)?.display_information?.name ?? 'FeatherDesk'))

async function load() {
  failed.value = false
  try {
    status.value = await unwrap(api.GET('/admin/integrations/slack'))
    if (linked.value) routes.value = (await unwrap(api.GET('/admin/integrations/slack/routes'))) ?? []
  } catch {
    failed.value = true
  }
}

/** Shared by the three connect buttons (setup, reconnect, broken token). */
const oauth = {
  provider: 'slack' as const,
  start: () => unwrap(api.POST('/admin/integrations/slack/oauth/start')),
  marker: () => status.value?.connected_at ?? null,
  check: async () => (await unwrap(api.GET('/admin/integrations/slack'))).connected_at ?? null,
}

async function onConnected(o: OAuthOutcome) {
  await load()
  if (o.result === 'ok') ui.notify(t('agent.integrations.slack.connected', { team: status.value?.team_name ?? 'Slack' }), 'success')
  else if (o.result === 'error') ui.notify(t('agent.integrations.oauth.failed', { provider: 'Slack', reason: reasonText('slack', o.reason) }), 'error')
}

async function disconnect() {
  if (!(await confirm({ body: t('agent.integrations.slack.disconnectConfirm'), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/integrations/slack/connection'))
    return true
  }, t('agent.integrations.slack.disconnected'))
  if (ok) await load()
}

function onCredsSaved(s: S['SlackStatus']) {
  status.value = s
  credsDialog.value = false
  ui.notify(t('agent.integrations.slack.setup.credentialsSaved'), 'success')
}

async function loadChannels(refresh = false) {
  channelsLoading.value = true
  channelsFailed.value = false
  try {
    channels.value = (await unwrap(api.GET('/admin/integrations/slack/channels', { params: { query: refresh ? { refresh: true } : {} } }))) ?? []
  } catch {
    channelsFailed.value = true
    if (refresh || !channels.value.length) void load() // a rejected token turns the page to its error state
  } finally {
    channelsLoading.value = false
  }
}

function openRule(r: S['SlackRoute'] | null, p?: string) {
  editing.value = r
  preset.value = p
  ruleDialog.value = true
  void loadChannels()
}

function onRuleSaved() {
  ruleDialog.value = false
  ui.notify(t('agent.common.saved'), 'success')
  void load()
}

async function toggle(r: S['SlackRoute'], active: boolean) {
  const res = await run(
    () => unwrap(api.PATCH('/admin/integrations/slack/routes/{id}', { params: { path: { id: r.id } }, body: { active } })),
    active ? t('agent.integrations.slack.rules.resumed') : t('agent.integrations.slack.rules.pausedToast'),
  )
  if (res) void load()
}

async function test(r: S['SlackRoute']) {
  const res = await run(() => unwrap(api.POST('/admin/integrations/slack/routes/{id}/test', { params: { path: { id: r.id } } })))
  if (!res) return
  if (res.ok) ui.notify(t('agent.integrations.slack.rules.testOk', { channel: r.channel_name }), 'success')
  else ui.notify(t('agent.integrations.slack.rules.testFailed', { reason: res.error ?? '' }), 'error')
  void load()
}

async function remove(r: S['SlackRoute']) {
  if (!(await confirm({ body: t('agent.integrations.slack.rules.deleteConfirm', { channel: r.channel_name }), color: 'error' }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/admin/integrations/slack/routes/{id}', { params: { path: { id: r.id } } }))
    return true
  }, t('agent.common.deleted'))
  if (ok) void load()
}

const projectName = (id: string) => cp.byId(id)?.name ?? id

onMounted(load)
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 1200px" data-testid="slack-page">
    <v-btn variant="text" size="small" :prepend-icon="mdiArrowLeft" to="/agent/admin/integrations" class="mb-2 ml-n2">{{ $t('agent.integrations.breadcrumb') }}</v-btn>
    <PageHeader title="Slack" :subtitle="$t('agent.integrations.slack.subtitle')" section="email" :icon="mdiSlack" />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-progress-linear v-if="!status && !failed" indeterminate />

    <template v-if="status && !linked">
      <!-- Setup: three steps -->
      <v-card border flat class="pa-5" data-testid="slack-setup">
        <div class="text-h6">{{ $t('agent.integrations.slack.setup.title') }}</div>
        <p class="text-body-2 text-medium-emphasis mb-4">{{ $t('agent.integrations.slack.setup.intro') }}</p>
        <v-alert v-if="!status.https" type="warning" variant="tonal" density="compact" class="mb-4" data-testid="slack-https-warning">
          {{ $t('agent.integrations.slack.setup.httpsWarning', { url: status.redirect_url.replace(/\/api\/.*$/, '') }) }}
        </v-alert>

        <div class="setup-step" :class="{ done: step > 1 }">
          <v-avatar size="32" :color="step > 1 ? 'success' : 'primary'" class="step-dot">
            <v-icon v-if="step > 1" :icon="mdiCheckCircle" size="20" />
            <span v-else>1</span>
          </v-avatar>
          <div class="flex-grow-1">
            <div class="text-subtitle-1 font-weight-medium">{{ $t('agent.integrations.slack.setup.step1') }}</div>
            <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.integrations.slack.setup.step1Body') }}</p>
            <div class="d-flex flex-wrap align-center ga-2 mb-3">
              <v-btn color="#4A154B" :prepend-icon="mdiSlack" :append-icon="mdiOpenInNew" :href="status.manifest_url" target="_blank" rel="noopener" data-testid="slack-create-app">
                {{ $t('agent.integrations.slack.setup.createApp') }}
              </v-btn>
              <span class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.setup.copyManifest') }}</span>
              <CopyButton :value="manifestJson" />
            </div>
            <v-text-field :model-value="status.redirect_url" :label="$t('agent.integrations.slack.setup.redirectUrl')" readonly density="compact" hide-details data-testid="slack-redirect-url">
              <template #append-inner><CopyButton :value="status.redirect_url" /></template>
            </v-text-field>
          </div>
        </div>

        <div class="setup-step" :class="{ done: step > 2 }">
          <v-avatar size="32" :color="step > 2 ? 'success' : 'primary'" class="step-dot">
            <v-icon v-if="step > 2" :icon="mdiCheckCircle" size="20" />
            <span v-else>2</span>
          </v-avatar>
          <div class="flex-grow-1">
            <div class="text-subtitle-1 font-weight-medium">{{ $t('agent.integrations.slack.setup.step2') }}</div>
            <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.integrations.slack.setup.step2Body') }}</p>
            <SlackAppForm :client-id="status.client_id" :secret-set="status.client_secret_set" @saved="onCredsSaved" />
          </div>
        </div>

        <div class="setup-step last" :class="{ disabled: step < 3 }">
          <v-avatar size="32" :color="step < 3 ? 'grey' : 'primary'" class="step-dot"><span>3</span></v-avatar>
          <div class="flex-grow-1">
            <div class="text-subtitle-1 font-weight-medium">{{ $t('agent.integrations.slack.setup.step3') }}</div>
            <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.integrations.slack.setup.step3Body') }}</p>
            <OAuthConnectButton v-bind="oauth" color="#4A154B" size="large" :prepend-icon="mdiSlack" :disabled="step < 3" testid="slack-connect" @done="onConnected">
              {{ $t('agent.integrations.slack.connect') }}
            </OAuthConnectButton>
          </div>
        </div>
      </v-card>
    </template>

    <template v-if="status && linked">
      <!-- Connection -->
      <v-alert v-if="status.state === 'error'" type="error" variant="tonal" :icon="mdiAlertOutline" class="mb-4" data-testid="slack-broken">
        <div class="font-weight-medium">{{ $t('agent.integrations.slack.brokenTitle') }}</div>
        <div class="text-body-2">{{ $t('agent.integrations.slack.brokenBody', { code: status.error ?? '' }) }}</div>
        <template #append>
          <OAuthConnectButton v-bind="oauth" color="error" variant="flat" testid="slack-reconnect-broken" @done="onConnected">{{ $t('agent.integrations.slack.reconnect') }}</OAuthConnectButton>
        </template>
      </v-alert>
      <v-card border flat class="mb-6" data-testid="slack-connection">
        <v-card-item>
          <template #prepend>
            <v-avatar rounded="lg" size="48" color="#4A154B"><v-icon :icon="mdiSlack" color="white" /></v-avatar>
          </template>
          <v-card-title data-testid="slack-team">{{ status.team_name }}</v-card-title>
          <v-card-subtitle>
            <a v-if="status.team_url" :href="status.team_url" target="_blank" rel="noopener">{{ status.team_url.replace(/^https?:\/\//, '').replace(/\/$/, '') }}</a>
          </v-card-subtitle>
          <template #append>
            <v-chip :color="status.state === 'connected' ? 'success' : 'error'" label variant="tonal" size="small">
              {{ status.state === 'connected' ? $t('agent.integrations.status.connected') : $t('agent.integrations.status.needsAttention') }}
            </v-chip>
          </template>
        </v-card-item>
        <v-card-text>
          <v-row dense class="text-body-2">
            <v-col cols="12" sm="4">
              <div class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.bot') }}</div>
              <div>{{ status.bot_name ? '@' + status.bot_name : status.bot_user_id }}</div>
            </v-col>
            <v-col cols="12" sm="4">
              <div class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.connectedBy', { name: status.connected_by?.name ?? '—' }) }}</div>
              <div><RelativeTime v-if="status.connected_at" :value="status.connected_at" /></div>
            </v-col>
            <v-col cols="12" sm="4">
              <div class="text-caption text-medium-emphasis">{{ $t('agent.integrations.slack.permissions') }}</div>
              <div class="d-flex flex-wrap ga-1 mt-1">
                <v-chip v-for="s in status.scopes" :key="s" size="x-small" label>{{ s }}</v-chip>
              </div>
            </v-col>
          </v-row>
        </v-card-text>
        <v-divider />
        <v-card-actions class="flex-wrap">
          <OAuthConnectButton v-bind="oauth" :prepend-icon="mdiRefresh" testid="slack-reconnect" @done="onConnected">{{ $t('agent.integrations.slack.reconnect') }}</OAuthConnectButton>
          <v-btn :prepend-icon="mdiKeyOutline" data-testid="slack-edit-credentials" @click="credsDialog = true">{{ $t('agent.integrations.slack.editCredentials') }}</v-btn>
          <v-spacer />
          <v-btn color="error" :prepend-icon="mdiLinkVariantOff" :disabled="busy" data-testid="slack-disconnect" @click="disconnect">{{ $t('agent.integrations.slack.disconnect') }}</v-btn>
        </v-card-actions>
      </v-card>

      <!-- Rules -->
      <div class="d-flex flex-wrap align-center ga-2 mb-3">
        <div class="flex-grow-1">
          <div class="text-h6">{{ $t('agent.integrations.slack.rules.title') }}</div>
          <div class="text-body-2 text-medium-emphasis">{{ $t('agent.integrations.slack.rules.subtitle') }}</div>
        </div>
        <v-btn v-if="routes.length" color="primary" :prepend-icon="mdiPlus" data-testid="slack-rule-new" @click="openRule(null)">{{ $t('agent.integrations.slack.rules.add') }}</v-btn>
      </div>
      <v-card v-if="!routes.length" border flat>
        <EmptyState :title="$t('agent.integrations.slack.rules.empty')" :body="$t('agent.integrations.slack.rules.emptyBody')" :icon="mdiSlack" section="email">
          <v-btn color="primary" :prepend-icon="mdiPlus" data-testid="slack-rule-quickstart" @click="openRule(null, 'newTickets')">
            {{ $t('agent.integrations.slack.rules.quickStart') }}
          </v-btn>
        </EmptyState>
      </v-card>
      <v-row v-else dense>
        <v-col v-for="r in routes" :key="r.id" cols="12" md="6">
          <SlackRouteCard
            :route="r"
            :project-name="projectName"
            :bot-name="status.bot_name ?? 'bot'"
            :busy="busy"
            @edit="openRule(r)"
            @test="test(r)"
            @remove="remove(r)"
            @toggle="(v: boolean) => toggle(r, v)"
          />
        </v-col>
      </v-row>
    </template>

    <v-dialog v-model="credsDialog" max-width="720">
      <v-card :title="$t('agent.integrations.slack.editCredentials')">
        <v-card-text>
          <SlackAppForm v-if="credsDialog && status" :client-id="status.client_id" :secret-set="status.client_secret_set" @saved="onCredsSaved">
            <template #cancel><v-btn variant="text" @click="credsDialog = false">{{ $t('app.cancel') }}</v-btn></template>
          </SlackAppForm>
        </v-card-text>
      </v-card>
    </v-dialog>

    <v-dialog v-model="ruleDialog" max-width="1100" scrollable>
      <v-card :title="editing ? $t('agent.integrations.slack.rules.edit') : $t('agent.integrations.slack.rules.new')">
        <v-card-text>
          <SlackRouteForm
            v-if="ruleDialog"
            :route="editing"
            :preset="preset"
            :channels="channels"
            :channels-loading="channelsLoading"
            :channels-failed="channelsFailed"
            :projects="cp.all.value"
            :app-name="appName"
            @saved="onRuleSaved"
            @cancel="ruleDialog = false"
            @refresh="loadChannels(true)"
          />
        </v-card-text>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<style scoped>
.setup-step {
  display: flex;
  gap: 16px;
  position: relative;
  padding-bottom: 24px;
}
.setup-step:not(.last)::before {
  content: '';
  position: absolute;
  left: 15px;
  top: 36px;
  bottom: 4px;
  width: 2px;
  background: rgba(var(--v-border-color), var(--v-border-opacity));
}
.setup-step.disabled .flex-grow-1 {
  opacity: 0.6;
}
.step-dot {
  flex: none;
  color: white;
  font-weight: 700;
}
</style>
