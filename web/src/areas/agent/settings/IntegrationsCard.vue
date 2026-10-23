<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * MCP switch card (MCP-011), on /agent/admin/integrations/ai-assistants (D-21): the
 * `integrations.mcp_enabled` switch (`GET`/`PATCH /admin/integrations`). It is
 * a single on/off setting, so it saves on toggle like the availability switch;
 * there is no form to validate.
 */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, unwrap } from '@/api/client'
import { useAction } from '../shared/useAction'

const { t } = useI18n()
const { run, busy } = useAction()
const enabled = ref(false)
const endpoint = ref('')
const loaded = ref(false)
const failed = ref(false)

onMounted(async () => {
  try {
    const [settings, info] = await Promise.all([unwrap(api.GET('/admin/integrations')), unwrap(api.GET('/integrations/mcp'))])
    enabled.value = settings.mcp_enabled ?? false
    endpoint.value = info.endpoint
    loaded.value = true
  } catch {
    failed.value = true
  }
})

async function toggle(v: boolean | null) {
  const next = !!v
  const res = await run(
    () => unwrap(api.PATCH('/admin/integrations', { body: { mcp_enabled: next } })),
    next ? t('agent.integrations.mcpOn') : t('agent.integrations.mcpOff'),
  )
  if (res) enabled.value = res.mcp_enabled ?? false
}
</script>

<template>
  <v-card border flat class="pa-4 mt-4" :title="$t('agent.integrations.title')" data-testid="integrations-card">
    <v-alert v-if="failed" type="error" variant="tonal" density="compact">{{ $t('agent.common.loadFailed') }}</v-alert>
    <v-progress-linear v-else-if="!loaded" indeterminate />
    <template v-else>
      <v-switch
        :model-value="enabled"
        :label="$t('agent.integrations.mcpEnabled')"
        :disabled="busy"
        color="primary"
        inset
        hide-details
        data-testid="integrations-mcp"
        @update:model-value="toggle"
      />
      <p class="text-body-2 text-medium-emphasis mt-2">{{ $t('agent.integrations.mcpHint') }}</p>
      <p v-if="enabled" class="text-body-2 mt-2" data-testid="integrations-endpoint">
        {{ $t('agent.integrations.endpoint') }} <code>{{ endpoint }}</code>
      </p>
    </template>
  </v-card>
</template>
