<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "Connect to <provider>" for any OAuth integration (D-21): a button that
 * opens the provider in a popup (never a redirect of this tab), the waiting
 * dialog with Cancel, and the "allow popups" notice when the browser blocks
 * it. The label goes in the default slot; v-btn attributes (color, size,
 * icon…) pass through. Emits `done` with the outcome (not for `blocked`).
 */
import { ref } from 'vue'
import { useOAuthConnect, type OAuthConnectOptions } from './useOAuthConnect'
import type { OAuthOutcome } from './oauthPopup'
import { OAUTH_PROVIDERS } from './providers'

defineOptions({ inheritAttrs: false })
const props = defineProps<OAuthConnectOptions & { disabled?: boolean; testid?: string }>()
const emit = defineEmits<{ done: [OAuthOutcome] }>()

const { connect, cancel, connecting } = useOAuthConnect(props)
const name = OAUTH_PROVIDERS[props.provider].name
const blocked = ref(false)

function onClick() {
  blocked.value = false
  void connect().then((o) => {
    if (o.result === 'blocked') blocked.value = true
    else emit('done', o)
  })
}
</script>

<template>
  <v-btn v-bind="$attrs" :disabled="disabled || connecting" :loading="connecting" :data-testid="testid" @click="onClick">
    <slot />
  </v-btn>
  <v-alert v-if="blocked" type="warning" variant="tonal" density="compact" class="mt-3" closable data-testid="oauth-blocked" @click:close="blocked = false">
    {{ $t('agent.integrations.oauth.blocked', { provider: name }) }}
  </v-alert>
  <v-dialog :model-value="connecting" max-width="420" persistent>
    <v-card class="pa-6 text-center" data-testid="oauth-waiting">
      <v-progress-circular indeterminate color="primary" size="48" class="mb-4" />
      <div class="text-body-1 mb-4">{{ $t('agent.integrations.oauth.waiting', { provider: name }) }}</div>
      <v-btn variant="text" data-testid="oauth-waiting-cancel" @click="cancel">{{ $t('app.cancel') }}</v-btn>
    </v-card>
  </v-dialog>
</template>
