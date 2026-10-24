<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, defineAsyncComponent, nextTick, ref } from 'vue'
import { validations, type Rule } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'
// The rich editor is its own chunk. If it cannot load (an old tab after an upgrade, a blocking proxy), a plain
// Markdown text box takes its place, so there is always somewhere to write.
const loadFailed = ref(false)
const TiptapSurface = defineAsyncComponent({
  loader: () => import('./editor/TiptapSurface.vue'),
  onError: (_err, retry, fail, attempts) => {
    if (attempts <= 1) return retry()
    loadFailed.value = true
    fail()
  },
})

const props = withDefaults(
  defineProps<{
    modelValue: string
    label?: string
    required?: boolean
    rows?: number
    disabled?: boolean
    errorMessages?: string[]
    extraRules?: Rule[]
    maxLength?: number
    testid?: string
  }>(),
  { rows: 6 },
)
const emit = defineEmits<{
  'update:modelValue': [string]
  submit: []
  files: [File[]]
}>()

const config = useConfigStore()
const maxLen = computed(() => props.maxLength ?? (config.config.message_max_length || 65536))
const rules = computed<Rule[]>(() => [
  ...(props.required ? [validations.required.any, validations.maxLength(maxLen.value)] : [validations.maxLength(maxLen.value)]),
  ...(props.extraRules ?? []),
])
const minHeightPx = computed(() => props.rows * 24)
// Same corners as the text fields (radiusDefaults in plugins/branding.ts: sharp 0, default, rounded lg).
const radiusClass = computed(() => ({ sharp: 'rounded-0', rounded: 'rounded-lg' })[config.branding.radius as string] ?? 'rounded')

const surface = ref<{
  insert: (text: string, replaceBefore?: number) => Promise<void>
  focus: () => void
  textBeforeCaret: () => string
} | null>(null)

async function insert(text: string, replaceBefore = 0) {
  if (surface.value && !loadFailed.value) return surface.value.insert(text, replaceBefore)
  // The rich editor loads lazily: a pick (canned response, KB link) that comes first is appended, not lost.
  const v = props.modelValue
  emit('update:modelValue', (replaceBefore ? v.slice(0, Math.max(0, v.length - replaceBefore)) : v) + text)
}

function focus() {
  void nextTick(() => surface.value?.focus())
}

function textBeforeCaret(): string {
  return surface.value?.textBeforeCaret() ?? ''
}

defineExpose({ insert, focus, textBeforeCaret })
</script>

<template>
  <v-input
    :model-value="modelValue"
    :rules="rules"
    :disabled="disabled"
    :error-messages="errorMessages"
    class="fd-md-editor"
    :data-testid="testid ?? 'md-input'"
  >
    <template #default="{ isValid }">
      <div class="fd-md-field w-100">
        <div
          v-if="label"
          class="text-caption text-medium-emphasis fd-md-label"
        >
          {{ label }}
        </div>
        <div
          :class="['fd-md-outline', radiusClass, { 'fd-md-outline--disabled': disabled, 'fd-md-outline--error': isValid.value === false || !!errorMessages?.length }]"
        >
          <textarea
            v-if="loadFailed"
            class="fd-md-fallback"
            :value="modelValue"
            :disabled="disabled"
            :aria-label="label || $t('editor.placeholder')"
            :placeholder="$t('editor.placeholder')"
            :style="{ minHeight: `${minHeightPx}px` }"
            data-testid="md-source"
            @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
            @keydown.enter.ctrl.prevent="emit('submit')"
            @keydown.enter.meta.prevent="emit('submit')"
          />
          <TiptapSurface
            v-else
            ref="surface"
            :model-value="modelValue"
            :rows="rows"
            :disabled="disabled"
            :min-height-px="minHeightPx"
            :aria-label="label"
            @update:model-value="(v: string) => emit('update:modelValue', v)"
            @submit="emit('submit')"
            @files="(f: File[]) => emit('files', f)"
          />
        </div>
        <div class="text-caption text-medium-emphasis fd-md-hint">
          {{ $t('editor.submitHint') }}
        </div>
      </div>
    </template>
  </v-input>
</template>

<style scoped>
.fd-md-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.fd-md-label {
  padding-inline-start: 12px;
}
.fd-md-outline {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface));
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.fd-md-outline:focus-within:not(.fd-md-outline--disabled) {
  border-color: rgb(var(--v-theme-primary));
  box-shadow: 0 0 0 1px rgb(var(--v-theme-primary));
}
.fd-md-outline--error {
  border-color: rgb(var(--v-theme-error));
}
.fd-md-outline--disabled {
  opacity: var(--v-disabled-opacity);
  pointer-events: none;
}
.fd-md-fallback {
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
.fd-md-fallback:focus {
  outline: none;
}
.fd-md-hint {
  padding-inline: 12px 4px;
}
@media (prefers-reduced-motion: reduce) {
  .fd-md-outline {
    transition: none;
  }
}
</style>
