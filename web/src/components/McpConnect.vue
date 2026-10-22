<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * "Connect an AI assistant" (MCP-010): shown on both API token pages while an
 * admin has switched the MCP endpoint on (`GET /integrations/mcp`). It shows
 * the endpoint, the recommended token scopes for this user and ready-to-paste
 * client configs. The token itself is never filled in: the snippets carry a
 * placeholder the user replaces with the token they just created.
 */
import { computed, onMounted, ref } from 'vue'
import { mdiCheck, mdiContentCopy, mdiRobotOutline } from '@mdi/js'
import { api, unwrap } from '@/api/client'
import type { components } from '@/api/schema'

type Info = components['schemas']['McpInfo']
type Client = 'claudeCode' | 'claudeDesktop' | 'cursor'

const TOKEN = 'fd_pat_…'
const info = ref<Info | null>(null)
const tab = ref<Client>('claudeCode')
const copied = ref<string | null>(null)

onMounted(async () => {
  try {
    info.value = await unwrap(api.GET('/integrations/mcp'))
  } catch {
    /* no helper when the details cannot be loaded */
  }
})

/** Product names stay untranslated. */
const clients: { id: Client; label: string }[] = [
  { id: 'claudeCode', label: 'Claude Code' },
  { id: 'claudeDesktop', label: 'Claude Desktop' },
  { id: 'cursor', label: 'Cursor' },
]

const snippets = computed<Record<Client, string>>(() => {
  const url = info.value?.endpoint ?? ''
  return {
    claudeCode: `claude mcp add --transport http featherdesk ${url} --header "Authorization: Bearer ${TOKEN}"`,
    claudeDesktop: JSON.stringify(
      {
        mcpServers: {
          featherdesk: {
            command: 'npx',
            args: ['-y', 'mcp-remote', url, '--header', 'Authorization:Bearer ${FD_TOKEN}'],
            env: { FD_TOKEN: TOKEN },
          },
        },
      },
      null,
      2,
    ),
    cursor: JSON.stringify({ mcpServers: { featherdesk: { url, headers: { Authorization: `Bearer ${TOKEN}` } } } }, null, 2),
  }
})

async function copy(key: string, text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = key
    setTimeout(() => (copied.value = null), 2000)
  } catch {
    /* clipboard blocked: the text stays selectable */
  }
}
</script>

<template>
  <v-expansion-panels v-if="info?.enabled" class="mb-4" data-testid="mcp-connect">
    <v-expansion-panel>
      <v-expansion-panel-title>
        <v-icon :icon="mdiRobotOutline" class="mr-2" />
        {{ $t('mcp.title') }}
      </v-expansion-panel-title>
      <v-expansion-panel-text>
        <p class="text-body-2 mb-4">{{ $t('mcp.intro') }}</p>

        <div class="text-caption text-medium-emphasis">{{ $t('mcp.endpoint') }}</div>
        <div class="d-flex align-center mb-4">
          <code class="fd-mcp-code flex-grow-1" data-testid="mcp-endpoint">{{ info.endpoint }}</code>
          <v-btn
            :icon="copied === 'endpoint' ? mdiCheck : mdiContentCopy"
            variant="text"
            size="small"
            :aria-label="copied === 'endpoint' ? $t('mcp.copied') : $t('mcp.copy')"
            @click="copy('endpoint', info.endpoint)"
          />
        </div>

        <div class="text-caption text-medium-emphasis mb-1">{{ $t('mcp.scopes') }}</div>
        <div class="mb-1" data-testid="mcp-scopes">
          <span class="text-body-2 mr-2">{{ $t('mcp.readWrite') }}</span>
          <v-chip v-for="s in info.recommended_scopes" :key="s" size="x-small" label class="mr-1">{{ s }}</v-chip>
        </div>
        <div class="mb-4">
          <span class="text-body-2 mr-2">{{ $t('mcp.readOnly') }}</span>
          <v-chip v-for="s in info.read_only_scopes" :key="s" size="x-small" label class="mr-1">{{ s }}</v-chip>
        </div>

        <v-tabs v-model="tab" density="compact" class="mb-2">
          <v-tab v-for="c in clients" :key="c.id" :value="c.id" :data-testid="`mcp-tab-${c.id}`">{{ c.label }}</v-tab>
        </v-tabs>
        <p class="text-body-2 mb-2">{{ $t(`mcp.hint.${tab}`) }}</p>
        <div class="d-flex align-start">
          <pre class="fd-mcp-code flex-grow-1" data-testid="mcp-snippet">{{ snippets[tab] }}</pre>
          <v-btn
            :icon="copied === tab ? mdiCheck : mdiContentCopy"
            variant="text"
            size="small"
            :aria-label="copied === tab ? $t('mcp.copied') : $t('mcp.copy')"
            data-testid="mcp-copy"
            @click="copy(tab, snippets[tab])"
          />
        </div>
        <p class="text-caption text-medium-emphasis mt-2">{{ $t('mcp.replaceToken', { placeholder: TOKEN }) }}</p>
      </v-expansion-panel-text>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<style scoped>
.fd-mcp-code {
  display: block;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 0.8125rem;
  padding: 8px 12px;
  border-radius: 6px;
  background: rgba(var(--v-theme-on-surface), 0.06);
  white-space: pre-wrap;
  word-break: break-all;
  overflow-x: auto;
}
</style>
