<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/integrations (D-21, INT-012, admin): every integration as a
 * card, grouped, with its live status; each opens its own config view.
 * Statuses load independently, so one failing endpoint leaves the others.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiAlertCircleOutline, mdiApi, mdiCheckCircleOutline, mdiRobotOutline, mdiSlack, mdiWebhook } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import IntegrationCard from './IntegrationCard.vue'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()

const slack = ref<S['SlackStatus'] | null>(null)
const mcp = ref<boolean | null>(null)
const hooks = ref<S['Webhook'][] | null>(null)

onMounted(async () => {
  const [s, m, w] = await Promise.allSettled([
    unwrap(api.GET('/admin/integrations/slack')),
    unwrap(api.GET('/admin/integrations')),
    unwrap(api.GET('/admin/webhooks')),
  ])
  if (s.status === 'fulfilled') slack.value = s.value
  if (m.status === 'fulfilled') mcp.value = m.value.mcp_enabled ?? false
  if (w.status === 'fulfilled') hooks.value = w.value ?? []
})

const ok = (text: string) => ({ text, color: 'success', icon: mdiCheckCircleOutline })
const idle = (text: string) => ({ text, color: 'default' })

const slackCard = computed(() => {
  const s = slack.value
  if (!s) return { status: null, detail: null, action: t('agent.integrations.configure') }
  switch (s.state) {
    case 'connected':
      return {
        status: ok(t('agent.integrations.status.connected')),
        detail: t('agent.integrations.cards.slack.connectedTo', { team: s.team_name ?? '', n: s.active_routes }),
        action: t('agent.integrations.manage'),
      }
    case 'error':
      return { status: { text: t('agent.integrations.status.needsAttention'), color: 'error', icon: mdiAlertCircleOutline }, detail: s.error, action: t('agent.integrations.manage') }
    case 'configured':
      return { status: idle(t('agent.integrations.status.setupStarted')), detail: null, action: t('agent.integrations.connect') }
    default:
      return { status: idle(t('agent.integrations.status.notConnected')), detail: null, action: t('agent.integrations.connect') }
  }
})

const activeHooks = computed(() => (hooks.value ?? []).filter((h) => h.active).length)

const groups = computed(() => [
  {
    key: 'chat',
    cards: [
      {
        testid: 'integration-slack', name: 'Slack', icon: mdiSlack, color: '#4A154B', to: '/agent/admin/integrations/slack',
        description: t('agent.integrations.cards.slack.description'), ...slackCard.value,
      },
    ],
  },
  {
    key: 'ai',
    cards: [
      {
        testid: 'integration-ai', name: t('agent.integrations.cards.ai.name'), icon: mdiRobotOutline, color: '#D97757', to: '/agent/admin/integrations/ai-assistants',
        description: t('agent.integrations.cards.ai.description'),
        status: mcp.value === null ? null : mcp.value ? ok(t('agent.integrations.status.on')) : idle(t('agent.integrations.status.off')),
        detail: null, action: t('agent.integrations.configure'),
      },
    ],
  },
  {
    key: 'developer',
    cards: [
      {
        testid: 'integration-webhooks', name: t('agent.integrations.cards.webhooks.name'), icon: mdiWebhook, color: '#C73A63', to: '/agent/admin/webhooks',
        description: t('agent.integrations.cards.webhooks.description'),
        status: hooks.value === null ? null : activeHooks.value ? ok(t('agent.integrations.status.activeCount', { n: activeHooks.value })) : idle(t('agent.integrations.status.noneActive')),
        detail: null, action: t('agent.integrations.manage'),
      },
      {
        testid: 'integration-api', name: t('agent.integrations.cards.api.name'), icon: mdiApi, color: '#1E88E5', href: '/api/docs',
        description: t('agent.integrations.cards.api.description'), status: null, detail: null, action: t('agent.integrations.cards.api.docs'),
      },
    ],
  },
])
</script>

<template>
  <v-container fluid class="pa-4" data-testid="integrations-page">
    <PageHeader :title="$t('agent.admin.nav.integrations')" :subtitle="$t('agent.integrations.subtitle')" />
    <section v-for="g in groups" :key="g.key" class="mb-6">
      <h2 class="text-overline text-medium-emphasis mb-2">{{ $t(`agent.integrations.groups.${g.key}`) }}</h2>
      <v-row dense>
        <v-col v-for="c in g.cards" :key="c.testid" cols="12" sm="6" lg="4">
          <IntegrationCard v-bind="c" />
        </v-col>
      </v-row>
    </section>
  </v-container>
</template>
