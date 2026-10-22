<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, ref } from 'vue'
import { mdiClose, mdiPaperclip } from '@mdi/js'
import { useI18n } from 'vue-i18n'
import { validations, formatBytes } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'
import { apiFetch } from '@/api/client'
import { ApiProblem } from '@/api/problem'

interface Item {
  key: number
  file: File
  status: 'uploading' | 'done' | 'error'
  id?: string
  error?: string
}

const props = withDefaults(defineProps<{ area: 'portal' | 'agent'; modelValue: string[] }>(), {})
const emit = defineEmits<{ 'update:modelValue': [string[]] }>()
const { t } = useI18n()
const config = useConfigStore()
const items = ref<Item[]>([])
const input = ref<HTMLInputElement | null>(null)
let seq = 0

const limits = computed(() => config.config.upload)
const maxFiles = computed(() => limits.value.max_files ?? 10)

/** Client-side check with the shared rules; returns an error message or null. */
function check(file: File): string | null {
  const size = validations.fileSize(limits.value.max_bytes)(file)
  if (size !== true) return size
  if (limits.value.allowed_types.length) {
    const type = validations.fileType(limits.value.allowed_types)(file)
    if (type !== true) return type
  }
  return null
}

function sync() {
  emit('update:modelValue', items.value.filter((i) => i.status === 'done' && i.id).map((i) => i.id!))
}

async function add(files: File[] | FileList) {
  for (const file of [...files]) {
    if (items.value.length >= maxFiles.value) {
      items.value.push({ key: ++seq, file, status: 'error', error: t('upload.tooMany', { max: maxFiles.value }) })
      continue
    }
    const err = check(file)
    const item: Item = { key: ++seq, file, status: err ? 'error' : 'uploading', error: err ?? undefined }
    items.value.push(item)
    if (err) continue
    const fd = new FormData()
    fd.append('file', file)
    try {
      const res = await apiFetch<{ id: string }>(`/${props.area}/attachments`, { method: 'POST', body: fd })
      const it = items.value.find((i) => i.key === item.key)
      if (it) Object.assign(it, { status: 'done', id: res.id })
    } catch (e) {
      const it = items.value.find((i) => i.key === item.key)
      if (it) Object.assign(it, { status: 'error', error: e instanceof ApiProblem ? e.userMessage : t('upload.failed') })
    }
    sync()
  }
}

function remove(key: number) {
  items.value = items.value.filter((i) => i.key !== key)
  sync()
}

function clear() {
  items.value = []
  sync()
}

defineExpose({ add, clear })
</script>

<template>
  <div data-testid="attachment-uploader">
    <input ref="input" type="file" multiple hidden data-testid="file-input" @change="(e) => add((e.target as HTMLInputElement).files ?? [])" />
    <v-btn
      variant="text"
      size="small"
      :prepend-icon="mdiPaperclip"
      @click="input?.click()"
      @dragover.prevent
      @drop.prevent="(e: DragEvent) => add(e.dataTransfer?.files ?? [])"
    >
      {{ $t('upload.drop') }}
    </v-btn>
    <div v-if="items.length" class="d-flex flex-wrap ga-2 mt-1">
      <v-chip
        v-for="i in items"
        :key="i.key"
        size="small"
        :color="i.status === 'error' ? 'error' : undefined"
        :title="i.error"
        data-testid="upload-item"
      >
        {{ i.file.name }} · {{ formatBytes(i.file.size) }}
        <span v-if="i.status === 'uploading'" class="ml-1">· {{ $t('upload.uploading') }}</span>
        <span v-if="i.error" class="ml-1" data-testid="upload-error">· {{ i.error }}</span>
        <template #append>
          <v-btn :icon="mdiClose" size="x-small" variant="text" :aria-label="$t('upload.remove')" @click="remove(i.key)" />
        </template>
      </v-chip>
    </div>
  </div>
</template>
