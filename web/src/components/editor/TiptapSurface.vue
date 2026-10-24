<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { EditorContent, useEditor } from '@tiptap/vue-3'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import EditorToolbar from './EditorToolbar.vue'
import { buildEditorExtensions } from './extensions'

const props = defineProps<{
  modelValue: string
  rows: number
  disabled?: boolean
  minHeightPx: number
  /** Accessible name of the text box (the field label, else the placeholder). */
  ariaLabel?: string
}>()
const emit = defineEmits<{
  'update:modelValue': [string]
  submit: []
  files: [File[]]
}>()

const { t } = useI18n()
const sourceMode = ref(false)
const sourceText = ref('')
const sourceRef = ref<HTMLTextAreaElement | null>(null)

// What this editor last sent up. The parent echoes it back as modelValue a tick later, by which time fast typing
// has moved on: only a value that differs from this one is an outside change (reset, canned text) to load.
let lastEmitted: string | null = null

const normalizeMd = (md: string) => {
  if (!md.replace(/\u00a0/g, ' ').trim()) return ''
  // The serializer ends with blank lines; they mean nothing in Markdown.
  return md.replace(/\s+$/, '')
}

const editor = useEditor({
  extensions: buildEditorExtensions(t('editor.placeholder')),
  content: props.modelValue,
  contentType: 'markdown',
  editable: !props.disabled,
  onUpdate: ({ editor: ed }) => {
    if (sourceMode.value) return
    lastEmitted = normalizeMd(ed.getMarkdown())
    emit('update:modelValue', lastEmitted)
  },
  editorProps: {
    attributes: {
      class: 'fd-tiptap-content',
      role: 'textbox',
      'aria-multiline': 'true',
      'aria-label': props.ariaLabel || t('editor.placeholder'),
    },
    handleKeyDown: (_view, event) => {
      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
        event.preventDefault()
        emit('submit')
        return true
      }
      return false
    },
    handlePaste: (_view, event) => {
      const files = [...(event.clipboardData?.files ?? [])]
      if (files.length) {
        event.preventDefault()
        emit('files', files)
        return true
      }
      return false
    },
    handleDrop: (_view, event) => {
      const files = [...(event.dataTransfer?.files ?? [])]
      if (files.length) {
        event.preventDefault()
        emit('files', files)
        return true
      }
      return false
    },
  },
})

watch(
  () => props.modelValue,
  (v) => {
    if (sourceMode.value) {
      sourceText.value = v
      return
    }
    const ed = editor.value
    if (!ed || v === lastEmitted) return
    lastEmitted = null
    if (v !== normalizeMd(ed.getMarkdown())) ed.commands.setContent(v || '', { contentType: 'markdown', emitUpdate: false })
  },
)

watch(
  () => props.disabled,
  (d) => editor.value?.setEditable(!d),
)

const surfaceStyle = computed(() => ({ minHeight: `${props.minHeightPx}px` }))

const toggleSource = () => {
  if (sourceMode.value) {
    emit('update:modelValue', sourceText.value)
    editor.value?.commands.setContent(sourceText.value || '', { contentType: 'markdown', emitUpdate: false })
    sourceMode.value = false
    return
  }
  sourceText.value = props.modelValue
  sourceMode.value = true
}

function onSourceInput(e: Event) {
  const v = (e.target as HTMLTextAreaElement).value
  sourceText.value = v
  emit('update:modelValue', v)
}

function onSourceKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    emit('submit')
  }
}

function onSourcePaste(e: ClipboardEvent) {
  const files = [...(e.clipboardData?.files ?? [])]
  if (files.length) {
    e.preventDefault()
    emit('files', files)
  }
}

function onSourceDrop(e: DragEvent) {
  const files = [...(e.dataTransfer?.files ?? [])]
  if (files.length) {
    e.preventDefault()
    emit('files', files)
  }
}

function textBeforeCaret(): string {
  if (sourceMode.value) {
    const el = sourceRef.value
    if (!el) return ''
    const pos = el.selectionStart ?? el.value.length
    const lineStart = el.value.lastIndexOf('\n', pos - 1) + 1
    return el.value.slice(lineStart, pos)
  }
  const ed = editor.value
  if (!ed) return ''
  const { $from } = ed.state.selection
  return $from.parent.textBetween(0, $from.parentOffset, undefined, '\n')
}

async function insert(text: string, replaceBefore = 0) {
  if (sourceMode.value) {
    const el = sourceRef.value
    const value = sourceText.value
    const start = el?.selectionStart ?? value.length
    const end = el?.selectionEnd ?? start
    const from = Math.max(0, start - replaceBefore)
    const next = value.slice(0, from) + text + value.slice(end)
    sourceText.value = next
    emit('update:modelValue', next)
    await Promise.resolve()
    if (el) {
      const pos = from + text.length
      el.focus()
      el.setSelectionRange(pos, pos)
    }
    return
  }
  const ed = editor.value
  if (!ed) return
  const { from, to } = ed.state.selection
  const deleteFrom = Math.max(0, from - replaceBefore)
  ed.chain()
    .focus()
    .deleteRange({ from: deleteFrom, to })
    .insertContentAt(deleteFrom, text, { contentType: 'markdown' })
    .run()
}

function focus() {
  if (sourceMode.value) {
    sourceRef.value?.focus()
    return
  }
  editor.value?.commands.focus()
}

/** The editor's current Markdown (what an edit would save); tests use it to check the round trip. */
const getMarkdown = () => normalizeMd(editor.value?.getMarkdown() ?? '')

defineExpose({ insert, focus, textBeforeCaret, getMarkdown, sourceMode })

onBeforeUnmount(() => editor.value?.destroy())
</script>

<template>
  <div class="fd-tiptap-surface">
    <EditorToolbar
      :editor="editor"
      :source-mode="sourceMode"
      :disabled="disabled"
      @toggle-source="toggleSource"
    />
    <div
      v-show="!sourceMode"
      class="fd-tiptap-body"
      :style="surfaceStyle"
    >
      <EditorContent :editor="editor" />
    </div>
    <textarea
      v-show="sourceMode"
      ref="sourceRef"
      class="fd-md-source"
      :value="sourceText"
      :disabled="disabled"
      :style="surfaceStyle"
      :aria-label="ariaLabel || $t('editor.placeholder')"
      data-testid="md-source"
      @input="onSourceInput"
      @keydown="onSourceKeydown"
      @paste="onSourcePaste"
      @drop="onSourceDrop"
    />
  </div>
</template>

<style scoped>
.fd-tiptap-body {
  padding: 8px 12px;
}
.fd-tiptap-body :deep(.tiptap) {
  outline: none;
  min-height: inherit;
}
.fd-tiptap-body :deep(.tiptap p.is-editor-empty:first-child::before) {
  color: rgba(var(--v-theme-on-surface), var(--v-medium-emphasis-opacity));
  content: attr(data-placeholder);
  float: left;
  height: 0;
  pointer-events: none;
}
.fd-tiptap-body :deep(.tiptap table) {
  border-collapse: collapse;
  width: 100%;
}
.fd-tiptap-body :deep(.tiptap th),
.fd-tiptap-body :deep(.tiptap td) {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 4px 8px;
}
.fd-md-source {
  box-sizing: border-box;
  width: 100%;
  padding: 8px 12px;
  border: 0;
  resize: vertical;
  font: inherit;
  line-height: 1.5;
  background: transparent;
  color: inherit;
}
.fd-md-source:focus {
  outline: none;
}
@media (prefers-reduced-motion: reduce) {
  .fd-tiptap-body :deep(*) {
    transition: none !important;
  }
}
</style>
