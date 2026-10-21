<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Renders Markdown through markdown-it (html:false) + DOMPurify (FE-010).
 * The two libraries (~50 KB gzip) are a lazy chunk loaded by the first
 * MarkdownView on screen, so no page carries them in its own chunk (FE-031).
 */
import { computed, shallowRef } from 'vue'
import { loadMarkdown, markdownRenderer, type RenderMarkdown } from '@/utils/markdownLoader'

const props = defineProps<{ source?: string | null }>()
const render = shallowRef<RenderMarkdown | null>(markdownRenderer())
if (!render.value) void loadMarkdown().then((r) => (render.value = r))
const html = computed(() => (render.value ? render.value(props.source) : ''))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- sanitised by markdown-it (html:false) + DOMPurify -->
  <div class="st-markdown" :aria-busy="!render" v-html="html" />
</template>

<style scoped>
.st-markdown :deep(p) { margin: 0 0 0.6em; }
.st-markdown :deep(p:last-child) { margin-bottom: 0; }
.st-markdown :deep(pre) { overflow-x: auto; padding: 0.6em; border-radius: 4px; background: rgba(127, 127, 127, 0.12); }
.st-markdown :deep(img) { max-width: 100%; height: auto; }
.st-markdown :deep(blockquote) { border-left: 3px solid rgba(127, 127, 127, 0.4); padding-left: 0.8em; margin: 0 0 0.6em; }
</style>
