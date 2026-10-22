<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * One branding image slot (FE-047): pick a file, check type and size on the
 * client (no request for a rejected file), upload, or reset to the default.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiRestore, mdiUpload } from '@mdi/js'
import { validations, type Rule } from '@/constants/validations'
import { apiFetch, api, unwrap } from '@/api/client'
import { ApiProblem } from '@/api/problem'
import { useConfirm } from '@/composables/useConfirm'
import type { components } from '@/api/schema'

type S = components['schemas']
const props = defineProps<{ slotName: S['BrandingSlot']; asset?: S['BrandingAsset'] | null }>()
const emit = defineEmits<{ changed: [S['BrandingAsset'] | null] }>()
const { t } = useI18n()
const { confirm } = useConfirm()

const MAX = 1024 * 1024
// The email logo goes into mail clients: PNG/JPEG only, never SVG (NOT-021).
const types = computed(() =>
  props.slotName === 'email_logo'
    ? ['image/png', 'image/jpeg', '.png', '.jpg', '.jpeg']
    : ['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml', '.png', '.jpg', '.jpeg', '.webp', '.svg'],
)
const accept = computed(() => (props.slotName === 'email_logo' ? 'image/png,image/jpeg' : 'image/png,image/jpeg,image/webp,image/svg+xml'))
const fileRules = computed<Rule[]>(() => [validations.fileType(types.value), validations.fileSize(MAX)])
const files = ref<File[]>([])
const error = ref<string | null>(null)
const busy = ref(false)

async function onPick(v: File | File[] | null) {
  error.value = null
  const file = Array.isArray(v) ? v[0] : v
  if (!file) return
  for (const rule of fileRules.value) {
    const r = rule(file)
    if (r !== true) {
      error.value = r
      return
    }
  }
  const fd = new FormData()
  fd.append('file', file)
  busy.value = true
  try {
    const asset = await apiFetch<S['BrandingAsset']>(`/admin/branding/assets/${props.slotName}`, { method: 'POST', body: fd })
    files.value = []
    emit('changed', asset)
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('upload.failed')
  } finally {
    busy.value = false
  }
}

async function reset() {
  if (!(await confirm({ title: t('agent.branding.resetSlotTitle'), body: t('agent.branding.resetSlotBody'), confirmText: t('agent.common.reset') }))) return
  busy.value = true
  try {
    await unwrap(api.DELETE('/admin/branding/assets/{slot}', { params: { path: { slot: props.slotName } } }))
    emit('changed', null)
  } catch (e) {
    error.value = e instanceof ApiProblem ? e.userMessage : t('errors.generic')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <v-card border flat class="pa-3 h-100" :data-testid="`slot-${slotName}`">
    <div class="d-flex align-center mb-2">
      <div class="text-subtitle-2 flex-grow-1">{{ $t(`agent.branding.slots.${slotName}`) }}</div>
      <v-btn
        v-if="asset"
        :prepend-icon="mdiRestore"
        variant="text"
        size="small"
        :disabled="busy"
        :data-testid="`slot-${slotName}-reset`"
        @click="reset"
      >
        {{ $t('agent.branding.useDefault') }}
      </v-btn>
    </div>
    <div class="fd-thumb mb-2">
      <img v-if="asset" :src="asset.url" :alt="$t(`agent.branding.slots.${slotName}`)" />
      <span v-else class="text-caption text-medium-emphasis">{{ $t('agent.branding.defaultImage') }}</span>
    </div>
    <v-file-input
      v-model="files"
      :label="$t('agent.branding.choose')"
      :accept="accept"
      :rules="fileRules"
      :prepend-icon="mdiUpload"
      :loading="busy"
      :error-messages="error ?? undefined"
      :hint="slotName === 'email_logo' ? $t('agent.branding.emailLogoHint') : $t('agent.branding.slotHint')"
      persistent-hint
      density="compact"
      :data-testid="`slot-${slotName}-input`"
      @update:model-value="onPick"
    />
  </v-card>
</template>

<style scoped>
.fd-thumb { height: 64px; display: flex; align-items: center; justify-content: center; border-radius: 4px; background: repeating-conic-gradient(rgba(var(--v-theme-on-surface), 0.06) 0% 25%, transparent 0% 50%) 50% / 16px 16px; }
.fd-thumb img { max-height: 56px; max-width: 100%; object-fit: contain; }
</style>
