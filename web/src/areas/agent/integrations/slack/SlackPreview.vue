<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Live preview of a rule (D-21, INT-014): how the channel will look with the
 * chosen events and options, drawn like a Slack message. With threads on,
 * the first event is the ticket's post and the rest reply in its thread.
 */
import { computed } from 'vue'
import { mdiSlack } from '@mdi/js'
import { TRIGGERS, type Trigger } from './triggers'
import { SAMPLE, priorityMeta, sampleLine, segments } from './preview'

const props = defineProps<{
  events: Trigger[]
  channel: string
  threads: boolean
  includeBody: boolean
  priority: string
  appName: string
}>()

const ordered = computed(() => TRIGGERS.map((t) => t.value).filter((v) => props.events.includes(v)))
const root = computed(() => ordered.value[0])
const rest = computed(() => ordered.value.slice(1, 4))
const pr = computed(() => priorityMeta(props.priority))
</script>

<template>
  <div class="slack-preview" data-testid="slack-preview">
    <div class="slack-channel text-caption font-weight-bold"># {{ channel || 'support' }}</div>
    <div v-if="!root" class="pa-4 text-caption text-medium-emphasis">—</div>
    <template v-else>
      <div class="slack-msg">
        <div class="slack-avatar"><v-icon :icon="mdiSlack" size="20" color="white" /></div>
        <div class="flex-grow-1 min-w-0">
          <div class="slack-head">
            <strong>{{ appName }}</strong><span class="slack-app">APP</span><span class="slack-time">10:42</span>
          </div>
          <div class="slack-link">{{ SAMPLE.ref }} {{ SAMPLE.subject }}</div>
          <div class="slack-text">
            <span v-for="(s, i) in segments(sampleLine(root, includeBody).line)" :key="i" :class="{ 'font-weight-bold': s.bold }">{{ s.text }}</span>
          </div>
          <div v-if="sampleLine(root, includeBody).quote" class="slack-quote">{{ sampleLine(root, includeBody).quote }}</div>
          <div class="slack-context">
            <span>{{ pr.emoji }} <strong>{{ pr.label }}</strong></span>
            <span>Status: <strong>Open</strong></span>
            <span>Assignee: <strong>{{ SAMPLE.agent }}</strong></span>
          </div>
          <div class="slack-button">Open ticket</div>
          <div v-if="threads && rest.length" class="slack-replies">{{ rest.length }} {{ rest.length === 1 ? 'reply' : 'replies' }}</div>
        </div>
      </div>
      <div v-for="ev in rest" :key="ev" class="slack-msg" :class="{ 'slack-thread': threads }" data-testid="slack-preview-reply">
        <div class="slack-avatar slack-avatar--small"><v-icon :icon="mdiSlack" size="14" color="white" /></div>
        <div class="flex-grow-1 min-w-0">
          <div v-if="!threads" class="slack-link">{{ SAMPLE.ref }} {{ SAMPLE.subject }}</div>
          <div class="slack-text">
            <span v-for="(s, i) in segments(sampleLine(ev, includeBody).line)" :key="i" :class="{ 'font-weight-bold': s.bold }">{{ s.text }}</span>
          </div>
          <div v-if="sampleLine(ev, includeBody).quote" class="slack-quote">{{ sampleLine(ev, includeBody).quote }}</div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.slack-preview {
  --slack-bg: #ffffff;
  --slack-fg: #1d1c1d;
  --slack-muted: #616061;
  --slack-border: #dddddd;
  --slack-link: #1264a3;
  background: var(--slack-bg);
  color: var(--slack-fg);
  border: 1px solid var(--slack-border);
  border-radius: 8px;
  font-size: 14px;
  line-height: 1.45;
  overflow: hidden;
}
.v-theme--dark .slack-preview {
  --slack-bg: #1a1d21;
  --slack-fg: #d1d2d3;
  --slack-muted: #ababad;
  --slack-border: #35373b;
  --slack-link: #1d9bd1;
}
.slack-channel {
  padding: 8px 12px;
  border-bottom: 1px solid var(--slack-border);
  color: var(--slack-fg);
}
.slack-msg {
  display: flex;
  gap: 8px;
  padding: 8px 12px;
}
.slack-thread {
  margin-left: 28px;
  border-left: 2px solid var(--slack-border);
}
.slack-avatar {
  flex: none;
  width: 36px;
  height: 36px;
  border-radius: 6px;
  background: #4a154b;
  display: grid;
  place-items: center;
}
.slack-avatar--small {
  width: 24px;
  height: 24px;
  border-radius: 4px;
}
.slack-head {
  display: flex;
  align-items: baseline;
  gap: 6px;
}
.slack-app {
  font-size: 10px;
  font-weight: 700;
  padding: 0 3px;
  border-radius: 2px;
  background: color-mix(in srgb, var(--slack-muted) 20%, transparent);
  color: var(--slack-muted);
}
.slack-time,
.slack-replies {
  font-size: 12px;
  color: var(--slack-muted);
}
.slack-replies {
  color: var(--slack-link);
  font-weight: 700;
  margin-top: 4px;
}
.slack-link {
  color: var(--slack-link);
  font-weight: 700;
}
.slack-quote {
  border-left: 4px solid var(--slack-border);
  padding-left: 8px;
  margin: 4px 0;
  color: var(--slack-fg);
}
.slack-context {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 12px;
  color: var(--slack-muted);
  margin-top: 4px;
}
.slack-button {
  display: inline-block;
  margin-top: 8px;
  padding: 2px 12px;
  border: 1px solid var(--slack-border);
  border-radius: 4px;
  font-size: 13px;
  font-weight: 700;
}
.min-w-0 {
  min-width: 0;
}
</style>
