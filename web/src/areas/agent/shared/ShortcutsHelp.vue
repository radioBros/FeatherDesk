<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The `?` keyboard shortcut help (FE-019). A lazy chunk of AgentLayout, loaded on first open (FE-031). */
const open = defineModel<boolean>({ required: true })

const SHORTCUTS = [
  { section: 'list', keys: [['j', 'next'], ['k', 'prev'], ['Enter', 'open'], ['x', 'select']] },
  { section: 'ticket', keys: [['r', 'reply'], ['n', 'note'], ['e', 'escalate'], ['?', 'help']] },
] as const
</script>

<template>
  <v-dialog v-model="open" max-width="480">
    <v-card :title="$t('agent.shortcuts.title')" data-testid="shortcuts-help">
      <v-card-text>
        <div v-for="s in SHORTCUTS" :key="s.section" class="mb-3">
          <div class="text-subtitle-2 mb-1">{{ $t(`agent.shortcuts.${s.section}`) }}</div>
          <div v-for="[key, label] in s.keys" :key="key" class="d-flex align-center ga-3 py-1">
            <kbd class="fd-kbd">{{ key }}</kbd>
            <span class="text-body-2">{{ $t(`agent.shortcuts.${label}`) }}</span>
          </div>
        </div>
        <div class="text-caption text-medium-emphasis">{{ $t('agent.shortcuts.hint') }}</div>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn @click="open = false">{{ $t('app.close') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.fd-kbd {
  min-width: 2em; text-align: center; padding: 2px 6px; border-radius: 4px; font-family: ui-monospace, Menlo, monospace;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); background: rgb(var(--v-theme-surface-variant)); color: rgb(var(--v-theme-on-surface-variant));
}
</style>
