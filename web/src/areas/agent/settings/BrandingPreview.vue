<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * Live preview panes (FE-047): login, portal home and agent list mockups
 * painted from the unsaved draft, so colour changes show before saving.
 */
import { computed } from 'vue'
import MarkdownView from '@/components/MarkdownView.vue'
import { normalizeHex, onColor } from '@/utils/color'
import { areaColors, type BrandingDraft } from './branding'

const props = defineProps<{ draft: BrandingDraft; logoUrl?: string | null }>()

const RADIUS = { sharp: '0px', default: '4px', rounded: '12px' } as const
const safe = (c: string, fallback: string) => normalizeHex(c ?? '') ?? fallback
function vars(area: 'portal' | 'agent') {
  const c = areaColors(props.draft, area)
  const primary = safe(c.primary, '#1a73e8')
  return {
    '--p-primary': primary,
    '--p-on-primary': onColor(primary),
    '--p-secondary': safe(c.secondary, '#5f6368'),
    '--p-accent': safe(c.accent, '#00897b'),
    '--p-radius': RADIUS[props.draft.radius] ?? '4px',
  }
}
const portal = computed(() => vars('portal'))
const agent = computed(() => vars('agent'))
const loginBg = computed(() => safe(props.draft.login_background_color, '#f5f5f5'))
const name = computed(() => props.draft.product_name || 'SimpleTickets')
</script>

<template>
  <div class="d-flex flex-column ga-4" data-testid="branding-preview">
    <div>
      <div class="text-caption text-medium-emphasis mb-1">{{ $t('agent.branding.previewLogin') }}</div>
      <div class="st-pane st-login" :style="{ ...portal, background: loginBg }" data-testid="preview-login">
        <div class="st-card">
          <img v-if="logoUrl" :src="logoUrl" alt="" class="st-logo" />
          <div class="st-title">{{ draft.login_title || $t('agent.branding.previewSignIn', { name }) }}</div>
          <div v-if="draft.login_subtitle" class="st-sub">{{ draft.login_subtitle }}</div>
          <div class="st-input" />
          <div class="st-input" />
          <div class="st-btn" data-testid="preview-login-button">{{ $t('auth.signIn') }}</div>
        </div>
      </div>
    </div>
    <div>
      <div class="text-caption text-medium-emphasis mb-1">{{ $t('agent.branding.previewPortal') }}</div>
      <div class="st-pane" :style="portal" data-testid="preview-portal">
        <div class="st-bar" data-testid="preview-portal-bar">
          <span class="st-brand">{{ name }}</span>
          <span class="st-pill">{{ $t('nav.newTicket') }}</span>
        </div>
        <div class="pa-3 st-body">
          <MarkdownView v-if="draft.welcome_md.trim()" :source="draft.welcome_md" />
          <div v-else class="st-sub">{{ $t('agent.branding.previewWelcome') }}</div>
          <a class="st-link" href="#" @click.prevent>{{ $t('agent.branding.previewLink') }}</a>
        </div>
      </div>
    </div>
    <div>
      <div class="text-caption text-medium-emphasis mb-1">{{ $t('agent.branding.previewAgent') }}</div>
      <div class="st-pane d-flex" :style="agent" data-testid="preview-agent">
        <div class="st-nav">
          <div class="st-nav-item st-active">{{ $t('nav.myTickets') }}</div>
          <div class="st-nav-item">{{ $t('nav.unassigned') }}</div>
        </div>
        <div class="flex-grow-1 pa-2">
          <div v-for="n in 3" :key="n" class="st-row">
            <span class="st-num">#{{ 1040 + n }}</span>
            <span class="st-line" />
            <span class="st-chip">{{ $t('status.open') }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.st-pane { border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); border-radius: var(--p-radius); overflow: hidden; background: #fff; color: #202124; font-size: 12px; }
.st-login { display: flex; justify-content: center; padding: 16px; }
.st-card { background: #fff; width: 200px; padding: 12px; border-radius: var(--p-radius); box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2); text-align: center; }
.st-logo { max-height: 24px; max-width: 100%; margin-bottom: 6px; }
.st-title { font-weight: 600; margin-bottom: 2px; }
.st-sub { color: #5f6368; margin-bottom: 6px; }
.st-input { height: 18px; border: 1px solid #c4c4c4; border-radius: var(--p-radius); margin: 6px 0; }
.st-btn { background: var(--p-primary); color: var(--p-on-primary); border-radius: var(--p-radius); padding: 4px; font-weight: 600; }
.st-bar { background: var(--p-primary); color: var(--p-on-primary); display: flex; justify-content: space-between; align-items: center; padding: 6px 10px; }
.st-brand { font-weight: 600; }
.st-pill { border: 1px solid currentColor; border-radius: var(--p-radius); padding: 1px 6px; }
.st-body :deep(p) { margin-bottom: 4px; }
.st-link { color: var(--p-primary); }
.st-nav { width: 90px; background: #f8f9fa; padding: 6px 4px; }
.st-nav-item { padding: 3px 6px; border-radius: var(--p-radius); margin-bottom: 2px; }
.st-active { background: color-mix(in srgb, var(--p-primary) 15%, transparent); color: var(--p-primary); font-weight: 600; }
.st-row { display: flex; align-items: center; gap: 6px; padding: 4px 0; border-bottom: 1px solid #eee; }
.st-num { color: #5f6368; }
.st-line { flex: 1; height: 6px; border-radius: 3px; background: #e0e0e0; }
.st-chip { background: color-mix(in srgb, var(--p-accent) 18%, transparent); color: color-mix(in srgb, var(--p-accent) 55%, #000); border-radius: var(--p-radius); padding: 0 6px; }
</style>
