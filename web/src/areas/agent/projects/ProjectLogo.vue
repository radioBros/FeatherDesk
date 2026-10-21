<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** The project's portal logo (D-20): PNG/JPEG/SVG up to 1 MB; checked here, no request for a rejected file. */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { validations, type Rule } from '@/constants/validations'
import { apiFetch } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ project: S['Project'] }>()
const emit = defineEmits<{ changed: [S['Project']] }>()
const { t } = useI18n()

const fileRules: Rule[] = [validations.fileType(['image/png', 'image/jpeg', 'image/svg+xml', '.png', '.jpg', '.jpeg', '.svg']), validations.fileSize(1024 * 1024)]
const files = ref<File[]>([])
const error = ref<string | null>(null)
const busy = ref(false)

async function onPick(v: File | File[] | null) {
  error.value = null
  const file = Array.isArray(v) ? v[0] : v
  if (!file) return
  for (const rule of fileRules) {
    const r = rule(file)
    if (r !== true) {
      error.value = r
      return
    }
  }
  const fd = new FormData()
  fd.append('file', file)
  await call(() => apiFetch<S['Project']>(`/admin/projects/${props.project.id}/logo`, { method: 'POST', body: fd }))
  files.value = []
}
async function remove() {
  await call(async () => {
    await apiFetch<void>(`/admin/projects/${props.project.id}/logo`, { method: 'DELETE' })
    return { ...props.project, logo_url: null, logo_asset_id: null }
  })
}
async function call(fn: () => Promise<S['Project'] | undefined>) {
  busy.value = true
  try {
    const p = await fn()
    if (p) emit('changed', p)
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="d-flex align-center ga-3 flex-wrap" data-testid="project-logo">
    <img v-if="project.logo_url" :src="project.logo_url" :alt="$t('projects.admin.logo.label')" style="max-height: 40px; max-width: 160px" data-testid="project-logo-img" />
    <v-file-input
      v-model="files"
      :label="$t('projects.admin.logo.label')"
      :hint="$t('projects.admin.logo.hint')"
      persistent-hint
      accept="image/png,image/jpeg,image/svg+xml"
      :rules="fileRules"
      :error-messages="error ?? undefined"
      :loading="busy"
      density="compact"
      prepend-icon=""
      style="min-width: 240px; flex: 1"
      data-testid="project-logo-input"
      @update:model-value="onPick"
    />
    <v-btn v-if="project.logo_url" variant="text" :disabled="busy" data-testid="project-logo-remove" @click="remove">{{ $t('projects.admin.logo.remove') }}</v-btn>
  </div>
</template>
