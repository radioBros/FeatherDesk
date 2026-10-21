<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/**
 * /agent/login (FE-020): "Sign in with SSO" (full-page redirect) and the local
 * form, shown per `/auth/config`. With SSO on and staff-local off, the local
 * form is a break-glass disclosure for admins (D-04 §5). `?error=` from the
 * OIDC callback is shown in plain words.
 */
import { computed, defineAsyncComponent, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfigStore } from '@/stores/config'
import LocalLoginForm from '@/forms/LocalLoginForm.vue'
import BrandLogo from '@/components/BrandLogo.vue'
import { safeRedirect } from '@/router/guards'
import type { MfaChallenge } from '@/components/mfa/mfa'

const KNOWN_ERRORS = ['no_role', 'disabled', 'oidc_failed', 'idp_unavailable'] as const

const config = useConfigStore()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const auth = computed(() => config.config.staff)
const brand = computed(() => config.branding)
// Login background from branding (FE-050): image, else colour, else the theme surface.
const background = computed(() => {
  const b = brand.value
  if (b.login_background_url) return { backgroundImage: `url("${encodeURI(b.login_background_url)}")`, backgroundSize: 'cover', backgroundPosition: 'center' }
  if (b.login_background_color) return { backgroundColor: b.login_background_color }
  return {}
})
const showLocal = ref(!auth.value.oidc || auth.value.local)
// Only needed after a correct password: kept out of the initial login JS (FE-031 budget).
const MfaStep = defineAsyncComponent(() => import('@/components/mfa/MfaStep.vue'))
const challenge = ref<MfaChallenge | null>(null)

const error = computed(() => {
  const raw = route.query.error
  const code = Array.isArray(raw) ? raw[0] : raw
  if (!code) return null
  return t(`agent.login.error.${(KNOWN_ERRORS as readonly string[]).includes(code) ? code : 'generic'}`)
})

function done() {
  void router.push(safeRedirect(route.query.redirect) ?? '/agent/tickets')
}
const ssoHref = computed(() => {
  const r = safeRedirect(route.query.redirect)
  return '/api/v1/auth/oidc/staff/start' + (r ? `?redirect=${encodeURIComponent(r)}` : '')
})
</script>

<template>
  <v-app>
    <v-main class="d-flex align-center justify-center" :class="{ 'st-login-bg': !Object.keys(background).length }" :style="background" data-testid="agent-login-main">
      <v-container style="max-width: 440px">
        <div class="text-center mb-6"><BrandLogo :height="40" /></div>
        <v-card border flat class="pa-2 st-login-card">
          <v-card-title class="text-h5 text-wrap" data-testid="agent-login-title">{{ brand.login_title || $t('agent.login.title') }}</v-card-title>
          <v-card-subtitle v-if="brand.login_subtitle" class="text-wrap">{{ brand.login_subtitle }}</v-card-subtitle>
          <v-card-text>
            <MfaStep v-if="challenge" :challenge="challenge" @done="done" @restart="challenge = null" />
            <template v-else>
              <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-4" data-testid="login-error">{{ error }}</v-alert>
              <template v-if="auth.oidc">
                <p class="text-body-2 text-medium-emphasis mb-3">{{ $t('agent.login.ssoHint') }}</p>
                <v-btn :href="ssoHref" color="primary" block size="large" class="mb-4" data-testid="sso-button">
                  {{ auth.oidc_label || $t('auth.sso') }}
                </v-btn>
              </template>
              <template v-if="auth.oidc && !auth.local">
                <v-btn v-if="!showLocal" variant="text" block size="small" data-testid="break-glass-toggle" @click="showLocal = true">
                  {{ $t('auth.breakGlass') }}
                </v-btn>
              </template>
              <LocalLoginForm v-if="showLocal" audience="staff" @success="done" @mfa="challenge = $event" />
            </template>
          </v-card-text>
        </v-card>
      </v-container>
    </v-main>
  </v-app>
</template>
