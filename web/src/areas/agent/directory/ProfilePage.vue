<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script setup lang="ts">
/** /agent/profile: name + language, password (local accounts), email signature (FE-059), active sessions. */
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { mdiLogout, mdiShieldAccountOutline } from '@mdi/js'
import PageHeader from '@/components/PageHeader.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import ProfileForm from './ProfileForm.vue'
import PasswordForm from './PasswordForm.vue'
import SignatureForm from './SignatureForm.vue'
import NotificationPrefsCard from '@/notifications/NotificationPrefsCard.vue'
import MfaSecuritySection from '@/components/mfa/MfaSecuritySection.vue'
import { api, unwrap } from '@/api/client'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { useConfirm } from '@/composables/useConfirm'
import { useAction } from '../shared/useAction'
import type { components } from '@/api/schema'

type S = components['schemas']
const { t } = useI18n()
const session = useSessionStore()
const router = useRouter()
const ui = useUiStore()
const { confirm } = useConfirm()
const { run } = useAction()
const signature = ref<string | null>(null)
const signatureFailed = ref(false)
const sessions = ref<S['Session'][]>([])
const sessionsLoading = ref(true)

async function loadSignature() {
  try {
    signature.value = (await unwrap(api.GET('/me/signature')))?.signature_md ?? ''
  } catch {
    signatureFailed.value = true
    signature.value = ''
  }
}
async function loadSessions() {
  sessionsLoading.value = true
  try {
    sessions.value = (await unwrap(api.GET('/me/sessions'))) ?? []
  } catch {
    sessions.value = []
  } finally {
    sessionsLoading.value = false
  }
}
onMounted(() => {
  void loadSignature()
  void loadSessions()
})
async function revoke(s: S['Session']) {
  if (!(await confirm({ body: t('agent.profile.revokeSession') }))) return
  const ok = await run(async () => {
    await unwrap(api.DELETE('/me/sessions/{id}', { params: { path: { id: s.id } } }))
    return true
  }, t('agent.profile.sessionRevoked'))
  if (!ok) return
  // Revoking the session in use signs this tab out (FE-037).
  if (s.current) {
    session.clear()
    void router.push('/agent/login')
    return
  }
  sessions.value = sessions.value.filter((x) => x.id !== s.id)
}
</script>

<template>
  <v-container fluid class="pa-4" style="max-width: 960px">
    <PageHeader :title="$t('nav.profile')" :subtitle="session.me?.email">
      <template #actions>
        <v-btn variant="tonal" to="/agent/profile/data" :prepend-icon="mdiShieldAccountOutline" data-testid="profile-privacy-link">{{ $t('privacy.my.title') }}</v-btn>
      </template>
    </PageHeader>
    <v-row>
      <v-col cols="12" md="6">
        <v-card border flat :title="$t('agent.profile.details')" class="mb-4">
          <v-card-text>
            <ProfileForm v-if="session.me" :name="session.me.name" :locale="session.me.locale" @saved="ui.notify($t('agent.common.saved'), 'success')" />
          </v-card-text>
        </v-card>
        <v-card v-if="session.me?.has_password" border flat :title="$t('agent.profile.password')" data-testid="password-card">
          <v-card-text>
            <PasswordForm @saved="ui.notify($t('agent.profile.passwordChanged'), 'success')" />
          </v-card-text>
        </v-card>
        <MfaSecuritySection v-if="session.me?.has_password" class="mt-4" />
      </v-col>
      <v-col cols="12" md="6">
        <NotificationPrefsCard area="agent" />
        <v-card border flat :title="$t('agent.profile.signature')" :subtitle="$t('agent.profile.signatureHint')" class="mb-4">
          <v-card-text>
            <v-alert v-if="signatureFailed" type="warning" variant="tonal" density="compact" class="mb-3">{{ $t('agent.common.loadFailed') }}</v-alert>
            <v-progress-linear v-if="signature === null" indeterminate />
            <SignatureForm v-else :signature="signature" @saved="(s) => { signature = s; ui.notify($t('agent.common.saved'), 'success') }" />
          </v-card-text>
        </v-card>
        <v-card border flat :title="$t('agent.profile.sessions')">
          <v-progress-linear v-if="sessionsLoading" indeterminate />
          <v-list density="compact" data-testid="session-list">
            <v-list-item v-for="s in sessions" :key="s.id" :title="s.user_agent || $t('agent.common.unknown')" data-testid="session-row">
              <template #subtitle>
                {{ s.ip ?? '' }}
                <RelativeTime v-if="s.last_seen_at" :value="s.last_seen_at" />
              </template>
              <template #append>
                <v-chip v-if="s.current" size="x-small" label color="primary">{{ $t('agent.profile.currentSession') }}</v-chip>
 <v-btn :icon="mdiLogout" size="small" variant="text" :aria-label="$t('agent.profile.revoke')" data-testid="session-revoke" @click="revoke(s)" />
              </template>
            </v-list-item>
          </v-list>
        </v-card>
      </v-col>
    </v-row>
  </v-container>
</template>
