<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useDisplay } from 'vuetify'
import { mdiCheckCircleOutline, mdiContentCopy } from '@mdi/js'
import BrandLogo from '@/components/BrandLogo.vue'
import { api, apiFetch, unwrap } from '@/api/client'
import { ApiProblem, fieldErrorsFrom } from '@/api/problem'
import { isLocale } from '@/i18n'
import { useConfigStore } from '@/stores/config'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import type { Me } from '@/types'
import SetupTokenForm from './forms/SetupTokenForm.vue'
import type { BrandingStep } from './forms/SetupBrandingForm.vue'
import { forget, initialState, persist, restore, type SetupState } from './state'

/**
 * First-run setup wizard (FE-049, branding decision §3):
 * token → admin → branding → SMTP (optional) → SSO info → done.
 *
 * Every step validates its own form before advancing. POST /setup/complete runs
 * when leaving the branding step: it creates the admin AND signs them in, which
 * the logo upload and the SMTP test (admin endpoints) need. A wrong token sends
 * the user back to step 1 with the server's message.
 */
// Only the token step is in the first view; the later steps (selects, file
// input, colour fields) load after mount, before the user can reach them (FE-031).
const loadAdmin = () => import('./forms/SetupAdminForm.vue')
const loadBranding = () => import('./forms/SetupBrandingForm.vue')
const loadSmtp = () => import('./forms/SetupSmtpForm.vue')
const SetupAdminForm = defineAsyncComponent(loadAdmin)
const SetupBrandingForm = defineAsyncComponent(loadBranding)
const SetupSmtpForm = defineAsyncComponent(loadSmtp)
onMounted(() => void Promise.all([loadAdmin(), loadBranding(), loadSmtp()]).catch(() => null))

const router = useRouter()
const { t, locale } = useI18n()
const { smAndDown } = useDisplay()
const config = useConfigStore()
const session = useSessionStore()
const ui = useUiStore()

const state = reactive<SetupState>(restore())
// In memory only — never in sessionStorage (see state.ts).
const adminPassword = ref('')
const tokenError = ref<string | null>(null)
const adminErrors = ref<Record<string, string[]>>({})
const brandingErrors = ref<Record<string, string[]>>({})
const adminNotice = ref<string | null>(null)
const finishing = ref(false)

// After a refresh before completion the password is gone: re-ask for it.
if (!state.completed && state.step === 3) {
  state.step = 2
  adminNotice.value = t('setup.admin.reenter')
}
// The admin session is the only proof steps 1–3 are done; without it start over.
if (state.completed && !session.me) Object.assign(state, initialState())

watch(state, () => persist(state), { deep: true, immediate: true })

const steps = computed(() => [
  { value: 1, title: t('setup.steps.token') },
  { value: 2, title: t('setup.steps.admin') },
  { value: 3, title: t('setup.steps.branding') },
  { value: 4, title: t('setup.steps.email') },
  { value: 5, title: t('setup.steps.sso') },
  { value: 6, title: t('setup.steps.done') },
])

const origin = typeof location !== 'undefined' ? location.origin : ''
const redirectUris = computed(() => [
  { label: t('setup.sso.staffRedirect'), value: `${origin}/api/v1/auth/oidc/staff/callback` },
  { label: t('setup.sso.customerRedirect'), value: `${origin}/api/v1/auth/oidc/customer/callback` },
  { label: t('setup.sso.backchannel'), value: `${origin}/api/v1/auth/oidc/staff/backchannel-logout` },
])

function onToken(token: string) {
  state.token = token
  tokenError.value = null
  state.step = 2
}

function onAdmin(a: { name: string; email: string; password: string }) {
  state.admin = { name: a.name, email: a.email }
  adminPassword.value = a.password
  adminErrors.value = {}
  adminNotice.value = null
  state.step = 3
}

function errorsWithPrefix(all: Record<string, string[]>, prefix: string) {
  return Object.fromEntries(Object.entries(all).filter(([k]) => k.startsWith(prefix)))
}

async function complete(b: BrandingStep) {
  state.branding = { product_name: b.product_name, company_name: b.company_name, primary_color: b.primary_color }
  if (!adminPassword.value) {
    adminNotice.value = t('setup.admin.reenter')
    state.step = 2
    return
  }
  let me: Me
  try {
    me = (await unwrap(
      api.POST('/setup/complete', {
        body: {
          setup_token: state.token,
          admin: { name: state.admin.name, email: state.admin.email, password: adminPassword.value },
          branding: {
            product_name: b.product_name,
            ...(b.company_name ? { company_name: b.company_name } : {}),
            primary_color: b.primary_color,
          },
          ...(isLocale(locale.value) ? { locale: locale.value } : {}),
        },
      }),
    )) as unknown as Me
  } catch (e) {
    if (!(e instanceof ApiProblem)) throw e
    if (e.status === 401 || e.status === 403) {
      tokenError.value = e.detail || e.title || t('setup.token.wrong')
      state.step = 1
      return
    }
    if (e.status === 404) {
      ui.notify(t('setup.alreadyDone'), 'warning')
      forget()
      await config.load()
      await router.replace('/agent/login')
      return
    }
    if (e.status === 422) {
      const errs = fieldErrorsFrom(e)
      if (errs.setup_token) {
        tokenError.value = errs.setup_token[0] ?? t('setup.token.wrong')
        state.step = 1
        return
      }
      const admin = errorsWithPrefix(errs, 'admin.')
      if (Object.keys(admin).length) {
        adminErrors.value = admin
        state.step = 2
        return
      }
      brandingErrors.value = errorsWithPrefix(errs, 'branding.')
    }
    throw e
  }
  adminPassword.value = ''
  session.set(me)
  state.completed = true
  config.setBranding({ product_name: b.product_name, company_name: b.company_name || undefined, primary_color: b.primary_color })
  if (b.logo) {
    const fd = new FormData()
    fd.append('file', b.logo)
    try {
      const asset = await apiFetch<{ url: string }>('/admin/branding/assets/logo_light', { method: 'POST', body: fd })
      if (asset?.url) config.setBranding({ logo_light_url: asset.url })
    } catch (e) {
      ui.notify(t('setup.branding.logoFailed', { reason: e instanceof ApiProblem ? e.userMessage : '' }), 'warning', 8000)
    }
  }
  state.step = 4
}

function onSmtpSaved(smtp: SetupState['smtp']) {
  state.smtp = smtp
  state.step = 5
}

async function copy(v: string) {
  try {
    await navigator.clipboard.writeText(v)
    ui.notify(t('setup.sso.copied'), 'success')
  } catch {
    /* clipboard blocked: the value is selectable */
  }
}

async function finish() {
  finishing.value = true
  forget()
  // Re-reading /auth/config flips setup_pending off, so the guard keeps /setup closed from now on.
  await config.load()
  finishing.value = false
  await router.replace('/agent')
}
</script>

<template>
  <v-app>
    <v-main>
      <v-container style="max-width: 860px" class="py-8">
        <div class="text-center mb-4"><BrandLogo :height="40" /></div>
        <h1 class="text-h5 text-center mb-6">{{ $t('setup.title') }}</h1>
        <v-stepper v-model="state.step" :mobile="smAndDown" alt-labels flat border data-testid="setup-stepper">
          <v-stepper-header>
            <template v-for="(s, i) in steps" :key="s.value">
              <v-stepper-item :value="s.value" :title="s.title" :complete="state.step > s.value" />
              <v-divider v-if="i < steps.length - 1" />
            </template>
          </v-stepper-header>
          <v-stepper-window>
            <v-stepper-window-item :value="1">
              <SetupTokenForm :token="state.token" :error="tokenError" @next="onToken" />
            </v-stepper-window-item>
            <v-stepper-window-item :value="2">
              <SetupAdminForm
                :key="`admin-${state.step}`"
                :name="state.admin.name"
                :email="state.admin.email"
                :errors="adminErrors"
                :notice="adminNotice"
                @next="onAdmin"
                @back="state.step = 1"
              />
            </v-stepper-window-item>
            <v-stepper-window-item :value="3">
              <SetupBrandingForm :initial="state.branding" :errors="brandingErrors" :complete="complete" @back="state.step = 2" />
            </v-stepper-window-item>
            <v-stepper-window-item :value="4">
              <SetupSmtpForm :initial="state.smtp" :admin-email="state.admin.email" @saved="onSmtpSaved" @skip="state.step = 5" />
            </v-stepper-window-item>
            <v-stepper-window-item :value="5">
              <div data-testid="setup-sso">
                <p class="text-body-2 mb-4">{{ $t('setup.sso.intro') }}</p>
                <v-list density="compact" class="mb-4">
                  <v-list-item v-for="u in redirectUris" :key="u.value" :subtitle="u.label">
                    <v-list-item-title><code class="st-uri">{{ u.value }}</code></v-list-item-title>
                    <template #append>
                      <v-btn :icon="mdiContentCopy" variant="text" size="small" :aria-label="$t('setup.sso.copy')" @click="copy(u.value)" />
                    </template>
                  </v-list-item>
                </v-list>
                <p class="text-body-2 text-medium-emphasis mb-4">{{ $t('setup.sso.later') }}</p>
                <div class="d-flex justify-end">
                  <v-btn color="primary" data-testid="setup-next" @click="state.step = 6">{{ $t('setup.next') }}</v-btn>
                </div>
              </div>
            </v-stepper-window-item>
            <v-stepper-window-item :value="6">
              <div class="text-center py-6" data-testid="setup-done">
                <v-icon :icon="mdiCheckCircleOutline" size="56" color="success" class="mb-3" />
                <h2 class="text-h6 mb-2">{{ $t('setup.done.title') }}</h2>
                <p class="text-body-2 mb-6">{{ $t('setup.done.body', { email: state.admin.email }) }}</p>
                <v-btn color="primary" size="large" :loading="finishing" data-testid="setup-finish" @click="finish">{{ $t('setup.done.open') }}</v-btn>
              </div>
            </v-stepper-window-item>
          </v-stepper-window>
        </v-stepper>
      </v-container>
    </v-main>
  </v-app>
</template>

<style scoped>
.st-uri { word-break: break-all; white-space: normal; }
</style>
