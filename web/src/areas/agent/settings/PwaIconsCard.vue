<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * App install icons (PWA): preview defaults or generated icons, generate from the
 * current logo in-browser, or reset to the bundled defaults.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { mdiRefresh, mdiCreation } from '@mdi/js'
import { apiFetch } from '@/api/client'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'

type S = components['schemas']

const props = defineProps<{
  settings: S['BrandingSettings']
}>()

const emit = defineEmits<{ refreshed: [S['BrandingSettings']] }>()

const { t, d } = useI18n()
const { confirm } = useConfirm()
const { run, busy } = useAction()

const pwa = computed(() => props.settings.pwa_icons ?? null)
const assets = computed(() => props.settings.assets ?? {})

const iconUrl = (file: string | undefined, fallback: string) => {
  if (!file) return fallback
  const sha = file.replace(/\.[^.]+$/, '')
  const v = sha.slice(0, 8)
  return `/branding/${file}?v=${v}`
}

const previews = computed(() => ({
  icon192: iconUrl(pwa.value?.icon_192, '/pwa/icon-192.png'),
  icon512: iconUrl(pwa.value?.icon_512, '/pwa/icon-512.png'),
  maskable: iconUrl(pwa.value?.maskable_512, '/pwa/maskable-512.png'),
  apple: iconUrl(pwa.value?.apple_180, '/pwa/apple-touch-icon.png'),
}))

const previewItems = computed(() => [
  { key: '192', label: '192 px', src: previews.value.icon192, testid: 'pwa-preview-192', mask: false },
  { key: '512', label: '512 px', src: previews.value.icon512, testid: 'pwa-preview-512', mask: false },
  { key: 'mask', label: t('agent.branding.pwa.maskable'), src: previews.value.maskable, testid: 'pwa-preview-maskable', mask: true },
  { key: 'apple', label: 'iOS 180 px', src: previews.value.apple, testid: 'pwa-preview-apple', mask: false },
])

// The square icon makes the best app icon; the light logo is the fallback. Its file name (`<sha>.<ext>`) is
// recorded as `source`, so a later logo change shows the icons as out of date.
const sourceImageUrl = computed(() => assets.value.icon?.url || assets.value.logo_light?.url || '/featherdesk-logo.svg')
const sourceAssetName = computed(() => (sourceImageUrl.value.startsWith('/branding/') ? sourceImageUrl.value.slice('/branding/'.length) : 'default'))

const logoStale = computed(() => pwa.value?.source != null && pwa.value.source !== '' && pwa.value.source !== sourceAssetName.value)

const statusText = computed(() => {
  if (!pwa.value?.generated_at) return t('agent.branding.pwa.statusDefault')
  if (logoStale.value) return t('agent.branding.pwa.statusStale')
  return t('agent.branding.pwa.statusGenerated', { date: d(new Date(pwa.value.generated_at), 'medium') })
})

const loadImage = (src: string) =>
  new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image()
    img.crossOrigin = 'anonymous'
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('load'))
    img.src = src
  })

const drawIcon = async (size: number, paddingRatio: number) => {
  const img = await loadImage(sourceImageUrl.value)
  const canvas = document.createElement('canvas')
  canvas.width = size
  canvas.height = size
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas')
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(0, 0, size, size)
  const pad = size * paddingRatio
  const box = size - pad * 2
  // An SVG without intrinsic size reports 0: treat it as square (it keeps its aspect ratio inside the box).
  const iw = img.naturalWidth || img.width || 1024
  const ih = img.naturalHeight || img.height || 1024
  const scale = Math.min(box / iw, box / ih)
  const w = iw * scale
  const h = ih * scale
  ctx.drawImage(img, (size - w) / 2, (size - h) / 2, w, h)
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('blob'))), 'image/png')
  })
}

const generate = () =>
  run(async () => {
    const [icon192, icon512, maskable512, apple180] = await Promise.all([
      drawIcon(192, 0.14),
      drawIcon(512, 0.14),
      drawIcon(512, 0.24),
      drawIcon(180, 0.14),
    ])
    const fd = new FormData()
    fd.append('icon_192', icon192, 'icon-192.png')
    fd.append('icon_512', icon512, 'icon-512.png')
    fd.append('maskable_512', maskable512, 'maskable-512.png')
    fd.append('apple_180', apple180, 'apple-180.png')
    fd.append('source', sourceAssetName.value)
    const out = await apiFetch<S['BrandingPwaIcons']>('/admin/branding/pwa-icons', { method: 'POST', body: fd })
    emit('refreshed', { ...props.settings, pwa_icons: out })
  }, t('agent.common.saved'))

const reset = async () => {
  if (!(await confirm({ title: t('agent.branding.pwa.resetTitle'), body: t('agent.branding.pwa.resetBody'), confirmText: t('agent.common.reset') }))) return
  await run(async () => {
    await apiFetch('/admin/branding/pwa-icons', { method: 'DELETE' })
    emit('refreshed', { ...props.settings, pwa_icons: null })
  }, t('agent.common.saved'))
}
</script>

<template>
  <v-card
    border
    flat
    class="pa-4 mb-4"
    data-testid="pwa-icons-card"
  >
    <h2 class="text-subtitle-1 mb-2">
      {{ $t('agent.branding.pwa.title') }}
    </h2>
    <p
      class="text-body-2 mb-3"
      :class="logoStale ? 'text-warning' : 'text-medium-emphasis'"
      data-testid="pwa-icons-status"
      :data-stale="logoStale ? 'true' : undefined"
    >
      {{ statusText }}
    </p>
    <div
      class="d-flex flex-wrap ga-3 mb-4"
      data-testid="pwa-icons-previews"
    >
      <div
        v-for="item in previewItems"
        :key="item.key"
        class="text-center"
      >
        <div
          :class="item.mask ? 'pwa-maskable-wrap' : ''"
        >
          <img
            :src="item.src"
            :alt="item.label"
            width="56"
            height="56"
            class="pwa-preview-img"
            :data-testid="item.testid"
          />
        </div>
        <div class="text-caption text-medium-emphasis mt-1">
          {{ item.label }}
        </div>
      </div>
    </div>
    <div class="d-flex flex-wrap ga-2 mb-3 fd-pwa-actions">
      <v-btn
        color="primary"
        :prepend-icon="mdiCreation"
        :loading="busy"
        data-testid="pwa-icons-generate"
        @click="generate"
      >
        {{ $t('agent.branding.pwa.generate') }}
      </v-btn>
      <v-btn
        v-if="pwa?.generated_at"
        variant="outlined"
        :prepend-icon="mdiRefresh"
        :loading="busy"
        data-testid="pwa-icons-reset"
        @click="reset"
      >
        {{ $t('agent.branding.pwa.reset') }}
      </v-btn>
    </div>
    <p class="text-caption text-medium-emphasis mb-0">
      {{ $t('agent.branding.pwa.hint') }}
    </p>
  </v-card>
</template>

<style scoped>
/* The long labels wrap inside the button on a phone instead of running past its edge. */
.fd-pwa-actions .v-btn { max-width: 100%; height: auto; min-height: 40px; }
.fd-pwa-actions .v-btn :deep(.v-btn__content) { white-space: normal; text-align: start; padding-block: 6px; }
.pwa-preview-img {
  display: block;
  object-fit: contain;
  background: #fff;
  border-radius: 8px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.pwa-maskable-wrap {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  overflow: hidden;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.pwa-maskable-wrap .pwa-preview-img {
  width: 56px;
  height: 56px;
  border: none;
  border-radius: 0;
}
</style>
