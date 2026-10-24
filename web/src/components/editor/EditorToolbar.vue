<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import type { Editor } from '@tiptap/vue-3'
import {
  mdiCodeBlockBrackets,
  mdiCodeTags,
  mdiFormatBold,
  mdiFormatHeader2,
  mdiFormatItalic,
  mdiFormatListBulleted,
  mdiFormatListNumbered,
  mdiFormatQuoteClose,
  mdiFormatStrikethrough,
  mdiLink,
  mdiLanguageMarkdown,
  mdiRedo,
  mdiUndo,
} from '@mdi/js'
import { computed, ref } from 'vue'
import { validations } from '@/constants/validations'

const props = defineProps<{
  editor: Editor | undefined
  sourceMode: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{ 'toggle-source': [] }>()

const linkOpen = ref(false)
const linkUrl = ref('')
const linkRules = [validations.url]

const canUndo = computed(() => props.editor?.can().undo() ?? false)
const canRedo = computed(() => props.editor?.can().redo() ?? false)

const active = (name: string, attrs?: Record<string, unknown>) =>
  computed(() => props.editor?.isActive(name, attrs) ?? false)

const run = (fn: () => void) => {
  if (props.disabled) return
  fn()
}

const openLink = () => {
  if (!props.editor || props.disabled) return
  const prev = props.editor.getAttributes('link').href as string | undefined
  linkUrl.value = prev ?? 'https://'
  linkOpen.value = true
}

const applyLink = () => {
  const url = linkUrl.value.trim()
  if (!url || linkRules.some((r) => r(url) !== true)) return
  props.editor?.chain().focus().extendMarkRange('link').setLink({ href: url }).run()
  linkOpen.value = false
}

const removeLink = () => {
  props.editor?.chain().focus().unsetLink().run()
  linkOpen.value = false
}
</script>

<template>
  <div
    class="fd-md-toolbar"
    role="toolbar"
    :aria-label="$t('editor.toolbar')"
  >
    <template v-if="!sourceMode">
      <v-btn
        data-intent="plain"
        :icon="mdiFormatBold"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.bold')"
        :aria-pressed="active('bold').value"
        data-testid="md-tool-bold"
        @click="run(() => editor?.chain().focus().toggleBold().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiFormatItalic"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.italic')"
        :aria-pressed="active('italic').value"
        data-testid="md-tool-italic"
        @click="run(() => editor?.chain().focus().toggleItalic().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiFormatStrikethrough"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.strike')"
        :aria-pressed="active('strike').value"
        data-testid="md-tool-strike"
        @click="run(() => editor?.chain().focus().toggleStrike().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiCodeTags"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.inlineCode')"
        :aria-pressed="active('code').value"
        data-testid="md-tool-code"
        @click="run(() => editor?.chain().focus().toggleCode().run())"
      />
      <v-menu
        v-model="linkOpen"
        :close-on-content-click="false"
        location="bottom"
      >
        <template #activator="{ props: p }">
          <v-btn
            data-intent="plain"
            v-bind="p"
            :icon="mdiLink"
            size="small"
            variant="text"
            :disabled="disabled"
            :aria-label="$t('editor.link')"
            :aria-pressed="active('link').value"
            data-testid="md-tool-link"
            @click="openLink"
          />
        </template>
        <v-card min-width="280" class="pa-3">
          <v-text-field
            v-model="linkUrl"
            :label="$t('editor.linkUrl')"
            :rules="linkRules"
            density="compact"
            variant="outlined"
            hide-details="auto"
            data-testid="md-link-url"
            @keydown.enter.prevent="applyLink"
          />
          <div class="d-flex ga-2 mt-2 justify-end">
            <v-btn
              data-intent="plain"
              size="small"
              variant="text"
              data-testid="md-link-remove"
              @click="removeLink"
            >
              {{ $t('editor.linkRemove') }}
            </v-btn>
            <v-btn
              data-intent="plain"
              size="small"
              color="primary"
              data-testid="md-link-apply"
              @click="applyLink"
            >
              {{ $t('editor.linkApply') }}
            </v-btn>
          </div>
        </v-card>
      </v-menu>
      <v-divider vertical class="mx-1" />
      <v-btn
        data-intent="plain"
        :icon="mdiFormatListBulleted"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.bulletList')"
        :aria-pressed="active('bulletList').value"
        data-testid="md-tool-bullet"
        @click="run(() => editor?.chain().focus().toggleBulletList().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiFormatListNumbered"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.numberedList')"
        :aria-pressed="active('orderedList').value"
        data-testid="md-tool-numbered"
        @click="run(() => editor?.chain().focus().toggleOrderedList().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiFormatQuoteClose"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.blockquote')"
        :aria-pressed="active('blockquote').value"
        data-testid="md-tool-quote"
        @click="run(() => editor?.chain().focus().toggleBlockquote().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiCodeBlockBrackets"
        size="small"
        variant="text"
        :disabled="disabled"
        :aria-label="$t('editor.codeBlock')"
        :aria-pressed="active('codeBlock').value"
        data-testid="md-tool-codeblock"
        @click="run(() => editor?.chain().focus().toggleCodeBlock().run())"
      />
      <v-menu location="bottom">
        <template #activator="{ props: p }">
          <v-btn
            data-intent="plain"
            v-bind="p"
            :icon="mdiFormatHeader2"
            size="small"
            variant="text"
            :disabled="disabled"
            :aria-label="$t('editor.heading')"
            :aria-pressed="active('heading').value"
            data-testid="md-tool-heading"
          />
        </template>
        <v-list density="compact">
          <v-list-item
            :title="$t('editor.heading2')"
            data-testid="md-tool-h2"
            @click="run(() => editor?.chain().focus().toggleHeading({ level: 2 }).run())"
          />
          <v-list-item
            :title="$t('editor.heading3')"
            data-testid="md-tool-h3"
            @click="run(() => editor?.chain().focus().toggleHeading({ level: 3 }).run())"
          />
        </v-list>
      </v-menu>
      <v-divider vertical class="mx-1" />
      <v-btn
        data-intent="plain"
        :icon="mdiUndo"
        size="small"
        variant="text"
        :disabled="disabled || !canUndo"
        :aria-label="$t('editor.undo')"
        data-testid="md-tool-undo"
        @click="run(() => editor?.chain().focus().undo().run())"
      />
      <v-btn
        data-intent="plain"
        :icon="mdiRedo"
        size="small"
        variant="text"
        :disabled="disabled || !canRedo"
        :aria-label="$t('editor.redo')"
        data-testid="md-tool-redo"
        @click="run(() => editor?.chain().focus().redo().run())"
      />
    </template>
    <v-spacer />
    <v-btn
      data-intent="plain"
      :icon="mdiLanguageMarkdown"
      size="small"
      :variant="sourceMode ? 'tonal' : 'text'"
      :color="sourceMode ? 'primary' : undefined"
      :disabled="disabled"
      :aria-label="$t('editor.markdownToggle')"
      :aria-pressed="sourceMode"
      data-testid="md-markdown-toggle"
      @click="emit('toggle-source')"
    />
  </div>
</template>

<style scoped>
/* Plain icon buttons; the formatting at the caret is shown in the primary colour. */
.fd-md-toolbar .v-btn[aria-pressed='true'] {
  background: rgba(var(--v-theme-primary), 0.14);
  color: rgb(var(--v-theme-primary));
}
.fd-md-toolbar {
  display: flex;
  flex-wrap: nowrap;
  align-items: center;
  gap: 2px;
  padding: 4px 6px;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}
.fd-md-toolbar .v-btn {
  min-width: 40px;
  min-height: 40px;
}
@media (prefers-reduced-motion: reduce) {
  .fd-md-toolbar .v-btn {
    transition: none;
  }
}
</style>
