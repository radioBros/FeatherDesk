<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/admin/branding (FE-047, admin, lazy chunk): every branding field,
 * per-slot image uploads, live preview panes from the unsaved draft and a
 * sandboxed email preview. After a save the theme and title apply live; other
 * tabs follow through `settings.branding_changed` on the `me` topic.
 */
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useTheme } from 'vuetify'
import { mdiRefresh } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import TwoColumnLayout from '@/components/TwoColumnLayout.vue'
import BrandingForm from './BrandingForm.vue'
import BrandingPreview from './BrandingPreview.vue'
import AssetSlot from './AssetSlot.vue'
import PwaIconsCard from './PwaIconsCard.vue'
import { api, unwrap } from '@/api/client'
import { validations } from '@/constants/validations'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'
import { applyDocumentBranding, buildThemes } from '@/plugins/branding'
import { toDraft, type BrandingDraft, type BrandingSettings } from './branding'
import { PREVIEW_TEMPLATE, previewUrl } from './types'
import type { components } from '@/api/schema'

type S = components['schemas']
const SLOTS: S['BrandingSlot'][] = ['logo_light', 'logo_dark', 'icon', 'favicon', 'email_logo', 'login_background']

const { t, locale } = useI18n()
const theme = useTheme()
const config = useConfigStore()
const ui = useUiStore()
const settings = ref<BrandingSettings | null>(null)
const draft = ref<BrandingDraft>(toDraft(null))
const loading = ref(true)
const failed = ref(false)
const formKey = ref(0)
const warnings = ref<S['FieldError'][]>([])
const previewVersion = ref(0)

async function load() {
  loading.value = true
  failed.value = false
  try {
    settings.value = await unwrap(api.GET('/admin/branding'))
    formKey.value++
  } catch {
    failed.value = true
  } finally {
    loading.value = false
  }
}
onMounted(load)

const assets = computed(() => settings.value?.assets ?? {})
const logoUrl = computed(() => assets.value.logo_light?.url ?? assets.value.icon?.url ?? null)
const iframeSrc = computed(() => previewUrl(PREVIEW_TEMPLATE, locale.value, previewVersion.value))

/** Re-reads the public config and repaints this tab without a reload. */
async function applyLive() {
  await config.load()
  const built = buildThemes(config.branding, 'agent')
  for (const name of ['light', 'dark'] as const) {
    const live = theme.themes.value[name]
    if (live) Object.assign(live.colors, built[name]!.colors)
  }
  applyDocumentBranding(config.branding, document, t('agent.admin.nav.branding'))
}

async function onSaved(res: S['BrandingUpdateResult']) {
  settings.value = { ...res.settings, assets: res.settings.assets ?? settings.value?.assets }
  warnings.value = res.warnings ?? []
  previewVersion.value++
  ui.notify(t('agent.common.saved'), 'success')
  await applyLive()
}

async function onAsset(slot: S['BrandingSlot'], asset: S['BrandingAsset'] | null) {
  if (!settings.value) return
  settings.value = { ...settings.value, assets: { ...(settings.value.assets ?? {}), [slot]: asset } }
  previewVersion.value++
  ui.notify(t('agent.common.saved'), 'success')
  await applyLive()
}

const warningText = (w: S['FieldError']) => w.message || validations.fromServer(w.code)
</script>

<template>
  <v-container fluid class="pa-4">
    <PageHeader :title="$t('agent.admin.nav.branding')" :subtitle="$t('agent.branding.subtitle')" />
    <v-alert v-if="failed" type="error" variant="tonal" class="mb-4" data-testid="load-error">
      {{ $t('agent.common.loadFailed') }}
      <template #append><v-btn variant="text" @click="load">{{ $t('app.retry') }}</v-btn></template>
    </v-alert>
    <v-progress-linear v-if="loading" indeterminate />
    <template v-if="settings && !loading">
      <v-alert v-if="warnings.length" type="warning" variant="tonal" class="mb-4" data-testid="branding-warnings">
        <div class="font-weight-medium">{{ $t('agent.branding.savedWithWarnings') }}</div>
        <ul class="pl-4">
          <li v-for="w in warnings" :key="w.field + w.code">{{ w.field }}: {{ warningText(w) }}</li>
        </ul>
      </v-alert>
      <TwoColumnLayout breakpoint="lg" :main-fr="7" :aside-fr="5">
        <template #main>
          <v-card border flat class="pa-4 mb-4">
            <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.images') }}</h2>
            <v-row density="compact">
              <v-col v-for="slot in SLOTS" :key="slot" cols="12" sm="6">
                <AssetSlot :slot-name="slot" :asset="assets[slot] ?? null" @changed="(a) => onAsset(slot, a)" />
              </v-col>
            </v-row>
          </v-card>
          <PwaIconsCard
            :settings="settings"
            @refreshed="(s) => { settings = s }"
          />
          <v-card border flat class="pa-4">
            <BrandingForm :key="formKey" :settings="settings" @draft="(d) => (draft = { ...d })" @saved="onSaved" />
          </v-card>
        </template>
        <template #aside>
            <v-card border flat class="pa-4">
              <h2 class="text-subtitle-1 mb-3">{{ $t('agent.branding.preview') }}</h2>
              <BrandingPreview :draft="draft" :logo-url="logoUrl" />
            </v-card>
            <v-card border flat class="pa-4">
              <div class="d-flex align-center mb-2">
                <h2 class="text-subtitle-1 flex-grow-1">{{ $t('agent.branding.emailPreview') }}</h2>
                <v-btn :icon="mdiRefresh" variant="text" size="small" :aria-label="$t('agent.common.refresh')" @click="previewVersion++" />
              </div>
              <p class="text-caption text-medium-emphasis mb-2">{{ $t('agent.branding.emailPreviewHint') }}</p>
              <!-- Empty sandbox: no scripts, no same-origin access, no forms (FE-047). -->
              <iframe
                :key="previewVersion"
                :src="iframeSrc"
                sandbox=""
                referrerpolicy="no-referrer"
                class="fd-iframe"
                :title="$t('agent.branding.emailPreview')"
                data-testid="email-preview-iframe"
              />
            </v-card>
        </template>
      </TwoColumnLayout>
    </template>
  </v-container>
</template>

<style scoped>
.fd-iframe { width: 100%; height: 420px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); border-radius: 4px; background: #fff; }
</style>
