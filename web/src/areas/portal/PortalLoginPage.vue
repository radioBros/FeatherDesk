<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { mdiBookOpenPageVariantOutline } from '@mdi/js'
import { useConfigStore } from '@/stores/config'
import LocalLoginForm from '@/forms/LocalLoginForm.vue'
import { safeRedirect } from '@/router/guards'
import AuthCard from './components/AuthCard.vue'
import MagicLinkForm from './forms/MagicLinkForm.vue'
import type { MfaChallenge } from '@/components/mfa/mfa'

/**
 * Portal sign-in (FE-013, FE-034, FE-050): methods per `/auth/config` customer
 * audience, branded frame; Register / Forgot / magic link only when email works.
 */
const config = useConfigStore()
const route = useRoute()
const router = useRouter()
const auth = computed(() => config.config.customer)
const emailOn = computed(() => config.config.email_enabled)
const magicMode = ref(false)
const magicSent = ref(false)
// Only needed after a correct password: kept out of the initial login JS (FE-031 budget).
const MfaStep = defineAsyncComponent(() => import('@/components/mfa/MfaStep.vue'))
const challenge = ref<MfaChallenge | null>(null)

function done() {
  void router.push(safeRedirect(route.query.redirect) ?? '/tickets')
}
const ssoHref = computed(() => {
  const r = safeRedirect(route.query.redirect)
  return '/api/v1/auth/oidc/customer/start' + (r ? `?redirect=${encodeURIComponent(r)}` : '')
})
</script>

<template>
  <AuthCard branded :title="config.branding.login_title || $t('auth.customerTitle')">
    <template v-if="auth.oidc && !challenge">
      <v-btn :href="ssoHref" block variant="outlined" class="mb-4" data-testid="sso-button">
        {{ auth.oidc_label || $t('auth.sso') }}
      </v-btn>
      <div v-if="auth.local || (emailOn && auth.magic_link)" class="text-center text-medium-emphasis mb-4">{{ $t('auth.or') }}</div>
    </template>

    <MfaStep v-if="challenge" :challenge="challenge" @done="done" @restart="challenge = null" />
    <template v-else-if="magicMode">
      <v-alert v-if="magicSent" type="info" variant="tonal" data-testid="magic-sent" role="status">{{ $t('portal.account.magicSent') }}</v-alert>
      <MagicLinkForm v-else @sent="magicSent = true" />
      <v-btn variant="text" block class="mt-2" @click="magicMode = false">{{ $t('portal.account.usePassword') }}</v-btn>
    </template>
    <template v-else>
      <LocalLoginForm v-if="auth.local" audience="customer" @success="done" @mfa="challenge = $event" />
      <v-btn
        v-if="emailOn && auth.magic_link"
        variant="text"
        block
        class="mt-2"
        data-testid="magic-toggle"
        @click="magicMode = true"
      >
        {{ $t('portal.account.emailMeLink') }}
      </v-btn>
    </template>

    <div v-if="emailOn && !challenge" class="d-flex justify-space-between flex-wrap ga-2 mt-4 text-body-2">
      <router-link v-if="auth.local" to="/forgot-password" data-testid="forgot-link">{{ $t('auth.forgot') }}</router-link>
      <router-link v-if="auth.self_registration" to="/register" data-testid="register-link">{{ $t('auth.register') }}</router-link>
    </div>

    <template #below>
      <div v-if="config.config.kb_enabled" class="text-center mt-4">
        <v-btn to="/kb" variant="text" :prepend-icon="mdiBookOpenPageVariantOutline" data-testid="login-kb-link">{{ $t('nav.help') }}</v-btn>
      </div>
    </template>
  </AuthCard>
</template>
