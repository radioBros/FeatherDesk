<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import MarkdownView from './MarkdownView.vue'
import { validations, type Rule } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'

const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    required?: boolean
    rows?: number
    disabled?: boolean
    errorMessages?: string[]
    /** Additional rules from `validations.*` (e.g. no internal KB links in a public reply). */
    extraRules?: Rule[]
    /** Overrides the length cap (defaults to the instance message max length). */
    maxLength?: number
    testid?: string
  }>(),
  { rows: 6 },
)
const emit = defineEmits<{
  'update:modelValue': [string]
  submit: []
  /** Pasted or dropped files; the parent uploads them (AttachmentUploader). */
  files: [File[]]
}>()

const config = useConfigStore()
const tab = ref<'write' | 'preview'>('write')
const maxLen = computed(() => props.maxLength ?? (config.config.message_max_length || 65536))
const rules = computed<Rule[]>(() => [
  ...(props.required ? [validations.required.any, validations.maxLength(maxLen.value)] : [validations.maxLength(maxLen.value)]),
  ...(props.extraRules ?? []),
])
const field = ref<{ $el?: HTMLElement; focus?: () => void } | null>(null)
const textarea = (): HTMLTextAreaElement | null => field.value?.$el?.querySelector?.('textarea') ?? null

/** Caret position in the textarea (end of text when unknown). */
function cursor(): number {
  const el = textarea()
  return el ? el.selectionStart ?? props.modelValue.length : props.modelValue.length
}

/**
 * Inserts text at the caret, optionally replacing `replaceBefore` characters
 * before it (canned responses, KB links, @mentions), and keeps the caret after
 * the inserted text.
 */
async function insert(text: string, replaceBefore = 0) {
  tab.value = 'write'
  const el = textarea()
  const value = props.modelValue
  const start = el ? el.selectionStart ?? value.length : value.length
  const end = el ? el.selectionEnd ?? start : start
  const from = Math.max(0, start - replaceBefore)
  const next = value.slice(0, from) + text + value.slice(end)
  emit('update:modelValue', next)
  await nextTick()
  const t = textarea()
  if (t) {
    const pos = from + text.length
    t.focus()
    t.setSelectionRange(pos, pos)
  }
}

function focus() {
  tab.value = 'write'
  void nextTick(() => textarea()?.focus())
}

defineExpose({ insert, cursor, focus })

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    emit('submit')
  }
}
function onPaste(e: ClipboardEvent) {
  const files = [...(e.clipboardData?.files ?? [])]
  if (files.length) {
    e.preventDefault()
    emit('files', files)
  }
}
function onDrop(e: DragEvent) {
  const files = [...(e.dataTransfer?.files ?? [])]
  if (files.length) {
    e.preventDefault()
    emit('files', files)
  }
}
</script>

<template>
  <div class="fd-md-editor">
    <v-tabs v-model="tab" density="compact" class="mb-1">
      <v-tab value="write" data-testid="md-write">{{ $t('editor.write') }}</v-tab>
      <v-tab value="preview" data-testid="md-preview">{{ $t('editor.preview') }}</v-tab>
    </v-tabs>
    <v-textarea
      v-show="tab === 'write'"
      ref="field"
      :model-value="modelValue"
      :label="label"
      :rows="rows"
      :rules="rules"
      :disabled="disabled"
      :error-messages="errorMessages"
      :placeholder="$t('editor.placeholder')"
      :hint="$t('editor.submitHint')"
      auto-grow
      variant="outlined"
      :data-testid="testid ?? 'md-input'"
      @update:model-value="(v: string) => emit('update:modelValue', v)"
      @keydown="onKeydown"
      @paste="onPaste"
      @drop="onDrop"
    />
    <v-sheet v-if="tab === 'preview'" border rounded class="pa-3" min-height="120" data-testid="md-preview-pane">
      <MarkdownView v-if="modelValue.trim()" :source="modelValue" />
      <span v-else class="text-medium-emphasis">{{ $t('editor.nothingToPreview') }}</span>
    </v-sheet>
  </div>
</template>
