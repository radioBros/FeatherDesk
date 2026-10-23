<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/oauth/complete (D-21, INT-015), shared by every OAuth integration:
 * where the server's callback sends the popup. It reports the outcome to the
 * admin page with `window.opener.postMessage` when the opener survived, and
 * over BroadcastChannel (COOP severs the opener after the provider's pages;
 * see oauthPopup.ts), then closes itself once acknowledged. It never
 * navigates anywhere: without an acknowledgement (the admin page was closed)
 * it says so and offers a link.
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { mdiAlertCircleOutline, mdiCheckCircleOutline } from '@mdi/js'
import { OAUTH_CHANNEL, type OAuthMessage } from './oauthPopup'
import { OAUTH_PROVIDERS, isOAuthProvider } from './providers'
import { useOAuthReason } from './reasons'

const route = useRoute()
const reasonText = useOAuthReason()
const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')

const provider = computed(() => (isOAuthProvider(q('provider')) ? q('provider') : ''))
const ok = computed(() => q('result') === 'ok')
const reason = computed(() => reasonText(provider.value, q('reason')))
const back = computed(() => (isOAuthProvider(provider.value) ? OAUTH_PROVIDERS[provider.value].configPath : '/agent/admin/integrations'))
const acked = ref(false)

let channel: BroadcastChannel | null = null

onMounted(() => {
  const state = q('state')
  if (!state) return
  const msg: OAuthMessage = { type: 'result', provider: provider.value, state, result: ok.value ? 'ok' : 'error', reason: q('reason') || undefined }
  try {
    window.opener?.postMessage(msg, window.location.origin)
  } catch {
    /* opener gone or cross-origin: the channel below still reaches it */
  }
  if (typeof BroadcastChannel === 'undefined') return
  channel = new BroadcastChannel(OAUTH_CHANNEL)
  channel.onmessage = (ev: MessageEvent<OAuthMessage>) => {
    if (ev.data?.type !== 'ack' || ev.data.state !== state) return
    acked.value = true
    window.close()
  }
  channel.postMessage(msg)
})
onBeforeUnmount(() => channel?.close())
</script>

<template>
  <v-container class="fill-height d-flex align-center justify-center" data-testid="oauth-complete">
    <v-card border flat max-width="440" class="pa-8 text-center">
      <v-icon :icon="ok ? mdiCheckCircleOutline : mdiAlertCircleOutline" :color="ok ? 'success' : 'error'" size="56" class="mb-4" />
      <h1 class="text-h6 mb-2">{{ $t('agent.integrations.oauth.title') }}</h1>
      <p class="text-body-2 mb-4" data-testid="oauth-complete-text">
        {{ ok ? $t('agent.integrations.oauth.ok') : $t('agent.integrations.oauth.error', { reason }) }}
      </p>
      <v-btn variant="tonal" color="primary" :to="back">{{ $t('agent.integrations.oauth.back') }}</v-btn>
    </v-card>
  </v-container>
</template>
